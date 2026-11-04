package service

import (
	"context"
	"io"
	"log/slog"
	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/kube"
	"spark-control-center/backend/internal/store"
	"testing"
	"time"
)

type identityStore struct {
	fakeStore
	archived domain.SparkApplication
}

func (s *identityStore) GetApplicationSnapshotByID(_ context.Context, ns, id string) (domain.SparkApplication, error) {
	if ns == s.archived.Namespace && id == s.archived.ID {
		return s.archived, nil
	}
	return domain.SparkApplication{}, store.ErrSnapshotNotFound
}
func TestArchivedUIDNeverReadsOrMutatesSameNameReplacement(t *testing.T) {
	old := domain.SparkApplication{ID: "old-uid", Name: "demo", Namespace: "spark", State: "COMPLETED", CreatedAt: "2026-10-01T00:00:00Z", FinishedAt: "2026-10-01T00:01:00Z", DriverPod: "demo-driver", Logs: []string{"old log"}}
	k := &fakeKubernetes{object: runningObject(), pods: []kube.Pod{{Name: "demo-driver", Role: "driver", ApplicationUID: "uid-1"}}}
	st := &identityStore{archived: old}
	svc := New(config.Config{Namespaces: []string{"spark"}}, k, fakePrometheus{}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	app, err := svc.GetApplication(context.Background(), "spark", "old-uid")
	if err != nil || app.ID != old.ID || !app.Historical || app.Logs[0] != "old log" {
		t.Fatal(app, err)
	}
	if _, err = svc.KillApplication(context.Background(), "spark", "old-uid", "admin", ""); HTTPStatus(err) != 409 {
		t.Fatal(err)
	}
	if _, err = svc.DeleteApplication(context.Background(), "spark", "old-uid", "admin", ""); HTTPStatus(err) != 409 {
		t.Fatal(err)
	}
	if _, err = svc.SparkUIProxyTarget(context.Background(), "spark", "old-uid"); HTTPStatus(err) != 409 {
		t.Fatal(err)
	}
	if _, err = svc.GetDriverLogs(context.Background(), "spark", "old-uid", domain.DriverLogQuery{Source: "kubernetes", Limit: 100}); HTTPStatus(err) != 410 {
		t.Fatal(err)
	}
	if k.restartDisabled || k.driverTerminated || k.deleted {
		t.Fatal("replacement was modified")
	}
	from, to := historicalRange(old, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	if from.Format(time.RFC3339) != old.CreatedAt || to.Format(time.RFC3339) != old.FinishedAt {
		t.Fatal(from, to)
	}
}

type stopKube struct {
	fakeKubernetes
	requests int
}

func (k *stopKube) RequestApplicationStop(_ context.Context, _ string, obj kube.Object) error {
	k.requests++
	obj["metadata"].(map[string]any)["annotations"] = map[string]any{kube.StopAnnotation: "2026-11-04T00:00:00Z"}
	obj["spec"].(map[string]any)["restartPolicy"] = map[string]any{"type": "Never"}
	return nil
}
func TestStopIntentCatchesOperatorResubmissionButNotReplacement(t *testing.T) {
	k := &stopKube{fakeKubernetes: fakeKubernetes{object: runningObject(), pods: []kube.Pod{{Name: "demo-driver", Role: "driver", ApplicationUID: "uid-1"}}}}
	k.object["spec"].(map[string]any)["restartPolicy"] = map[string]any{"type": "Always"}
	svc := New(config.Config{Namespaces: []string{"spark"}}, k, fakePrometheus{}, &fakeStore{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := svc.KillApplication(context.Background(), "spark", "demo", "admin", ""); err != nil {
		t.Fatal(err)
	}
	if k.requests != 1 || k.deleted {
		t.Fatal("missing retained stop intent")
	}
	k.driverTerminated = false
	// Model the operator invalidating/recreating the driver after spec change.
	k.pods = []kube.Pod{{Name: "demo-new-driver", Role: "driver", ApplicationUID: "uid-1"}, {Name: "foreign-driver", Role: "driver", ApplicationUID: "other"}}
	if err := svc.enforceStop(context.Background(), "spark", "demo", "uid-1"); err != nil || !k.driverTerminated {
		t.Fatal(err)
	}
	if got := k.deletedPods[len(k.deletedPods)-1]; got != "deadline:demo-new-driver" {
		t.Fatal(got)
	}
	k.driverTerminated = false
	k.object["metadata"].(map[string]any)["uid"] = "new-uid"
	if err := svc.enforceStop(context.Background(), "spark", "demo", "uid-1"); err != nil || k.driverTerminated {
		t.Fatal("stale stop affected replacement", err)
	}
}
