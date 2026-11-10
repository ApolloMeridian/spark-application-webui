package service

import (
	"context"
	"math"
	"slices"
	"time"

	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/kube"
)

type overviewNodeSnapshot struct {
	elasticByNode map[string]bool
	fixedCapacity domain.ResourceAmount
}

func (s *Service) collectOverviewNodes(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		work, cancel := context.WithTimeout(ctx, 10*time.Second)
		nodes, err := s.kubernetes.ListNodes(work)
		cancel()
		if err != nil {
			s.overviewNodes.Store(nil)
			if ctx.Err() == nil {
				s.logger.Warn("overview resource pools unavailable", "error", err)
			}
		} else {
			s.overviewNodes.Store(classifyOverviewNodes(nodes, s.config))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func classifyOverviewNodes(nodes []kube.Object, cfg config.Config) *overviewNodeSnapshot {
	snapshot := &overviewNodeSnapshot{elasticByNode: make(map[string]bool, len(nodes))}
	for _, node := range nodes {
		name := kube.String(node, "metadata", "name")
		if name == "" || kube.String(node, "metadata", "deletionTimestamp") != "" {
			continue
		}
		if _, exists := snapshot.elasticByNode[name]; exists {
			continue
		}
		labels := kube.StringMap(node, "metadata", "labels")
		elastic := slices.ContainsFunc(cfg.OverviewElasticLabels, func(rule map[string]string) bool {
			return len(rule) > 0 && matchesNodeLabels(labels, rule)
		})
		if elastic || (cfg.OverviewDetectVirtual && isVirtualNode(node, labels)) {
			snapshot.elasticByNode[name] = true
			continue
		}
		if !matchesNodeLabels(labels, cfg.OverviewFixedLabels) {
			continue
		}
		snapshot.elasticByNode[name] = false
		cpu := kube.ParseCPU(kube.String(node, "status", "allocatable", "cpu"))
		memory := kube.ParseMemoryGiB(kube.String(node, "status", "allocatable", "memory"))
		if cpu > 0 && !math.IsInf(cpu, 0) {
			snapshot.fixedCapacity.CPU += cpu
		}
		if memory > 0 && !math.IsInf(memory, 0) {
			snapshot.fixedCapacity.MemoryGiB += memory
		}
	}
	return snapshot
}

func matchesNodeLabels(labels, rule map[string]string) bool {
	for key, value := range rule {
		if actual, exists := labels[key]; !exists || actual != value {
			return false
		}
	}
	return true
}

func isVirtualNode(node kube.Object, labels map[string]string) bool {
	if labels["node.kubernetes.io/instance-type"] == "eklet" || labels["type"] == "virtual-kubelet" {
		return true
	}
	if _, exists := labels["virtual-kubelet.io/provider"]; exists {
		return true
	}
	taints, _ := kube.Slice(node, "spec", "taints")
	for _, raw := range taints {
		taint, _ := raw.(map[string]any)
		switch kube.String(taint, "key") {
		case "eks.tke.cloud.tencent.com/eklet", "virtual-kubelet.io/provider":
			return true
		}
	}
	return false
}

func (nodes *overviewNodeSnapshot) summarize(apps []domain.SparkApplication) *domain.OverviewResourcePools {
	var fixed, elastic struct {
		usage             domain.OverviewPoolUsage
		running, observed int
	}
	add := func(node, phase string, resource domain.PodResource) {
		isElastic, included := nodes.elasticByNode[node]
		if !included || phase == "FAILED" || phase == "SUCCEEDED" || phase == "COMPLETED" {
			return
		}
		pool := &fixed
		if isElastic {
			pool = &elastic
		}
		addResources(&pool.usage.Requested, resource.Request)
		if phase == "RUNNING" {
			pool.running++
		}
		if resource.Current != nil {
			pool.observed++
			addResources(&pool.usage.Used, *resource.Current)
		}
	}
	for _, app := range apps {
		if app.Historical || terminalState(app.State) {
			continue
		}
		add(app.DriverNode, app.DriverPhase, app.Driver)
		for _, executor := range app.Executors {
			add(executor.Node, executor.RawState, executor.Resources)
		}
	}
	fixed.usage.MetricsAvailable = fixed.running == 0 || fixed.observed > 0
	elastic.usage.MetricsAvailable = elastic.running == 0 || elastic.observed > 0
	return &domain.OverviewResourcePools{Fixed: fixed.usage, Elastic: elastic.usage}
}
