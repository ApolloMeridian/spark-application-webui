package prometheus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUsageAndCapacity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		query := r.URL.Query().Get("query")
		if strings.Contains(r.URL.Path, "query_range") {
			values := [][]any{{float64(100), "0.5"}, {float64(160), "0.7"}}
			if strings.Contains(query, "memory_working_set") {
				values = [][]any{{float64(100), "1.5"}, {float64(160), "2.0"}}
			}
			response := map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "matrix",
					"result": []any{map[string]any{
						"metric": map[string]string{"pod": "demo-driver"},
						"values": values,
					}},
				},
			}
			_ = json.NewEncoder(w).Encode(response)
			return
		}
		value := "16"
		if strings.Contains(query, `resource="memory"`) {
			value = "64"
		}
		response := map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []any{map[string]any{
					"metric": map[string]string{},
					"value":  []any{float64(160), value},
				}},
			},
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := New(server.URL, 2*time.Second, time.Hour)
	usage, history, err := client.Usage(context.Background(), "spark", []string{"demo-driver"})
	if err != nil {
		t.Fatal(err)
	}
	if usage["demo-driver"].Current.CPU != 0.7 || usage["demo-driver"].Peak.MemoryGiB != 2 || len(history) != 2 {
		t.Fatalf("unexpected usage: %#v history=%#v", usage, history)
	}
	capacity, err := client.Capacity(context.Background())
	if err != nil || capacity.CPU != 16 || capacity.MemoryGiB != 64 {
		t.Fatalf("unexpected capacity: %#v err=%v", capacity, err)
	}
}
