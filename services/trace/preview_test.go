package trace

import (
	"strings"
	"testing"

	"go-agent-studio/services/aitypes"
)

func TestTextPreviewRedactsAndTruncates(t *testing.T) {
	input := "api_key=secret-value " + strings.Repeat("测", PreviewCharLimit+20)

	got := TextPreview(input)

	if strings.Contains(got, "secret-value") {
		t.Fatalf("preview should redact sensitive values: %q", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("preview should include redaction marker: %q", got)
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("preview should mark truncated content: %q", got)
	}
}

func TestMessagePreviewsIncludeContentAndToolCalls(t *testing.T) {
	messages := []aitypes.Message{
		{
			Role:    aitypes.RoleAssistant,
			Content: "需要先查资料",
			ToolCalls: []aitypes.ToolCall{
				{ID: "call_1", Name: "search_product_docs", Arguments: `{"query":"Dify 工作流"}`},
			},
		},
	}

	got := MessagePreviews(messages)

	if len(got) != 1 {
		t.Fatalf("expected one message preview, got %d", len(got))
	}
	if got[0].ContentPreview != "需要先查资料" {
		t.Fatalf("unexpected content preview: %q", got[0].ContentPreview)
	}
	if len(got[0].ToolCalls) != 1 || got[0].ToolCalls[0].Name != "search_product_docs" {
		t.Fatalf("tool call preview missing: %#v", got[0].ToolCalls)
	}
}
