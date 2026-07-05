package customer

import (
	"context"
	"encoding/json"
	"fmt"
	"go-agent-studio/common"
	"go-agent-studio/services/aitypes"
	"go-agent-studio/services/memory"
	"go-agent-studio/services/simplechat"
	"go-agent-studio/services/trace"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
)

const (
	defaultSSEHeartbeat = 15 * time.Second
)

var sseHeartbeatInterval = defaultSSEHeartbeat

// Handler handles customer端 HTTP requests.
type Handler struct {
	chat             *simplechat.SimpleChat
	store            *memory.Store
	tools            *aitypes.ToolRegistry
	tracer           *trace.Recorder
	maxMessageChars  int
	maxTurns         int
	maxDailyVisitors int
}

// HandlerOptions holds handler configuration.
type HandlerOptions struct {
	MaxMessageChars  int
	MaxTurns         int
	MaxDailyVisitors int
}

// NewHandler creates a new customer handler.
func NewHandler(chat *simplechat.SimpleChat, store *memory.Store, tools *aitypes.ToolRegistry, tracer *trace.Recorder, opts HandlerOptions) *Handler {
	if opts.MaxMessageChars < 0 {
		opts.MaxMessageChars = 0
	}
	if opts.MaxTurns <= 0 {
		opts.MaxTurns = 10
	}
	if opts.MaxDailyVisitors <= 0 {
		opts.MaxDailyVisitors = 1000
	}
	return &Handler{
		chat:             chat,
		store:            store,
		tools:            tools,
		tracer:           tracer,
		maxMessageChars:  opts.MaxMessageChars,
		maxTurns:         opts.MaxTurns,
		maxDailyVisitors: opts.MaxDailyVisitors,
	}
}

// RegisterRoutes registers customer端 routes.
func (h *Handler) RegisterRoutes(g *echo.Group) {
	g.POST("/chat", h.Chat)
	g.POST("/reset", h.Reset)
	g.GET("/tools", h.Tools)
	g.GET("/sessions", h.ListSessions)
	g.GET("/sessions/:id", h.GetSession)
	g.GET("/sessions/:id/trace", h.GetSessionTrace)
}

type chatRequest struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

type streamError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

type publicToolSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Chat handles customer chat requests with SSE streaming.
func (h *Handler) Chat(c echo.Context) error {
	if err := common.RequireJSONContentType(c); err != nil {
		return common.Fail(c, http.StatusUnsupportedMediaType, err.Error())
	}
	var req chatRequest
	if err := common.BindJSONStrict(c, &req); err != nil {
		return common.Fail(c, http.StatusBadRequest, "invalid request")
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if err := common.ValidateOptionalSessionID(req.SessionID); err != nil {
		return common.Fail(c, http.StatusBadRequest, err.Error())
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		return common.Fail(c, http.StatusBadRequest, "message is required")
	}
	if h.maxMessageChars > 0 && len([]rune(req.Message)) > h.maxMessageChars {
		return common.Fail(c, http.StatusRequestEntityTooLarge, fmt.Sprintf("message is too long; max %d characters", h.maxMessageChars))
	}

	// Check if ready
	if h.chat == nil {
		return common.Fail(c, http.StatusServiceUnavailable, "chat service is not configured")
	}
	if h.store == nil {
		return common.Fail(c, http.StatusServiceUnavailable, "memory store is not configured")
	}

	// Load or create session
	var isNewSession bool
	if req.SessionID == "" {
		isNewSession = true
	} else {
		_, found, err := h.store.Get(req.SessionID)
		if err != nil {
			return common.Fail(c, http.StatusServiceUnavailable, "session load failed")
		}
		if !found {
			isNewSession = true
		}
	}

	// Check daily visitor limit for new sessions
	if isNewSession {
		count, err := h.store.CountActiveSessionsToday(string(aitypes.RoleCustomer))
		if err != nil {
			return common.Fail(c, http.StatusServiceUnavailable, "session load failed")
		}
		if count >= h.maxDailyVisitors {
			return common.Fail(c, http.StatusTooManyRequests, "每日访客额度已满，请明天再试。")
		}
	}

	// Load session
	session, err := h.loadChatSession(req.SessionID)
	if err != nil {
		return common.Fail(c, http.StatusServiceUnavailable, "session load failed")
	}
	if session == nil {
		return common.Fail(c, http.StatusNotFound, "session not found")
	}

	// Check turn limit
	userMsg := aitypes.NewMessage(aitypes.RoleUser, req.Message)
	messages := append([]aitypes.Message(nil), session.Messages...)
	messages = append(messages, userMsg)
	sessionTitle := memory.TitleFromMessages(messages)

	if userTurnCount(session.Messages) >= h.maxTurns {
		// Turn limit reached
		res := c.Response()
		res.Header().Set(echo.HeaderContentType, "text/event-stream")
		res.Header().Set(echo.HeaderCacheControl, "no-cache, no-transform")
		res.Header().Set(echo.HeaderConnection, "keep-alive")
		res.Header().Set("X-Accel-Buffering", "no")
		res.WriteHeader(http.StatusOK)

		var writeMu sync.Mutex
		flush := func(event string, payload interface{}) {
			flushSSEEvent(res, res.Flush, &writeMu, common.RequestID(c), event, payload)
		}

		flush("session", map[string]string{"session_id": session.ID, "title": sessionTitle})
		flush("title", map[string]string{"session_id": session.ID, "title": sessionTitle})
		flush("message", userMsg)

		assistantMsg := aitypes.NewMessage(aitypes.RoleAssistant, fmt.Sprintf("已达到最大对话轮数（%d 轮），请开启新会话继续。", h.maxTurns))
		flush("message", assistantMsg)
		flush("usage", map[string]interface{}{
			"tool_calls_count":  0,
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"max_user_turns":    h.maxTurns,
		})

		session.Messages = append(messages, assistantMsg)
		if err := h.store.Save(session); err != nil {
			flush("error", streamError{Code: "session_save_failed", Message: "session save failed", RequestID: common.RequestID(c)})
		}
		flush("done", map[string]interface{}{})
		return nil
	}

	// Setup SSE response
	res := c.Response()
	res.Header().Set(echo.HeaderContentType, "text/event-stream")
	res.Header().Set(echo.HeaderCacheControl, "no-cache, no-transform")
	res.Header().Set(echo.HeaderConnection, "keep-alive")
	res.Header().Set("X-Accel-Buffering", "no")
	res.WriteHeader(http.StatusOK)

	var writeMu sync.Mutex
	flush := func(event string, payload interface{}) {
		flushSSEEvent(res, res.Flush, &writeMu, common.RequestID(c), event, payload)
	}
	flushError := func(code, message string) {
		flush("error", streamError{Code: code, Message: message, RequestID: common.RequestID(c)})
	}

	flush("session", map[string]string{"session_id": session.ID, "title": sessionTitle})
	flush("title", map[string]string{"session_id": session.ID, "title": sessionTitle})
	flush("message", userMsg)

	// Start heartbeat
	ctx := c.Request().Context()
	stopHeartbeat := startSSEHeartbeat(ctx, sseHeartbeatInterval, flush)
	defer stopHeartbeat()

	// Execute simple chat
	result, err := h.chat.Chat(ctx, simplechat.ChatOptions{
		SessionID: session.ID,
		RequestID: common.RequestID(c),
		Messages:  messages,
		OnMessage: func(m aitypes.Message) {
			flush("message", m)
		},
		OnTool: func(event simplechat.ToolCallEvent) {
			flush("tool_call", event)
		},
	})

	if err != nil {
		flushError("chat_failed", "service temporarily unable to complete this answer; please try again later")
	}

	if result != nil {
		session.Messages = result.Messages
		if err := h.store.Save(session); err != nil {
			flushError("session_save_failed", "session save failed")
		}
		flush("usage", map[string]interface{}{
			"tool_calls_count":  result.ToolCallsCount,
			"prompt_tokens":     result.PromptTokens,
			"completion_tokens": result.CompletionTokens,
			"max_user_turns":    h.maxTurns,
		})
	}

	stopHeartbeat()
	flush("done", map[string]interface{}{})
	return nil
}

// Reset resets a customer session.
func (h *Handler) Reset(c echo.Context) error {
	if err := common.RequireJSONContentType(c); err != nil {
		return common.Fail(c, http.StatusUnsupportedMediaType, err.Error())
	}
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := common.BindJSONStrictAllowEmpty(c, &req); err != nil {
		return common.Fail(c, http.StatusBadRequest, "invalid request")
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if err := common.ValidateRequiredSessionID(req.SessionID); err != nil {
		return common.Fail(c, http.StatusBadRequest, err.Error())
	}

	if h.store == nil {
		return common.Fail(c, http.StatusServiceUnavailable, "memory store is not configured")
	}

	session, found, err := h.store.Get(req.SessionID)
	if err != nil {
		return common.Fail(c, http.StatusInternalServerError, "session reset failed")
	}
	if !found || (session.Role != "" && session.Role != string(aitypes.RoleCustomer)) {
		return common.Fail(c, http.StatusNotFound, "session not found")
	}

	if err := h.store.Delete(req.SessionID); err != nil {
		return common.Fail(c, http.StatusInternalServerError, "session reset failed")
	}
	if h.tracer != nil {
		if err := h.tracer.DeleteSession(req.SessionID); err != nil {
			return common.Fail(c, http.StatusInternalServerError, "trace reset failed")
		}
	}
	return common.Success(c, "session reset", nil)
}

// Tools returns available tools for customer.
func (h *Handler) Tools(c echo.Context) error {
	if h.tools == nil {
		return common.Fail(c, http.StatusServiceUnavailable, "tool registry is not configured")
	}
	return common.Success(c, "tools", publicToolSummaries(h.tools))
}

// ListSessions returns a list of sessions for customer.
func (h *Handler) ListSessions(c echo.Context) error {
	if h.store == nil {
		return common.Fail(c, http.StatusServiceUnavailable, "memory store is not configured")
	}
	limit := 50
	sessions, err := h.store.ListSummariesByRole(limit, string(aitypes.RoleCustomer))
	if err != nil {
		return common.Fail(c, http.StatusInternalServerError, "failed to list sessions")
	}
	return common.Success(c, "sessions", map[string]interface{}{
		"sessions": sessions,
	})
}

// GetSession returns session details for customer.
func (h *Handler) GetSession(c echo.Context) error {
	sessionID := strings.TrimSpace(c.Param("id"))
	if err := common.ValidateRequiredSessionID(sessionID); err != nil {
		return common.Fail(c, http.StatusBadRequest, err.Error())
	}
	if h.store == nil {
		return common.Fail(c, http.StatusServiceUnavailable, "memory store is not configured")
	}

	session, found, err := h.store.Get(sessionID)
	if err != nil {
		return common.Fail(c, http.StatusInternalServerError, "session load failed")
	}
	if !found || (session.Role != "" && session.Role != string(aitypes.RoleCustomer)) {
		return common.Fail(c, http.StatusNotFound, "session not found")
	}

	if session.Role == "" {
		session.Role = string(aitypes.RoleCustomer)
		if err := h.store.Save(session); err != nil {
			return common.Fail(c, http.StatusInternalServerError, "session upgrade failed")
		}
	}

	return common.Success(c, "session", map[string]interface{}{
		"session": session,
	})
}

// GetSessionTrace returns session trace for customer.
func (h *Handler) GetSessionTrace(c echo.Context) error {
	sessionID := strings.TrimSpace(c.Param("id"))
	if err := common.ValidateRequiredSessionID(sessionID); err != nil {
		return common.Fail(c, http.StatusBadRequest, err.Error())
	}
	if h.tracer == nil {
		return common.Fail(c, http.StatusServiceUnavailable, "trace recorder is not configured")
	}

	// Verify session exists and is customer role
	if h.store != nil {
		session, found, err := h.store.Get(sessionID)
		if err != nil {
			return common.Fail(c, http.StatusInternalServerError, "session load failed")
		}
		if !found || (session.Role != "" && session.Role != string(aitypes.RoleCustomer)) {
			return common.Fail(c, http.StatusNotFound, "session not found")
		}
		if session.Role == "" {
			session.Role = string(aitypes.RoleCustomer)
			if err := h.store.Save(session); err != nil {
				return common.Fail(c, http.StatusInternalServerError, "session upgrade failed")
			}
		}
	}

	events, err := h.tracer.ListWithError(sessionID)
	if err != nil {
		return common.Fail(c, http.StatusInternalServerError, "trace load failed")
	}

	return common.Success(c, "trace", events)
}

func (h *Handler) loadChatSession(sessionID string) (*memory.Session, error) {
	if h.store == nil {
		return nil, fmt.Errorf("memory store is not configured")
	}
	if sessionID != "" {
		session, found, err := h.store.Get(sessionID)
		if err != nil || !found {
			return session, err
		}
		if session.Role != "" && session.Role != string(aitypes.RoleCustomer) {
			return nil, nil
		}
		if session.Role == "" {
			session.Role = string(aitypes.RoleCustomer)
			if err := h.store.Save(session); err != nil {
				return nil, err
			}
		}
		return session, nil
	}
	return h.store.GetOrCreateForRoleWithError(sessionID, string(aitypes.RoleCustomer))
}

func userTurnCount(messages []aitypes.Message) int {
	count := 0
	for _, message := range messages {
		if message.Role == aitypes.RoleUser {
			count++
		}
	}
	return count
}

func publicToolSummaries(registry *aitypes.ToolRegistry) []publicToolSummary {
	names := registry.Names()
	out := make([]publicToolSummary, 0, len(names))
	for _, name := range names {
		out = append(out, publicToolSummary{
			Name:        name,
			Description: publicToolDescription(name),
		})
	}
	return out
}

func publicToolDescription(name string) string {
	switch name {
	case "search_labor_law":
		return "劳动法咨询能力"
	default:
		return "智能客服能力"
	}
}

func flushSSEEvent(w io.Writer, flush func(), writeMu *sync.Mutex, requestID, event string, payload interface{}) {
	if err := writeSSEEvent(w, flush, writeMu, event, payload); err != nil && event != "error" {
		_ = writeSSEEvent(w, flush, writeMu, "error", streamError{
			Code:      "sse_encode_failed",
			Message:   "stream event encode failed",
			RequestID: requestID,
		})
	}
}

func writeSSEEvent(w io.Writer, flush func(), writeMu *sync.Mutex, event string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if writeMu != nil {
		writeMu.Lock()
		defer writeMu.Unlock()
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return err
	}
	if flush != nil {
		flush()
	}
	return nil
}

func startSSEHeartbeat(ctx context.Context, interval time.Duration, flush func(string, interface{})) func() {
	if interval <= 0 || flush == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				flush("heartbeat", map[string]string{"time": now.UTC().Format(time.RFC3339Nano)})
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
