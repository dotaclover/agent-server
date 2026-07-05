package llm

import (
	"context"
	"fmt"
	"go-agent-studio/config"
	"go-agent-studio/services/aitypes"
	"strings"
	"time"
)

// NewProviderFromConfig creates the DeepSeek chat provider; a missing API key
// returns a placeholder that fails clearly when Chat is called.
func NewProviderFromConfig(cfg config.AIConfig, timeout time.Duration) aitypes.LLMProvider {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return newUnconfiguredProvider()
	}
	return NewDeepSeekProvider(DeepSeekProviderConfig{
		Name:    "deepseek",
		BaseURL: cfg.BaseURL,
		APIKey:  cfg.APIKey,
		Model:   cfg.ChatModel,
		Timeout: timeout,
	})
}

type unconfiguredProvider struct {
	name    string
	message string
}

func newUnconfiguredProvider() *unconfiguredProvider {
	return &unconfiguredProvider{
		name:    "unconfigured-deepseek",
		message: "DeepSeek 缺少 API key；请设置 DEEPSEEK_API_KEY",
	}
}

func (p *unconfiguredProvider) Name() string        { return p.name }
func (p *unconfiguredProvider) SupportsTools() bool { return true }
func (p *unconfiguredProvider) Chat(ctx context.Context, messages []aitypes.Message, tools []*aitypes.Tool, cfg *aitypes.LLMConfig) (*aitypes.LLMResponse, error) {
	return nil, fmt.Errorf(p.message)
}

func effectiveLLMConfig(cfg *aitypes.LLMConfig) aitypes.LLMConfig {
	if cfg == nil {
		return aitypes.LLMConfig{Temperature: 0.5, MaxTokens: 2048}
	}
	out := *cfg
	if out.MaxTokens <= 0 {
		out.MaxTokens = 2048
	}
	return out
}
