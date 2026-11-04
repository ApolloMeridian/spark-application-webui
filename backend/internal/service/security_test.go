package service

import (
	"context"
	"io"
	"log/slog"
	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/kube"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRelatedPodsNeverUseApplicationNamePrefix(t *testing.T) {
	app := runningObject()
	app["metadata"].(map[string]any)["name"] = "etl"
	pods := []kube.Pod{
		{Name: "etl-driver", Role: "driver"},
		{Name: "owned-exec", Role: "executor", ApplicationUID: "uid-1"},
		{Name: "etl-daily-exec", Role: "executor", Labels: map[string]string{"sparkoperator.k8s.io/app-name": "etl-daily"}},
		{Name: "etl-daily-driver", Role: "driver"},
		{Name: "etl-forged", Role: "executor", ApplicationUID: "foreign", Labels: map[string]string{"sparkoperator.k8s.io/app-name": "etl"}},
	}
	result := relatedPods(app, pods)
	if len(result) != 2 || result[0].Name != "etl-driver" || result[1].Name != "owned-exec" {
		t.Fatalf("foreign Pods selected: %+v", result)
	}
}

type cacheKube struct {
	fakeKubernetes
	calls atomic.Int32
}

func TestNativeExecutorSelectorComesFromValidatedDriver(t *testing.T) {
	app := runningObject()
	pods := []kube.Pod{
		{Name: "driver", Role: "driver", ApplicationUID: "uid-1", Labels: map[string]string{"spark-app-selector": "native-app-id"}},
		{Name: "native-exec", Role: "executor", Labels: map[string]string{"spark-app-selector": "native-app-id"}},
		{Name: "other-exec", Role: "executor", Labels: map[string]string{"spark-app-selector": "other-id"}},
		{Name: "conflicting-exec", Role: "executor", ApplicationUID: "foreign", Labels: map[string]string{"spark-app-selector": "native-app-id"}},
	}
	result := relatedPods(app, pods)
	if len(result) != 2 || result[1].Name != "native-exec" {
		t.Fatalf("native Executor association failed: %+v", result)
	}
	pods[0].ApplicationUID = "foreign"
	if result = relatedPods(app, pods); len(result) != 0 {
		t.Fatalf("foreign Driver selector was trusted: %+v", result)
	}
}

func (k *cacheKube) ListSparkApplications(ctx context.Context, ns string) ([]kube.Object, error) {
	k.calls.Add(1)
	time.Sleep(20 * time.Millisecond)
	return []kube.Object{k.object}, nil
}

type cacheStore struct {
	fakeStore
	writes atomic.Int32
}

func TestKillDoesNotTrustForeignDriverStatus(t *testing.T) {
	for _, withOwnedDriver := range []bool{false, true} {
		app := runningObject()
		app["status"].(map[string]any)["driverInfo"] = map[string]any{"podName": "other-driver"}
		k := &fakeKubernetes{object: app, pods: []kube.Pod{{Name: "other-driver", Role: "driver", ApplicationUID: "foreign"}}}
		if withOwnedDriver {
			k.pods = append(k.pods, kube.Pod{Name: "demo-driver", Role: "driver", ApplicationUID: "uid-1"})
		}
		s := New(config.Config{Namespaces: []string{"spark"}}, k, fakePrometheus{}, &fakeStore{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		_, err := s.KillApplication(context.Background(), "spark", "demo", "admin", "test")
		if withOwnedDriver {
			if err != nil || len(k.deletedPods) != 1 || k.deletedPods[0] != "deadline:demo-driver" {
				t.Fatal("validated Driver was not used", k.deletedPods, err)
			}
		} else if err == nil || k.restartDisabled || k.driverTerminated || len(k.deletedPods) != 0 {
			t.Fatal("unverified Driver was mutated", err, k.deletedPods)
		}
	}
}

func (s *cacheStore) UpsertApplications(context.Context, []domain.SparkApplication) error {
	s.writes.Add(1)
	return nil
}
func TestListCoalescesRequestsWithoutWritingHistory(t *testing.T) {
	k := &cacheKube{fakeKubernetes: fakeKubernetes{object: runningObject()}}
	pg := &cacheStore{}
	s := New(config.Config{Namespaces: []string{"spark"}}, k, fakePrometheus{}, pg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.ListApplications(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if k.calls.Load() != 1 || pg.writes.Load() != 0 {
		t.Fatal(k.calls.Load(), pg.writes.Load())
	}
	s.publishChange(domain.ApplicationChange{Namespace: "spark", Name: "demo"})
	if _, err := s.ListApplications(context.Background()); err != nil {
		t.Fatal(err)
	}
	if k.calls.Load() != 2 {
		t.Fatal("Watch did not invalidate cache")
	}
}
