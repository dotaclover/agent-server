package tools

import (
	"go-agent-studio/config"
	"go-agent-studio/services/aitypes"
	"go-agent-studio/services/buildinfo"
	mcpservice "go-agent-studio/services/mcp"
	"go-agent-studio/services/memory"
	"go-agent-studio/services/security"
	"go-agent-studio/services/tools/admin"
	"go-agent-studio/services/tools/customer"
	"go-agent-studio/services/tools/operator"
	"go-agent-studio/services/trace"
	"path/filepath"
	"strings"
)

type Runtime struct {
	Config        *config.Config
	MCP           *mcpservice.Manager
	Memory        *memory.Store
	Tracer        *trace.Recorder
	PublicTools   *aitypes.ToolRegistry
	OperatorTools *aitypes.ToolRegistry
	AdminTools    *aitypes.ToolRegistry
	PromptDir     string
}

type CapabilityStatus struct {
	App       map[string]interface{} `json:"app"`
	Chat      map[string]interface{} `json:"chat"`
	Agent     map[string]interface{} `json:"agent"`
	HTTP      map[string]interface{} `json:"http"`
	RateLimit map[string]interface{} `json:"rate_limit"`
	Persist   map[string]interface{} `json:"persistence"`
	MCP       map[string]interface{} `json:"mcp"`
	Tools     map[string]interface{} `json:"tools"`
}

// CustomerTools wraps customer-facing tool registration
type CustomerTools struct {
	ragAPIEndpoint string
}

// NewCustomerTools creates a new customer tools instance
func NewCustomerTools(ragAPIEndpoint string) *CustomerTools {
	return &CustomerTools{
		ragAPIEndpoint: ragAPIEndpoint,
	}
}

// RegisterAll registers all customer tools
func (ct *CustomerTools) RegisterAll(registry *aitypes.ToolRegistry) {
	customer.RegisterTools(registry, customer.Config{
		RAGAPIEndpoint: ct.ragAPIEndpoint,
	})
}

type OperatorTools struct {
	provider aitypes.LLMProvider
}

func NewOperatorTools(provider aitypes.LLMProvider) *OperatorTools {
	return &OperatorTools{
		provider: provider,
	}
}

// RegisterAll registers all operator tools
func (ot *OperatorTools) RegisterAll(registry *aitypes.ToolRegistry) {
	operator.RegisterTools(registry, operator.Config{
		LLMProvider: ot.provider,
	})
}

// AdminTools wraps admin-facing tool registration
type AdminTools struct {
	runtime Runtime
}

// NewAdminTools creates a new admin tools instance
func NewAdminTools(runtime Runtime) *AdminTools {
	return &AdminTools{runtime: runtime}
}

// RegisterAll registers all admin tools
func (at *AdminTools) RegisterAll(registry *aitypes.ToolRegistry) {
	admin.RegisterTools(registry, admin.Config{
		StatusBuilder: func() interface{} {
			return BuildCapabilityStatus(at.runtime)
		},
	})
}

func BuildCapabilityStatus(rt Runtime) CapabilityStatus {
	cfg := rt.Config
	if cfg == nil {
		cfg = config.Load()
	}
	mcpStatus := map[string]interface{}{
		"enabled":           cfg.MCP.Enabled,
		"config_configured": strings.TrimSpace(cfg.MCP.ConfigPath) != "",
		"config_file":       safeConfigFileName(cfg.MCP.ConfigPath),
		"count":             0,
		"servers":           []string{},
	}
	if rt.MCP != nil {
		for key, value := range rt.MCP.Status() {
			mcpStatus[key] = value
		}
	}
	build := buildinfo.Current()
	chatConfigured := cfg.AI.APIKey != ""
	return CapabilityStatus{
		App: map[string]interface{}{
			"name":    cfg.App.Name,
			"debug":   cfg.App.Debug,
			"version": build.Version,
			"build":   build.Build,
		},
		Chat: map[string]interface{}{
			"provider":        cfg.AI.Provider,
			"base_url":        security.SafeURLOrigin(cfg.AI.BaseURL),
			"model":           cfg.AI.ChatModel,
			"api_key_present": cfg.AI.APIKey != "",
			"configured":      chatConfigured,
			"timeout":         cfg.AI.RequestTimeout.String(),
			"temperature":     cfg.AI.Temperature,
			"max_tokens":      cfg.AI.MaxTokens,
		},
		Agent: map[string]interface{}{
			"max_message_chars": cfg.Agent.MaxMessageChars,
			"chat_timeout":      cfg.Agent.ChatTimeout.String(),
			"planner_timeout":   cfg.Agent.PlannerTimeout.String(),
			"tool_timeout":      cfg.Agent.ToolTimeout.String(),
			"auto_confirm_mcp":  cfg.Agent.AutoConfirmMCP,
			"planner":           "llm",
		},
		HTTP: map[string]interface{}{
			"body_limit":             cfg.HTTP.BodyLimit,
			"cors_allowed_origins":   cfg.HTTP.CORSAllowedOrigins,
			"secure_headers_enabled": cfg.HTTP.SecureHeadersEnabled,
			"hsts_max_age":           cfg.HTTP.HSTSMaxAge,
			"access_log_enabled":     cfg.App.AccessLog,
		},
		RateLimit: map[string]interface{}{
			"enabled": cfg.Rate.Enabled,
			"rps":     cfg.Rate.RPS,
			"burst":   cfg.Rate.Burst,
		},
		Persist: map[string]interface{}{
			"database_configured": cfg.App.DBPath != "",
			"memory_persistent":   rt.MemoryPersistent(),
			"trace_persistent":    rt.TracePersistent(),
		},
		MCP: mcpStatus,
		Tools: map[string]interface{}{
			"customer": registryCapabilityStatus(rt.PublicTools),
			"public":   registryCapabilityStatus(rt.PublicTools),
			"operator": registryCapabilityStatus(rt.OperatorTools),
			"admin":    registryCapabilityStatus(rt.AdminTools),
		},
	}
}

func (rt Runtime) MemoryPersistent() bool {
	return rt.Memory != nil && rt.Memory.Persistent()
}

func (rt Runtime) TracePersistent() bool {
	return rt.Tracer != nil && rt.Tracer.Persistent()
}

func safeConfigFileName(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Base(filepath.Clean(path))
}

func registryCapabilityStatus(registry *aitypes.ToolRegistry) map[string]interface{} {
	names := []string{}
	if registry != nil {
		names = registry.Names()
	}
	return map[string]interface{}{
		"count": len(names),
		"names": names,
	}
}
