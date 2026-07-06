package router

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"fmt"
	"go-agent-studio/common"
	"go-agent-studio/config"
	customermodule "go-agent-studio/modules/customer"
	mcpmodule "go-agent-studio/modules/mcp"
	operatormodule "go-agent-studio/modules/operator"
	"go-agent-studio/server/web"
	"go-agent-studio/services/agentcore"
	"go-agent-studio/services/aitypes"
	"go-agent-studio/services/buildinfo"
	mcpservice "go-agent-studio/services/mcp"
	"go-agent-studio/services/memory"
	"go-agent-studio/services/persistence"
	"go-agent-studio/services/security"
	"go-agent-studio/services/simplechat"
	"go-agent-studio/services/tools"
	"go-agent-studio/services/trace"
	"go-agent-studio/utils"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	xrate "golang.org/x/time/rate"
)

type Deps struct {
	Memory        *memory.Store
	PublicTools   *aitypes.ToolRegistry
	OperatorTools *aitypes.ToolRegistry
	AdminTools    *aitypes.ToolRegistry
	Tracer        *trace.Recorder
	MCP           *mcpservice.Manager
	Auth          config.AuthConfig
	Agent         config.AgentConfig
	Customer      config.CustomerConfig
	Operator      config.OperatorConfig
	Admin         config.AdminConfig
	RateLimit     config.RateLimitConfig
	HTTP          config.HTTPConfig
	DB            *sql.DB
	AuditLogger   *log.Logger
	Status        tools.CapabilityStatus
	AgentFactory  func(*aitypes.ToolRegistry, aitypes.AgentRole) *agentcore.Agent
	LLMProvider   aitypes.LLMProvider
}

type ReadyResponse struct {
	Status string                 `json:"status"`
	Checks map[string]interface{} `json:"checks"`
}

const (
	adminSessionListDefaultLimit  = 50
	adminSessionListMaxLimit      = 100
	adminSessionDetailMaxMessages = 200
	adminSessionUpdatedMaxHours   = 24 * 365 * 10
	adminCleanupMaxOlderThanHours = 24 * 365 * 10

	headerCrossOriginOpenerPolicy       = "Cross-Origin-Opener-Policy"
	headerPermissionsPolicy             = "Permissions-Policy"
	headerXPermittedCrossDomainPolicies = "X-Permitted-Cross-Domain-Policies"
)

func RegisterRoutes(e *echo.Echo, deps Deps) {
	e.HTTPErrorHandler = httpErrorHandler
	e.Use(requestID())
	if deps.HTTP.SecureHeadersEnabled {
		e.Use(secureHeaders(deps.HTTP))
	}
	liveHandler := func(c echo.Context) error {
		return liveHealth(c)
	}
	readyHandler := func(c echo.Context) error {
		return readyHealth(c, deps)
	}
	e.GET("/health/live", liveHandler)
	e.HEAD("/health/live", liveHandler)
	e.GET("/health/ready", readyHandler)
	e.HEAD("/health/ready", readyHandler)

	api := e.Group("/api/v1")
	api.Use(apiNoStore())
	if deps.HTTP.BodyLimit != "" {
		api.Use(middleware.BodyLimit(deps.HTTP.BodyLimit))
	}
	if deps.RateLimit.Enabled {
		api.Use(rateLimiter(deps.RateLimit))
	}
	auditLogger := internalAuditLogger(deps)

	// Customer端 (公开，产品文档问答)
	var customerPrompt string
	if data, err := os.ReadFile(filepath.Join("data/prompts", "agent_customer.txt")); err == nil {
		customerPrompt = string(data)
	} else {
		utils.Logger.Printf("[Router] warning: failed to load agent_customer.txt prompt file: %v", err)
	}

	customermodule := customermodule.NewHandler(
		simplechat.New(deps.LLMProvider, deps.PublicTools, deps.Tracer, simplechat.Config{
			MaxTurns:     deps.Customer.MaxTurns,
			ChatTimeout:  deps.Agent.ChatTimeout,
			ToolTimeout:  deps.Agent.ToolTimeout,
			SystemPrompt: customerPrompt,
		}),
		deps.Memory,
		deps.PublicTools,
		deps.Tracer,
		customermodule.HandlerOptions{
			MaxMessageChars:  deps.Agent.MaxMessageChars,
			MaxTurns:         deps.Customer.MaxTurns,
			MaxDailyVisitors: deps.Customer.MaxDailyVisitors,
		},
	)
	customermodule.RegisterRoutes(api.Group("/customer"))

	// Operator端 (需要API Key，写作助手)
	if deps.Auth.OperatorAPIKey != "" && deps.OperatorTools != nil {
		operator := api.Group("/operator")
		if auditLogger != nil {
			operator.Use(internalAudit("operator", auditLogger))
		}
		operator.Use(keyAuth(deps.Auth.OperatorAPIKey))
		operatormodule.NewHandlerWithConfig(deps.Memory, deps.OperatorTools, deps.Tracer, deps.AgentFactory, operatormodule.HandlerOptions{
			Role:            aitypes.RoleOperator,
			ExposeInternal:  true,
			MaxMessageChars: deps.Agent.MaxMessageChars,
			ChatTimeout:     deps.Agent.ChatTimeout,
			MaxTurns:        deps.Operator.MaxTurns,
		}).RegisterRoutes(operator.Group("/agent"))
	}
	if deps.Auth.AdminAPIKey != "" && deps.AdminTools != nil {
		admin := api.Group("/admin")
		if auditLogger != nil {
			admin.Use(internalAudit("admin", auditLogger))
		}
		admin.Use(keyAuth(deps.Auth.AdminAPIKey))
		admin.GET("/status", func(c echo.Context) error {
			return common.Success(c, "agent status", deps.Status)
		})
		admin.GET("/sessions", func(c echo.Context) error {
			return listAdminSessions(c, deps)
		})
		admin.GET("/sessions/stats", func(c echo.Context) error {
			return getAdminSessionStats(c, deps)
		})
		admin.POST("/sessions/cleanup", func(c echo.Context) error {
			return cleanupAdminSessions(c, deps)
		})
		mcpmodule.NewHandler(deps.MCP).RegisterRoutes(admin.Group("/mcp"))
		admin.GET("/sessions/:id/trace", func(c echo.Context) error {
			return getAdminSessionTrace(c, deps)
		})
		admin.GET("/sessions/:id", func(c echo.Context) error {
			return getAdminSession(c, deps)
		})
	}

	web.Register(e)
}

func liveHealth(c echo.Context) error {
	if c.Request().Method == http.MethodHead {
		return c.NoContent(http.StatusOK)
	}
	return common.Success(c, "live", map[string]interface{}{"status": "live", "build": buildinfo.Current()})
}

func readyHealth(c echo.Context, deps Deps) error {
	ready := buildReadyResponse(c.Request().Context(), deps)
	publicReady := publicReadyResponse(ready)
	if ready.Status != "ready" {
		if c.Request().Method == http.MethodHead {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		return c.JSON(http.StatusServiceUnavailable, common.APIResponse{
			Code:      http.StatusServiceUnavailable,
			Message:   "not ready",
			Data:      publicReady,
			RequestID: common.RequestID(c),
		})
	}
	if c.Request().Method == http.MethodHead {
		return c.NoContent(http.StatusOK)
	}
	return common.Success(c, "ready", publicReady)
}

type cleanupSessionsRequest struct {
	OlderThanHours int    `json:"older_than_hours"`
	DryRun         bool   `json:"dry_run"`
	ConfirmDelete  bool   `json:"confirm_delete"`
	Role           string `json:"role"`
}

type cleanupSessionsResponse struct {
	DeletedSessions int      `json:"deleted_sessions"`
	MatchedSessions int      `json:"matched_sessions"`
	SessionIDs      []string `json:"session_ids"`
	DryRun          bool     `json:"dry_run"`
	Role            string   `json:"role,omitempty"`
}

func cleanupAdminSessions(c echo.Context, deps Deps) error {
	if deps.Memory == nil {
		return common.Fail(c, http.StatusServiceUnavailable, "memory store is not configured")
	}
	if err := common.RequireJSONContentType(c); err != nil {
		return common.Fail(c, http.StatusUnsupportedMediaType, err.Error())
	}
	var req cleanupSessionsRequest
	if err := common.BindJSONStrict(c, &req); err != nil {
		return common.Fail(c, http.StatusBadRequest, "invalid request body")
	}
	if req.OlderThanHours <= 0 || req.OlderThanHours > adminCleanupMaxOlderThanHours {
		return common.Fail(c, http.StatusBadRequest, fmt.Sprintf("older_than_hours must be between 1 and %d", adminCleanupMaxOlderThanHours))
	}
	roleFilter, err := parseAdminSessionRoleFilter(req.Role)
	if err != nil {
		return common.Fail(c, http.StatusBadRequest, err.Error())
	}
	cutoff := time.Now().Add(-time.Duration(req.OlderThanHours) * time.Hour)
	if req.DryRun {
		sessionIDs, err := deps.Memory.ListOlderThanByRole(cutoff, roleFilter)
		if err != nil {
			return common.Fail(c, http.StatusInternalServerError, "session cleanup preview failed")
		}
		sort.Strings(sessionIDs)
		return common.Success(c, "sessions cleanup preview", cleanupSessionsResponse{
			DeletedSessions: 0,
			MatchedSessions: len(sessionIDs),
			SessionIDs:      sessionIDs,
			DryRun:          true,
			Role:            roleFilter,
		})
	}
	if !req.ConfirmDelete {
		return common.Fail(c, http.StatusBadRequest, "confirm_delete must be true for session cleanup")
	}
	sessionIDs, err := deps.Memory.DeleteOlderThanByRole(cutoff, roleFilter)
	if err != nil {
		return common.Fail(c, http.StatusInternalServerError, "session cleanup failed")
	}
	sort.Strings(sessionIDs)
	if deps.Tracer != nil {
		for _, sessionID := range sessionIDs {
			if err := deps.Tracer.DeleteSession(sessionID); err != nil {
				return common.Fail(c, http.StatusInternalServerError, "trace cleanup failed")
			}
		}
	}
	return common.Success(c, "sessions cleaned up", cleanupSessionsResponse{
		DeletedSessions: len(sessionIDs),
		MatchedSessions: len(sessionIDs),
		SessionIDs:      sessionIDs,
		DryRun:          false,
		Role:            roleFilter,
	})
}

func listAdminSessions(c echo.Context, deps Deps) error {
	if deps.Memory == nil {
		return common.Fail(c, http.StatusServiceUnavailable, "memory store is not configured")
	}
	limit, err := parseAdminSessionListLimit(c.QueryParam("limit"))
	if err != nil {
		return common.Fail(c, http.StatusBadRequest, err.Error())
	}
	filters := map[string]interface{}{"limit": limit}
	roleFilter, err := parseAdminSessionRoleFilter(c.QueryParam("role"))
	if err != nil {
		return common.Fail(c, http.StatusBadRequest, err.Error())
	}
	if roleFilter != "" {
		filters["role"] = roleFilter
	}
	var sessions []memory.SessionSummary
	if raw := strings.TrimSpace(c.QueryParam("updated_within_hours")); raw != "" {
		hours, parseErr := strconv.Atoi(raw)
		if parseErr != nil || hours <= 0 || hours > adminSessionUpdatedMaxHours {
			return common.Fail(c, http.StatusBadRequest, fmt.Sprintf("updated_within_hours must be between 1 and %d", adminSessionUpdatedMaxHours))
		}
		since := time.Now().Add(-time.Duration(hours) * time.Hour)
		sessions, err = deps.Memory.ListSummariesUpdatedSinceByRole(limit, since, roleFilter)
		filters["updated_within_hours"] = hours
		filters["updated_since"] = since.UTC().Format(time.RFC3339Nano)
	} else {
		sessions, err = deps.Memory.ListSummariesByRole(limit, roleFilter)
	}
	if err != nil {
		return common.Fail(c, http.StatusInternalServerError, "session list failed")
	}
	return common.Success(c, "sessions", map[string]interface{}{
		"sessions": sessions,
		"count":    len(sessions),
		"filters":  filters,
	})
}

func parseAdminSessionListLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return adminSessionListDefaultLimit, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("limit must be an integer between 1 and %d", adminSessionListMaxLimit)
	}
	if value <= 0 || value > adminSessionListMaxLimit {
		return 0, fmt.Errorf("limit must be between 1 and %d", adminSessionListMaxLimit)
	}
	return value, nil
}

func parseAdminSessionRoleFilter(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	role := memory.NormalizeSessionRoleFilter(raw)
	if role == "" {
		return "", fmt.Errorf("role must be one of customer, operator, admin, legacy")
	}
	return role, nil
}

func getAdminSessionStats(c echo.Context, deps Deps) error {
	if deps.Memory == nil {
		return common.Fail(c, http.StatusServiceUnavailable, "memory store is not configured")
	}
	stats, err := deps.Memory.Stats()
	if err != nil {
		return common.Fail(c, http.StatusInternalServerError, "session stats failed")
	}
	return common.Success(c, "session stats", stats)
}

func getAdminSession(c echo.Context, deps Deps) error {
	if deps.Memory == nil {
		return common.Fail(c, http.StatusServiceUnavailable, "memory store is not configured")
	}
	sessionID := strings.TrimSpace(c.Param("id"))
	if err := common.ValidateRequiredSessionID(sessionID); err != nil {
		return common.Fail(c, http.StatusBadRequest, err.Error())
	}
	session, found, err := deps.Memory.Get(sessionID)
	if err != nil {
		return common.Fail(c, http.StatusInternalServerError, "session load failed")
	}
	if !found {
		return common.Fail(c, http.StatusNotFound, "session not found")
	}
	originalMessages := session.Messages
	totalMessages := len(originalMessages)
	truncated := false
	maxMessages, err := parseAdminSessionDetailMaxMessages(c.QueryParam("max_messages"))
	if err != nil {
		return common.Fail(c, http.StatusBadRequest, err.Error())
	}
	if maxMessages > 0 && totalMessages > maxMessages {
		session.Messages = append([]aitypes.Message(nil), originalMessages[totalMessages-maxMessages:]...)
		truncated = true
	}
	return common.Success(c, "session", map[string]interface{}{
		"session":           session,
		"total_messages":    totalMessages,
		"returned_messages": len(session.Messages),
		"truncated":         truncated,
		"summary": memory.SessionSummary{
			ID:                    session.ID,
			Role:                  session.Role,
			MessageCount:          totalMessages,
			UserMessageCount:      countMessages(originalMessages, aitypes.RoleUser),
			AssistantMessageCount: countMessages(originalMessages, aitypes.RoleAssistant),
			CreatedAt:             session.CreatedAt,
			UpdatedAt:             session.UpdatedAt,
		},
	})
}

func parseAdminSessionDetailMaxMessages(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("max_messages must be an integer between 1 and %d", adminSessionDetailMaxMessages)
	}
	if value > adminSessionDetailMaxMessages {
		return 0, fmt.Errorf("max_messages must be between 1 and %d", adminSessionDetailMaxMessages)
	}
	if value <= 0 {
		return 0, fmt.Errorf("max_messages must be between 1 and %d", adminSessionDetailMaxMessages)
	}
	return value, nil
}

func getAdminSessionTrace(c echo.Context, deps Deps) error {
	if deps.Tracer == nil {
		return common.Fail(c, http.StatusServiceUnavailable, "trace recorder is not configured")
	}
	sessionID := strings.TrimSpace(c.Param("id"))
	if err := common.ValidateRequiredSessionID(sessionID); err != nil {
		return common.Fail(c, http.StatusBadRequest, err.Error())
	}
	opts, err := traceQueryOptions(c)
	if err != nil {
		return common.Fail(c, http.StatusBadRequest, err.Error())
	}
	if opts.Active() {
		result, err := deps.Tracer.QueryWithError(sessionID, opts)
		if err != nil {
			return common.Fail(c, http.StatusInternalServerError, "trace load failed")
		}
		return common.Success(c, "trace", result)
	}
	events, err := deps.Tracer.ListWithError(sessionID)
	if err != nil {
		return common.Fail(c, http.StatusInternalServerError, "trace load failed")
	}
	return common.Success(c, "trace", events)
}

func traceQueryOptions(c echo.Context) (trace.QueryOptions, error) {
	return parseTraceQueryOptions(c.QueryParam("limit"), c.QueryParam("type"), c.QueryParam("status"), c.QueryParam("since_hours"))
}

func parseTraceQueryOptions(rawLimit, eventType, status, sinceHours string) (trace.QueryOptions, error) {
	return trace.ParseQueryOptionsFromParams(rawLimit, eventType, status, sinceHours)
}

func countMessages(messages []aitypes.Message, role aitypes.Role) int {
	count := 0
	for _, message := range messages {
		if message.Role == role {
			count++
		}
	}
	return count
}

func buildReadyResponse(ctx context.Context, deps Deps) ReadyResponse {
	checks := map[string]interface{}{}
	ready := true

	dbCheck := map[string]interface{}{"configured": deps.DB != nil, "ok": deps.DB != nil}
	if deps.DB != nil {
		pingCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		err := deps.DB.PingContext(pingCtx)
		cancel()
		if err != nil {
			dbCheck["ok"] = false
			dbCheck["error"] = err.Error()
		}
		sqliteStatus := persistence.InspectSQLite(deps.DB)
		dbCheck["sqlite"] = sqliteStatus
		if !sqliteStatus.OK {
			dbCheck["ok"] = false
			dbCheck["error"] = sqliteStatus.Error
		}
	}
	if ok, _ := dbCheck["ok"].(bool); !ok {
		ready = false
	}
	checks["database"] = dbCheck

	chatCheck := map[string]interface{}{"configured": false, "ok": false}
	if deps.Status.Chat != nil {
		for key, value := range deps.Status.Chat {
			chatCheck[key] = value
		}
	}
	if configured, _ := chatCheck["configured"].(bool); configured {
		chatCheck["ok"] = true
	} else {
		chatCheck["error"] = "chat provider is not configured"
		ready = false
	}
	checks["ai_chat"] = chatCheck

	memoryCheck := map[string]interface{}{"configured": deps.Memory != nil, "ok": false, "persistent": false}
	if deps.Memory != nil {
		persistent := deps.Memory.Persistent()
		memoryCheck["persistent"] = persistent
		if !persistent {
			memoryCheck["error"] = "memory store is not persistent"
		} else if stats, err := deps.Memory.Stats(); err != nil {
			memoryCheck["ok"] = false
			memoryCheck["error"] = err.Error()
		} else {
			memoryCheck["ok"] = true
			memoryCheck["stats"] = stats
		}
	}
	if ok, _ := memoryCheck["ok"].(bool); !ok {
		ready = false
	}
	checks["memory"] = memoryCheck

	traceCheck := map[string]interface{}{"configured": deps.Tracer != nil, "ok": false, "persistent": false}
	if deps.Tracer != nil {
		persistent := deps.Tracer.Persistent()
		traceCheck["persistent"] = persistent
		if !persistent {
			traceCheck["error"] = "trace recorder is not persistent"
		} else if stats, err := deps.Tracer.Stats(); err != nil {
			traceCheck["ok"] = false
			traceCheck["error"] = err.Error()
		} else {
			traceCheck["ok"] = true
			traceCheck["stats"] = stats
		}
	}
	if ok, _ := traceCheck["ok"].(bool); !ok {
		ready = false
	}
	checks["trace"] = traceCheck

	if deps.MCP != nil {
		checks["mcp"] = deps.MCP.Status()
	} else {
		checks["mcp"] = map[string]interface{}{"count": 0, "servers": []string{}}
	}

	status := "ready"
	if !ready {
		status = "not_ready"
	}
	return ReadyResponse{Status: status, Checks: redactReadyDiagnostics(checks)}
}

func publicReadyResponse(ready ReadyResponse) ReadyResponse {
	checks := map[string]interface{}{}
	for name, raw := range ready.Checks {
		check, ok := raw.(map[string]interface{})
		if !ok {
			checks[name] = raw
			continue
		}
		publicCheck := map[string]interface{}{}
		for _, key := range []string{"ok", "configured", "loaded", "persistent", "count"} {
			if value, exists := check[key]; exists {
				publicCheck[key] = value
			}
		}
		checks[name] = publicCheck
	}
	return ReadyResponse{Status: ready.Status, Checks: checks}
}

func redactReadyDiagnostics(checks map[string]interface{}) map[string]interface{} {
	if checks == nil {
		return nil
	}
	out := make(map[string]interface{}, len(checks))
	for key, value := range checks {
		out[key] = redactReadyDiagnosticValue(value)
	}
	return out
}

func redactReadyDiagnosticValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, child := range v {
			out[key] = redactReadyDiagnosticValue(child)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, child := range v {
			out[i] = redactReadyDiagnosticValue(child)
		}
		return out
	case []string:
		out := make([]string, len(v))
		for i, child := range v {
			out[i] = security.RedactSensitive(child)
		}
		return out
	case persistence.SQLiteStatus:
		v.Error = security.RedactSensitive(v.Error)
		return v
	case string:
		return security.RedactSensitive(v)
	case error:
		return security.RedactSensitive(v.Error())
	default:
		return v
	}
}

func httpErrorHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}
	code := http.StatusInternalServerError
	message := http.StatusText(code)
	if he, ok := err.(*echo.HTTPError); ok {
		code = he.Code
		if text := publicHTTPErrorMessage(he.Message, code); text != "" {
			message = text
		}
	}
	if writeErr := common.Fail(c, code, message); writeErr != nil {
		c.Logger().Error(writeErr)
	}
}

func publicHTTPErrorMessage(value interface{}, code int) string {
	if code >= http.StatusInternalServerError {
		return http.StatusText(code)
	}
	return httpErrorMessage(value, code)
}

func httpErrorMessage(value interface{}, code int) string {
	switch v := value.(type) {
	case string:
		return security.RedactSensitive(v)
	case error:
		return security.RedactSensitive(v.Error())
	default:
		return http.StatusText(code)
	}
}

func requestID() echo.MiddlewareFunc {
	return middleware.RequestIDWithConfig(middleware.RequestIDConfig{
		TargetHeader: echo.HeaderXRequestID,
		RequestIDHandler: func(c echo.Context, id string) {
			c.Set("request_id", id)
		},
	})
}

func apiNoStore() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if c.Response().Header().Get(echo.HeaderCacheControl) == "" {
				c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
			}
			return next(c)
		}
	}
}

func secureHeaders(cfg config.HTTPConfig) echo.MiddlewareFunc {
	base := middleware.SecureWithConfig(middleware.SecureConfig{
		XSSProtection:      "1; mode=block",
		ContentTypeNosniff: "nosniff",
		XFrameOptions:      "SAMEORIGIN",
		HSTSMaxAge:         cfg.HSTSMaxAge,
		ReferrerPolicy:     "strict-origin-when-cross-origin",
		ContentSecurityPolicy: strings.Join([]string{
			"default-src 'self'",
			"base-uri 'self'",
			"form-action 'self'",
			"frame-ancestors 'self'",
			"object-src 'none'",
			"script-src 'self'",
			"style-src 'self'",
			"img-src 'self' data:",
			"connect-src 'self'",
		}, "; "),
	})
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return base(func(c echo.Context) error {
			headers := c.Response().Header()
			headers.Set(headerCrossOriginOpenerPolicy, "same-origin")
			headers.Set(headerPermissionsPolicy, "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()")
			headers.Set(headerXPermittedCrossDomainPolicies, "none")
			return next(c)
		})
	}
}

func rateLimiter(cfg config.RateLimitConfig) echo.MiddlewareFunc {
	rps := cfg.RPS
	if rps <= 0 {
		rps = 20
	}
	burst := cfg.Burst
	if burst <= 0 {
		burst = int(rps)
		if burst <= 0 {
			burst = 1
		}
	}
	retryAfterSeconds := int(math.Ceil(1 / rps))
	if retryAfterSeconds < 1 {
		retryAfterSeconds = 1
	}
	return middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		IdentifierExtractor: rateLimitIdentifier,
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
			Rate:  xrate.Limit(rps),
			Burst: burst,
		}),
		DenyHandler: func(c echo.Context, identifier string, err error) error {
			c.Response().Header().Set(echo.HeaderRetryAfter, strconv.Itoa(retryAfterSeconds))
			return common.Fail(c, http.StatusTooManyRequests, "rate limit exceeded")
		},
	})
}

func rateLimitIdentifier(c echo.Context) (string, error) {
	if c == nil || c.Request() == nil {
		return "", nil
	}
	remoteAddr := strings.TrimSpace(c.Request().RemoteAddr)
	if remoteAddr == "" {
		return "", nil
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil || strings.TrimSpace(host) == "" {
		return remoteAddr, nil
	}
	return host, nil
}

func internalAuditLogger(deps Deps) *log.Logger {
	if !deps.Auth.AuditLog {
		return nil
	}
	if deps.AuditLogger != nil {
		return deps.AuditLogger
	}
	return utils.Logger
}

func internalAudit(role string, logger *log.Logger) echo.MiddlewareFunc {
	if logger == nil {
		logger = utils.Logger
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			err := next(c)
			status := c.Response().Status
			if status == 0 {
				status = http.StatusOK
			}
			if err != nil {
				if he, ok := err.(*echo.HTTPError); ok {
					status = he.Code
				} else if status < http.StatusBadRequest {
					status = http.StatusInternalServerError
				}
			}
			entry := map[string]interface{}{
				"event":      "internal_api_access",
				"role":       role,
				"request_id": security.RedactSensitive(common.RequestID(c)),
				"remote_ip":  security.RedactSensitive(c.RealIP()),
				"method":     c.Request().Method,
				"path":       security.RedactSensitive(c.Request().URL.Path),
				"route":      security.RedactSensitive(c.Path()),
				"status":     status,
				"latency_ns": time.Since(start).Nanoseconds(),
				"user_agent": security.RedactSensitive(c.Request().UserAgent()),
			}
			if err != nil {
				entry["error"] = security.RedactSensitive(httpErrorMessage(err, status))
			}
			if data, marshalErr := json.Marshal(entry); marshalErr == nil {
				logger.Print(string(data))
			} else {
				logger.Printf(`{"event":"internal_api_access","role":%q,"status":%d,"marshal_error":%q}`, role, status, marshalErr.Error())
			}
			return err
		}
	}
}

func keyAuth(expected string) echo.MiddlewareFunc {
	expectedHash := sha256.Sum256([]byte(expected))
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			key := internalAPIKeyFromRequest(c.Request())
			keyHash := sha256.Sum256([]byte(key))
			if key == "" || subtle.ConstantTimeCompare(keyHash[:], expectedHash[:]) != 1 {
				return common.Fail(c, http.StatusUnauthorized, "unauthorized")
			}
			return next(c)
		}
	}
}

func internalAPIKeyFromRequest(req *http.Request) string {
	if req == nil {
		return ""
	}
	if key := strings.TrimSpace(req.Header.Get("X-Agent-API-Key")); key != "" {
		return key
	}
	auth := strings.TrimSpace(req.Header.Get(echo.HeaderAuthorization))
	if auth == "" {
		return ""
	}
	scheme, token, ok := strings.Cut(auth, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}
