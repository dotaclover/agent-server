package simplechat

import (
	"context"
	"fmt"
	"go-agent-studio/services/aitypes"
	"go-agent-studio/services/security"
	"go-agent-studio/services/trace"
	"strings"
	"time"
)

// SimpleChat implements a lightweight conversation loop for Customer端.
// It does not use the full Agent orchestration (no Plan/Route/Execution phases).
// Instead, it directly calls LLM with tools, executes any tool_calls, and returns
// the final answer. This is suitable for simple Q&A scenarios like labor law consultation.
type SimpleChat struct {
	provider aitypes.LLMProvider
	tools    *aitypes.ToolRegistry
	tracer   *trace.Recorder
	config   Config
}

// Config holds SimpleChat configuration.
type Config struct {
	SystemPrompt string
	MaxTurns     int           // Maximum conversation turns (default 10)
	Temperature  float64       // LLM temperature (default 0.7)
	MaxTokens    int           // Max tokens per response (default 1024)
	ChatTimeout  time.Duration // Overall chat timeout (default 2 minutes)
	ToolTimeout  time.Duration // Individual tool execution timeout (default 30s)
}

// New creates a new SimpleChat instance.
func New(provider aitypes.LLMProvider, tools *aitypes.ToolRegistry, tracer *trace.Recorder, cfg Config) *SimpleChat {
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = defaultCustomerSystemPrompt()
	}
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = 10
	}
	if cfg.Temperature < 0 {
		cfg.Temperature = 0.7
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 1024
	}
	if cfg.ChatTimeout <= 0 {
		cfg.ChatTimeout = 2 * time.Minute
	}
	if cfg.ToolTimeout <= 0 {
		cfg.ToolTimeout = 30 * time.Second
	}
	return &SimpleChat{
		provider: provider,
		tools:    tools,
		tracer:   tracer,
		config:   cfg,
	}
}

// ChatOptions holds options for a single chat request.
type ChatOptions struct {
	SessionID string
	RequestID string
	Messages  []aitypes.Message
	OnMessage func(aitypes.Message)
	OnTool    func(ToolCallEvent)
}

// ToolCallEvent represents a tool call event in simple chat.
type ToolCallEvent struct {
	ToolName  string                 `json:"tool_name"`
	Arguments string                 `json:"arguments"`
	Result    string                 `json:"result,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Status    string                 `json:"status"` // "started", "succeeded", "failed"
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// ChatResult holds the result of a chat request.
type ChatResult struct {
	Messages         []aitypes.Message `json:"messages"`
	ToolCallsCount   int               `json:"tool_calls_count"`
	PromptTokens     int               `json:"prompt_tokens"`
	CompletionTokens int               `json:"completion_tokens"`
}

// Chat executes a simple conversation turn.
// Flow:
// 1. Prepare messages with system prompt
// 2. Call LLM with tools
// 3. If LLM returns tool_calls, execute them
// 4. Call LLM again with tool results to get final answer
// 5. Return final answer
func (s *SimpleChat) Chat(ctx context.Context, opts ChatOptions) (*ChatResult, error) {
	if s == nil || s.provider == nil {
		return nil, fmt.Errorf("simplechat provider is not configured")
	}
	if s.tools == nil {
		return nil, fmt.Errorf("simplechat tools registry is not configured")
	}

	ctx, cancel := context.WithTimeout(ctx, s.config.ChatTimeout)
	defer cancel()

	result := &ChatResult{Messages: opts.Messages}

	// Prepare messages for LLM
	systemMsg := aitypes.NewMessage(aitypes.RoleSystem, s.config.SystemPrompt)
	conversationMessages := append([]aitypes.Message{systemMsg}, opts.Messages...)

	s.trace(opts.SessionID, "simple_chat_start", "Simple chat started", "", "running", 0, map[string]interface{}{
		"provider": s.provider.Name(),
		"tools":    s.tools.Names(),
	}, opts.RequestID)

	// Call LLM with tools
	llmCfg := &aitypes.LLMConfig{
		Temperature: s.config.Temperature,
		MaxTokens:   s.config.MaxTokens,
	}

	toolSchemas := s.tools.List()
	s.trace(opts.SessionID, "llm_request", fmt.Sprintf("LLM chat request with %d tools", len(toolSchemas)), "", "running", 0, map[string]interface{}{
		"config":               llmCfg,
		"message_count":        len(conversationMessages),
		"request_messages":     trace.MessagePreviews(conversationMessages),
		"request_text_preview": trace.MessagesTextPreview(conversationMessages),
		"tool_count":           len(toolSchemas),
		"tools":                s.tools.Names(),
	}, opts.RequestID)

	start := time.Now()
	resp, err := s.provider.Chat(ctx, conversationMessages, toolSchemas, llmCfg)
	elapsed := time.Since(start)

	if err != nil {
		errMessage := security.RedactSensitive(err.Error())
		s.trace(opts.SessionID, "llm_error", errMessage, "", "failed", elapsed.Milliseconds(), nil, opts.RequestID)
		return result, fmt.Errorf("llm call failed: %s", errMessage)
	}
	if resp == nil {
		s.trace(opts.SessionID, "llm_error", "LLM provider returned nil response", "", "failed", elapsed.Milliseconds(), nil, opts.RequestID)
		return result, fmt.Errorf("llm call returned nil response")
	}

	result.PromptTokens += resp.PromptTokens
	result.CompletionTokens += resp.CompletionTokens

	s.trace(opts.SessionID, "llm_response", "LLM response received", "", "succeeded", elapsed.Milliseconds(), map[string]interface{}{
		"content_len":       len(resp.Content),
		"response_preview":  trace.TextPreview(resp.Content),
		"tool_calls":        trace.ToolCallPreviews(resp.ToolCalls),
		"tool_calls_count":  len(resp.ToolCalls),
		"prompt_tokens":     resp.PromptTokens,
		"completion_tokens": resp.CompletionTokens,
	}, opts.RequestID)

	// If no tool calls, return the response directly
	if len(resp.ToolCalls) == 0 {
		content := polishCustomerAnswer(resp.Content)
		if usedCustomerFallbackAnswer(resp.Content) {
			content = contextualCustomerFallbackAnswer(latestUserGoal(opts.Messages))
		}
		assistantMsg := aitypes.NewMessage(aitypes.RoleAssistant, content)
		result.Messages = append(opts.Messages, assistantMsg)
		if opts.OnMessage != nil {
			opts.OnMessage(assistantMsg)
		}
		s.trace(opts.SessionID, "simple_chat_done", "Simple chat completed (no tools)", "", "succeeded", 0, map[string]interface{}{
			"content_len":            len(content),
			"final_response_preview": trace.TextPreview(content),
			"raw_content_len":        len(resp.Content),
			"raw_response_preview":   trace.TextPreview(resp.Content),
			"used_fallback_answer":   usedCustomerFallbackAnswer(resp.Content),
		}, opts.RequestID)
		return result, nil
	}

	// Execute tool calls
	result.ToolCallsCount = len(resp.ToolCalls)
	toolResults := make([]string, 0, len(resp.ToolCalls))

	for _, toolCall := range resp.ToolCalls {
		if opts.OnTool != nil {
			opts.OnTool(ToolCallEvent{
				ToolName:  toolCall.Name,
				Arguments: toolCall.Arguments,
				Status:    "started",
			})
		}

		toolStart := time.Now()
		toolResult, toolErr := s.executeTool(ctx, opts.SessionID, toolCall.Name, toolCall.Arguments, opts.RequestID)
		toolElapsed := time.Since(toolStart)

		if toolErr != nil {
			errMessage := security.RedactSensitive(toolErr.Error())
			s.trace(opts.SessionID, "tool_error", errMessage, toolCall.Name, "failed", toolElapsed.Milliseconds(), map[string]interface{}{
				"arguments": toolCall.Arguments,
			}, opts.RequestID)
			toolResults = append(toolResults, fmt.Sprintf("[%s 执行失败：%s]", toolCall.Name, errMessage))
			if opts.OnTool != nil {
				opts.OnTool(ToolCallEvent{
					ToolName:  toolCall.Name,
					Arguments: toolCall.Arguments,
					Error:     errMessage,
					Status:    "failed",
				})
			}
			continue
		}

		s.trace(opts.SessionID, "tool_success", fmt.Sprintf("Tool %s executed successfully", toolCall.Name), toolCall.Name, "succeeded", toolElapsed.Milliseconds(), map[string]interface{}{
			"result_len": len(toolResult),
		}, opts.RequestID)
		toolResults = append(toolResults, fmt.Sprintf("[%s 结果]\n%s", toolCall.Name, toolResult))
		if opts.OnTool != nil {
			opts.OnTool(ToolCallEvent{
				ToolName:  toolCall.Name,
				Arguments: toolCall.Arguments,
				Result:    toolResult,
				Status:    "succeeded",
			})
		}
	}

	// Build synthesis prompt with tool results
	synthesisPrompt := buildSynthesisPrompt(latestUserGoal(opts.Messages), toolResults)
	synthesisMessages := []aitypes.Message{
		aitypes.NewMessage(aitypes.RoleSystem, s.config.SystemPrompt+"\n\n请基于用户问题和工具返回的参考资料回答。不要说明资料是如何取得的。"),
		aitypes.NewMessage(aitypes.RoleUser, synthesisPrompt),
	}

	// Call LLM again for final answer
	s.trace(opts.SessionID, "llm_synthesis_request", "LLM synthesis request", "", "running", 0, map[string]interface{}{
		"config":               llmCfg,
		"message_count":        len(synthesisMessages),
		"request_messages":     trace.MessagePreviews(synthesisMessages),
		"request_text_preview": trace.MessagesTextPreview(synthesisMessages),
	}, opts.RequestID)
	synthStart := time.Now()
	synthResp, synthErr := s.provider.Chat(ctx, synthesisMessages, nil, llmCfg)
	synthElapsed := time.Since(synthStart)

	if synthErr != nil {
		errMessage := security.RedactSensitive(synthErr.Error())
		s.trace(opts.SessionID, "llm_synthesis_error", errMessage, "", "failed", synthElapsed.Milliseconds(), nil, opts.RequestID)
		return result, fmt.Errorf("llm synthesis failed: %s", errMessage)
	}
	if synthResp == nil {
		s.trace(opts.SessionID, "llm_synthesis_error", "LLM provider returned nil response", "", "failed", synthElapsed.Milliseconds(), nil, opts.RequestID)
		return result, fmt.Errorf("llm synthesis returned nil response")
	}

	result.PromptTokens += synthResp.PromptTokens
	result.CompletionTokens += synthResp.CompletionTokens

	content := polishCustomerAnswer(synthResp.Content)
	if usedCustomerFallbackAnswer(synthResp.Content) {
		content = contextualCustomerFallbackAnswer(latestUserGoal(opts.Messages))
	}
	s.trace(opts.SessionID, "llm_synthesis_response", "LLM synthesis response received", "", "succeeded", synthElapsed.Milliseconds(), map[string]interface{}{
		"content_len":            len(content),
		"final_response_preview": trace.TextPreview(content),
		"raw_content_len":        len(synthResp.Content),
		"raw_response_preview":   trace.TextPreview(synthResp.Content),
		"prompt_tokens":          synthResp.PromptTokens,
		"completion_tokens":      synthResp.CompletionTokens,
		"used_fallback_answer":   usedCustomerFallbackAnswer(synthResp.Content),
	}, opts.RequestID)
	assistantMsg := aitypes.NewMessage(aitypes.RoleAssistant, content)
	result.Messages = append(opts.Messages, assistantMsg)
	if opts.OnMessage != nil {
		opts.OnMessage(assistantMsg)
	}

	s.trace(opts.SessionID, "simple_chat_done", "Simple chat completed", "", "succeeded", 0, map[string]interface{}{
		"tool_calls_count": result.ToolCallsCount,
	}, opts.RequestID)

	return result, nil
}

// executeTool executes a single tool with timeout.
func (s *SimpleChat) executeTool(ctx context.Context, sessionID, toolName, arguments, requestID string) (string, error) {
	tool, ok := s.tools.Get(toolName)
	if !ok {
		return "", fmt.Errorf("tool %s not found", toolName)
	}

	toolCtx, cancel := context.WithTimeout(ctx, s.config.ToolTimeout)
	defer cancel()

	result, err := tool.Execute(toolCtx, arguments)
	if err != nil {
		return "", err
	}

	return result, nil
}

// trace records a trace event.
func (s *SimpleChat) trace(sessionID, eventType, message, toolName, status string, durationMS int64, payload map[string]interface{}, requestID ...string) {
	if s.tracer == nil {
		return
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	if len(requestID) > 0 && requestID[0] != "" {
		payload["request_id"] = requestID[0]
	}
	s.tracer.Add(sessionID, trace.Event{
		Type:       eventType,
		Message:    message,
		ToolName:   toolName,
		Status:     status,
		DurationMS: durationMS,
		Payload:    payload,
	})
}

// latestUserGoal returns the latest user message content.
func latestUserGoal(messages []aitypes.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == aitypes.RoleUser {
			return messages[i].Content
		}
	}
	return ""
}

// buildSynthesisPrompt builds a prompt for LLM to synthesize tool results into final answer.
func buildSynthesisPrompt(userGoal string, toolResults []string) string {
	var b strings.Builder
	b.WriteString("用户问题：")
	b.WriteString(userGoal)
	b.WriteString("\n\n参考资料：\n")
	for _, result := range toolResults {
		b.WriteString(result)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

const customerFallbackAnswer = "抱歉，刚才没有生成有效回答。你可以再问一次，或补充你想了解的 Dify 功能、应用类型、知识库配置或发布场景，我会重新帮你查询文档。"

func contextualCustomerFallbackAnswer(userGoal string) string {
	goal := strings.ToLower(strings.TrimSpace(userGoal))
	if strings.Contains(goal, "win11") || strings.Contains(goal, "windows") || strings.Contains(goal, "本机") {
		return "Windows 11 本机安装 Dify，建议走 WSL 2 + Docker Desktop：先用管理员 PowerShell 执行 `wsl --install` 并重启，安装 Docker Desktop 时启用 WSL 2 后端，然后在 Ubuntu 终端中进入 Dify 的 docker 目录执行 `docker compose up -d`。启动完成后浏览器访问 `http://localhost`，首次进入后创建管理员账号。\n\n如果 80 端口被占用，可以在 `.env` 中改外部端口；Docker Desktop 建议至少分配 4GB 内存。以上由 AI 生成，仅供参考。"
	}
	return customerFallbackAnswer + "\n\n以上由 AI 生成，仅供参考。"
}

// polishCustomerAnswer removes internal implementation details from LLM response.
func polishCustomerAnswer(content string) string {
	text := strings.TrimSpace(content)
	text = strings.TrimPrefix(text, "结论：")
	text = strings.TrimSpace(text)

	replacements := []struct {
		old string
		new string
	}{
		{"本地知识库中的", ""},
		{"本地知识库中", ""},
		{"本地知识库", "参考资料"},
		{"依据工具检索结果：", ""},
		{"依据工具结果：", ""},
		{"工具结果", "参考资料"},
		{"RAG", "参考资料"},
		{"mock 模式", "演示环境"},
		{"执行计划", "处理过程"},
	}
	for _, replacement := range replacements {
		text = strings.ReplaceAll(text, replacement.old, replacement.new)
	}

	text = strings.TrimSpace(text)
	normalizedText := normalizeCustomerAnswerText(text)
	if text == "" || normalizedText == "以上由AI生成，仅供参考。" {
		text = customerFallbackAnswer
		normalizedText = normalizeCustomerAnswerText(text)
	}
	if !strings.Contains(normalizedText, "以上由AI生成，仅供参考。") {
		if text != "" {
			text += "\n\n"
		}
		text += "以上由 AI 生成，仅供参考。"
	}

	return text
}

func usedCustomerFallbackAnswer(content string) bool {
	text := strings.TrimSpace(content)
	text = strings.TrimPrefix(text, "结论：")
	text = strings.TrimSpace(text)
	return text == "" || normalizeCustomerAnswerText(text) == "以上由AI生成，仅供参考。"
}

func normalizeCustomerAnswerText(text string) string {
	text = strings.ReplaceAll(text, " ", "")
	text = strings.ReplaceAll(text, "\t", "")
	text = strings.ReplaceAll(text, "\r", "")
	text = strings.ReplaceAll(text, "\n", "")
	text = strings.ReplaceAll(text, ",", "，")
	return text
}

// defaultCustomerSystemPrompt returns the default system prompt for Customer端.
func defaultCustomerSystemPrompt() string {
	return `你是 Dify 产品文档问答助手。

职责：
- 回答 Dify 产品使用相关问题
- 说明应用、工作流、知识库、节点、发布和监控等功能
- 提供清晰、可执行的操作建议

工具使用：
- 当需要产品文档依据时，使用可用工具搜索
- 引用来源时标注文档模块或功能名称

回答规范：
- 准确：基于产品文档
- 清晰：默认 400-700 字，按步骤或要点展开；用户明确要求简短时再压缩
- 实用：给出可行建议
- 引用：标注文档来源或功能模块

不要说"本地知识库"、"工具结果"、"RAG"等内部术语，用"参考资料"代替。`
}
