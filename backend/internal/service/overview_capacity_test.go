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
	metrics "spark-control-center/backend/internal/prometheus"
)

func TestOverviewPoolsUseScheduledPodsAndFixedAllocatable(t *testing.T) {
	node := func(name, cpu, memory string, labels map[string]any) kube.Object {
		return kube.Object{
			"metadata": map[string]any{"name": name, "labels": labels},
			"status":   map[string]any{"allocatable": map[string]any{"cpu": cpu, "memory": memory}},
		}
	}
	nodes := []kube.Object{
		node("fixed", "7500m", "30Gi", map[string]any{"capacity.example.org/pool": "fixed"}),
		node("tke", "100k", "5Ti", map[string]any{"node.kubernetes.io/instance-type": "eklet", "capacity.example.org/pool": "elastic"}),
		node("acs", "100k", "5Ti", map[string]any{"type": "virtual-kubelet", "capacity.example.org/pool": "elastic"}),
		node("gke", "32", "64Gi", map[string]any{"capacity.example.org/pool": "fixed", "cloud.google.com/gke-nodepool": "spark-autoscaling"}),
		node("system", "8", "16Gi", nil),
	}
	cfg := config.Config{
		Namespaces: []string{"spark"}, OverviewDetectVirtual: true,
		OverviewFixedLabels:   map[string]string{"capacity.example.org/pool": "fixed"},
		OverviewElasticLabels: []map[string]string{{"cloud.google.com/gke-nodepool": "spark-autoscaling"}},
	}
	pod := func(name, role, phase, node string, cpu, memory float64) kube.Pod {
		return kube.Pod{
			Name: name, Namespace: "spark", Role: role, Phase: phase, Node: node,
			Labels:  map[string]string{"sparkoperator.k8s.io/app-name": "demo"},
			Request: domain.ResourceAmount{CPU: cpu, MemoryGiB: memory},
		}
	}
	pods := []kube.Pod{
		pod("demo-driver", "driver", "RUNNING", "fixed", 1, 2),
		pod("demo-exec-tke", "executor", "RUNNING", "tke", 2, 4),
		pod("demo-exec-acs", "executor", "PENDING", "acs", 3, 6),
		pod("demo-exec-gke", "executor", "RUNNING", "gke", 4, 8),
		pod("demo-exec-unscheduled", "executor", "PENDING", "", 64, 128),
		pod("demo-exec-done", "executor", "SUCCEEDED", "tke", 64, 128),
	}
	prom := overviewPrometheus{
		"demo-driver":   {Current: domain.ResourceAmount{CPU: 0.5, MemoryGiB: 1}},
		"demo-exec-tke": {Current: domain.ResourceAmount{CPU: 1, MemoryGiB: 2}},
		"demo-exec-gke": {Current: domain.ResourceAmount{CPU: 2, MemoryGiB: 4}},
	}
	getSummary := func(cfg config.Config, pods []kube.Pod) domain.DashboardSummary {
		t.Helper()
		svc := New(cfg, &fakeKubernetes{object: runningObject(), pods: pods}, prom, &fakeStore{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		svc.overviewNodes.Store(classifyOverviewNodes(nodes, cfg))
		now := time.Now()
		summary, err := svc.Summary(context.Background(), now.Add(-time.Hour), now)
		if err != nil {
			t.Fatal(err)
		}
		return summary
	}
	wantFixedCapacity := domain.ResourceAmount{CPU: 7.5, MemoryGiB: 30}
	wantElastic := domain.OverviewPoolUsage{
		Requested: domain.ResourceAmount{CPU: 9, MemoryGiB: 18},
		Used:      domain.ResourceAmount{CPU: 3, MemoryGiB: 6}, MetricsAvailable: true,
	}
	for _, autoDetect := range []bool{true, false} {
		cfg.OverviewDetectVirtual = autoDetect
		if !autoDetect {
			cfg.OverviewElasticLabels = append(cfg.OverviewElasticLabels, map[string]string{"capacity.example.org/pool": "elastic"})
		}
		summary := getSummary(cfg, pods)
		if summary.Capacity != wantFixedCapacity || summary.ResourcePools.Fixed.Requested.CPU != 1 ||
			summary.ResourcePools.Fixed.Used.CPU != 0.5 || summary.ResourcePools.Elastic != wantElastic {
			t.Fatalf("autoDetect=%v capacity=%+v pools=%+v", autoDetect, summary.Capacity, summary.ResourcePools)
		}
	}
	idle := getSummary(cfg, pods[:1])
	if idle.ResourcePools.Elastic != (domain.OverviewPoolUsage{MetricsAvailable: true}) {
		t.Fatalf("elastic nodes without pods must show zero usage and requests: %+v", idle.ResourcePools.Elastic)
	}
}

func TestOverviewIncludesExecutorsWhenSparkNameDiffersFromResourceName(t *testing.T) {
	const (
		appName  = "kafka-hp-jvm-agg-2"
		executor = "driverhphourtoday-attempt-exec-1"
		nodeName = "eklet-subnet"
	)
	app := runningObject()
	app["metadata"].(map[string]any)["name"] = appName
	status := app["status"].(map[string]any)
	status["driverInfo"] = map[string]any{"podName": appName + "-driver"}
	status["executorState"] = map[string]any{executor: "RUNNING"}

	pods := []kube.Pod{
		{
			Name: appName + "-driver", Role: "driver", Phase: "RUNNING", Node: "worker",
			ApplicationUID: "uid-1",
			Labels:         map[string]string{"spark-app-name": "driverhphourtoday", "spark-app-selector": "spark-current"},
		},
		{
			Name: executor, Role: "executor", Phase: "RUNNING", Node: nodeName,
			Labels:   map[string]string{"spark-app-name": "driverhphourtoday", "spark-app-selector": "spark-current"},
			NodePool: "baseline",
			Request:  domain.ResourceAmount{CPU: 2, MemoryGiB: 6},
		},
		{
			Name: "another-attempt-exec-1", Role: "executor", Phase: "RUNNING", Node: nodeName,
			Labels:  map[string]string{"spark-app-name": "driverhphourtoday", "spark-app-selector": "spark-other"},
			Request: domain.ResourceAmount{CPU: 32, MemoryGiB: 64},
		},
	}
	cfg := config.Config{
		Namespaces:            []string{"spark"},
		OverviewElasticLabels: []map[string]string{{"node.kubernetes.io/instance-type": "eklet"}},
	}
	usage := domain.ResourceAmount{CPU: 1.25, MemoryGiB: 3}
	prom := overviewPrometheus{executor: {Current: usage}}
	svc := New(cfg, &fakeKubernetes{object: app, pods: pods}, prom, &fakeStore{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.overviewNodes.Store(classifyOverviewNodes([]kube.Object{{
		"metadata": map[string]any{"name": nodeName, "labels": map[string]any{"node.kubernetes.io/instance-type": "eklet"}},
	}}, cfg))

	ctx := context.Background()
	detail, err := svc.GetApplication(ctx, "spark", appName)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Executors) != 1 || detail.Executors[0].Node != nodeName || detail.Executors[0].NodePool != "autoscale" {
		t.Fatalf("expected only the current executor on %s: %+v", nodeName, detail.Executors)
	}
	now := time.Now()
	summary, err := svc.Summary(ctx, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	want := domain.OverviewPoolUsage{Requested: pods[1].Request, Used: usage, MetricsAvailable: true}
	if summary.ResourcePools == nil {
		t.Fatal("resource pools missing")
	}
	if got := summary.ResourcePools.Elastic; got != want {
		t.Fatalf("elastic usage = %+v, want %+v", got, want)
	}
}

type overviewPrometheus map[string]metrics.PodUsage

func (p overviewPrometheus) Usage(context.Context, string, []string) (map[string]metrics.PodUsage, []domain.MetricPoint, error) {
	return p, nil, nil
}

func (p overviewPrometheus) UsageRange(ctx context.Context, ns string, pods []string, _, _ time.Time) (map[string]metrics.PodUsage, []domain.MetricPoint, error) {
	return p.Usage(ctx, ns, pods)
}
