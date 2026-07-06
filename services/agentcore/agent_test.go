package agentcore

import (
	"strings"
	"testing"
)

func TestPolishCustomerAnswerFallsBackForEmptyContent(t *testing.T) {
	got, polished := polishCustomerAnswer("")

	if !polished {
		t.Fatal("fallback answer should be reported as polished")
	}
	if got == "以上由 AI 生成，仅供参考。" {
		t.Fatal("empty content should not become a disclaimer-only answer")
	}
	if !stringsContains(got, "没有生成有效回答") {
		t.Fatalf("fallback answer missing useful retry guidance: %q", got)
	}
	if !stringsContains(got, "以上由 AI 生成，仅供参考。") {
		t.Fatalf("fallback answer missing disclaimer: %q", got)
	}
}

func TestPolishCustomerAnswerFallsBackForDisclaimerOnlyContent(t *testing.T) {
	got, polished := polishCustomerAnswer("  以上由 AI 生成，仅供参考。  ")

	if !polished {
		t.Fatal("disclaimer-only answer should be reported as polished")
	}
	if !stringsContains(got, "没有生成有效回答") {
		t.Fatalf("disclaimer-only content should use fallback answer: %q", got)
	}
}

func TestPolishCustomerAnswerKeepsNormalContent(t *testing.T) {
	got, polished := polishCustomerAnswer("结论：工作流适合处理单轮任务。")

	if !polished {
		t.Fatal("normal customer answer should be polished when disclaimer is appended")
	}
	if !stringsContains(got, "工作流适合处理单轮任务。") {
		t.Fatalf("normal content was not preserved: %q", got)
	}
	if !stringsContains(got, "以上由 AI 生成，仅供参考。") {
		t.Fatalf("normal answer missing disclaimer: %q", got)
	}
}

func stringsContains(s, substr string) bool {
	return strings.Contains(s, substr)
}
