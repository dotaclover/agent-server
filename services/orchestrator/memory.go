package orchestrator

import (
	"fmt"
	"strings"
)

type WorkingMemory struct {
	Snapshot MemorySnapshot
}

func NewWorkingMemory(goal string) *WorkingMemory {
	return &WorkingMemory{Snapshot: MemorySnapshot{Goal: goal, Facts: ExtractProductDocFacts(goal)}}
}

func (m *WorkingMemory) SetPlan(plan Plan) {
	cp := plan
	m.Snapshot.Plan = &cp
}

func (m *WorkingMemory) AddResult(result ExecutionResult) {
	m.Snapshot.StepResults = append(m.Snapshot.StepResults, result)
	m.Snapshot.Summary = m.BuildSummary()
}

func (m *WorkingMemory) BuildSummary() string {
	var parts []string
	for _, result := range m.Snapshot.StepResults {
		status := result.Status
		if result.Error != "" {
			status += ": " + result.Error
		}
		parts = append(parts, fmt.Sprintf("%s via %s => %s", result.StepID, result.ToolName, status))
	}
	return strings.Join(parts, "; ")
}

func ExtractProductDocFacts(text string) []string {
	userText := userAuthoredFactText(text)
	if userText == "" {
		return nil
	}
	facts := make([]string, 0, 4)
	seen := map[string]struct{}{}
	add := func(fact string) {
		if fact == "" {
			return
		}
		if _, ok := seen[fact]; ok {
			return
		}
		seen[fact] = struct{}{}
		facts = append(facts, fact)
	}
	topics := []struct {
		keywords []string
		fact     string
	}{
		{[]string{"工作流", "workflow"}, "议题：工作流"},
		{[]string{"对话流", "chatflow"}, "议题：对话流"},
		{[]string{"知识库", "dataset", "knowledge"}, "议题：知识库"},
		{[]string{"节点", "node"}, "议题：节点配置"},
		{[]string{"发布", "webapp", "api"}, "议题：发布与集成"},
		{[]string{"模型", "供应商", "provider"}, "议题：模型供应商"},
		{[]string{"团队", "成员", "权限"}, "议题：团队与权限"},
		{[]string{"日志", "监控", "trace"}, "议题：监控与日志"},
	}
	for _, topic := range topics {
		if containsAnyText(userText, topic.keywords) {
			add(topic.fact)
		}
	}
	return facts
}

func userAuthoredFactText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	var parts []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "当前问题："):
			parts = append(parts, strings.TrimPrefix(line, "当前问题："))
		case strings.HasPrefix(line, "用户："):
			parts = append(parts, strings.TrimPrefix(line, "用户："))
		case hasAnyPrefix(line, "客服：", "assistant：", "助手："):
			continue
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	return text
}

func containsAnyText(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
