package service

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/kube"
)

type logWindowKube struct {
	fakeKubernetes
	pod string
}

func (f *logWindowKube) PodLogWindow(_ context.Context, _, pod string, q domain.DriverLogQuery) (domain.DriverLogWindow, error) {
	f.pod = pod
	return domain.DriverLogWindow{Lines: []string{"first"}, StartLine: q.StartLine}, nil
}

type driverRoleLogs struct {
	role, pod string
	limit     int
}

func (f *driverRoleLogs) QueryLogs(context.Context, string, string, time.Time, time.Time, string, int) ([]domain.LogEntry, error) {
	panic("executor query must not be used for driver")
}
func (f *driverRoleLogs) QueryRoleLogs(_ context.Context, _, pod, role string, _, _ time.Time, _ string, limit int) ([]domain.LogEntry, error) {
	f.role, f.pod, f.limit = role, pod, limit
	return []domain.LogEntry{{Timestamp: "2026-10-20T00:00:00Z", Line: "retained driver"}}, nil
}
func TestDriverLogSourceUsesApplicationDriverAndNamespaces(t *testing.T) {
	k := &logWindowKube{fakeKubernetes: fakeKubernetes{object: kube.Object{"status": map[string]any{"driverInfo": map[string]any{"podName": "actual-driver"}}}}}
	l := &driverRoleLogs{}
	s := New(config.Config{Namespaces: []string{"spark"}, LokiMaxEntries: 5000}, k, fakePrometheus{}, &fakeStore{}, slog.New(slog.NewTextHandler(io.Discard, nil)), l)
	q := domain.DriverLogQuery{Source: "kubernetes", Mode: "range", StartLine: 1, EndLine: 2000, Limit: 2000}
	if _, err := s.GetDriverLogs(context.Background(), "spark", "demo", q); err != nil || k.pod != "actual-driver" {
		t.Fatal(k.pod, err)
	}
	if _, err := s.GetDriverLogs(context.Background(), "private", "demo", q); err == nil {
		t.Fatal("private namespace accepted")
	}
	q.Source = "loki"
	q.Limit = 10000
	q.From = "2026-10-20T00:00:00Z"
	q.To = "2026-10-20T01:00:00Z"
	q.Direction = "forward"
	out, err := s.GetDriverLogs(context.Background(), "spark", "demo", q)
	if err != nil || l.role != "driver" || l.pod != "actual-driver" || l.limit != 5000 || out.Lines[0] != "2026-10-20T00:00:00Z retained driver" {
		t.Fatal(out, l, err)
	}
	s.logs = nil
	if _, err = s.GetDriverLogs(context.Background(), "spark", "demo", q); err == nil {
		t.Fatal("missing Loki silently accepted")
	}
}
