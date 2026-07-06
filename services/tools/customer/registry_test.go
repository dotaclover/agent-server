package customer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCallRAGAPIFormatsTextFieldResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"query": "试用期",
			"results": [{
				"title": "劳动合同法",
				"source": "劳动合同法",
				"section": "第十九条",
				"text": "劳动合同期限三个月以上不满一年的，试用期不得超过一个月。",
				"score": 1
			}],
			"total": 1
		}`))
	}))
	defer server.Close()

	got, err := callRAGAPI(context.Background(), server.URL, "试用期")
	if err != nil {
		t.Fatalf("callRAGAPI returned error: %v", err)
	}
	for _, want := range []string{"劳动合同期限三个月以上", "标题：劳动合同法", "条款：第十九条", "来源：劳动合同法"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted result missing %q: %s", want, got)
		}
	}
}
