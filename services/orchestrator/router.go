package orchestrator

import (
	"fmt"
	"go-agent-studio/services/aitypes"
	"strings"
)

type ToolRouter struct {
	tools *aitypes.ToolRegistry
	// autoConfirmMCP, when true, makes the router inject confirm_destructive=true
	// for external MCP tools so the MCP tool flow is not blocked by the executor's
	// destructive gate. Set false to require explicit confirmation instead.
	autoConfirmMCP bool
}

func NewToolRouter(tools *aitypes.ToolRegistry) *ToolRouter {
	return &ToolRouter{tools: tools, autoConfirmMCP: true}
}

// NewToolRouterWithConfirm builds a router with an explicit auto-confirm policy
// for external MCP tools.
func NewToolRouterWithConfirm(tools *aitypes.ToolRegistry, autoConfirmMCP bool) *ToolRouter {
	return &ToolRouter{tools: tools, autoConfirmMCP: autoConfirmMCP}
}

func (r *ToolRouter) Route(goal string, step Step) (RouteDecision, error) {
	if !step.NeedTool {
		return RouteDecision{StepID: step.ID, Confidence: 1, Reason: "step does not need a tool"}, nil
	}
	toolName := step.ToolHint
	if toolName == "" {
		return RouteDecision{}, fmt.Errorf("step has no tool hint")
	}
	if _, ok := r.tools.Get(toolName); !ok {
		return RouteDecision{}, fmt.Errorf("tool %q is not registered", toolName)
	}
	args := copyArguments(step.Arguments)

	// Auto-confirm destructive external MCP tools when enabled.
	if r.autoConfirmMCP && strings.Contains(toolName, "_") && !strings.HasPrefix(toolName, "generate_") && toolName != "search_product_docs" && toolName != "agent_status" {
		setDefaultArg(args, "confirm_destructive", true)
	}

	return RouteDecision{
		StepID:     step.ID,
		ToolName:   toolName,
		Arguments:  args,
		Confidence: confidenceFor(step, toolName),
		Reason:     fmt.Sprintf("intent=%s routed to %s", step.Intent, toolName),
	}, nil
}

func copyArguments(arguments map[string]interface{}) map[string]interface{} {
	copied := map[string]interface{}{}
	for key, value := range arguments {
		copied[key] = value
	}
	return copied
}

func setDefaultArg(args map[string]interface{}, key string, value interface{}) {
	if args == nil {
		return
	}
	if _, exists := args[key]; exists {
		return
	}
	args[key] = value
}

func confidenceFor(step Step, toolName string) float64 {
	if step.ToolHint == toolName {
		return 0.92
	}
	return 0.72
}
