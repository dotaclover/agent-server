package memory

import (
	"strings"
	"testing"

	"go-agent-studio/services/aitypes"
)

func TestTitleFromMessagesUsesFirstUserMessage(t *testing.T) {
	messages := []aitypes.Message{
		aitypes.NewMessage(aitypes.RoleAssistant, "欢迎语"),
		aitypes.NewMessage(aitypes.RoleUser, "Dify 工作流和对话流有什么区别？"),
		aitypes.NewMessage(aitypes.RoleAssistant, "工作流适合单轮任务，对话流适合多轮对话。"),
		aitypes.NewMessage(aitypes.RoleUser, "那知识库怎么接入？"),
	}

	got := TitleFromMessages(messages)

	if got != "Dify 工作流和对话流有什么区别？" {
		t.Fatalf("expected first user question as title, got %q", got)
	}
}

func TestTitleFromMessagesCompactsWhitespace(t *testing.T) {
	messages := []aitypes.Message{
		aitypes.NewMessage(aitypes.RoleUser, "  第一行\n\n第二行\t第三行  "),
	}

	got := TitleFromMessages(messages)

	if got != "第一行 第二行 第三行" {
		t.Fatalf("expected compact title, got %q", got)
	}
}

func TestTitleFromMessagesTruncatesLongTitle(t *testing.T) {
	messages := []aitypes.Message{
		aitypes.NewMessage(aitypes.RoleUser, strings.Repeat("长", 130)),
	}

	got := TitleFromMessages(messages)

	if len([]rune(got)) != 123 {
		t.Fatalf("expected 120 chars plus ellipsis, got %d chars", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected truncated title suffix, got %q", got)
	}
}
