package trace

import (
	"fmt"
	"go-agent-studio/services/aitypes"
	"go-agent-studio/services/security"
	"strings"
)

const PreviewCharLimit = 1000

type MessagePreview struct {
	Role           aitypes.Role      `json:"role"`
	Name           string            `json:"name,omitempty"`
	ToolCallID     string            `json:"tool_call_id,omitempty"`
	ContentChars   int               `json:"content_chars"`
	ContentPreview string            `json:"content_preview"`
	ToolCalls      []ToolCallPreview `json:"tool_calls_preview,omitempty"`
}

type ToolCallPreview struct {
	ID            string `json:"id,omitempty"`
	Name          string `json:"name"`
	ArgumentChars int    `json:"argument_chars"`
	Arguments     string `json:"arguments_preview"`
}

func TextPreview(text string) string {
	return TextPreviewWithLimit(text, PreviewCharLimit)
}

func TextPreviewWithLimit(text string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = PreviewCharLimit
	}
	text = strings.TrimSpace(security.RedactSensitive(text))
	return truncateRunes(text, maxRunes)
}

func MessagePreviews(messages []aitypes.Message) []MessagePreview {
	out := make([]MessagePreview, 0, len(messages))
	for _, message := range messages {
		out = append(out, MessagePreview{
			Role:           message.Role,
			Name:           message.Name,
			ToolCallID:     message.ToolCallID,
			ContentChars:   len([]rune(message.Content)),
			ContentPreview: TextPreview(message.Content),
			ToolCalls:      ToolCallPreviews(message.ToolCalls),
		})
	}
	return out
}

func MessagesTextPreview(messages []aitypes.Message) string {
	var b strings.Builder
	for _, message := range messages {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("[")
		b.WriteString(string(message.Role))
		if message.Name != "" {
			b.WriteString(" name=")
			b.WriteString(message.Name)
		}
		if message.ToolCallID != "" {
			b.WriteString(" tool_call_id=")
			b.WriteString(message.ToolCallID)
		}
		b.WriteString("]\n")
		b.WriteString(message.Content)
		for _, toolCall := range message.ToolCalls {
			b.WriteString("\n[tool_call ")
			b.WriteString(toolCall.Name)
			b.WriteString("]\n")
			b.WriteString(toolCall.Arguments)
		}
	}
	return TextPreview(b.String())
}

func ToolCallPreviews(toolCalls []aitypes.ToolCall) []ToolCallPreview {
	if len(toolCalls) == 0 {
		return nil
	}
	out := make([]ToolCallPreview, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		out = append(out, ToolCallPreview{
			ID:            toolCall.ID,
			Name:          toolCall.Name,
			ArgumentChars: len([]rune(toolCall.Arguments)),
			Arguments:     TextPreview(toolCall.Arguments),
		})
	}
	return out
}

func truncateRunes(text string, maxRunes int) string {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return fmt.Sprintf("%s... [truncated, %d chars total]", string(runes[:maxRunes]), len(runes))
}
