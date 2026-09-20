package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/kube"
	metrics "spark-control-center/backend/internal/prometheus"
)

type fakeKubernetes struct {
	object          kube.Object
	pods            []kube.Pod
	deleteErr       error
	podDeleteErr    error
	restartPatchErr error
	deleted         bool
	restartDisabled bool
	deletedPods     []string
	created         kube.Object
	createErr       error
}

func (f *fakeKubernetes) Ready(context.Context, string) error { return nil }
func (f *fakeKubernetes) ListSparkApplications(context.Context, string) ([]kube.Object, error) {
	return []kube.Object{f.object}, nil
}
func (f *fakeKubernetes) GetSparkApplication(context.Context, string, string) (kube.Object, error) {
	return f.object, nil
}
func (f *fakeKubernetes) CreateSparkApplication(_ context.Context, _ string, object kube.Object) (kube.Object, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.created = object
	if f.object != nil {
		return f.object, nil
	}
	return object, nil
}
func (f *fakeKubernetes) DisableSparkApplicationRestart(context.Context, string, string) error {
	f.restartDisabled = f.restartPatchErr == nil
	return f.restartPatchErr
}
func (f *fakeKubernetes) DeleteSparkApplication(context.Context, string, string) error {
	f.deleted = f.deleteErr == nil
	return f.deleteErr
}
func (f *fakeKubernetes) ForceDeletePod(_ context.Context, _, name string) error {
	if f.podDeleteErr != nil {
		return f.podDeleteErr
	}
	f.deletedPods = append(f.deletedPods, name)
	return nil
}
func (f *fakeKubernetes) ListPods(context.Context, string) ([]kube.Pod, error) { return f.pods, nil }
func (f *fakeKubernetes) PodLog(context.Context, string, string, int) ([]string, error) {
	return []string{"INFO ready"}, nil
}
func (f *fakeKubernetes) ListEvents(context.Context, string) ([]domain.KubernetesEvent, error) {
	return []domain.KubernetesEvent{}, nil
}

type fakePrometheus struct{}

func (fakePrometheus) Usage(context.Context, string, []string) (map[string]metrics.PodUsage, []domain.MetricPoint, error) {
	return map[string]metrics.PodUsage{"demo-driver": {Current: domain.ResourceAmount{CPU: 0.5, MemoryGiB: 1}, Peak: domain.ResourceAmount{CPU: 0.8, MemoryGiB: 1.5}}}, []domain.MetricPoint{}, nil
}
func (fakePrometheus) Capacity(context.Context) (domain.ResourceAmount, error) {
	return domain.ResourceAmount{CPU: 8, MemoryGiB: 32}, nil
}

type fakeStore struct{ rows []domain.OperationAudit }

func (f *fakeStore) Insert(_ context.Context, row domain.OperationAudit) error {
	f.rows = append(f.rows, row)
	return nil
}
func (f *fakeStore) List(context.Context, int) ([]domain.OperationAudit, error)          { return f.rows, nil }
func (f *fakeStore) UpsertApplications(context.Context, []domain.SparkApplication) error { return nil }
func (f *fakeStore) HistorySummary(_ context.Context, from, to time.Time) (domain.HistorySummary, error) {
	return domain.HistorySummary{Submitted: 1, From: from.UTC().Format(time.RFC3339), To: to.UTC().Format(time.RFC3339)}, nil
}
func (f *fakeStore) Ping(context.Context) error { return nil }
func (f *fakeStore) Close()                     {}

func runningObject() kube.Object {
	return kube.Object{
		"metadata": map[string]any{"name": "demo", "namespace": "spark", "uid": "uid-1", "creationTimestamp": "2026-09-20T00:00:00Z", "labels": map[string]any{"owner": "data-team"}},
		"spec":     map[string]any{"image": "spark:3.5", "sparkVersion": "3.5.3", "driver": map[string]any{"cores": float64(1), "memory": "2g"}, "executor": map[string]any{"cores": float64(2), "memory": "4g"}},
		"status":   map[string]any{"applicationState": map[string]any{"state": "RUNNING"}, "driverInfo": map[string]any{"podName": "demo-driver"}, "submissionID": "sub-1"},
	}
}

func completedObject() kube.Object {
	object := runningObject()
	status := object["status"].(map[string]any)
	status["applicationState"] = map[string]any{"state": "COMPLETED"}
	status["executorState"] = map[string]any{
		"demo-exec-1": "FAILED",
		"demo-exec-2": "COMPLETED",
	}
	status["terminationTime"] = "2026-09-20T00:05:00Z"
	return object
}

func TestListMapsRealResources(t *testing.T) {
	kubernetes := &fakeKubernetes{object: runningObject(), pods: []kube.Pod{{Name: "demo-driver", Namespace: "spark", Role: "driver", Phase: "RUNNING", Node: "worker-1", NodePool: "baseline", Request: domain.ResourceAmount{CPU: 1, MemoryGiB: 2}, Labels: map[string]string{"sparkoperator.k8s.io/app-name": "demo"}}}}
	store := &fakeStore{}
	svc := New(config.Config{ClusterName: "kubernetes", Namespaces: []string{"spark"}}, kubernetes, fakePrometheus{}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	apps, err := svc.ListApplications(context.Background())
	if err != nil || len(apps) != 1 {
		t.Fatalf("list failed: apps=%d err=%v", len(apps), err)
	}
	app := apps[0]
	if app.State != "RUNNING" || app.Owner != "data-team" || app.Driver.Current == nil || app.Driver.Current.CPU != 0.5 {
		t.Fatalf("unexpected mapped application: %#v", app)
	}
}

func TestKillRetainsApplicationAndForceDeletesPods(t *testing.T) {
	kubernetes := &fakeKubernetes{object: runningObject(), pods: []kube.Pod{
		{Name: "demo-driver", Role: "driver", Labels: map[string]string{"sparkoperator.k8s.io/app-name": "demo"}},
		{Name: "demo-exec-1", Role: "executor", Labels: map[string]string{"sparkoperator.k8s.io/app-name": "demo"}},
	}}
	store := &fakeStore{}
	svc := New(config.Config{Namespaces: []string{"spark"}}, kubernetes, fakePrometheus{}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	audit, err := svc.KillApplication(context.Background(), "spark", "demo", "test")
	if err != nil || kubernetes.deleted || !kubernetes.restartDisabled || audit.Result != "SUCCESS" || len(store.rows) != 1 {
		t.Fatalf("unexpected kill result: appDeleted=%v restartDisabled=%v audit=%#v rows=%d err=%v", kubernetes.deleted, kubernetes.restartDisabled, audit, len(store.rows), err)
	}
	if audit.Operation != "KILL" || len(kubernetes.deletedPods) != 2 || kubernetes.deletedPods[0] != "demo-driver" || kubernetes.deletedPods[1] != "demo-exec-1" {
		t.Fatalf("expected driver/executor force deletion with retained CR, pods=%v audit=%#v", kubernetes.deletedPods, audit)
	}
}

func TestKillFailureIsAudited(t *testing.T) {
	kubernetes := &fakeKubernetes{object: runningObject(), podDeleteErr: errors.New("forbidden")}
	store := &fakeStore{}
	svc := New(config.Config{Namespaces: []string{"spark"}}, kubernetes, fakePrometheus{}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	audit, err := svc.KillApplication(context.Background(), "spark", "demo", "test")
	if err == nil || audit.Result != "FAILED" || len(store.rows) != 1 {
		t.Fatalf("expected failed audit, got audit=%#v rows=%d err=%v", audit, len(store.rows), err)
	}
}

func TestSubmitCreatesApplicationAndAudits(t *testing.T) {
	created := runningObject()
	kubernetes := &fakeKubernetes{object: created}
	store := &fakeStore{}
	svc := New(config.Config{ClusterName: "kubernetes", Namespaces: []string{"spark"}, SparkAPIVersion: "sparkoperator.k8s.io/v1beta2"}, kubernetes, fakePrometheus{}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	manifest := "apiVersion: sparkoperator.k8s.io/v1beta2\nkind: SparkApplication\nmetadata:\n  name: demo\n  namespace: spark\nspec:\n  image: spark:3.5\n"
	app, err := svc.SubmitApplication(context.Background(), "spark", manifest)
	if err != nil || kubernetes.created == nil || app.Name != "demo" || len(store.rows) != 1 || store.rows[0].Operation != "SUBMIT" {
		t.Fatalf("unexpected submit: app=%#v created=%#v audit=%#v err=%v", app, kubernetes.created, store.rows, err)
	}
}

func TestSubmitRejectsNamespaceMismatch(t *testing.T) {
	svc := New(config.Config{Namespaces: []string{"spark"}, SparkAPIVersion: "sparkoperator.k8s.io/v1beta2"}, &fakeKubernetes{}, fakePrometheus{}, &fakeStore{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	manifest := "apiVersion: sparkoperator.k8s.io/v1beta2\nkind: SparkApplication\nmetadata:\n  name: demo\n  namespace: other\n"
	if _, err := svc.SubmitApplication(context.Background(), "spark", manifest); err == nil {
		t.Fatal("expected namespace mismatch to be rejected")
	}
}

func TestSparkUIProxyTargetUsesDriverService(t *testing.T) {
	object := runningObject()
	object["status"].(map[string]any)["driverInfo"] = map[string]any{"podName": "demo-driver", "webUIServiceName": "demo-ui-svc", "webUIPort": float64(4040)}
	svc := New(config.Config{Namespaces: []string{"spark"}}, &fakeKubernetes{object: object}, fakePrometheus{}, &fakeStore{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	target, err := svc.SparkUIProxyTarget(context.Background(), "spark", "demo")
	if err != nil || target != "http://demo-ui-svc.spark.svc:4040" {
		t.Fatalf("unexpected Spark UI target: %q err=%v", target, err)
	}
}

func TestEventLogDetection(t *testing.T) {
	object := completedObject()
	object["spec"].(map[string]any)["sparkConf"] = map[string]any{"spark.eventLog.enabled": "true", "spark.eventLog.dir": "s3a://spark-history/event-logs"}
	svc := New(config.Config{}, &fakeKubernetes{}, fakePrometheus{}, &fakeStore{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if app := svc.mapApplication(object, nil, map[string]metrics.PodUsage{}); !app.EventLogEnabled {
		t.Fatal("expected EventLog-enabled application")
	}
}

func TestSummaryExcludesCompletedApplicationResources(t *testing.T) {
	kubernetes := &fakeKubernetes{object: completedObject()}
	store := &fakeStore{}
	svc := New(config.Config{ClusterName: "kubernetes", Namespaces: []string{"spark"}}, kubernetes, fakePrometheus{}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	summary, err := svc.Summary(context.Background(), time.Now().Add(-7*24*time.Hour), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 1 || summary.ByState["COMPLETED"] != 1 {
		t.Fatalf("completed app must remain in status totals: %#v", summary)
	}
	if summary.Requested.CPU != 0 || summary.Requested.MemoryGiB != 0 || summary.Used.CPU != 0 || summary.NodePools.Pending != 0 {
		t.Fatalf("completed resources must not contribute to live capacity: %#v", summary)
	}
}

func TestSummaryExcludesHistoricalExecutorsOfRunningApplication(t *testing.T) {
	object := runningObject()
	status := object["status"].(map[string]any)
	status["executorState"] = map[string]any{
		"demo-exec-1": "FAILED",
		"demo-exec-2": "COMPLETED",
		"demo-exec-3": "RUNNING",
	}
	kubernetes := &fakeKubernetes{object: object, pods: []kube.Pod{
		{Name: "demo-driver", Namespace: "spark", Role: "driver", Phase: "RUNNING", Node: "worker-1", NodePool: "baseline", Request: domain.ResourceAmount{CPU: 1, MemoryGiB: 2}, Labels: map[string]string{"sparkoperator.k8s.io/app-name": "demo"}},
		{Name: "demo-exec-3", Namespace: "spark", Role: "executor", Phase: "RUNNING", Node: "worker-2", NodePool: "baseline", Request: domain.ResourceAmount{CPU: 2, MemoryGiB: 4}, Labels: map[string]string{"sparkoperator.k8s.io/app-name": "demo"}},
	}}
	svc := New(config.Config{ClusterName: "kubernetes", Namespaces: []string{"spark"}}, kubernetes, fakePrometheus{}, &fakeStore{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	summary, err := svc.Summary(context.Background(), time.Now().Add(-7*24*time.Hour), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Requested.CPU != 3 || summary.Requested.MemoryGiB != 6 {
		t.Fatalf("historical executors must not contribute to live requests: %#v", summary.Requested)
	}
	if summary.NodePools.Baseline != 1 || summary.NodePools.Pending != 0 {
		t.Fatalf("historical executors must not contribute to node pools: %#v", summary.NodePools)
	}
}

func TestDeleteTerminalApplicationAndAudit(t *testing.T) {
	kubernetes := &fakeKubernetes{object: completedObject()}
	store := &fakeStore{}
	svc := New(config.Config{Namespaces: []string{"spark"}}, kubernetes, fakePrometheus{}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	audit, err := svc.DeleteApplication(context.Background(), "spark", "demo", "cleanup")
	if err != nil || !kubernetes.deleted || audit.Operation != "DELETE" || audit.Result != "SUCCESS" {
		t.Fatalf("unexpected delete result: deleted=%v audit=%#v err=%v", kubernetes.deleted, audit, err)
	}
}

func TestDeleteRejectsRunningApplication(t *testing.T) {
	kubernetes := &fakeKubernetes{object: runningObject()}
	store := &fakeStore{}
	svc := New(config.Config{Namespaces: []string{"spark"}}, kubernetes, fakePrometheus{}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	audit, err := svc.DeleteApplication(context.Background(), "spark", "demo", "cleanup")
	if err == nil || kubernetes.deleted || audit.Operation != "DELETE" || audit.Result != "FAILED" {
		t.Fatalf("expected terminal-state guard, deleted=%v audit=%#v err=%v", kubernetes.deleted, audit, err)
	}
}

func TestCompletedApplicationNormalizesHistoricalFailedExecutor(t *testing.T) {
	svc := New(config.Config{ClusterName: "kubernetes", Namespaces: []string{"spark"}}, &fakeKubernetes{}, fakePrometheus{}, &fakeStore{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	app := svc.mapApplication(completedObject(), nil, map[string]metrics.PodUsage{})
	states := map[string]domain.ExecutorPod{}
	for _, executor := range app.Executors {
		states[executor.Name] = executor
	}
	failed := states["demo-exec-1"]
	if failed.State != "TERMINATED" || failed.RawState != "FAILED" {
		t.Fatalf("expected neutral presentation with raw state preserved, got %#v", failed)
	}
	if states["demo-exec-2"].State != "SUCCEEDED" {
		t.Fatalf("completed executor should be SUCCEEDED, got %#v", states["demo-exec-2"])
	}
}
