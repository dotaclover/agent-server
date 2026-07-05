package orchestrator

import "time"

type StepStatus string

const (
	StepPending   StepStatus = "pending"
	StepRunning   StepStatus = "running"
	StepSucceeded StepStatus = "succeeded"
	StepFailed    StepStatus = "failed"
	StepSkipped   StepStatus = "skipped"
)

type Plan struct {
	ID             string    `json:"id"`
	Goal           string    `json:"goal"`
	Strategy       string    `json:"strategy"`
	PlannerMode    string    `json:"planner_mode,omitempty"`
	PlannerSource  string    `json:"planner_source,omitempty"`
	FallbackReason string    `json:"fallback_reason,omitempty"`
	Steps          []Step    `json:"steps"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Step struct {
	ID          string                 `json:"id"`
	Title       string                 `json:"title"`
	Intent      string                 `json:"intent"`
	NeedTool    bool                   `json:"need_tool"`
	ToolHint    string                 `json:"tool_hint,omitempty"`
	Status      StepStatus             `json:"status"`
	Reason      string                 `json:"reason,omitempty"`
	Arguments   map[string]interface{} `json:"arguments,omitempty"`
	Result      string                 `json:"result,omitempty"`
	Error       string                 `json:"error,omitempty"`
	DurationMS  int64                  `json:"duration_ms,omitempty"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
}

type RouteDecision struct {
	StepID     string                 `json:"step_id"`
	ToolName   string                 `json:"tool_name"`
	Arguments  map[string]interface{} `json:"arguments"`
	Confidence float64                `json:"confidence"`
	Reason     string                 `json:"reason"`
}

type ExecutionResult struct {
	StepID              string `json:"step_id"`
	ToolName            string `json:"tool_name"`
	Status              string `json:"status"`
	Result              string `json:"result,omitempty"`
	Error               string `json:"error,omitempty"`
	DurationMS          int64  `json:"duration_ms"`
	Attempts            int    `json:"attempts,omitempty"`
	ResultTruncated     bool   `json:"result_truncated,omitempty"`
	OriginalResultChars int    `json:"original_result_chars,omitempty"`
}

type MemorySnapshot struct {
	Goal        string            `json:"goal"`
	Plan        *Plan             `json:"plan,omitempty"`
	StepResults []ExecutionResult `json:"step_results"`
	Summary     string            `json:"summary"`
	Facts       []string          `json:"facts,omitempty"`
}
