package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/workflow"
)

func TestCronPreviewWindowHTTP(t *testing.T) {
	server := &Server{config: config.Config{WorkflowsEnabled: true}, flows: &workflow.Service{}}
	for _, test := range []struct {
		name, body    string
		status, count int
	}{
		{"partial inclusive range", `{"cron":"*/5 * * * * *","timeZone":"UTC","from":"2026-10-08T00:00:00Z","startsAt":"2026-10-08T00:00:10Z","endsAt":"2026-10-08T00:00:20Z"}`, 200, 3},
		{"cap ten", `{"cron":"* * * * * *","timeZone":"UTC","from":"2026-10-08T00:00:00Z"}`, 200, 10},
		{"none", `{"cron":"0 0 2 * * *","timeZone":"UTC","from":"2026-10-08T00:00:00Z","startsAt":"2026-10-08T03:00:00Z","endsAt":"2026-10-08T04:00:00Z"}`, 400, 0},
		{"invalid timestamp", `{"cron":"* * * * * *","timeZone":"UTC","startsAt":"invalid"}`, 400, 0},
		{"too many", `{"cron":"* * * * * *","timeZone":"UTC","count":11}`, 400, 0},
		{"reversed", `{"cron":"* * * * * *","timeZone":"UTC","startsAt":"2026-10-09T00:00:00Z","endsAt":"2026-10-08T00:00:00Z"}`, 400, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			server.workflowRequest(recorder, httptest.NewRequest("POST", "/v1/workflows/cron-preview", strings.NewReader(test.body)))
			if recorder.Code != test.status {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if test.status == 200 {
				var values []string
				if err := json.Unmarshal(recorder.Body.Bytes(), &values); err != nil {
					t.Fatal(err)
				}
				if len(values) != test.count {
					t.Fatalf("values=%v", values)
				}
			}
		})
	}
}
