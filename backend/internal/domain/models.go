package domain

import "time"

type ResourceAmount struct {
	CPU       float64 `json:"cpu"`
	MemoryGiB float64 `json:"memoryGiB"`
}

type PodResource struct {
	Request ResourceAmount  `json:"request"`
	Current *ResourceAmount `json:"current,omitempty"`
	Peak    *ResourceAmount `json:"peak,omitempty"`
}

type MetricPoint struct {
	Time      string  `json:"time"`
	CPU       float64 `json:"cpu"`
	MemoryGiB float64 `json:"memoryGiB"`
}

type ExecutorPod struct {
	Name      string      `json:"name"`
	State     string      `json:"state"`
	Resources PodResource `json:"resources"`
	Node      string      `json:"node,omitempty"`
	NodePool  string      `json:"nodePool,omitempty"`
	StartedAt string      `json:"startedAt,omitempty"`
	Restarts  int64       `json:"restarts"`
}

type KubernetesEvent struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	Reason         string `json:"reason"`
	Message        string `json:"message"`
	Source         string `json:"source"`
	Timestamp      string `json:"timestamp"`
	Count          int64  `json:"count"`
	InvolvedObject string `json:"-"`
}

type SparkApplication struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Namespace          string            `json:"namespace"`
	Cluster            string            `json:"cluster"`
	Owner              string            `json:"owner"`
	Team               string            `json:"team"`
	State              string            `json:"state"`
	CreatedAt          string            `json:"createdAt"`
	StartedAt          string            `json:"startedAt,omitempty"`
	FinishedAt         string            `json:"finishedAt,omitempty"`
	Image              string            `json:"image"`
	SparkVersion       string            `json:"sparkVersion"`
	SparkApplicationID string            `json:"sparkApplicationId,omitempty"`
	SubmissionID       string            `json:"submissionId"`
	DriverPod          string            `json:"driverPod"`
	DriverNode         string            `json:"driverNode,omitempty"`
	DriverNodePool     string            `json:"driverNodePool,omitempty"`
	Driver             PodResource       `json:"driver"`
	Executors          []ExecutorPod     `json:"executors"`
	Metrics            []MetricPoint     `json:"metrics"`
	Events             []KubernetesEvent `json:"events"`
	Logs               []string          `json:"logs"`
	YAML               string            `json:"yaml"`
	PendingReason      string            `json:"pendingReason,omitempty"`
	ErrorMessage       string            `json:"errorMessage,omitempty"`
}

type DashboardSummary struct {
	Total     int            `json:"total"`
	ByState   map[string]int `json:"byState"`
	Requested ResourceAmount `json:"requested"`
	Used      ResourceAmount `json:"used"`
	Capacity  ResourceAmount `json:"capacity"`
	NodePools struct {
		Baseline  int `json:"baseline"`
		Autoscale int `json:"autoscale"`
		Pending   int `json:"pending"`
	} `json:"nodePools"`
}

type OperationAudit struct {
	ID              string `json:"id"`
	ApplicationName string `json:"applicationName"`
	Namespace       string `json:"namespace"`
	Operator        string `json:"operator"`
	Operation       string `json:"operation"`
	Reason          string `json:"reason,omitempty"`
	Timestamp       string `json:"timestamp"`
	Result          string `json:"result"`
	Message         string `json:"message"`
}

func NewAudit(id, namespace, application, operator, reason, result, message string) OperationAudit {
	return OperationAudit{
		ID: id, Namespace: namespace, ApplicationName: application, Operator: operator,
		Operation: "KILL", Reason: reason, Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Result: result, Message: message,
	}
}
