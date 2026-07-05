package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gommonbytes "github.com/labstack/gommon/bytes"
)

type Config struct {
	App      AppConfig
	AI       AIConfig
	MCP      MCPConfig
	Auth     AuthConfig
	Agent    AgentConfig
	Customer CustomerConfig
	Operator OperatorConfig
	Admin    AdminConfig
	Rate     RateLimitConfig
	HTTP     HTTPConfig
}

type AppConfig struct {
	Name            string
	Host            string
	Port            string
	DBPath          string
	Debug           bool
	AccessLog       bool
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

type AIConfig struct {
	Provider       string
	APIKey         string
	BaseURL        string
	ChatModel      string
	Temperature    float64
	MaxTokens      int
	RequestTimeout time.Duration
}

type MCPConfig struct {
	ConfigPath string
	Enabled    bool
}

type AuthConfig struct {
	OperatorAPIKey string
	AdminAPIKey    string
	AuditLog       bool
}

type AgentConfig struct {
	MaxMessageChars int
	ChatTimeout     time.Duration
	ToolTimeout     time.Duration
	PlannerTimeout  time.Duration
	AutoConfirmMCP  bool
}

type CustomerConfig struct {
	MaxTurns         int
	MaxDailyVisitors int
	RAGAPIEndpoint   string
}

type OperatorConfig struct {
	MaxTurns int
}

type AdminConfig struct {
	SessionRetentionDays int
}

type RateLimitConfig struct {
	Enabled bool
	RPS     float64
	Burst   int
}

type HTTPConfig struct {
	BodyLimit            string
	CORSAllowedOrigins   []string
	SecureHeadersEnabled bool
	HSTSMaxAge           int
}

const internalAPIKeyMinLength = 6

func DefaultCORSAllowedOrigins() []string {
	return []string{
		"http://127.0.0.1:9090",
		"http://localhost:9090",
	}
}

func Load() *Config {
	return &Config{
		App: AppConfig{
			Name:            env("APP_NAME", "Go Agent Studio"),
			Host:            env("APP_HOST", "127.0.0.1"),
			Port:            env("APP_PORT", "9090"),
			DBPath:          env("APP_DB_PATH", "data/agent_studio.db"),
			Debug:           envBool("APP_DEBUG", true),
			AccessLog:       envBool("APP_ACCESS_LOG_ENABLED", true),
			ReadTimeout:     envDuration("APP_READ_TIMEOUT", 30*time.Second),
			WriteTimeout:    envDuration("APP_WRITE_TIMEOUT", 0),
			IdleTimeout:     envDuration("APP_IDLE_TIMEOUT", 60*time.Second),
			ShutdownTimeout: envDuration("APP_SHUTDOWN_TIMEOUT", 20*time.Second),
		},
		AI: loadAIConfig(),
		MCP: MCPConfig{
			ConfigPath: env("MCP_CONFIG_PATH", "mcp.json"),
			Enabled:    envBool("MCP_ENABLED", true),
		},
		Auth: AuthConfig{
			OperatorAPIKey: env("AGENT_OPERATOR_API_KEY", ""),
			AdminAPIKey:    env("AGENT_ADMIN_API_KEY", ""),
			AuditLog:       envBool("AGENT_AUDIT_LOG_ENABLED", true),
		},
		Agent: AgentConfig{
			MaxMessageChars: envInt("AGENT_MAX_MESSAGE_CHARS", 4000),
			ChatTimeout:     envDuration("AGENT_CHAT_TIMEOUT", 10*time.Minute),
			ToolTimeout:     envDuration("AGENT_TOOL_TIMEOUT", 30*time.Second),
			PlannerTimeout:  envDuration("AGENT_PLANNER_TIMEOUT", 5*time.Second),
			AutoConfirmMCP:  envBool("AGENT_AUTO_CONFIRM_MCP", true),
		},
		Customer: CustomerConfig{
			MaxTurns:         envInt("CUSTOMER_MAX_TURNS", 10),
			MaxDailyVisitors: envInt("CUSTOMER_MAX_DAILY_VISITORS", 1000),
			RAGAPIEndpoint:   env("CUSTOMER_RAG_API_ENDPOINT", "http://localhost:9093/api/search"),
		},
		Operator: OperatorConfig{
			MaxTurns: envInt("OPERATOR_MAX_TURNS", 30),
		},
		Admin: AdminConfig{
			SessionRetentionDays: envInt("ADMIN_SESSION_RETENTION_DAYS", 90),
		},
		Rate: RateLimitConfig{
			Enabled: envBool("APP_RATE_LIMIT_ENABLED", true),
			RPS:     envFloat("APP_RATE_LIMIT_RPS", 20),
			Burst:   envInt("APP_RATE_LIMIT_BURST", 60),
		},
		HTTP: HTTPConfig{
			BodyLimit:            env("APP_BODY_LIMIT", "2M"),
			CORSAllowedOrigins:   envList("APP_CORS_ALLOWED_ORIGINS", DefaultCORSAllowedOrigins()),
			SecureHeadersEnabled: envBool("APP_SECURE_HEADERS_ENABLED", true),
			HSTSMaxAge:           envInt("APP_HSTS_MAX_AGE", 31536000),
		},
	}
}

func ValidateForServe(cfg *Config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	if cfg.AI.Temperature < 0 || cfg.AI.Temperature > 2 {
		return errors.New("AI_TEMPERATURE must be between 0 and 2")
	}
	if err := validateHost("APP_HOST", cfg.App.Host); err != nil {
		return err
	}
	if err := validatePort("APP_PORT", cfg.App.Port); err != nil {
		return err
	}
	if err := validateSQLitePath("APP_DB_PATH", cfg.App.DBPath); err != nil {
		return err
	}
	if err := validateBaseURL("DEEPSEEK_BASE_URL", cfg.AI.BaseURL); err != nil {
		return err
	}
	if cfg.AI.MaxTokens <= 0 {
		return errors.New("AI_MAX_TOKENS must be greater than 0")
	}
	if cfg.AI.RequestTimeout <= 0 {
		return errors.New("AI_REQUEST_TIMEOUT must be greater than 0")
	}
	if cfg.Agent.MaxMessageChars <= 0 {
		return errors.New("AGENT_MAX_MESSAGE_CHARS must be greater than 0")
	}
	if cfg.Agent.ChatTimeout <= 0 {
		return errors.New("AGENT_CHAT_TIMEOUT must be greater than 0")
	}
	if cfg.Agent.ToolTimeout <= 0 {
		return errors.New("AGENT_TOOL_TIMEOUT must be greater than 0")
	}
	if cfg.Agent.PlannerTimeout <= 0 {
		return errors.New("AGENT_PLANNER_TIMEOUT must be greater than 0")
	}
	if cfg.Customer.MaxTurns <= 0 {
		return errors.New("CUSTOMER_MAX_TURNS must be greater than 0")
	}
	if cfg.Customer.MaxDailyVisitors <= 0 {
		return errors.New("CUSTOMER_MAX_DAILY_VISITORS must be greater than 0")
	}
	if cfg.Operator.MaxTurns <= 0 {
		return errors.New("OPERATOR_MAX_TURNS must be greater than 0")
	}
	if cfg.Admin.SessionRetentionDays <= 0 {
		return errors.New("ADMIN_SESSION_RETENTION_DAYS must be greater than 0")
	}
	if cfg.App.ReadTimeout <= 0 {
		return errors.New("APP_READ_TIMEOUT must be greater than 0")
	}
	if cfg.App.WriteTimeout < 0 {
		return errors.New("APP_WRITE_TIMEOUT cannot be negative")
	}
	if cfg.App.IdleTimeout <= 0 {
		return errors.New("APP_IDLE_TIMEOUT must be greater than 0")
	}
	if cfg.App.ShutdownTimeout <= 0 {
		return errors.New("APP_SHUTDOWN_TIMEOUT must be greater than 0")
	}
	if err := validateBodyLimit(cfg.HTTP.BodyLimit); err != nil {
		return err
	}
	if cfg.HTTP.HSTSMaxAge < 0 {
		return errors.New("APP_HSTS_MAX_AGE cannot be negative")
	}
	if err := validateCORSAllowedOrigins(cfg.HTTP.CORSAllowedOrigins); err != nil {
		return err
	}
	if cfg.Rate.Enabled {
		if cfg.Rate.RPS <= 0 {
			return errors.New("APP_RATE_LIMIT_RPS must be greater than 0 when rate limiting is enabled")
		}
		if cfg.Rate.Burst <= 0 {
			return errors.New("APP_RATE_LIMIT_BURST must be greater than 0 when rate limiting is enabled")
		}
	}
	if err := validateInternalAPIKey("AGENT_OPERATOR_API_KEY", cfg.Auth.OperatorAPIKey); err != nil {
		return err
	}
	if err := validateInternalAPIKey("AGENT_ADMIN_API_KEY", cfg.Auth.AdminAPIKey); err != nil {
		return err
	}
	operatorKey := strings.TrimSpace(cfg.Auth.OperatorAPIKey)
	adminKey := strings.TrimSpace(cfg.Auth.AdminAPIKey)
	if operatorKey != "" && adminKey != "" && operatorKey == adminKey {
		return errors.New("AGENT_OPERATOR_API_KEY and AGENT_ADMIN_API_KEY must be different")
	}
	return nil
}

func validatePort(name, value string) error {
	port := strings.TrimSpace(value)
	if port == "" {
		return fmt.Errorf("%s must be between 1 and 65535", name)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%s must be between 1 and 65535", name)
	}
	return nil
}

func validateHost(name, value string) error {
	host := strings.TrimSpace(value)
	if host != value {
		return fmt.Errorf("%s cannot have leading or trailing whitespace", name)
	}
	if host == "" {
		return nil
	}
	if strings.ContainsAny(host, " \t\r\n") || host == "*" {
		return fmt.Errorf("%s must be a host or IP without scheme, path, query, fragment, or port", name)
	}
	if strings.Contains(host, "://") || strings.ContainsAny(host, `/\?#`) {
		return fmt.Errorf("%s must be a host or IP without scheme, path, query, fragment, or port", name)
	}
	if strings.Contains(host, ":") {
		return fmt.Errorf("%s must be a host or IP without scheme, path, query, fragment, or port", name)
	}
	return nil
}

func validateSQLitePath(name, value string) error {
	path := strings.TrimSpace(value)
	if path == "" {
		return fmt.Errorf("%s cannot be blank", name)
	}
	if strings.HasSuffix(path, "/") || strings.HasSuffix(path, `\`) {
		return fmt.Errorf("%s must point to a sqlite database file, not a directory", name)
	}
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return fmt.Errorf("%s must point to a sqlite database file, not a directory", name)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("%s cannot be inspected: %w", name, err)
	}
	parent := filepath.Dir(path)
	if parent == "." || parent == "" {
		return nil
	}
	if info, err := os.Stat(parent); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s parent must be a directory", name)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("%s parent cannot be inspected: %w", name, err)
	}
	return nil
}

func validateBaseURL(name, value string) error {
	raw := strings.TrimSpace(value)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s must be an absolute http(s) URL", name)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%s must be an absolute http(s) URL", name)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return fmt.Errorf("%s must be an absolute http(s) URL", name)
	}
	return nil
}

func validateCORSAllowedOrigins(origins []string) error {
	for _, origin := range origins {
		value := strings.TrimSpace(origin)
		if value == "*" {
			continue
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return errors.New("APP_CORS_ALLOWED_ORIGINS entries must be * or absolute http(s) origins without path, query, fragment, or userinfo")
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return errors.New("APP_CORS_ALLOWED_ORIGINS entries must be * or absolute http(s) origins without path, query, fragment, or userinfo")
		}
		if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
			return errors.New("APP_CORS_ALLOWED_ORIGINS entries must be * or absolute http(s) origins without path, query, fragment, or userinfo")
		}
	}
	return nil
}

func validateBodyLimit(value string) error {
	limit := strings.TrimSpace(value)
	if limit == "" {
		return nil
	}
	n, err := gommonbytes.Parse(limit)
	if err != nil || n <= 0 {
		return errors.New("APP_BODY_LIMIT must be empty or a positive byte size such as 512K, 2M, or 1GiB")
	}
	return nil
}

func validateInternalAPIKey(name, value string) error {
	if value != "" && strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s cannot be blank when configured", name)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s cannot have leading or trailing whitespace", name)
	}
	if value != "" && len(value) < internalAPIKeyMinLength {
		return fmt.Errorf("%s must be at least %d characters when configured", name, internalAPIKeyMinLength)
	}
	return nil
}

func loadAIConfig() AIConfig {
	return AIConfig{
		Provider:       "deepseek",
		APIKey:         envTrimmed("DEEPSEEK_API_KEY", ""),
		BaseURL:        trimRightSlash(envTrimmed("DEEPSEEK_BASE_URL", "https://api.deepseek.com")),
		ChatModel:      envTrimmed("DEEPSEEK_MODEL", "deepseek-v4-flash"),
		Temperature:    envFloat("AI_TEMPERATURE", 0.5),
		MaxTokens:      envInt("AI_MAX_TOKENS", 2048),
		RequestTimeout: envDuration("AI_REQUEST_TIMEOUT", 180*time.Second),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envTrimmed(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return strings.TrimSpace(fallback)
}

func envBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envFloat(key string, fallback float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return n
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func envList(key string, fallback []string) []string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parts := strings.Split(v, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return fallback
	}
	return values
}

func trimRightSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
