package admin

import (
	"context"
	"encoding/json"
	"go-agent-studio/services/aitypes"
)

// Config holds admin tool configuration.
type Config struct {
	StatusProvider interface {
		MemoryPersistent() bool
		TracePersistent() bool
	}
	StatusBuilder func() interface{}
}

// RegisterTools registers admin-facing tools.
func RegisterTools(registry *aitypes.ToolRegistry, cfg Config) {
	if cfg.StatusBuilder != nil {
		registerAgentStatusWithBuilder(registry, cfg.StatusBuilder)
	}
}

// registerAgentStatusWithBuilder registers the system status tool for admin using a builder function.
func registerAgentStatusWithBuilder(registry *aitypes.ToolRegistry, builder func() interface{}) {
	registry.Register(&aitypes.Tool{
		Name:        "agent_status",
		Description: "查看当前 Agent 运行状态和工具配置。适合诊断系统能力。",
		Parameters:  `{"type":"object","properties":{}}`,
		Execute: func(ctx context.Context, arguments string) (string, error) {
			status := builder()
			data, _ := json.MarshalIndent(status, "", "  ")
			return string(data), nil
		},
	})
}
