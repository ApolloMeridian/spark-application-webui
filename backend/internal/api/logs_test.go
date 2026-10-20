package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
)

type driverLogService struct {
	fakeService
	query domain.DriverLogQuery
}

func (s *driverLogService) GetDriverLogs(_ context.Context, _, _ string, q domain.DriverLogQuery) (domain.DriverLogWindow, error) {
	s.query = q
	return domain.DriverLogWindow{Source: q.Source, Mode: q.Mode, Lines: []string{"early-line"}, StartLine: q.StartLine, EndLine: q.StartLine, Limit: q.Limit}, nil
}

func TestDriverLogsQueryAndNamespaceValidation(t *testing.T) {
	reader := &driverLogService{}
	server := &Server{service: reader, config: config.Config{Namespaces: []string{"spark"}}}
	for _, test := range []struct {
		query string
		want  int
	}{
		{"source=kubernetes&mode=range&startLine=1&endLine=10000", 200},
		{"source=kubernetes&mode=range&startLine=6001&endLine=8000", 200},
		{"source=kubernetes&mode=range&startLine=1&endLine=10001", 400},
		{"mode=range&startLine=-1&endLine=100", 400},
		{"mode=range&startLine=100&endLine=1", 400},
		{"limit=10001", 400}, {"limit=abc", 400}, {"source=arbitrary", 400},
		{"source=loki&from=2026-10-20T00:00:00Z&to=2026-10-20T01:00:00Z", 200},
		{"source=loki&from=bad&to=bad", 400},
	} {
		r := httptest.NewRequest("GET", "/driver/logs?"+test.query, nil)
		r.SetPathValue("namespace", "spark")
		r.SetPathValue("name", "job")
		w := httptest.NewRecorder()
		server.driverLogs(w, r)
		if w.Code != test.want {
			t.Fatalf("%s: %d %s", test.query, w.Code, w.Body.String())
		}
		if w.Code == 200 {
			var out domain.DriverLogWindow
			if err := json.NewDecoder(strings.NewReader(w.Body.String())).Decode(&out); err != nil {
				t.Fatal(err)
			}
		}
	}
	r := httptest.NewRequest("GET", "/driver/logs", nil)
	r.SetPathValue("namespace", "private")
	w := httptest.NewRecorder()
	server.driverLogs(w, r)
	if w.Code != 403 {
		t.Fatal("namespace not enforced", w.Code)
	}
}
