package orchestrator

import (
	"fmt"
	"regexp"
	"strings"
)

type WorkingMemory struct {
	Snapshot MemorySnapshot
}

func NewWorkingMemory(goal string) *WorkingMemory {
	return &WorkingMemory{Snapshot: MemorySnapshot{Goal: goal, Facts: ExtractLaborLawFacts(goal)}}
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

var (
	contractYearsPatterns = []*regexp.Regexp{
		regexp.MustCompile(`合同(?:期限)?(?:是|为|签|签订|约定)?\s*([一二两三四五六七八九十0-9]+)\s*年`),
		regexp.MustCompile(`签(?:了|订)?\s*([一二两三四五六七八九十0-9]+)\s*年(?:合同|期限)?`),
	}
	workedMonthsPattern = regexp.MustCompile(`(?:工作|试用|入职|干了).{0,8}?([一二两三四五六七八九十0-9]+)\s*个?月`)
)

func ExtractLaborLawFacts(text string) []string {
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
	for _, pattern := range contractYearsPatterns {
		if match := pattern.FindStringSubmatch(userText); len(match) == 2 {
			add("劳动合同期限：" + normalizeChineseNumber(match[1]) + "年")
			break
		}
	}
	if match := workedMonthsPattern.FindStringSubmatch(userText); len(match) == 2 {
		add("已工作/试用时间：" + normalizeChineseNumber(match[1]) + "个月")
	}
	if containsAnyText(userText, []string{"新公司", "第一次签", "首次签"}) {
		add("用人单位关系：新公司或首次约定")
	}
	if containsAnyText(userText, []string{"辞退", "解除合同", "解除劳动合同", "开除", "裁掉"}) {
		add("争议类型：用人单位解除劳动合同")
	}
	if containsAnyText(userText, []string{"试用期"}) {
		add("议题：试用期")
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

func normalizeChineseNumber(value string) string {
	value = strings.TrimSpace(value)
	digits := map[string]string{
		"一": "1",
		"二": "2",
		"两": "2",
		"三": "3",
		"四": "4",
		"五": "5",
		"六": "6",
		"七": "7",
		"八": "8",
		"九": "9",
		"十": "10",
	}
	if digit, ok := digits[value]; ok {
		return digit
	}
	return value
}
