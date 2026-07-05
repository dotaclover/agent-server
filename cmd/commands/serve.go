package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"go-agent-studio/config"
	"go-agent-studio/server/router"
	"go-agent-studio/services/agentcore"
	"go-agent-studio/services/aitypes"
	"go-agent-studio/services/llm"
	mcpservice "go-agent-studio/services/mcp"
	"go-agent-studio/services/memory"
	"go-agent-studio/services/persistence"
	"go-agent-studio/services/security"
	"go-agent-studio/services/tools"
	"go-agent-studio/services/trace"
	"go-agent-studio/utils"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type ServeCommand struct{}

func (c *ServeCommand) Name() string        { return "serve" }
func (c *ServeCommand) Description() string { return "start HTTP server" }

func (c *ServeCommand) Execute(args []string) error {
	envResult := loadDotEnv()
	flags := parseKVArgs(args)
	cfg := config.Load()
	if flags["host"] != "" {
		cfg.App.Host = flags["host"]
	}
	if flags["port"] != "" {
		cfg.App.Port = flags["port"]
	}
	logDotEnv(envResult)
	logAIConfig(cfg)
	if err := config.ValidateForServe(cfg); err != nil {
		return fmt.Errorf("invalid serve config: %w", err)
	}

	mcpManager := mcpservice.NewManager()

	// Create separate tool registries for each tier
	customerRegistry := aitypes.NewToolRegistry()
	operatorRegistry := aitypes.NewToolRegistry()
	adminRegistry := aitypes.NewToolRegistry()

	// Register tools using the new structured approach
	llmProvider := llm.NewProviderFromConfig(cfg.AI, cfg.AI.RequestTimeout)

	// Customer tools (simple Q&A)
	customerTools := tools.NewCustomerTools(cfg.Customer.RAGAPIEndpoint)
	customerTools.RegisterAll(customerRegistry)

	// Operator tools (content creation)
	operatorTools := tools.NewOperatorTools(llmProvider)
	operatorTools.RegisterAll(operatorRegistry)

	// Admin tools (system management)
	runtime := tools.Runtime{
		Config:        cfg,
		MCP:           mcpManager,
		PublicTools:   customerRegistry,
		OperatorTools: operatorRegistry,
		AdminTools:    adminRegistry,
		Memory:        nil, // Will be set later
		Tracer:        nil, // Will be set later
		PromptDir:     "data/prompts",
	}
	adminTools := tools.NewAdminTools(runtime)
	adminTools.RegisterAll(adminRegistry)

	if cfg.MCP.Enabled {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		if err := mcpManager.LoadConfig(ctx, cfg.MCP.ConfigPath); err != nil {
			utils.Logger.Printf("[MCP] load failed: %v", err)
		} else if err := mcpManager.RegisterTools(ctx, adminRegistry); err != nil {
			utils.Logger.Printf("[MCP] register tools failed: %v", err)
		} else {
			// Keep public Customer focused on labor-law Q&A; expose MCP examples to Operator/Admin only.
			mcpManager.CopyTools(operatorRegistry, adminRegistry)
		}
		cancel()
	}
	defer mcpManager.Close()

	db, err := persistence.OpenSQLite(cfg.App.DBPath)
	if err != nil {
		return fmt.Errorf("open sqlite db: %w", err)
	}
	defer db.Close()
	utils.Logger.Printf("[DB] sqlite path=%s", cfg.App.DBPath)

	memStore := memory.NewSQLiteStore(db, 80)
	recorder := trace.NewSQLiteRecorder(db, 300)

	// Update runtime with memory and tracer
	runtime.Memory = memStore
	runtime.Tracer = recorder
	status := tools.BuildCapabilityStatus(runtime)

	providerFactory := func() aitypes.LLMProvider {
		return llm.NewProviderFromConfig(cfg.AI, cfg.AI.RequestTimeout)
	}
	agentFactory := func(registry *aitypes.ToolRegistry, role aitypes.AgentRole) *agentcore.Agent {
		return agentcore.New(providerFactory(), registry, recorder, agentcore.Config{
			SystemPrompt:       agentcore.SystemPromptForWithDir(role, "data/prompts"),
			MaxToolStepsPerRun: 4,
			Temperature:        cfg.AI.Temperature,
			MaxTokens:          cfg.AI.MaxTokens,
			ToolTimeout:        cfg.Agent.ToolTimeout,
			PlannerTimeout:     cfg.Agent.PlannerTimeout,
			AutoConfirmMCP:     cfg.Agent.AutoConfirmMCP,
			PromptDir:          "data/prompts",
		})
	}

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(middleware.Recover())
	e.Use(corsMiddleware(cfg.HTTP))
	if cfg.App.AccessLog {
		e.Use(accessLogger())
	}
	router.RegisterRoutes(e, router.Deps{
		Memory:        memStore,
		PublicTools:   customerRegistry,
		OperatorTools: operatorRegistry,
		AdminTools:    adminRegistry,
		Tracer:        recorder,
		MCP:           mcpManager,
		Auth:          cfg.Auth,
		Agent:         cfg.Agent,
		Customer:      cfg.Customer,
		Operator:      cfg.Operator,
		Admin:         cfg.Admin,
		RateLimit:     cfg.Rate,
		HTTP:          cfg.HTTP,
		DB:            db,
		Status:        status,
		AgentFactory:  agentFactory,
		LLMProvider:   llmProvider,
	})

	addr := serverListenAddress(cfg.App.Host, cfg.App.Port)
	server := &http.Server{
		Addr:         addr,
		ReadTimeout:  cfg.App.ReadTimeout,
		WriteTimeout: cfg.App.WriteTimeout,
		IdleTimeout:  cfg.App.IdleTimeout,
	}
	errCh := make(chan error, 1)
	go func() {
		utils.Logger.Printf("server listening on http://%s", addr)
		if err := e.StartServer(server); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), cfg.App.ShutdownTimeout)
		defer cancel()
		_ = e.Shutdown(ctx)
		errCh <- nil
	}()
	return <-errCh
}

func serverListenAddress(host, port string) string {
	return net.JoinHostPort(host, port)
}

func accessLogger() echo.MiddlewareFunc {
	return accessLoggerWithOutput(nil)
}

func corsMiddleware(cfg config.HTTPConfig) echo.MiddlewareFunc {
	allowedOrigins := cfg.CORSAllowedOrigins
	if len(allowedOrigins) == 0 {
		allowedOrigins = config.DefaultCORSAllowedOrigins()
	}
	return middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: allowedOrigins,
		AllowMethods: []string{
			http.MethodGet,
			http.MethodHead,
			http.MethodPost,
			http.MethodOptions,
		},
		AllowHeaders: []string{
			echo.HeaderOrigin,
			echo.HeaderContentType,
			echo.HeaderAccept,
			echo.HeaderAuthorization,
			echo.HeaderXRequestID,
			"X-Agent-API-Key",
		},
		ExposeHeaders: []string{echo.HeaderXRequestID},
		MaxAge:        86400,
	})
}

func accessLoggerWithOutput(output io.Writer) echo.MiddlewareFunc {
	if output == nil {
		output = os.Stdout
	}
	logger := log.New(output, "", 0)
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			err := next(c)
			req := c.Request()
			res := c.Response()
			status := res.Status
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
				"time":          time.Now().Format(time.RFC3339Nano),
				"request_id":    security.RedactSensitive(requestIDFromContext(c)),
				"remote_ip":     security.RedactSensitive(c.RealIP()),
				"host":          security.RedactSensitive(req.Host),
				"method":        req.Method,
				"path":          security.RedactSensitive(req.URL.Path),
				"route":         security.RedactSensitive(c.Path()),
				"protocol":      req.Proto,
				"status":        status,
				"latency_ns":    time.Since(start).Nanoseconds(),
				"latency_human": time.Since(start).String(),
				"bytes_in":      req.ContentLength,
				"bytes_out":     res.Size,
				"user_agent":    security.RedactSensitive(req.UserAgent()),
			}
			if req.ContentLength < 0 {
				entry["bytes_in"] = 0
			}
			if err != nil {
				entry["error"] = security.RedactSensitive(err.Error())
			} else {
				entry["error"] = ""
			}
			if data, marshalErr := json.Marshal(entry); marshalErr == nil {
				logger.Print(string(data))
			} else {
				logger.Printf(`{"event":"access_log","status":%d,"marshal_error":%q}`, status, security.RedactSensitive(marshalErr.Error()))
			}
			return err
		}
	}
}

func requestIDFromContext(c echo.Context) string {
	if c == nil {
		return ""
	}
	if id, ok := c.Get("request_id").(string); ok && id != "" {
		return id
	}
	if c.Request() != nil {
		if id := c.Request().Header.Get(echo.HeaderXRequestID); id != "" {
			return id
		}
	}
	if c.Response() != nil {
		return c.Response().Header().Get(echo.HeaderXRequestID)
	}
	return ""
}

func logDotEnv(result dotEnvLoadResult) {
	if result.Loaded {
		utils.Logger.Printf("[ENV] loaded .env path=%s cwd=%s keys=%d applied=%d", result.Path, result.CWD, result.Keys, result.Applied)
		return
	}
	utils.Logger.Printf("[ENV] .env not loaded path=%s cwd=%s error=%s", result.Path, result.CWD, result.Error)
}

func logAIConfig(cfg *config.Config) {
	summary := aiRuntimeSummary(cfg.AI)
	utils.Logger.Printf("[AI] chat provider=%s model=%s base_url=%s api_key_present=%t", summary.ChatProvider, summary.ChatModel, summary.ChatBaseURL, summary.ChatAPIKeyPresent)
}

type aiRuntimeSummaryInfo struct {
	ChatProvider      string
	ChatModel         string
	ChatBaseURL       string
	ChatAPIKeyPresent bool
}

func aiRuntimeSummary(ai config.AIConfig) aiRuntimeSummaryInfo {
	return aiRuntimeSummaryInfo{
		ChatProvider:      ai.Provider,
		ChatModel:         ai.ChatModel,
		ChatBaseURL:       security.SafeURLOrigin(ai.BaseURL),
		ChatAPIKeyPresent: ai.APIKey != "",
	}
}

func ensureParent(path string) error {
	dir := ""
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			dir = path[:i]
			break
		}
	}
	if dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}

func printDone(format string, args ...interface{}) {
	fmt.Printf(format+"\n", args...)
}
