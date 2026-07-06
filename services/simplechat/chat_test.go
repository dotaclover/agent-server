package simplechat

import (
	"strings"
	"testing"
)

func TestPolishCustomerAnswerFallsBackForEmptyContent(t *testing.T) {
	got := polishCustomerAnswer("")

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
	got := polishCustomerAnswer("  以上由 AI 生成，仅供参考。  ")

	if !stringsContains(got, "没有生成有效回答") {
		t.Fatalf("disclaimer-only content should use fallback answer: %q", got)
	}
}

func TestPolishCustomerAnswerKeepsNormalContent(t *testing.T) {
	got := polishCustomerAnswer("结论：工作流适合处理单轮任务。")

	if !stringsContains(got, "工作流适合处理单轮任务。") {
		t.Fatalf("normal content was not preserved: %q", got)
	}
	if !stringsContains(got, "以上由 AI 生成，仅供参考。") {
		t.Fatalf("normal answer missing disclaimer: %q", got)
	}
}

func TestUsedCustomerFallbackAnswer(t *testing.T) {
	if !usedCustomerFallbackAnswer("") {
		t.Fatal("empty answer should be marked as fallback")
	}
	if !usedCustomerFallbackAnswer("以上由 AI 生成，仅供参考。") {
		t.Fatal("disclaimer-only answer should be marked as fallback")
	}
	if usedCustomerFallbackAnswer("工作流适合处理单轮任务。") {
		t.Fatal("normal answer should not be marked as fallback")
	}
}

func stringsContains(s, substr string) bool {
	return strings.Contains(s, substr)
}
