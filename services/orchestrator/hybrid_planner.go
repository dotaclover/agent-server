package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"go-agent-studio/services/aitypes"
	"go-agent-studio/services/security"
	"go-agent-studio/services/trace"
	"strings"
	"time"
)

// LLMPlanner 用 LLM 的 tool_calls 能力生成执行计划。不再有规则回退——LLM 失败时返回只含 synthesis 步骤的保底 Plan。
type LLMPlanner struct {
	provider aitypes.LLMProvider
	timeout  time.Duration
	Tracer   *trace.Recorder
}

// NewLLMPlanner 创建纯 LLM 驱动的计划生成器。
func NewLLMPlanner(provider aitypes.LLMProvider, timeout time.Duration) *LLMPlanner {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	return &LLMPlanner{provider: provider, timeout: timeout}
}

func (p *LLMPlanner) CreatePlan(ctx context.Context, goal string, tools *aitypes.ToolRegistry) Plan {
	if p == nil || p.provider == nil {
		return emptyPlan(goal, "provider_unavailable")
	}
	if tools == nil || len(tools.List()) == 0 {
		return emptyPlan(goal, "tools_unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	sessionID, _ := ctx.Value(sessionIDContextKey).(string)
	requestID, _ := ctx.Value(requestIDContextKey).(string)

	messages := []aitypes.Message{
		aitypes.NewMessage(aitypes.RoleSystem, planningSystemPrompt(tools)),
		aitypes.NewMessage(aitypes.RoleUser, goal),
	}

	toolList := tools.List()
	planCfg := &aitypes.LLMConfig{Temperature: 0, MaxTokens: 1024}

	if p.Tracer != nil && sessionID != "" {
		p.trace(sessionID, requestID, "llm_request", "Planner requesting LLM to generate execution plan", "", "succeeded", 0, map[string]interface{}{
			"config":               planCfg,
			"message_count":        len(messages),
			"request_messages":     trace.MessagePreviews(messages),
			"request_text_preview": trace.MessagesTextPreview(messages),
			"tools":                toolNamesFromList(toolList),
		})
	}

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	start := time.Now()
	resp, err := p.provider.Chat(ctx, messages, toolList, planCfg)
	elapsed := time.Since(start)

	if err != nil {
		if p.Tracer != nil && sessionID != "" {
			p.trace(sessionID, requestID, "llm_error", security.RedactSensitive(err.Error()), "", "failed", elapsed.Milliseconds(), nil)
		}
		return emptyPlan(goal, "provider_error: "+safeLLMError(err))
	}
	if resp == nil {
		if p.Tracer != nil && sessionID != "" {
			p.trace(sessionID, requestID, "llm_error", "LLM provider returned nil response", "", "failed", elapsed.Milliseconds(), nil)
		}
		return emptyPlan(goal, "nil_response")
	}

	if p.Tracer != nil && sessionID != "" {
		p.trace(sessionID, requestID, "llm_response", "LLM generated execution plan", "", "succeeded", elapsed.Milliseconds(), map[string]interface{}{
			"content_len":       len(resp.Content),
			"response_preview":  trace.TextPreview(resp.Content),
			"tool_calls":        trace.ToolCallPreviews(resp.ToolCalls),
			"tool_calls_count":  len(resp.ToolCalls),
			"prompt_tokens":     resp.PromptTokens,
			"completion_tokens": resp.CompletionTokens,
		})
	}

	if len(resp.ToolCalls) == 0 {
		return emptyPlan(goal, "empty_tool_calls")
	}
	plan, ok := planFromLLMToolCalls(goal, resp.ToolCalls, tools)
	if !ok {
		return emptyPlan(goal, "no_registered_tool_calls")
	}
	return plan
}

func (p *LLMPlanner) trace(sessionID, requestID, eventType, message, toolName, status string, durationMS int64, payload map[string]interface{}) {
	if p.Tracer == nil {
		return
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	if requestID != "" {
		payload["request_id"] = requestID
	}
	p.Tracer.Add(sessionID, trace.Event{
		Type:       eventType,
		Message:    message,
		ToolName:   toolName,
		Status:     status,
		DurationMS: durationMS,
		Payload:    payload,
	})
}

func planningSystemPrompt(tools *aitypes.ToolRegistry) string {
	var b strings.Builder
	b.WriteString("你是 Go Agent Studio 的计划生成器。根据用户问题和可用工具，决定需要执行哪些步骤。\n\n")
	b.WriteString("可用工具：\n")
	for _, t := range tools.List() {
		b.WriteString("- ")
		b.WriteString(t.Name)
		if t.Description != "" {
			b.WriteString(": ")
			b.WriteString(t.Description)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n规则：\n")
	b.WriteString("1. 根据用户问题和工具描述，选择最合适的工具（可以组合多个），通过 tool_calls 表达；如果无需工具则直接回复不调用。\n")
	b.WriteString("2. 只调用当前可用工具列表中存在的工具，不要编造不存在的工具。\n")
	b.WriteString("3. 用户说\"刚才那个\"\"上面那个\"等引用 → 结合对话上下文理解，不要跳过工具。\n")
	b.WriteString("4. 如果用户问题与所有可用工具都不相关，直接回复不调用任何工具。\n")
	return b.String()
}

func safeLLMError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.Join(strings.Fields(security.RedactSensitive(err.Error())), " ")
	const max = 180
	if len([]rune(message)) <= max {
		return message
	}
	return string([]rune(message)[:max])
}

// emptyPlan 返回只有一个 synthesis 步骤的空计划（LLM 规划失败时的保底行为）。
func emptyPlan(goal, reason string) Plan {
	now := time.Now()
	return Plan{
		ID:             "plan_" + aitypes.NewID(),
		Goal:           goal,
		Strategy:       "llm planner v2.0; fallback: " + reason,
		PlannerMode:    "llm",
		PlannerSource:  "llm_fallback",
		FallbackReason: reason,
		CreatedAt:      now,
		UpdatedAt:      now,
		Steps: []Step{{
			ID:       "step_1",
			Title:    "综合结果并回答",
			Intent:   "synthesize_answer",
			NeedTool: false,
			Status:   StepPending,
			Reason:   "LLM 规划失败（" + reason + "），直接用对话上下文回答。",
		}},
	}
}

func planFromLLMToolCalls(goal string, calls []aitypes.ToolCall, tools *aitypes.ToolRegistry) (Plan, bool) {
	now := time.Now()
	plan := Plan{
		ID:            "plan_" + aitypes.NewID(),
		Goal:          goal,
		Strategy:      "llm planner v2.0; tool_calls mapped to explicit steps, then synthesize final answer",
		PlannerMode:   "llm",
		PlannerSource: "llm_tool_calls",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	seen := map[string]bool{}
	for _, call := range calls {
		toolName := strings.TrimSpace(call.Name)
		if toolName == "" || seen[toolName] {
			continue
		}
		if _, ok := tools.Get(toolName); !ok {
			continue
		}
		stepID := fmt.Sprintf("step_%d", len(plan.Steps)+1)
		step := Step{
			ID:        stepID,
			Title:     toolTitleFromName(toolName),
			Intent:    toolIntentFromName(toolName),
			NeedTool:  true,
			ToolHint:  toolName,
			Status:    StepPending,
			Reason:    "LLM 计划器选择此工具。",
			Arguments: parseLLMPlanArguments(call.Arguments),
		}
		plan.Steps = append(plan.Steps, step)
		seen[toolName] = true
	}
	if len(plan.Steps) == 0 {
		return Plan{}, false
	}
	plan.Steps = append(plan.Steps, Step{
		ID:       fmt.Sprintf("step_%d", len(plan.Steps)+1),
		Title:    "综合结果并回答",
		Intent:   "synthesize_answer",
		NeedTool: false,
		Status:   StepPending,
		Reason:   "将工具结果组织为最终回答。",
	})
	return plan, true
}

func parseLLMPlanArguments(raw string) map[string]interface{} {
	args := map[string]interface{}{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return args
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return map[string]interface{}{}
	}
	return args
}

// toolNamesFromList extracts just the names for trace payloads (keeping them small).
func toolNamesFromList(tools []*aitypes.Tool) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return names
}

// TitleFromName returns a human-readable Chinese title for a tool name.
func toolTitleFromName(name string) string {
	switch name {
	case "search_labor_law":
		return "检索劳动法知识库"
	case "craft_image_prompt":
		return "生成图片 Prompt"
	case "craft_video_prompt":
		return "生成视频 Prompt"
	case "agent_status":
		return "检查 Agent 状态"
	case "generate_image":
		return "生成图片素材"
	case "generate_video":
		return "生成视频素材"
	case "outline_creator":
		return "创建写作大纲"
	case "content_writer":
		return "撰写内容段落"
	case "style_refiner":
		return "润色优化内容"
	default:
		if strings.Contains(name, "_") {
			return fmt.Sprintf("调用工具 %s", name)
		}
		return name
	}
}

// IntentFromName maps a tool name to an intent label used by Router for
// default-argument filling.
func toolIntentFromName(name string) string {
	switch name {
	case "search_labor_law":
		return "retrieve_references"
	case "craft_image_prompt":
		return "generate_image_prompt"
	case "craft_video_prompt":
		return "generate_video_prompt"
	case "agent_status":
		return "inspect_agent_status"
	case "generate_image":
		return "generate_image"
	case "generate_video":
		return "generate_video"
	case "outline_creator":
		return "create_outline"
	case "content_writer":
		return "write_content"
	case "style_refiner":
		return "refine_style"
	default:
		if strings.Contains(name, "_") {
			return "call_tool"
		}
		return name
	}
}
