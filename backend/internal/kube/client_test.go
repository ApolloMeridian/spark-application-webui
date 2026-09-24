package kube

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestMutatingRequestsUseExpectedKubernetesSemantics(t *testing.T) {
	var requests []struct {
		method, path, contentType, rawQuery string
		body                                map[string]any
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entry := struct {
			method, path, contentType, rawQuery string
			body                                map[string]any
		}{method: r.Method, path: r.URL.Path, contentType: r.Header.Get("Content-Type"), rawQuery: r.URL.RawQuery}
		_ = json.NewDecoder(r.Body).Decode(&entry.body)
		requests = append(requests, entry)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"metadata":{"name":"demo","namespace":"spark"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"kind":"Status","status":"Success"}`))
	}))
	defer server.Close()
	client := &Client{baseURL: server.URL, token: "token", http: &http.Client{Timeout: time.Second}, apiGroup: "sparkoperator.k8s.io", apiVersion: "v1beta2", resourcePath: "sparkapplications", allowedLookup: map[string]bool{"spark": true}}
	if _, err := client.CreateSparkApplication(context.Background(), "spark", Object{"apiVersion": "sparkoperator.k8s.io/v1beta2", "kind": "SparkApplication"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DryRunCreateSparkApplication(context.Background(), "spark", Object{"apiVersion": "sparkoperator.k8s.io/v1beta2", "kind": "SparkApplication"}); err != nil {
		t.Fatal(err)
	}
	if err := client.DisableSparkApplicationRestart(context.Background(), "spark", "demo"); err != nil {
		t.Fatal(err)
	}
	if err := client.ForceDeletePod(context.Background(), "spark", "demo-driver"); err != nil {
		t.Fatal(err)
	}
	if err := client.TerminatePodByDeadline(context.Background(), "spark", "demo-driver"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 5 || requests[0].method != http.MethodPost || requests[1].method != http.MethodPost || requests[2].method != http.MethodPatch || requests[3].method != http.MethodDelete || requests[4].method != http.MethodPatch {
		t.Fatalf("unexpected requests: %#v", requests)
	}
	if requests[1].rawQuery != "dryRun=All&fieldManager=spark-control-center" {
		t.Fatalf("dry-run request must use Kubernetes server-side validation, got %q", requests[1].rawQuery)
	}
	if requests[2].contentType != "application/merge-patch+json" || String(Object(requests[2].body), "spec", "restartPolicy", "type") != "Never" {
		t.Fatalf("unexpected restart patch: %#v", requests[2])
	}
	if requests[3].body["gracePeriodSeconds"] != float64(0) || requests[3].body["propagationPolicy"] != "Background" {
		t.Fatalf("unexpected pod deletion: %#v", requests[3])
	}
	if requests[4].contentType != "application/merge-patch+json" || Int64(Object(requests[4].body), "spec", "activeDeadlineSeconds") != 1 {
		t.Fatalf("unexpected driver deadline patch: %#v", requests[4])
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
