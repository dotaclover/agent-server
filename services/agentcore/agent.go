package agentcore

import (
	"context"
	"fmt"
	"go-agent-studio/services/aitypes"
	"go-agent-studio/services/orchestrator"
	"go-agent-studio/services/security"
	"go-agent-studio/services/trace"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Agent struct {
	provider aitypes.LLMProvider
	tools    *aitypes.ToolRegistry
	tracer   *trace.Recorder
	config   Config
}

type Config struct {
	SystemPrompt string
	// MaxToolStepsPerRun caps how many tool-backed steps may actually execute in
	// a single agent run; steps beyond the cap are skipped while the final
	// synthesis step always runs. Acts as a safety rail against an over-long plan.
	MaxToolStepsPerRun int
	Temperature        float64
	MaxTokens          int
	ToolTimeout        time.Duration
	PlannerTimeout     time.Duration
	AutoConfirmMCP     bool
	// PromptDir is the directory under which per-role and per-tool prompt
	// overrides are loaded. Empty means no file-based overrides (Go defaults only).
	PromptDir string
}

type RunOptions struct {
	SessionID string
	RequestID string
	Role      aitypes.AgentRole
	Messages  []aitypes.Message
	OnMessage func(aitypes.Message)
	OnPlan    func(orchestrator.Plan)
	OnStep    func(orchestrator.Step)
	OnRoute   func(orchestrator.RouteDecision)
	OnExec    func(orchestrator.ExecutionResult)
}

type RunResult struct {
	Messages         []aitypes.Message            `json:"messages"`
	Plan             *orchestrator.Plan           `json:"plan,omitempty"`
	Memory           *orchestrator.MemorySnapshot `json:"memory,omitempty"`
	Iterations       int                          `json:"iterations"`
	PromptTokens     int                          `json:"prompt_tokens"`
	CompletionTokens int                          `json:"completion_tokens"`
}

func New(provider aitypes.LLMProvider, tools *aitypes.ToolRegistry, tracer *trace.Recorder, cfg Config) *Agent {
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt()
	}
	if cfg.MaxToolStepsPerRun <= 0 {
		cfg.MaxToolStepsPerRun = 4
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 2048
	}
	if cfg.ToolTimeout <= 0 {
		cfg.ToolTimeout = orchestrator.DefaultToolTimeout
	}
	if cfg.PlannerTimeout <= 0 {
		cfg.PlannerTimeout = 5 * time.Second
	}
	return &Agent{provider: provider, tools: tools, tracer: tracer, config: cfg}
}

func (a *Agent) Run(ctx context.Context, opts RunOptions) (*RunResult, error) {
	if a == nil {
		return &RunResult{Messages: opts.Messages}, fmt.Errorf("agent is not configured")
	}
	goal := latestUserGoal(opts.Messages)
	if goal == "" {
		return &RunResult{Messages: opts.Messages}, fmt.Errorf("no user goal found")
	}
	if a.provider == nil {
		a.trace(opts.SessionID, "run_failed", "LLM provider is not configured", "", "failed", 0, nil, opts.RequestID)
		return &RunResult{Messages: opts.Messages}, fmt.Errorf("llm provider is not configured")
	}
	contextualGoal := contextualUserGoal(opts.Messages)
	result := &RunResult{Iterations: 1}
	llmCfg := &aitypes.LLMConfig{Temperature: a.config.Temperature, MaxTokens: a.config.MaxTokens}

	a.trace(opts.SessionID, "run_start", "Agent run started", "", "running", 0, map[string]interface{}{
		"provider":        a.provider.Name(),
		"tools":           a.tools.Names(),
		"planner":         "llm",
		"planner_timeout": a.config.PlannerTimeout.String(),
		"tool_timeout":    a.config.ToolTimeout.String(),
	}, opts.RequestID)

	loop := orchestrator.NewLoopController(
		a.planner(),
		orchestrator.NewToolRouterWithConfirm(a.tools, a.config.AutoConfirmMCP),
		orchestrator.NewExecutorWithTimeout(a.tools, a.config.ToolTimeout),
		a.tracer,
	)
	sink := runEventSink{
		onPlan:  opts.OnPlan,
		onStep:  opts.OnStep,
		onRoute: opts.OnRoute,
		onExec:  opts.OnExec,
	}
	plan, workingMemory, err := loop.RunWithOptions(ctx, opts.SessionID, contextualGoal, a.tools, sink, orchestrator.RunOptions{RequestID: opts.RequestID, MaxToolSteps: a.config.MaxToolStepsPerRun})
	result.Plan = &plan
	if workingMemory != nil {
		snapshot := workingMemory.Snapshot
		result.Memory = &snapshot
	}
	if err != nil {
		result.Messages = opts.Messages
		return result, err
	}

	synthesisPrompt := loop.BuildSynthesisPrompt(plan, workingMemory, string(opts.Role))
	messages := []aitypes.Message{
		aitypes.NewMessage(aitypes.RoleSystem, systemPromptForWithDir(opts.Role, a.config.PromptDir)+"\n\n请基于用户问题、最近对话和可用参考资料回答。不要说明参考资料是如何取得或被组织的。"),
		aitypes.NewMessage(aitypes.RoleUser, synthesisPrompt),
	}

	a.trace(opts.SessionID, "llm_request", "LLM synthesis request payload", "", "succeeded", 0, map[string]interface{}{
		"config":               llmCfg,
		"message_count":        len(messages),
		"request_messages":     trace.MessagePreviews(messages),
		"request_text_preview": trace.MessagesTextPreview(messages),
	}, opts.RequestID)

	start := time.Now()
	resp, err := a.provider.Chat(ctx, messages, nil, llmCfg)
	elapsed := time.Since(start)
	if err != nil {
		errMessage := security.RedactSensitive(err.Error())
		a.trace(opts.SessionID, "llm_error", errMessage, "", "failed", elapsed.Milliseconds(), nil, opts.RequestID)
		result.Messages = opts.Messages
		return result, fmt.Errorf("synthesis llm call failed: %s", errMessage)
	}
	if resp == nil {
		a.trace(opts.SessionID, "llm_error", "LLM provider returned nil response", "", "failed", elapsed.Milliseconds(), nil, opts.RequestID)
		result.Messages = opts.Messages
		return result, fmt.Errorf("synthesis llm call returned nil response")
	}
	result.PromptTokens += resp.PromptTokens
	result.CompletionTokens += resp.CompletionTokens
	content := resp.Content
	polished := false
	if opts.Role == "" || opts.Role == aitypes.RoleCustomer {
		content, polished = polishCustomerAnswer(content)
	}
	a.trace(opts.SessionID, "llm_response", "LLM synthesized final answer", "", "succeeded", elapsed.Milliseconds(), map[string]interface{}{
		"answer_polished":        polished,
		"content_len":            len(content),
		"final_response_preview": trace.TextPreview(content),
		"raw_content_len":        len(resp.Content),
		"raw_response_preview":   trace.TextPreview(resp.Content),
		"prompt_tokens":          resp.PromptTokens,
		"completion_tokens":      resp.CompletionTokens,
	}, opts.RequestID)

	finalMessages := append([]aitypes.Message(nil), opts.Messages...)
	assistantMsg := aitypes.NewMessage(aitypes.RoleAssistant, content)
	finalMessages = append(finalMessages, assistantMsg)
	if opts.OnMessage != nil {
		opts.OnMessage(assistantMsg)
	}
	result.Messages = finalMessages
	a.trace(opts.SessionID, "run_done", "Agent returned final answer", "", "succeeded", 0, map[string]interface{}{"plan_id": plan.ID}, opts.RequestID)
	return result, nil
}

func (a *Agent) planner() orchestrator.PlannerProvider {
	if a == nil || a.provider == nil {
		return orchestrator.NewLLMPlanner(nil, 0)
	}
	return orchestrator.NewLLMPlanner(a.provider, a.config.PlannerTimeout)
}

func (a *Agent) trace(sessionID, eventType, message, toolName, status string, durationMS int64, payload map[string]interface{}, requestID ...string) {
	if a.tracer == nil {
		return
	}
	payload = withRequestID(payload, requestID...)
	a.tracer.Add(sessionID, trace.Event{
		Type:       eventType,
		Message:    message,
		ToolName:   toolName,
		Status:     status,
		DurationMS: durationMS,
		Payload:    payload,
	})
}

func withRequestID(payload map[string]interface{}, requestID ...string) map[string]interface{} {
	if len(requestID) == 0 || requestID[0] == "" {
		return payload
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	payload["request_id"] = requestID[0]
	return payload
}

type runEventSink struct {
	onPlan  func(orchestrator.Plan)
	onStep  func(orchestrator.Step)
	onRoute func(orchestrator.RouteDecision)
	onExec  func(orchestrator.ExecutionResult)
}

func (s runEventSink) OnPlan(plan orchestrator.Plan) {
	if s.onPlan != nil {
		s.onPlan(plan)
	}
}

func (s runEventSink) OnStep(step orchestrator.Step) {
	if s.onStep != nil {
		s.onStep(step)
	}
}

func (s runEventSink) OnRoute(decision orchestrator.RouteDecision) {
	if s.onRoute != nil {
		s.onRoute(decision)
	}
}

func (s runEventSink) OnExecution(result orchestrator.ExecutionResult) {
	if s.onExec != nil {
		s.onExec(result)
	}
}

func latestUserGoal(messages []aitypes.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == aitypes.RoleUser {
			return messages[i].Content
		}
	}
	return ""
}

func contextualUserGoal(messages []aitypes.Message) string {
	current := latestUserGoal(messages)
	if current == "" {
		return ""
	}
	recent := recentConversation(messages, 6)
	if len(recent) <= 1 {
		return current
	}
	var b strings.Builder
	b.WriteString("当前问题：")
	b.WriteString(current)
	b.WriteString("\n\n最近对话：\n")
	for _, message := range recent {
		role := "助手"
		if message.Role == aitypes.RoleUser {
			role = "用户"
		}
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		b.WriteString(role)
		b.WriteString("：")
		b.WriteString(truncateRunes(content, 260))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func recentConversation(messages []aitypes.Message, max int) []aitypes.Message {
	if max <= 0 || len(messages) == 0 {
		return nil
	}
	filtered := make([]aitypes.Message, 0, max)
	for i := len(messages) - 1; i >= 0 && len(filtered) < max; i-- {
		if messages[i].Role == aitypes.RoleUser || messages[i].Role == aitypes.RoleAssistant {
			filtered = append(filtered, messages[i])
		}
	}
	for i, j := 0, len(filtered)-1; i < j; i, j = i+1, j-1 {
		filtered[i], filtered[j] = filtered[j], filtered[i]
	}
	return filtered
}

func truncateRunes(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "..."
}

const customerFallbackAnswer = "抱歉，刚才没有生成有效回答。你可以再问一次，或补充入职时间、劳动合同约定、工资工时、解除或争议经过等关键信息，我会重新帮你判断。"

func polishCustomerAnswer(content string) (string, bool) {
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
	return text, text != content
}

func normalizeCustomerAnswerText(text string) string {
	text = strings.ReplaceAll(text, " ", "")
	text = strings.ReplaceAll(text, "\t", "")
	text = strings.ReplaceAll(text, "\r", "")
	text = strings.ReplaceAll(text, "\n", "")
	text = strings.ReplaceAll(text, ",", "，")
	return text
}

func DefaultSystemPrompt() string {
	return loadPromptFile("", "", `你是 Go Agent Studio 中的劳动法智能客服。

行为原则：
1. 面向普通用户，用直接、温和、可执行的方式回答劳动法咨询。
2. 优先处理试用期、劳动合同、工资、加班、社保、工伤、年假、辞退、离职、经济补偿、仲裁等劳动用工问题。
3. 需要法条、知识库或案例依据时优先使用 search_labor_law 的结果，再给结论和依据。
4. 对非劳动法问题，礼貌说明服务范围，并引导用户回到劳动用工咨询，不要输出生硬的"工具结果不足"。
5. 不要把 Planner、Tool Router、Executor、Memory、Loop 等内部编排细节暴露给普通用户。
6. 不要用"结论：""依据："这类固定模板开头，像真实 AI 客服一样自然回答。
7. 可以引用法律名称和条款，例如"根据《劳动合同法》第十九条"，但不要说"本地知识库""工具结果""RAG""mock"。
8. 回答末尾用一句简短提示："以上由 AI 生成，仅供参考。"；事实不足时再说明还需要哪些材料。`)
}

func SystemPromptFor(role aitypes.AgentRole) string {
	return systemPromptForWithDir(role, "")
}

func SystemPromptForWithDir(role aitypes.AgentRole, promptDir string) string {
	return systemPromptForWithDir(role, promptDir)
}

func systemPromptForWithDir(role aitypes.AgentRole, promptDir string) string {
	switch role {
	case aitypes.RoleOperator:
		return loadPromptFile(promptDir, "agent_operator.txt", defaultOperatorPrompt)
	case aitypes.RoleAdmin:
		return loadPromptFile(promptDir, "agent_admin.txt", defaultAdminPrompt)
	default:
		return loadPromptFile(promptDir, "agent_customer.txt", DefaultSystemPrompt())
	}
}

// loadPromptFile tries to read a prompt override from promptDir/name. Falls back
// to fallback when the directory is empty or the file doesn't exist.
func loadPromptFile(promptDir, name, fallback string) string {
	if promptDir == "" {
		return fallback
	}
	data, err := os.ReadFile(filepath.Join(promptDir, name))
	if err != nil {
		return fallback
	}
	return string(data)
}

var defaultOperatorPrompt = `你是 Go Agent Studio 的劳动法内容运营助手。

行为原则：
1. 帮助运营人员产出劳动法相关问答、文章草稿、短视频脚本、封面图提示词和视频提示词。
2. 回答时可以给出内容结构、标题、脚本、分镜和合规提示。
3. 可以引用法律名称和条款，便于运营人员核实，但不要说"本地知识库""RAG""mock"。
4. 不要暴露 Planner、Tool Router、Executor、Memory、Loop 等内部组件名。
5. 末尾加一句：以上由 AI 生成，仅供参考。`

var defaultAdminPrompt = `你是 Go Agent Studio 的系统运维助手。

行为原则：
1. 可以协助检查系统状态、工具清单、外部 RAG 工具注册、MCP 连接、SQLite 会话和 Trace 持久化状态。
2. 回答可以偏技术，但仍应清晰、可执行。
3. 可以总结工具执行结果和系统状态，不要编造未返回的数据。
4. 涉及密钥或敏感配置时只说明是否配置，不要输出密钥值。`
