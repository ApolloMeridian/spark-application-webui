package loki

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestQueryLogsBuildsSelectorAndSorts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if !strings.Contains(query, `namespace="spark"`) || !strings.Contains(query, `pod="demo-exec-1"`) || !strings.Contains(query, `spark_role="executor"`) {
			t.Fatalf("unexpected selector: %s", query)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[{"stream":{"container":"spark-kubernetes-executor"},"values":[["2000000000","second"],["1000000000","first"]]}]}}`))
	}))
	defer server.Close()
	client := New(server.URL, time.Second, 5000)
	entries, err := client.QueryLogs(context.Background(), "spark", "demo-exec-1", time.Unix(0, 0), time.Unix(3, 0), "forward", 100)
	if err != nil || len(entries) != 2 || entries[0].Line != "first" || entries[1].Line != "second" {
		t.Fatalf("unexpected entries: %#v err=%v", entries, err)
	}
}
