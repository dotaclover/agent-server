package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"go-agent-studio/services/aitypes"
	"go-agent-studio/services/security"
	"time"
)

const (
	DefaultToolTimeout     = 30 * time.Second
	MaxToolResultChars     = 12000
	toolResultTruncatedMsg = "\n\n[tool result truncated]"
)

type Executor struct {
	tools   *aitypes.ToolRegistry
	timeout time.Duration
}

func NewExecutor(tools *aitypes.ToolRegistry) *Executor {
	return NewExecutorWithTimeout(tools, DefaultToolTimeout)
}

func NewExecutorWithTimeout(tools *aitypes.ToolRegistry, timeout time.Duration) *Executor {
	if timeout <= 0 {
		timeout = DefaultToolTimeout
	}
	return &Executor{tools: tools, timeout: timeout}
}

func (e *Executor) Execute(ctx context.Context, decision RouteDecision) ExecutionResult {
	start := time.Now()
	result := ExecutionResult{
		StepID:   decision.StepID,
		ToolName: decision.ToolName,
		Status:   "succeeded",
		Attempts: 1,
	}
	tool, ok := e.tools.Get(decision.ToolName)
	if !ok {
		result.Status = "failed"
		result.Error = fmt.Sprintf("tool %q not found", decision.ToolName)
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	if tool.Destructive && !confirmedDestructive(decision.Arguments) {
		result.Status = "failed"
		result.Error = fmt.Sprintf("destructive tool %q requires confirm_destructive=true", decision.ToolName)
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	args, err := json.Marshal(decision.Arguments)
	if err != nil {
		result.Status = "failed"
		result.Error = fmt.Sprintf("serialize tool arguments: %v", err)
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	toolCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	out, attempts, err := executeToolWithRetry(toolCtx, tool, string(args))
	result.Attempts = attempts
	if err != nil {
		result.Status = "failed"
		result.Error = security.RedactSensitive(err.Error())
		if toolCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			result.Error = fmt.Sprintf("tool %q timed out after %s", decision.ToolName, e.timeout)
		}
	} else {
		result.Result, result.ResultTruncated, result.OriginalResultChars = normalizeToolResult(out)
	}
	result.DurationMS = time.Since(start).Milliseconds()
	return result
}

func normalizeToolResult(out string) (string, bool, int) {
	originalChars := len([]rune(out))
	if originalChars <= MaxToolResultChars {
		return out, false, 0
	}
	runes := []rune(out)
	return string(runes[:MaxToolResultChars]) + toolResultTruncatedMsg, true, originalChars
}

func executeToolWithRetry(ctx context.Context, tool *aitypes.Tool, args string) (string, int, error) {
	maxAttempts := maxToolAttempts(tool)
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		out, err := tool.Execute(ctx, args)
		if err == nil {
			return out, i + 1, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return "", i + 1, err
		}
		if i+1 < maxAttempts {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", i + 1, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return "", maxAttempts, lastErr
}

func maxToolAttempts(tool *aitypes.Tool) int {
	if tool == nil || tool.Destructive {
		return 1
	}
	return 2
}

func confirmedDestructive(args map[string]interface{}) bool {
	if args == nil {
		return false
	}
	confirmed, _ := args["confirm_destructive"].(bool)
	return confirmed
}
