package kube

import (
	"math"
	"testing"
)

func TestResourceQuantityParsing(t *testing.T) {
	tests := []struct {
		name string
		got  float64
		want float64
	}{
		{"millicpu", ParseCPU("250m"), 0.25},
		{"cpu", ParseCPU("2"), 2},
		{"gibibytes", ParseMemoryGiB("8Gi"), 8},
		{"mebibytes", ParseMemoryGiB("512Mi"), 0.5},
		{"decimal gigabytes", ParseMemoryGiB("1G"), 1e9 / (1 << 30)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if math.Abs(test.got-test.want) > 0.000001 {
				t.Fatalf("got %f, want %f", test.got, test.want)
			}
		})
	}
}

func TestParsePod(t *testing.T) {
	object := Object{
		"metadata": map[string]any{"name": "example-exec-1", "namespace": "spark", "labels": map[string]any{"spark-role": "executor", "sparkoperator.k8s.io/app-name": "example"}},
		"spec": map[string]any{"nodeName": "worker-1", "containers": []any{
			map[string]any{"resources": map[string]any{"requests": map[string]any{"cpu": "1500m", "memory": "2Gi"}}},
		}},
		"status": map[string]any{"phase": "Running", "startTime": "2026-09-20T00:00:00Z", "containerStatuses": []any{map[string]any{"restartCount": float64(2)}}},
	}
	pod := ParsePod(object)
	if pod.Name != "example-exec-1" || pod.Role != "executor" || pod.Phase != "RUNNING" {
		t.Fatalf("unexpected pod identity: %#v", pod)
	}
	if pod.Request.CPU != 1.5 || pod.Request.MemoryGiB != 2 || pod.Restarts != 2 {
		t.Fatalf("unexpected pod resources: %#v", pod)
	}
	if pod.NodePool != "baseline" {
		t.Fatalf("expected scheduled pod to default to baseline, got %q", pod.NodePool)
	}
}
