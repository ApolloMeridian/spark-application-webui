package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/kube"
	metrics "spark-control-center/backend/internal/prometheus"
	"spark-control-center/backend/internal/store"
)

type Kubernetes interface {
	Ready(context.Context, string) error
	ListSparkApplications(context.Context, string) ([]kube.Object, error)
	GetSparkApplication(context.Context, string, string) (kube.Object, error)
	CreateSparkApplication(context.Context, string, kube.Object) (kube.Object, error)
	DisableSparkApplicationRestart(context.Context, string, string) error
	DeleteSparkApplication(context.Context, string, string) error
	ForceDeletePod(context.Context, string, string) error
	TerminatePodByDeadline(context.Context, string, string) error
	ListPods(context.Context, string) ([]kube.Pod, error)
	PodLog(context.Context, string, string, int) ([]string, error)
	ListEvents(context.Context, string) ([]domain.KubernetesEvent, error)
}

type Prometheus interface {
	Usage(context.Context, string, []string) (map[string]metrics.PodUsage, []domain.MetricPoint, error)
	UsageRange(context.Context, string, []string, time.Time, time.Time) (map[string]metrics.PodUsage, []domain.MetricPoint, error)
	Capacity(context.Context) (domain.ResourceAmount, error)
}

const consoleOwnerAnnotation = "spark-control-center.io/submitted-by"

type LogSource interface {
	QueryLogs(context.Context, string, string, time.Time, time.Time, string, int) ([]domain.LogEntry, error)
}

type Service struct {
	config     config.Config
	kubernetes Kubernetes
	prometheus Prometheus
	logs       LogSource
	audits     store.AuditStore
	logger     *slog.Logger
}

func New(cfg config.Config, kubernetes Kubernetes, prometheus Prometheus, audits store.AuditStore, logger *slog.Logger, logSources ...LogSource) *Service {
	service := &Service{config: cfg, kubernetes: kubernetes, prometheus: prometheus, audits: audits, logger: logger}
	if len(logSources) > 0 {
		service.logs = logSources[0]
	}
	return service
}

func (s *Service) Ready(ctx context.Context) error {
	if err := s.kubernetes.Ready(ctx, s.config.Namespaces[0]); err != nil {
		return fmt.Errorf("Kubernetes: %w", err)
	}
	if err := s.audits.Ping(ctx); err != nil {
		return fmt.Errorf("PostgreSQL: %w", err)
	}
	return nil
}

func (s *Service) ListApplications(ctx context.Context) ([]domain.SparkApplication, error) {
	applications := make([]domain.SparkApplication, 0)
	for _, namespace := range s.config.Namespaces {
		objects, err := s.kubernetes.ListSparkApplications(ctx, namespace)
		if err != nil {
			return nil, fmt.Errorf("list SparkApplications in %s: %w", namespace, err)
		}
		pods, err := s.kubernetes.ListPods(ctx, namespace)
		if err != nil {
			return nil, fmt.Errorf("list pods in %s: %w", namespace, err)
		}
		podNames := sparkPodNames(objects, pods)
		usage, _, metricsErr := s.prometheus.Usage(ctx, namespace, podNames)
		if metricsErr != nil {
			s.logger.Warn("Prometheus pod query failed; returning Kubernetes data without live usage", "namespace", namespace, "error", metricsErr)
			usage = map[string]metrics.PodUsage{}
		}
		for _, object := range objects {
			related := relatedPods(object, pods)
			app := s.mapApplication(object, related, usage)
			applications = append(applications, app)
		}
	}
	sort.Slice(applications, func(i, j int) bool { return applications[i].CreatedAt > applications[j].CreatedAt })
	if err := s.audits.UpsertApplications(ctx, applications); err != nil {
		s.logger.Warn("could not persist SparkApplication history", "error", err)
	}
	return applications, nil
}

func (s *Service) GetApplication(ctx context.Context, namespace, name string) (domain.SparkApplication, error) {
	if !s.config.NamespaceAllowed(namespace) {
		return domain.SparkApplication{}, &kube.APIError{StatusCode: 403, Message: "namespace is not configured for this service"}
	}
	object, err := s.kubernetes.GetSparkApplication(ctx, namespace, name)
	if err != nil {
		return domain.SparkApplication{}, err
	}
	pods, err := s.kubernetes.ListPods(ctx, namespace)
	if err != nil {
		return domain.SparkApplication{}, err
	}
	related := relatedPods(object, pods)
	podNames := applicationPodNames(object, related)
	usage, history, metricsErr := s.prometheus.Usage(ctx, namespace, podNames)
	if metricsErr != nil {
		s.logger.Warn("Prometheus application query failed", "namespace", namespace, "application", name, "error", metricsErr)
		usage = map[string]metrics.PodUsage{}
		history = []domain.MetricPoint{}
	}
	app := s.mapApplication(object, related, usage)
	app.Metrics = history

	allEvents, eventErr := s.kubernetes.ListEvents(ctx, namespace)
	if eventErr != nil {
		s.logger.Warn("Kubernetes events query failed", "namespace", namespace, "application", name, "error", eventErr)
	} else {
		app.Events = filterEvents(allEvents, object, related)
		for i := len(app.Events) - 1; i >= 0; i-- {
			if app.Events[i].Reason == "FailedScheduling" {
				app.PendingReason = app.Events[i].Message
				break
			}
		}
	}
	logs, logErr := s.kubernetes.PodLog(ctx, namespace, app.DriverPod, 5000)
	if logErr != nil {
		s.logger.Warn("driver log query failed", "namespace", namespace, "application", name, "pod", app.DriverPod, "error", logErr)
	} else {
		app.Logs = logs
	}
	raw, _ := json.MarshalIndent(object, "", "  ")
	app.YAML = string(raw) + "\n"
	return app, nil
}

func (s *Service) GetApplicationMetrics(ctx context.Context, namespace, name string, from, to time.Time) ([]domain.MetricPoint, error) {
	if !s.config.NamespaceAllowed(namespace) {
		return nil, &kube.APIError{StatusCode: 403, Message: "namespace is not configured for this service"}
	}
	if !from.Before(to) {
		return nil, &kube.APIError{StatusCode: 400, Message: "from timestamp must be before to timestamp"}
	}
	object, err := s.kubernetes.GetSparkApplication(ctx, namespace, name)
	if err != nil {
		return nil, err
	}
	pods, err := s.kubernetes.ListPods(ctx, namespace)
	if err != nil {
		return nil, err
	}
	related := relatedPods(object, pods)
	podNames := applicationPodNames(object, related)
	_, history, err := s.prometheus.UsageRange(ctx, namespace, podNames, from, to)
	if err != nil {
		return nil, fmt.Errorf("query Prometheus application history: %w", err)
	}
	return history, nil
}

func (s *Service) SparkUIProxyTarget(ctx context.Context, namespace, name string) (string, error) {
	if !s.config.NamespaceAllowed(namespace) {
		return "", &kube.APIError{StatusCode: 403, Message: "namespace is not configured for this service"}
	}
	object, err := s.kubernetes.GetSparkApplication(ctx, namespace, name)
	if err != nil {
		return "", err
	}
	if normalizedState(kube.String(object, "status", "applicationState", "state")) != "RUNNING" {
		return "", &kube.APIError{StatusCode: 409, Message: "Spark UI proxy is available only for RUNNING applications"}
	}
	serviceName := strings.TrimSpace(kube.String(object, "status", "driverInfo", "webUIServiceName"))
	if !validDNSLabel(serviceName) {
		return "", &kube.APIError{StatusCode: 404, Message: "Spark driver UI Service is not available"}
	}
	port := int(kube.Int64(object, "status", "driverInfo", "webUIPort"))
	if port <= 0 {
		port = 4040
	}
	if port > 65535 {
		return "", &kube.APIError{StatusCode: 502, Message: "Spark driver UI Service has an invalid port"}
	}
	return fmt.Sprintf("http://%s.%s.svc:%d", serviceName, namespace, port), nil
}

func (s *Service) GetExecutorLogs(ctx context.Context, namespace, name, pod string, from, to time.Time, direction string, limit int) ([]domain.LogEntry, error) {
	if !s.config.NamespaceAllowed(namespace) {
		return nil, &kube.APIError{StatusCode: 403, Message: "namespace is not configured for this service"}
	}
	if s.logs == nil {
		return nil, &kube.APIError{StatusCode: 503, Message: "Loki is not configured"}
	}
	object, err := s.kubernetes.GetSparkApplication(ctx, namespace, name)
	if err != nil {
		return nil, err
	}
	validExecutor := false
	if states, ok := kube.Map(object, "status", "executorState"); ok {
		_, validExecutor = states[pod]
	}
	if !validExecutor {
		pods, listErr := s.kubernetes.ListPods(ctx, namespace)
		if listErr != nil {
			return nil, listErr
		}
		for _, related := range relatedPods(object, pods) {
			if related.Name == pod && strings.EqualFold(related.Role, "executor") {
				validExecutor = true
				break
			}
		}
	}
	if !validExecutor {
		return nil, &kube.APIError{StatusCode: 404, Message: "executor does not belong to this SparkApplication"}
	}
	entries, err := s.logs.QueryLogs(ctx, namespace, pod, from, to, direction, limit)
	if err != nil {
		return nil, fmt.Errorf("query executor logs: %w", err)
	}
	return entries, nil
}

func (s *Service) Summary(ctx context.Context, from, to time.Time) (domain.DashboardSummary, error) {
	apps, err := s.ListApplications(ctx)
	if err != nil {
		return domain.DashboardSummary{}, err
	}
	summary := domain.DashboardSummary{Total: len(apps), ByState: map[string]int{}}
	expectedMetricPods := 0
	observedMetricPods := 0
	for _, app := range apps {
		summary.ByState[app.State]++
		if app.State != "RUNNING" {
			if app.State == "PENDING" || app.State == "SUBMITTED" || app.State == "FAILING" {
				for _, executor := range app.Executors {
					if executor.Node == "" {
						summary.NodePools.Pending++
					}
				}
			}
			continue
		}
		addResources(&summary.Requested, app.Driver.Request)
		expectedMetricPods++
		if app.Driver.Current != nil {
			addResources(&summary.Used, *app.Driver.Current)
			observedMetricPods++
		}
		for _, executor := range app.Executors {
			if executor.State != "RUNNING" && executor.State != "PENDING" {
				continue
			}
			addResources(&summary.Requested, executor.Resources.Request)
			if executor.Resources.Current != nil {
				addResources(&summary.Used, *executor.Resources.Current)
				observedMetricPods++
			}
			if executor.State == "RUNNING" {
				expectedMetricPods++
			}
			if executor.Node == "" {
				summary.NodePools.Pending++
			} else if executor.NodePool == "autoscale" {
				summary.NodePools.Autoscale++
			} else {
				summary.NodePools.Baseline++
			}
		}
	}
	summary.MetricsAvailable = expectedMetricPods == 0 || observedMetricPods > 0
	capacity, metricsErr := s.prometheus.Capacity(ctx)
	if metricsErr != nil {
		s.logger.Warn("Prometheus capacity query failed; using requested resources as chart denominator", "error", metricsErr)
		capacity = summary.Requested
	}
	if capacity.CPU <= 0 {
		capacity.CPU = max(summary.Requested.CPU, 1)
	}
	if capacity.MemoryGiB <= 0 {
		capacity.MemoryGiB = max(summary.Requested.MemoryGiB, 1)
	}
	summary.Capacity = capacity
	history, historyErr := s.audits.HistorySummary(ctx, from, to)
	if historyErr != nil {
		s.logger.Warn("application history query failed; deriving history from current Kubernetes objects", "error", historyErr)
		history = currentHistorySummary(apps, from, to)
	}
	summary.History = history
	return summary, nil
}

func (s *Service) KillApplication(ctx context.Context, namespace, name, operator, reason string) (domain.OperationAudit, error) {
	if !s.config.NamespaceAllowed(namespace) {
		return domain.OperationAudit{}, &kube.APIError{StatusCode: 403, Message: "namespace is not configured for this service"}
	}
	object, err := s.kubernetes.GetSparkApplication(ctx, namespace, name)
	if err != nil {
		audit := domain.NewAudit(newID(), namespace, name, operator, reason, "FAILED", "SparkApplication lookup failed: "+err.Error())
		s.persistAudit(ctx, audit)
		return audit, err
	}
	state := normalizedState(kube.String(object, "status", "applicationState", "state"))
	if state != "RUNNING" && state != "SUBMITTED" {
		err = &kube.APIError{StatusCode: 409, Message: "only RUNNING or SUBMITTED SparkApplications can be killed"}
		audit := domain.NewOperationAudit(newID(), namespace, name, operator, "KILL", reason, "FAILED", err.Error())
		s.persistAudit(ctx, audit)
		return audit, err
	}
	pods, err := s.kubernetes.ListPods(ctx, namespace)
	if err != nil {
		audit := domain.NewOperationAudit(newID(), namespace, name, operator, "KILL", reason, "FAILED", "Kubernetes pod lookup failed: "+err.Error())
		s.persistAudit(ctx, audit)
		return audit, err
	}
	if err := s.kubernetes.DisableSparkApplicationRestart(ctx, namespace, name); err != nil {
		audit := domain.NewOperationAudit(newID(), namespace, name, operator, "KILL", reason, "FAILED", "Could not disable SparkApplication restart policy: "+err.Error())
		s.persistAudit(ctx, audit)
		return audit, err
	}
	related := relatedPods(object, pods)
	driverPod := kube.String(object, "status", "driverInfo", "podName")
	if driverPod == "" {
		for _, pod := range related {
			if pod.Role == "driver" {
				driverPod = pod.Name
				break
			}
		}
	}
	driverPod = firstNonEmpty(driverPod, name+"-driver")
	if err := s.kubernetes.TerminatePodByDeadline(ctx, namespace, driverPod); err != nil {
		audit := domain.NewOperationAudit(newID(), namespace, name, operator, "KILL", reason, "FAILED", "Driver deadline termination failed: "+err.Error())
		s.persistAudit(ctx, audit)
		return audit, err
	}
	executorsDeleted := 0
	for _, pod := range related {
		if pod.Role != "executor" {
			continue
		}
		if err := s.kubernetes.ForceDeletePod(ctx, namespace, pod.Name); err != nil {
			audit := domain.NewOperationAudit(newID(), namespace, name, operator, "KILL", reason, "FAILED", "Executor cleanup failed for "+pod.Name+": "+err.Error())
			s.persistAudit(ctx, audit)
			return audit, err
		}
		executorsDeleted++
	}
	audit := domain.NewOperationAudit(newID(), namespace, name, operator, "KILL", reason, "SUCCESS", fmt.Sprintf("Driver pod terminated by deadline and retained; %d executor pods cleaned; SparkApplication retained", executorsDeleted))
	if err := s.persistAudit(ctx, audit); err != nil {
		return audit, fmt.Errorf("SparkApplication pods were terminated but audit persistence failed: %w", err)
	}
	return audit, nil
}

func (s *Service) SubmitApplication(ctx context.Context, namespace, manifest, operator string) (domain.SparkApplication, error) {
	if !s.config.NamespaceAllowed(namespace) {
		return domain.SparkApplication{}, &kube.APIError{StatusCode: 403, Message: "namespace is not configured for this service"}
	}
	var object kube.Object
	if err := yaml.Unmarshal([]byte(manifest), &object); err != nil {
		return domain.SparkApplication{}, &kube.APIError{StatusCode: 400, Message: "invalid SparkApplication YAML: " + err.Error()}
	}
	if kube.String(object, "apiVersion") != s.config.SparkAPIVersion || kube.String(object, "kind") != "SparkApplication" {
		return domain.SparkApplication{}, &kube.APIError{StatusCode: 400, Message: "YAML must contain kind SparkApplication with apiVersion " + s.config.SparkAPIVersion}
	}
	metadata, ok := kube.Map(object, "metadata")
	if !ok {
		return domain.SparkApplication{}, &kube.APIError{StatusCode: 400, Message: "YAML metadata is required"}
	}
	name := strings.TrimSpace(fmt.Sprint(metadata["name"]))
	if name == "" || name == "<nil>" {
		return domain.SparkApplication{}, &kube.APIError{StatusCode: 400, Message: "YAML metadata.name is required"}
	}
	manifestNamespace := strings.TrimSpace(fmt.Sprint(metadata["namespace"]))
	if manifestNamespace == "<nil>" {
		manifestNamespace = ""
	}
	if manifestNamespace != "" && manifestNamespace != namespace {
		return domain.SparkApplication{}, &kube.APIError{StatusCode: 400, Message: "YAML metadata.namespace must match the selected namespace"}
	}
	metadata["namespace"] = namespace
	annotations, ok := metadata["annotations"].(map[string]any)
	if !ok {
		annotations = map[string]any{}
		metadata["annotations"] = annotations
	}
	annotations[consoleOwnerAnnotation] = operator
	for _, field := range []string{"uid", "resourceVersion", "generation", "creationTimestamp", "managedFields", "deletionTimestamp"} {
		delete(metadata, field)
	}
	delete(object, "status")
	created, err := s.kubernetes.CreateSparkApplication(ctx, namespace, object)
	if err != nil {
		audit := domain.NewOperationAudit(newID(), namespace, name, operator, "SUBMIT", "", "FAILED", "Kubernetes create failed: "+err.Error())
		s.persistAudit(ctx, audit)
		return domain.SparkApplication{}, err
	}
	application := s.mapApplication(created, nil, map[string]metrics.PodUsage{})
	if err := s.audits.UpsertApplications(ctx, []domain.SparkApplication{application}); err != nil {
		s.logger.Warn("could not persist submitted SparkApplication history", "namespace", namespace, "application", name, "error", err)
	}
	audit := domain.NewOperationAudit(newID(), namespace, name, operator, "SUBMIT", "", "SUCCESS", "SparkApplication created by Kubernetes API")
	if err := s.persistAudit(ctx, audit); err != nil {
		return application, fmt.Errorf("SparkApplication was created but audit persistence failed: %w", err)
	}
	return application, nil
}

func (s *Service) DeleteApplication(ctx context.Context, namespace, name, operator, reason string) (domain.OperationAudit, error) {
	if !s.config.NamespaceAllowed(namespace) {
		return domain.OperationAudit{}, &kube.APIError{StatusCode: 403, Message: "namespace is not configured for this service"}
	}
	object, err := s.kubernetes.GetSparkApplication(ctx, namespace, name)
	if err != nil {
		audit := domain.NewOperationAudit(newID(), namespace, name, operator, "DELETE", reason, "FAILED", "SparkApplication lookup failed: "+err.Error())
		s.persistAudit(ctx, audit)
		return audit, err
	}
	state := normalizedState(kube.String(object, "status", "applicationState", "state"))
	if !terminalState(state) {
		err = &kube.APIError{StatusCode: 409, Message: "only terminal SparkApplications can be deleted"}
		audit := domain.NewOperationAudit(newID(), namespace, name, operator, "DELETE", reason, "FAILED", err.Error())
		s.persistAudit(ctx, audit)
		return audit, err
	}
	terminalApplication := s.mapApplication(object, nil, map[string]metrics.PodUsage{})
	if err := s.audits.UpsertApplications(ctx, []domain.SparkApplication{terminalApplication}); err != nil {
		s.logger.Warn("could not persist terminal SparkApplication before deletion", "namespace", namespace, "application", name, "error", err)
	}
	if err := s.kubernetes.DeleteSparkApplication(ctx, namespace, name); err != nil {
		audit := domain.NewOperationAudit(newID(), namespace, name, operator, "DELETE", reason, "FAILED", "Kubernetes delete failed: "+err.Error())
		s.persistAudit(ctx, audit)
		return audit, err
	}
	audit := domain.NewOperationAudit(newID(), namespace, name, operator, "DELETE", reason, "SUCCESS", "Terminal SparkApplication deletion accepted by Kubernetes API")
	if err := s.persistAudit(ctx, audit); err != nil {
		return audit, fmt.Errorf("SparkApplication was deleted but audit persistence failed: %w", err)
	}
	return audit, nil
}

func (s *Service) ListAudit(ctx context.Context) ([]domain.OperationAudit, error) {
	return s.audits.List(ctx, 1000)
}

func (s *Service) persistAudit(ctx context.Context, audit domain.OperationAudit) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = s.audits.Insert(ctx, audit); err == nil {
			return nil
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
			}
		}
	}
	s.logger.Error("could not persist operation audit", "audit_id", audit.ID, "error", err)
	return err
}

func (s *Service) mapApplication(object kube.Object, pods []kube.Pod, usage map[string]metrics.PodUsage) domain.SparkApplication {
	name := kube.String(object, "metadata", "name")
	namespace := kube.String(object, "metadata", "namespace")
	annotations := kube.StringMap(object, "metadata", "annotations")
	driverPodName := firstNonEmpty(kube.String(object, "status", "driverInfo", "podName"), name+"-driver")
	state := normalizedState(kube.String(object, "status", "applicationState", "state"))
	app := domain.SparkApplication{
		ID: firstNonEmpty(kube.String(object, "metadata", "uid"), namespace+"/"+name), Name: name, Namespace: namespace,
		Cluster: s.config.ClusterName, Owner: firstNonEmpty(annotations[consoleOwnerAnnotation], "unknown"), State: state,
		CreatedAt:  kube.String(object, "metadata", "creationTimestamp"),
		StartedAt:  firstNonEmpty(kube.String(object, "status", "lastSubmissionAttemptTime"), kube.String(object, "status", "applicationState", "stateTransitionTime")),
		FinishedAt: kube.String(object, "status", "terminationTime"), Image: firstNonEmpty(kube.String(object, "spec", "image"), kube.String(object, "spec", "driver", "image")),
		SparkVersion: kube.String(object, "spec", "sparkVersion"), SparkApplicationID: kube.String(object, "status", "sparkApplicationId"),
		SparkUIAvailable: state == "RUNNING" && validDNSLabel(kube.String(object, "status", "driverInfo", "webUIServiceName")),
		EventLogEnabled:  eventLogEnabled(object),
		SubmissionID:     firstNonEmpty(kube.String(object, "status", "submissionID"), kube.String(object, "status", "submissionId"), kube.String(object, "metadata", "uid")),
		DriverPod:        driverPodName, Metrics: []domain.MetricPoint{}, Events: []domain.KubernetesEvent{}, Logs: []string{}, YAML: "",
		ErrorMessage: firstNonEmpty(kube.String(object, "status", "applicationState", "errorMessage"), kube.String(object, "status", "errorMessage")),
	}
	driverFallback := resourceFromSpec(object, "driver")
	executorFallback := resourceFromSpec(object, "executor")
	app.Driver = domain.PodResource{Request: driverFallback}

	executorByName := map[string]domain.ExecutorPod{}
	if states, ok := kube.Map(object, "status", "executorState"); ok {
		for podName, rawState := range states {
			raw := strings.ToUpper(strings.TrimSpace(fmt.Sprint(rawState)))
			executorByName[podName] = domain.ExecutorPod{Name: podName, State: executorPresentationState(state, raw), RawState: raw, Resources: domain.PodResource{Request: executorFallback}}
		}
	}
	for _, pod := range pods {
		resource := domain.PodResource{Request: pod.Request}
		if resource.Request.CPU == 0 && resource.Request.MemoryGiB == 0 {
			if strings.EqualFold(pod.Role, "driver") || pod.Name == driverPodName {
				resource.Request = driverFallback
			} else {
				resource.Request = executorFallback
			}
		}
		if podUsage, ok := usage[pod.Name]; ok {
			current, peak := podUsage.Current, podUsage.Peak
			resource.Current, resource.Peak = &current, &peak
		}
		if strings.EqualFold(pod.Role, "driver") || pod.Name == driverPodName {
			app.Driver = resource
			app.DriverPod = pod.Name
			app.DriverNode = pod.Node
			app.DriverNodePool = pod.NodePool
			continue
		}
		executorByName[pod.Name] = domain.ExecutorPod{
			Name: pod.Name, State: executorPresentationState(state, pod.Phase), RawState: pod.Phase, Resources: resource, Node: pod.Node,
			NodePool: pod.NodePool, StartedAt: pod.StartedAt, Restarts: pod.Restarts,
		}
	}
	if len(executorByName) == 0 {
		instances := int(kube.Int64(object, "spec", "executor", "instances"))
		for index := 0; index < instances; index++ {
			placeholderState := "PENDING"
			if state == "COMPLETED" {
				placeholderState = "SUCCEEDED"
			} else if state == "FAILED" || state == "SUBMISSION_FAILED" || state == "KILLED" {
				placeholderState = "FAILED"
			}
			podName := fmt.Sprintf("%s-executor-%d", name, index+1)
			executorByName[podName] = domain.ExecutorPod{Name: podName, State: placeholderState, Resources: domain.PodResource{Request: executorFallback}}
		}
	}
	app.Executors = make([]domain.ExecutorPod, 0, len(executorByName))
	for _, executor := range executorByName {
		app.Executors = append(app.Executors, executor)
	}
	sort.Slice(app.Executors, func(i, j int) bool { return app.Executors[i].Name < app.Executors[j].Name })
	return app
}

func eventLogEnabled(object kube.Object) bool {
	sparkConf, ok := kube.Map(object, "spec", "sparkConf")
	if !ok {
		return false
	}
	enabled := strings.EqualFold(strings.TrimSpace(fmt.Sprint(sparkConf["spark.eventLog.enabled"])), "true")
	directory := strings.TrimSpace(fmt.Sprint(sparkConf["spark.eventLog.dir"]))
	return enabled && directory != "" && directory != "<nil>"
}

func validDNSLabel(value string) bool {
	if value == "" || len(value) > 63 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}

func relatedPods(object kube.Object, pods []kube.Pod) []kube.Pod {
	name := kube.String(object, "metadata", "name")
	driver := kube.String(object, "status", "driverInfo", "podName")
	executors := map[string]bool{}
	if states, ok := kube.Map(object, "status", "executorState"); ok {
		for podName := range states {
			executors[podName] = true
		}
	}
	result := make([]kube.Pod, 0)
	for _, pod := range pods {
		labelMatch := pod.Labels["sparkoperator.k8s.io/app-name"] == name || pod.Labels["spark-app-name"] == name
		exactMatch := pod.Name == driver || executors[pod.Name]
		prefixFallback := (pod.Role == "driver" || pod.Role == "executor") && strings.HasPrefix(pod.Name, name+"-")
		if labelMatch || exactMatch || prefixFallback {
			result = append(result, pod)
		}
	}
	return result
}

func applicationPodNames(object kube.Object, related []kube.Pod) []string {
	seen := map[string]bool{}
	if driver := kube.String(object, "status", "driverInfo", "podName"); driver != "" {
		seen[driver] = true
	}
	if states, ok := kube.Map(object, "status", "executorState"); ok {
		for podName := range states {
			if podName != "" {
				seen[podName] = true
			}
		}
	}
	for _, pod := range related {
		seen[pod.Name] = true
	}
	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func sparkPodNames(objects []kube.Object, pods []kube.Pod) []string {
	seen := map[string]bool{}
	for _, object := range objects {
		for _, pod := range relatedPods(object, pods) {
			seen[pod.Name] = true
		}
	}
	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	return result
}

func filterEvents(events []domain.KubernetesEvent, object kube.Object, pods []kube.Pod) []domain.KubernetesEvent {
	// Core/v1 event conversion intentionally keeps the involved object out of the public model.
	// Match by message as a portable fallback across Kubernetes versions and event producers.
	names := []string{kube.String(object, "metadata", "name")}
	for _, pod := range pods {
		names = append(names, pod.Name)
	}
	filtered := make([]domain.KubernetesEvent, 0)
	for _, event := range events {
		for _, name := range names {
			if name != "" && (event.InvolvedObject == name || strings.Contains(event.Message, name)) {
				filtered = append(filtered, event)
				break
			}
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Timestamp > filtered[j].Timestamp })
	return filtered
}

func resourceFromSpec(object kube.Object, component string) domain.ResourceAmount {
	cpuText := kube.String(object, "spec", component, "cores")
	memoryText := kube.String(object, "spec", component, "memory")
	cpu, _ := strconv.ParseFloat(cpuText, 64)
	return domain.ResourceAmount{CPU: cpu, MemoryGiB: parseSparkMemory(memoryText)}
}

func parseSparkMemory(value string) float64 {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	lower := strings.ToLower(trimmed)
	units := []struct {
		suffix string
		gib    float64
	}{{"t", 1024}, {"g", 1}, {"m", 1.0 / 1024}, {"k", 1.0 / (1024 * 1024)}}
	for _, unit := range units {
		if strings.HasSuffix(lower, unit.suffix) && !strings.HasSuffix(lower, "i"+unit.suffix) {
			number, _ := strconv.ParseFloat(strings.TrimSuffix(lower, unit.suffix), 64)
			return number * unit.gib
		}
	}
	return kube.ParseMemoryGiB(trimmed)
}

func normalizedState(state string) string {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "RUNNING":
		return "RUNNING"
	case "COMPLETED", "SUCCEEDING":
		return "COMPLETED"
	case "FAILED":
		return "FAILED"
	case "SUBMISSION_FAILED":
		return "SUBMISSION_FAILED"
	case "PENDING", "PENDING_RERUN":
		return "PENDING"
	case "SUBMITTED", "NEW":
		return "SUBMITTED"
	case "FAILING", "INVALIDATING":
		return "FAILING"
	case "INVALIDATED", "KILLED":
		return "KILLED"
	default:
		return "UNKNOWN"
	}
}

func executorState(state string) string {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "RUNNING":
		return "RUNNING"
	case "PENDING", "UNKNOWN":
		return "PENDING"
	case "COMPLETED", "SUCCEEDED":
		return "SUCCEEDED"
	default:
		return "FAILED"
	}
}

func executorPresentationState(applicationState, rawExecutorState string) string {
	state := executorState(rawExecutorState)
	// Spark Operator keeps the historical executorState map after application completion.
	// Executors commonly receive SIGTERM during dynamic-allocation scale-down or final cleanup,
	// which is recorded as FAILED even though the SparkApplication completed successfully.
	if applicationState == "COMPLETED" && state == "FAILED" {
		return "TERMINATED"
	}
	return state
}

func terminalState(state string) bool {
	return state == "COMPLETED" || state == "FAILED" || state == "SUBMISSION_FAILED" || state == "KILLED"
}

func currentHistorySummary(applications []domain.SparkApplication, from, to time.Time) domain.HistorySummary {
	result := domain.HistorySummary{From: from.UTC().Format(time.RFC3339), To: to.UTC().Format(time.RFC3339)}
	for _, app := range applications {
		createdAt, createdErr := time.Parse(time.RFC3339, app.CreatedAt)
		if createdErr == nil && !createdAt.Before(from) && !createdAt.After(to) {
			result.Submitted++
		}
		if app.State != "FAILED" && app.State != "SUBMISSION_FAILED" {
			continue
		}
		failedAt, failedErr := time.Parse(time.RFC3339, app.FinishedAt)
		if failedErr == nil && !failedAt.Before(from) && !failedAt.After(to) {
			result.Failed++
		}
	}
	return result
}

func addResources(target *domain.ResourceAmount, value domain.ResourceAmount) {
	target.CPU += value.CPU
	target.MemoryGiB += value.MemoryGiB
}

func newID() string {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return fmt.Sprintf("op-%d", time.Now().UnixNano())
	}
	return "op-" + hex.EncodeToString(data)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func HTTPStatus(err error) int {
	var apiErr *kube.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode > 0 {
		return apiErr.StatusCode
	}
	return 500
}
