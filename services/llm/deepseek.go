package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go-agent-studio/services/aitypes"
	"net/http"
	"strings"
	"time"
)

type DeepSeekProvider struct {
	name       string
	apiURL     string
	apiKey     string
	model      string
	httpClient *http.Client
}

type DeepSeekProviderConfig struct {
	Name    string
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

func NewDeepSeekProvider(cfg DeepSeekProviderConfig) *DeepSeekProvider {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	apiURL := base + "/chat/completions"
	if strings.HasSuffix(base, "/chat/completions") {
		apiURL = base
	}
	return &DeepSeekProvider{
		name:       cfg.Name,
		apiURL:     apiURL,
		apiKey:     cfg.APIKey,
		model:      cfg.Model,
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (p *DeepSeekProvider) Name() string        { return p.name }
func (p *DeepSeekProvider) SupportsTools() bool { return true }

func (p *DeepSeekProvider) Chat(ctx context.Context, messages []aitypes.Message, tools []*aitypes.Tool, cfg *aitypes.LLMConfig) (*aitypes.LLMResponse, error) {
	llmCfg := effectiveLLMConfig(cfg)
	reqMessages := make([]deepSeekMessage, 0, len(messages))
	for _, m := range messages {
		msg := deepSeekMessage{Role: string(m.Role), Content: m.Content, Name: m.Name, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			if err := validateToolCallJSONArguments("deepseek", tc.Name, tc.ID, tc.Arguments); err != nil {
				return nil, err
			}
			msg.ToolCalls = append(msg.ToolCalls, deepSeekToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: deepSeekFunction{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			})
		}
		reqMessages = append(reqMessages, msg)
	}
	body := deepSeekChatRequest{
		Model:       p.model,
		Messages:    reqMessages,
		Temperature: llmCfg.Temperature,
		MaxTokens:   llmCfg.MaxTokens,
	}
	for _, tool := range tools {
		var params map[string]interface{}
		if err := json.Unmarshal([]byte(tool.Parameters), &params); err != nil {
			params = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		body.Tools = append(body.Tools, deepSeekTool{
			Type: "function",
			Function: deepSeekToolDef{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  params,
			},
		})
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal chat request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chat request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := ReadLimitedBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read chat response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiStatusError(p.name, resp.StatusCode, respBody)
	}

	var result deepSeekChatResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse chat response: %w", err)
	}
	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("chat api returned no choices")
	}
	choice := result.Choices[0]
	out := &aitypes.LLMResponse{
		Content:          choice.Message.Content,
		PromptTokens:     result.Usage.PromptTokens,
		CompletionTokens: result.Usage.CompletionTokens,
	}
	for _, tc := range choice.Message.ToolCalls {
		if err := validateToolCallJSONArguments("deepseek response", tc.Function.Name, tc.ID, tc.Function.Arguments); err != nil {
			return nil, err
		}
		out.ToolCalls = append(out.ToolCalls, aitypes.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return out, nil
}

type deepSeekChatRequest struct {
	Model       string            `json:"model"`
	Messages    []deepSeekMessage `json:"messages"`
	Tools       []deepSeekTool    `json:"tools,omitempty"`
	Temperature float64           `json:"temperature"`
	MaxTokens   int               `json:"max_tokens"`
}

type deepSeekMessage struct {
	Role       string             `json:"role"`
	Content    string             `json:"content"`
	Name       string             `json:"name,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
	ToolCalls  []deepSeekToolCall `json:"tool_calls,omitempty"`
}

type deepSeekTool struct {
	Type     string          `json:"type"`
	Function deepSeekToolDef `json:"function"`
}

type deepSeekToolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type deepSeekToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function deepSeekFunction `json:"function"`
}

type deepSeekFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type deepSeekChatResponse struct {
	Choices []struct {
		Message deepSeekMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
