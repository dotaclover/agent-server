package aitypes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"time"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type AgentRole string

const (
	RoleCustomer AgentRole = "customer"
	RoleOperator AgentRole = "operator"
	RoleAdmin    AgentRole = "admin"
)

type Message struct {
	ID         string     `json:"id"`
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolFunc func(ctx context.Context, arguments string) (string, error)

type Tool struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Parameters  string   `json:"parameters"`
	Execute     ToolFunc `json:"-"`
	Destructive bool     `json:"destructive"`
}

type ToolRegistry struct {
	tools map[string]*Tool
	order []string
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: map[string]*Tool{}}
}

func (r *ToolRegistry) Register(tool *Tool) {
	if r == nil || tool == nil || tool.Name == "" {
		return
	}
	if r.tools == nil {
		r.tools = map[string]*Tool{}
	}
	if _, exists := r.tools[tool.Name]; !exists {
		r.order = append(r.order, tool.Name)
	}
	r.tools[tool.Name] = tool
}

func (r *ToolRegistry) Get(name string) (*Tool, bool) {
	if r == nil || r.tools == nil || name == "" {
		return nil, false
	}
	t, ok := r.tools[name]
	return t, ok
}

func (r *ToolRegistry) List() []*Tool {
	if r == nil || r.tools == nil {
		return nil
	}
	out := make([]*Tool, 0, len(r.order))
	for _, name := range r.order {
		if t, ok := r.tools[name]; ok {
			out = append(out, t)
		}
	}
	return out
}

func (r *ToolRegistry) Names() []string {
	if r == nil {
		return nil
	}
	names := append([]string(nil), r.order...)
	sort.Strings(names)
	return names
}

type LLMProvider interface {
	Name() string
	SupportsTools() bool
	Chat(ctx context.Context, messages []Message, tools []*Tool, cfg *LLMConfig) (*LLMResponse, error)
}

type LLMConfig struct {
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
}

type LLMResponse struct {
	Content          string     `json:"content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	PromptTokens     int        `json:"prompt_tokens"`
	CompletionTokens int        `json:"completion_tokens"`
}

func NewMessage(role Role, content string) Message {
	return Message{
		ID:        NewID(),
		Role:      role,
		Content:   content,
		CreatedAt: time.Now(),
	}
}

func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b)
}
