package customer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCallRAGAPIFormatsTextFieldResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"query": "Dify 工作流",
			"results": [{
				"title": "核心概念",
				"source": "Dify 中文文档",
				"section": "工作流",
				"text": "构建工作流应用来处理单轮任务，Web 应用界面和 API 提供了便捷的批量执行多个任务的访问方式。",
				"score": 1
			}],
			"total": 1
		}`))
	}))
	defer server.Close()

	got, err := callRAGAPI(context.Background(), server.URL, "Dify 工作流")
	if err != nil {
		t.Fatalf("callRAGAPI returned error: %v", err)
	}
	for _, want := range []string{"构建工作流应用", "标题：核心概念", "条款：工作流", "来源：Dify 中文文档"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted result missing %q: %s", want, got)
		}
	}
}

func TestCallRAGAPIReturnsNoResultsMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"query": "随便问",
			"results": [],
			"total": 0,
			"min_score": 0.5,
			"message": "未找到相关度不低于 50% 的参考资料，请换个更具体的问题或降低阈值后重试。"
		}`))
	}))
	defer server.Close()

	got, err := callRAGAPI(context.Background(), server.URL, "随便问")
	if err != nil {
		t.Fatalf("callRAGAPI returned error: %v", err)
	}
	for _, want := range []string{"查询：随便问", "未找到相关度不低于 50%"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted empty result missing %q: %s", want, got)
		}
	}
}

func TestCallRAGAPIEnrichesProductFollowUpQuery(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotQuery = req.Query
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"query":"ok","results":[],"total":0}`))
	}))
	defer server.Close()

	_, err := callRAGAPI(context.Background(), server.URL, "本地安装和线上版本有什么区别")
	if err != nil {
		t.Fatalf("callRAGAPI returned error: %v", err)
	}
	for _, want := range []string{"Dify", "Dify Cloud", "自部署", "Docker Compose"} {
		if !strings.Contains(gotQuery, want) {
			t.Fatalf("enriched query missing %q: %s", want, gotQuery)
		}
	}
}
