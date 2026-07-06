package orchestrator

import (
	"context"
	"fmt"
	"go-agent-studio/services/aitypes"
	"go-agent-studio/services/trace"
	"strings"
	"time"
)

type EventSink interface {
	OnPlan(Plan)
	OnStep(Step)
	OnRoute(RouteDecision)
	OnExecution(ExecutionResult)
}

type LoopController struct {
	planner  PlannerProvider
	router   *ToolRouter
	executor *Executor
	tracer   *trace.Recorder
}

type RunOptions struct {
	RequestID string
	// MaxToolSteps caps how many tool-backed steps may actually execute in a
	// single run; steps beyond the cap are marked skipped. The synthesis step
	// always runs. 0 means no cap.
	MaxToolSteps int
}

type PlannerProvider interface {
	CreatePlan(ctx context.Context, goal string, tools *aitypes.ToolRegistry) Plan
}

func NewLoopController(planner PlannerProvider, router *ToolRouter, executor *Executor, tracer *trace.Recorder) *LoopController {
	return &LoopController{planner: planner, router: router, executor: executor, tracer: tracer}
}

type contextKey string

const (
	sessionIDContextKey contextKey = "session_id"
	requestIDContextKey contextKey = "request_id"
)

func (l *LoopController) Run(ctx context.Context, sessionID, goal string, tools *aitypes.ToolRegistry, sink EventSink) (Plan, *WorkingMemory, error) {
	return l.RunWithOptions(ctx, sessionID, goal, tools, sink, RunOptions{})
}

func (l *LoopController) RunWithOptions(ctx context.Context, sessionID, goal string, tools *aitypes.ToolRegistry, sink EventSink, opts RunOptions) (Plan, *WorkingMemory, error) {
	ctx = context.WithValue(ctx, sessionIDContextKey, sessionID)
	if opts.RequestID != "" {
		ctx = context.WithValue(ctx, requestIDContextKey, opts.RequestID)
	}
	plan := l.planner.CreatePlan(ctx, goal, tools)
	mem := NewWorkingMemory(goal)
	mem.SetPlan(plan)
	if sink != nil {
		sink.OnPlan(plan)
	}
	l.trace(sessionID, opts.RequestID, "plan_created", "Planner created explicit plan", "", "succeeded", 0, map[string]interface{}{"plan": plan})

	toolStepsRun := 0
	for i := range plan.Steps {
		step := plan.Steps[i]
		step.Status = StepRunning
		plan.Steps[i] = step
		plan.UpdatedAt = time.Now()
		mem.SetPlan(plan)
		if sink != nil {
			sink.OnStep(step)
		}
		l.trace(sessionID, opts.RequestID, "step_start", step.Title, step.ToolHint, "running", 0, map[string]interface{}{"step": step})

		if !step.NeedTool {
			step.Status = StepSucceeded
			step.Result = "ready_to_synthesize"
			now := time.Now()
			step.CompletedAt = &now
			plan.Steps[i] = step
			mem.SetPlan(plan)
			if sink != nil {
				sink.OnStep(step)
			}
			l.trace(sessionID, opts.RequestID, "step_done", step.Title, step.ToolHint, "succeeded", 0, map[string]interface{}{
				"result": ExecutionResult{
					StepID: step.ID,
					Status: "succeeded",
					Result: step.Result,
				},
			})
			continue
		}

		if opts.MaxToolSteps > 0 && toolStepsRun >= opts.MaxToolSteps {
			step.Status = StepSkipped
			step.Reason = fmt.Sprintf("已达单次运行工具步骤上限 %d，跳过该工具步骤。", opts.MaxToolSteps)
			now := time.Now()
			step.CompletedAt = &now
			plan.Steps[i] = step
			plan.UpdatedAt = now
			mem.SetPlan(plan)
			if sink != nil {
				sink.OnStep(step)
			}
			l.trace(sessionID, opts.RequestID, "step_skipped", "Tool step skipped; reached MaxToolSteps cap for this run", step.ToolHint, "skipped", 0, map[string]interface{}{"step_id": step.ID, "max_tool_steps": opts.MaxToolSteps})
			continue
		}

		decision, err := l.router.Route(goal, step)
		if err != nil {
			step.Status = StepFailed
			step.Error = err.Error()
			now := time.Now()
			step.CompletedAt = &now
			plan.Steps[i] = step
			mem.SetPlan(plan)
			if sink != nil {
				sink.OnStep(step)
			}
			return plan, mem, err
		}
		step.Arguments = decision.Arguments
		plan.Steps[i] = step
		if sink != nil {
			sink.OnRoute(decision)
		}
		l.trace(sessionID, opts.RequestID, "route_decision", decision.Reason, decision.ToolName, "succeeded", 0, map[string]interface{}{"decision": decision})

		result := l.executor.Execute(ctx, decision)
		mem.AddResult(result)
		toolStepsRun++
		step.Result = result.Result
		step.Error = result.Error
		step.DurationMS = result.DurationMS
		if result.Status == "succeeded" {
			step.Status = StepSucceeded
		} else {
			step.Status = StepFailed
		}
		now := time.Now()
		step.CompletedAt = &now
		plan.Steps[i] = step
		plan.UpdatedAt = now
		mem.SetPlan(plan)
		if sink != nil {
			sink.OnExecution(result)
			sink.OnStep(step)
		}
		l.trace(sessionID, opts.RequestID, "step_done", step.Title, decision.ToolName, result.Status, result.DurationMS, map[string]interface{}{"result": result})
		if result.Status != "succeeded" {
			l.trace(sessionID, opts.RequestID, "step_degraded", "Tool step failed; continuing to synthesis with available context", decision.ToolName, "degraded", result.DurationMS, map[string]interface{}{"step_id": step.ID, "error": result.Error})
			continue
		}
	}
	return plan, mem, nil
}

func (l *LoopController) BuildSynthesisPrompt(plan Plan, mem *WorkingMemory, role string) string {
	var b strings.Builder
	if role == "admin" {
		b.WriteString("请扮演面向系统管理员的智能运维助手，基于下面的用户问题、最近对话、系统状态和工具执行结果回答。\n\n")
	} else if role == "operator" {
		b.WriteString("请扮演面向运营人员的智能助手，基于下面的用户问题、最近对话和检索结果回答。\n\n")
	} else {
		b.WriteString("请扮演面向普通用户的 Dify 产品文档助手，基于下面的用户问题、最近对话和检索结果回答。\n\n")
	}
	b.WriteString("用户问题与上下文：\n")
	b.WriteString(plan.Goal)
	b.WriteString("\n\n参考资料：\n")
	if mem == nil || len(mem.Snapshot.StepResults) == 0 {
		b.WriteString("- 暂无可用参考资料，可以基于常识谨慎回答，并说明还需要哪些关键信息。\n")
	} else {
		if len(mem.Snapshot.Facts) > 0 {
			b.WriteString("已知事实：\n")
			for _, fact := range mem.Snapshot.Facts {
				b.WriteString("- " + fact + "\n")
			}
			b.WriteString("\n")
		}
		for _, result := range mem.Snapshot.StepResults {
			if result.Error != "" {
				b.WriteString("- " + synthesisToolErrorMessage(result.Error) + "\n")
				continue
			}
			if result.Result != "" {
				b.WriteString(result.Result + "\n")
			}
		}
	}
	b.WriteString("\n回答要求：")
	if role == "admin" {
		b.WriteString("\n- 用专业、清晰且对运维人员友好的口吻回答问题。")
		b.WriteString("\n- 如果用户是在追问，必须结合最近对话理解省略信息。")
		b.WriteString("\n- 可以直接给出技术细节和诊断结果（可以提外部 RAG、MCP、SQLite、Trace 和执行计划等技术名词）。")
		b.WriteString("\n- 给出直接的技术与状态回复，不要在末尾添加“以上由 AI 生成，仅供参考”的免责说明。")
	} else if role == "operator" {
		b.WriteString("\n- 用自然专业的口吻直接回答问题。")
		b.WriteString("\n- 如果用户是在追问，必须结合最近对话理解省略信息。")
		b.WriteString("\n- 不要主动在回答中提及“本地知识库”等底层实现概念，除非用户主动问起。")
	} else {
		b.WriteString("\n- 用自然客服口吻，直接回答用户当前问题，不要用“结论：”“依据：”作为固定开头。")
		b.WriteString("\n- 如果用户是在追问，必须结合最近对话理解省略信息。")
		b.WriteString("\n- 如果参考资料标记 reliable_references=false，说明检索没有可靠命中；不要硬编产品功能细节，优先说明需要查看对应文档或补充具体场景。")
		b.WriteString("\n- 可以引用 Dify 文档模块、功能名称或配置项，但不要提“本地知识库”“工具结果”“RAG”“mock”“执行计划”。")
		b.WriteString("\n- 不要要求用户补充与当前问题无关的信息；只有事实确实不足时才追问关键事实。")
		b.WriteString("\n- 末尾加一句：以上由 AI 生成，仅供参考。")
	}
	return b.String()
}

func synthesisToolErrorMessage(errText string) string {
	if strings.Contains(errText, "requires confirm_destructive=true") {
		return errText
	}
	return "参考资料暂不可用，请基于已知事实谨慎回答。"
}

func (l *LoopController) trace(sessionID, requestID, eventType, message, toolName, status string, durationMS int64, payload map[string]interface{}) {
	if l.tracer == nil {
		return
	}
	if requestID != "" {
		if payload == nil {
			payload = map[string]interface{}{}
		}
		payload["request_id"] = requestID
	}
	l.tracer.Add(sessionID, trace.Event{
		Type:       eventType,
		Message:    message,
		ToolName:   toolName,
		Status:     status,
		DurationMS: durationMS,
		Payload:    payload,
	})
}
