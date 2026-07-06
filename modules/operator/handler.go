package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"go-agent-studio/common"
	"go-agent-studio/services/agentcore"
	"go-agent-studio/services/aitypes"
	"go-agent-studio/services/memory"
	"go-agent-studio/services/orchestrator"
	"go-agent-studio/services/security"
	"go-agent-studio/services/trace"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
)

const (
	maxUserTurnsPerSession = 30
	defaultSSEHeartbeat    = 15 * time.Second
)

var sseHeartbeatInterval = defaultSSEHeartbeat

type Handler struct {
	store            *memory.Store
	factory          func(*aitypes.ToolRegistry, aitypes.AgentRole) *agentcore.Agent
	tools            *aitypes.ToolRegistry
	recorder         *trace.Recorder
	role             aitypes.AgentRole
	exposeInternal   bool
	maxMessageChars  int
	chatTimeout      time.Duration
	maxTurns         int
	maxDailyVisitors int
}

type HandlerOptions struct {
	Role             aitypes.AgentRole
	ExposeInternal   bool
	MaxMessageChars  int
	ChatTimeout      time.Duration
	MaxTurns         int
	MaxDailyVisitors int
}

type publicToolSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func NewHandler(store *memory.Store, tools *aitypes.ToolRegistry, recorder *trace.Recorder, factory func(*aitypes.ToolRegistry, aitypes.AgentRole) *agentcore.Agent) *Handler {
	return NewHandlerWithOptions(store, tools, recorder, factory, aitypes.RoleCustomer, false, 0)
}

func NewHandlerWithRole(store *memory.Store, tools *aitypes.ToolRegistry, recorder *trace.Recorder, factory func(*aitypes.ToolRegistry, aitypes.AgentRole) *agentcore.Agent, role aitypes.AgentRole, exposeInternal bool) *Handler {
	return NewHandlerWithOptions(store, tools, recorder, factory, role, exposeInternal, 0)
}

func NewHandlerWithOptions(store *memory.Store, tools *aitypes.ToolRegistry, recorder *trace.Recorder, factory func(*aitypes.ToolRegistry, aitypes.AgentRole) *agentcore.Agent, role aitypes.AgentRole, exposeInternal bool, maxMessageChars int) *Handler {
	return NewHandlerWithConfig(store, tools, recorder, factory, HandlerOptions{
		Role:            role,
		ExposeInternal:  exposeInternal,
		MaxMessageChars: maxMessageChars,
	})
}

func NewHandlerWithConfig(store *memory.Store, tools *aitypes.ToolRegistry, recorder *trace.Recorder, factory func(*aitypes.ToolRegistry, aitypes.AgentRole) *agentcore.Agent, opts HandlerOptions) *Handler {
	role := opts.Role
	if role == "" {
		role = aitypes.RoleCustomer
	}
	maxMessageChars := opts.MaxMessageChars
	if maxMessageChars < 0 {
		maxMessageChars = 0
	}
	chatTimeout := opts.ChatTimeout
	if chatTimeout <= 0 {
		chatTimeout = 10 * time.Minute
	}
	maxTurns := opts.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 30
	}
	maxDailyVisitors := opts.MaxDailyVisitors
	if maxDailyVisitors <= 0 {
		maxDailyVisitors = 10
	}
	return &Handler{
		store:            store,
		tools:            tools,
		recorder:         recorder,
		factory:          factory,
		role:             role,
		exposeInternal:   opts.ExposeInternal,
		maxMessageChars:  maxMessageChars,
		chatTimeout:      chatTimeout,
		maxTurns:         maxTurns,
		maxDailyVisitors: maxDailyVisitors,
	}
}

func (h *Handler) RegisterRoutes(g *echo.Group) {
	g.POST("/chat", h.Chat)
	g.POST("/reset", h.Reset)
	g.GET("/tools", h.Tools)
	g.GET("/sessions", h.ListSessions)
	g.GET("/sessions/:id/trace", h.Trace)
	g.GET("/sessions/:id", h.GetSession)
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
	if h != nil && h.maxMessageChars > 0 && len([]rune(req.Message)) > h.maxMessageChars {
		return common.Fail(c, http.StatusRequestEntityTooLarge, fmt.Sprintf("message is too long; max %d characters", h.maxMessageChars))
	}
	if ready, err := h.ensureChatReady(c); !ready {
		return err
	}

	var isNewSession bool
	if req.SessionID == "" {
		isNewSession = true
	} else {
		_, found, err := h.store.Get(req.SessionID)
		if err != nil {
			h.recordStorageError(req.SessionID, "session_load_error", err)
			return common.Fail(c, http.StatusServiceUnavailable, "session load failed")
		}
		if !found {
			isNewSession = true
		}
	}

	if isNewSession && h.role == aitypes.RoleCustomer {
		count, err := h.store.CountActiveSessionsToday(string(aitypes.RoleCustomer))
		if err != nil {
			h.recordStorageError(req.SessionID, "session_load_error", err)
			return common.Fail(c, http.StatusServiceUnavailable, "session load failed")
		}
		if count >= h.maxDailyVisitors {
			return common.Fail(c, http.StatusTooManyRequests, "每日访客额度已满，请明天再试。")
		}
	}

	ag := h.factory(h.tools, h.role)
	if ag == nil {
		return common.Fail(c, http.StatusServiceUnavailable, "agent is not configured")
	}

	session, err := h.loadChatSession(req.SessionID)
	if err != nil {
		h.recordStorageError(req.SessionID, "session_load_error", err)
		return common.Fail(c, http.StatusServiceUnavailable, "session load failed")
	}
	if session == nil {
		return common.Fail(c, http.StatusNotFound, "session not found")
	}
	userMsg := aitypes.NewMessage(aitypes.RoleUser, req.Message)
	messages := append([]aitypes.Message(nil), session.Messages...)
	messages = append(messages, userMsg)
	sessionTitle := memory.TitleFromMessages(messages)

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

	if userTurnCount(session.Messages) >= h.maxTurns {
		assistantMsg := aitypes.NewMessage(aitypes.RoleAssistant, fmt.Sprintf("已达到最大对话轮数（%d 轮），请开启新会话继续。", h.maxTurns))
		flush("message", assistantMsg)
		flush("usage", map[string]interface{}{
			"iterations":        0,
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"max_user_turns":    h.maxTurns,
		})
		session.Messages = append(messages, assistantMsg)
		if err := h.store.Save(session); err != nil {
			h.recordStorageError(session.ID, "session_save_error", err)
			flushError("session_save_failed", "session save failed")
		}
		flush("done", map[string]interface{}{})
		return nil
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), h.chatTimeout)
	defer cancel()
	stopHeartbeat := startSSEHeartbeat(ctx, sseHeartbeatInterval, flush)
	defer stopHeartbeat()
	opts := agentcore.RunOptions{
		SessionID: session.ID,
		RequestID: common.RequestID(c),
		Role:      h.role,
		Messages:  messages,
		OnMessage: func(m aitypes.Message) {
			flush("message", m)
		},
	}
	if h.exposeInternal {
		opts.OnPlan = func(plan orchestrator.Plan) {
			flush("plan", plan)
		}
		opts.OnStep = func(step orchestrator.Step) {
			flush("step", step)
		}
		opts.OnRoute = func(decision orchestrator.RouteDecision) {
			flush("route", decision)
		}
		opts.OnExec = func(result orchestrator.ExecutionResult) {
			flush("execution", result)
		}
	}
	result, err := ag.Run(ctx, opts)
	if err != nil {
		flushError("agent_run_failed", h.publicAgentRunErrorMessage(err))
	}
	if result != nil {
		session.Messages = result.Messages
		if result.Memory != nil {
			session.Summary = result.Memory.Summary
			session.Facts = result.Memory.Facts
		}
		if err := h.store.Save(session); err != nil {
			h.recordStorageError(session.ID, "session_save_error", err)
			flushError("session_save_failed", "session save failed")
		}
		if h.exposeInternal && result.Plan != nil {
			flush("plan", result.Plan)
		}
		flush("usage", map[string]interface{}{
			"iterations":        result.Iterations,
			"prompt_tokens":     result.PromptTokens,
			"completion_tokens": result.CompletionTokens,
		})
	}
	stopHeartbeat()
	flush("done", map[string]interface{}{})
	return nil
}

func (h *Handler) loadChatSession(sessionID string) (*memory.Session, error) {
	if h == nil || h.store == nil {
		return nil, fmt.Errorf("memory store is not configured")
	}
	if sessionID != "" {
		session, found, err := h.store.Get(sessionID)
		if err != nil || !found {
			return session, err
		}
		if !h.canAccessSession(session) {
			return nil, nil
		}
		if session.Role == "" && h.role == aitypes.RoleCustomer {
			session.Role = string(aitypes.RoleCustomer)
			if err := h.store.Save(session); err != nil {
				return nil, err
			}
		}
		return session, nil
	}
	return h.store.GetOrCreateForRoleWithError(sessionID, string(h.role))
}

func (h *Handler) publicAgentRunErrorMessage(err error) string {
	if h == nil || h.role == "" || h.role == aitypes.RoleCustomer {
		return "service temporarily unable to complete this answer; please try again later"
	}
	if err == nil {
		return "agent run failed"
	}
	return security.RedactSensitive(err.Error())
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

func userTurnCount(messages []aitypes.Message) int {
	count := 0
	for _, message := range messages {
		if message.Role == aitypes.RoleUser {
			count++
		}
	}
	return count
}

func (h *Handler) ensureChatReady(c echo.Context) (bool, error) {
	if h == nil || h.store == nil {
		return false, common.Fail(c, http.StatusServiceUnavailable, "memory store is not configured")
	}
	if h.tools == nil {
		return false, common.Fail(c, http.StatusServiceUnavailable, "tool registry is not configured")
	}
	if h.factory == nil {
		return false, common.Fail(c, http.StatusServiceUnavailable, "agent factory is not configured")
	}
	return true, nil
}

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
	if h == nil || h.store == nil {
		return common.Fail(c, http.StatusServiceUnavailable, "memory store is not configured")
	}
	if h.role != "" {
		session, found, err := h.store.Get(req.SessionID)
		if err != nil {
			return common.Fail(c, http.StatusInternalServerError, "session reset failed")
		}
		if !found || !h.canAccessSession(session) {
			return common.Fail(c, http.StatusNotFound, "session not found")
		}
	}
	if err := h.store.Delete(req.SessionID); err != nil {
		return common.Fail(c, http.StatusInternalServerError, "session reset failed")
	}
	if h.recorder != nil {
		if err := h.recorder.DeleteSession(req.SessionID); err != nil {
			return common.Fail(c, http.StatusInternalServerError, "trace reset failed")
		}
	}
	return common.Success(c, "session reset", nil)
}

func (h *Handler) canAccessSession(session *memory.Session) bool {
	if h == nil || session == nil {
		return false
	}
	switch h.role {
	case aitypes.RoleCustomer:
		return session.Role == "" || session.Role == string(aitypes.RoleCustomer)
	case aitypes.RoleOperator:
		return session.Role == string(aitypes.RoleOperator)
	case aitypes.RoleAdmin:
		return session.Role == string(aitypes.RoleAdmin)
	default:
		return false
	}
}

func (h *Handler) recordStorageError(sessionID, eventType string, err error) {
	if h.recorder == nil || err == nil {
		return
	}
	_, _ = h.recorder.AddWithError(sessionID, trace.Event{
		Type:    eventType,
		Message: err.Error(),
		Status:  "failed",
	})
}

func (h *Handler) Tools(c echo.Context) error {
	if h == nil || h.tools == nil {
		return common.Fail(c, http.StatusServiceUnavailable, "tool registry is not configured")
	}
	if !h.exposeInternal || h.role == aitypes.RoleCustomer {
		return common.Success(c, "tools", publicToolSummaries(h.tools))
	}
	return common.Success(c, "tools", h.tools.List())
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
	case "search_product_docs":
		return "产品文档检索能力"
	case "craft_image_prompt":
		return "AI 图片生成 Prompt 助手"
	case "craft_video_prompt":
		return "AI 短视频生成 Prompt 助手"
	default:
		return "智能客服能力"
	}
}

func (h *Handler) Trace(c echo.Context) error {
	if !h.exposeInternal {
		return common.Fail(c, http.StatusNotFound, "not found")
	}
	if h.recorder == nil {
		return common.Fail(c, http.StatusServiceUnavailable, "trace recorder is not configured")
	}
	sessionID := strings.TrimSpace(c.Param("id"))
	if err := common.ValidateRequiredSessionID(sessionID); err != nil {
		return common.Fail(c, http.StatusBadRequest, err.Error())
	}
	session, found, err := h.store.Get(sessionID)
	if err != nil {
		return common.Fail(c, http.StatusInternalServerError, "session load failed")
	}
	if found && !h.canAccessSession(session) {
		return common.Fail(c, http.StatusForbidden, "unauthorized access to session of a different role")
	}
	opts, err := trace.ParseQueryOptionsFromParams(c.QueryParam("limit"), c.QueryParam("type"), c.QueryParam("status"), c.QueryParam("since_hours"))
	if err != nil {
		return common.Fail(c, http.StatusBadRequest, err.Error())
	}
	if opts.Active() {
		result, err := h.recorder.QueryWithError(sessionID, opts)
		if err != nil {
			return common.Fail(c, http.StatusInternalServerError, "trace load failed")
		}
		return common.Success(c, "trace", result)
	}
	events, err := h.recorder.ListWithError(sessionID)
	if err != nil {
		return common.Fail(c, http.StatusInternalServerError, "trace load failed")
	}
	return common.Success(c, "trace", events)
}

func (h *Handler) GetSession(c echo.Context) error {
	if h.store == nil {
		return common.Fail(c, http.StatusServiceUnavailable, "memory store is not configured")
	}
	sessionID := strings.TrimSpace(c.Param("id"))
	if err := common.ValidateRequiredSessionID(sessionID); err != nil {
		return common.Fail(c, http.StatusBadRequest, err.Error())
	}
	session, found, err := h.store.Get(sessionID)
	if err != nil {
		return common.Fail(c, http.StatusInternalServerError, "session load failed")
	}
	if !found {
		return common.Fail(c, http.StatusNotFound, "session not found")
	}
	if !h.canAccessSession(session) {
		return common.Fail(c, http.StatusForbidden, "unauthorized access to session of a different role")
	}
	return common.Success(c, "session", map[string]interface{}{
		"session": session,
	})
}

// ListSessions returns a list of sessions for operator.
func (h *Handler) ListSessions(c echo.Context) error {
	limit := 100 // default limit
	sessions, err := h.store.ListSummariesByRole(limit, string(aitypes.RoleOperator))
	if err != nil {
		return common.Fail(c, http.StatusInternalServerError, "failed to list sessions")
	}
	return common.Success(c, "sessions", map[string]interface{}{
		"sessions": sessions,
	})
}
