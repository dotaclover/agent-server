package customer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go-agent-studio/services/aitypes"
	"go-agent-studio/services/llm"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxToolTextChars      = 1000
	defaultRequestTimeout = 10 * time.Second
)

// Config holds customer tool configuration.
type Config struct {
	RAGAPIEndpoint string // 外部RAG服务地址 (例如 http://localhost:9093/api/search)
}

// RegisterTools registers all customer-facing tools.
func RegisterTools(registry *aitypes.ToolRegistry, cfg Config) {
	if cfg.RAGAPIEndpoint != "" {
		registerSearchLaborLaw(registry, cfg.RAGAPIEndpoint)
	}
}

// registerSearchLaborLaw registers the labor law search tool that calls external RAG API.
func registerSearchLaborLaw(registry *aitypes.ToolRegistry, endpoint string) {
	registry.Register(&aitypes.Tool{
		Name:        "search_labor_law",
		Description: "搜索劳动法知识库，返回相关法条、案例和政策解释。适用于试用期、劳动合同、工资、加班、社保、工伤、年假、辞退、离职、经济补偿、仲裁等劳动用工问题。",
		Parameters: `{
			"type":"object",
			"properties":{
				"query":{"type":"string","description":"搜索关键词或问题，例如：试用期最长多久、加班费如何计算"}
			},
			"required":["query"]
		}`,
		Execute: func(ctx context.Context, arguments string) (string, error) {
			var args struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal([]byte(arguments), &args); err != nil {
				return "", err
			}
			query, err := normalizeRequiredToolText("query", args.Query)
			if err != nil {
				return "", err
			}
			return callRAGAPI(ctx, endpoint, query)
		},
	})
}

// callRAGAPI calls external RAG service (9093 or 9094).
func callRAGAPI(ctx context.Context, endpoint, query string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", fmt.Errorf("RAG API endpoint is not configured")
	}

	reqBody := map[string]interface{}{
		"query": query,
		"top_k": 5,
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal RAG request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, defaultRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("RAG API call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := llm.ReadLimitedBody(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read RAG response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("RAG API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse and format response
	var result struct {
		Results []struct {
			Content  string                 `json:"content"`
			Text     string                 `json:"text"`
			Title    string                 `json:"title"`
			Section  string                 `json:"section"`
			Score    float64                `json:"score"`
			Source   string                 `json:"source"`
			Metadata map[string]interface{} `json:"metadata"`
		} `json:"results"`
		Query   string `json:"query"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		// If response is not JSON, return as-is
		return string(respBody), nil
	}

	// Format results for LLM consumption
	var b strings.Builder
	b.WriteString(fmt.Sprintf("查询：%s\n\n", result.Query))
	if len(result.Results) == 0 {
		message := strings.TrimSpace(result.Message)
		if message == "" {
			message = "未找到达到最低相关度要求的参考资料。"
		}
		b.WriteString(message)
		return strings.TrimSpace(b.String()), nil
	}
	b.WriteString("参考资料：\n\n")
	for i, item := range result.Results {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			content = strings.TrimSpace(item.Text)
		}
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, content))
		if item.Title != "" {
			b.WriteString(fmt.Sprintf("   标题：%s\n", item.Title))
		}
		if item.Section != "" {
			b.WriteString(fmt.Sprintf("   条款：%s\n", item.Section))
		}
		if item.Source != "" {
			b.WriteString(fmt.Sprintf("   来源：%s\n", item.Source))
		}
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String()), nil
}

func normalizeRequiredToolText(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if utf8.RuneCountInString(value) > maxToolTextChars {
		return "", fmt.Errorf("%s is too long; max %d characters", field, maxToolTextChars)
	}
	return value, nil
}
