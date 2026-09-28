package domain

import "time"

type UserRole string

const (
	RoleViewer UserRole = "viewer"
	RoleAdmin  UserRole = "admin"
)

type User struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"displayName"`
	Email       string   `json:"email"`
	Role        UserRole `json:"role"`
	Namespaces  []string `json:"namespaces"`
	AuthSource  string   `json:"authSource"`
	Disabled    bool     `json:"disabled"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
}

type ApplicationDiagnostic struct {
	Code           string `json:"code"`
	Severity       string `json:"severity"`
	Summary        string `json:"summary"`
	Detail         string `json:"detail,omitempty"`
	Recommendation string `json:"recommendation,omitempty"`
}

type LifecycleEvent struct {
	Time   string `json:"time"`
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

type ManifestPreview struct {
	Name           string   `json:"name"`
	Namespace      string   `json:"namespace"`
	OriginalYAML   string   `json:"originalYaml"`
	ServerYAML     string   `json:"serverYaml"`
	Warnings       []string `json:"warnings"`
	DryRunAccepted bool     `json:"dryRunAccepted"`
}

type ApplicationChange struct {
	Type            string `json:"type"`
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
	Timestamp       string `json:"timestamp"`
}

type OIDCLoginState struct {
	StateHash    string
	Nonce        string
	CodeVerifier string
	RedirectURL  string
	ReturnURL    string
	ExpiresAt    time.Time
}

type UserSession struct {
	TokenHash string
	UserID    string
	ExpiresAt time.Time
}

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

type LogEntry struct {
	Timestamp string            `json:"timestamp"`
	Line      string            `json:"line"`
	Labels    map[string]string `json:"labels,omitempty"`
}

type ExecutorPod struct {
	Name      string      `json:"name"`
	State     string      `json:"state"`
	RawState  string      `json:"rawState,omitempty"`
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
	ID                 string                  `json:"id"`
	Name               string                  `json:"name"`
	Namespace          string                  `json:"namespace"`
	Cluster            string                  `json:"cluster"`
	Owner              string                  `json:"owner"`
	State              string                  `json:"state"`
	CreatedAt          string                  `json:"createdAt"`
	StartedAt          string                  `json:"startedAt,omitempty"`
	FinishedAt         string                  `json:"finishedAt,omitempty"`
	Image              string                  `json:"image"`
	SparkVersion       string                  `json:"sparkVersion"`
	SparkApplicationID string                  `json:"sparkApplicationId,omitempty"`
	SparkUIAvailable   bool                    `json:"sparkUiAvailable"`
	EventLogEnabled    bool                    `json:"eventLogEnabled"`
	SubmissionID       string                  `json:"submissionId"`
	DriverPod          string                  `json:"driverPod"`
	DriverNode         string                  `json:"driverNode,omitempty"`
	DriverNodePool     string                  `json:"driverNodePool,omitempty"`
	Driver             PodResource             `json:"driver"`
	Executors          []ExecutorPod           `json:"executors"`
	Metrics            []MetricPoint           `json:"metrics"`
	Events             []KubernetesEvent       `json:"events"`
	Logs               []string                `json:"logs"`
	YAML               string                  `json:"yaml"`
	PendingReason      string                  `json:"pendingReason,omitempty"`
	ErrorMessage       string                  `json:"errorMessage,omitempty"`
	Historical         bool                    `json:"historical,omitempty"`
	Diagnostics        []ApplicationDiagnostic `json:"diagnostics,omitempty"`
	Lifecycle          []LifecycleEvent        `json:"lifecycle,omitempty"`
}

type DashboardSummary struct {
	Total            int            `json:"total"`
	ByState          map[string]int `json:"byState"`
	Requested        ResourceAmount `json:"requested"`
	Used             ResourceAmount `json:"used"`
	Capacity         ResourceAmount `json:"capacity"`
	MetricsAvailable bool           `json:"metricsAvailable"`
	NodePools        struct {
		Baseline  int `json:"baseline"`
		Autoscale int `json:"autoscale"`
		Pending   int `json:"pending"`
	} `json:"nodePools"`
	History HistorySummary `json:"history"`
}

type HistorySummary struct {
	Submitted int    `json:"submitted"`
	Failed    int    `json:"failed"`
	From      string `json:"from"`
	To        string `json:"to"`
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

type TemplateParameter struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Default     string   `json:"default,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Pattern     string   `json:"pattern,omitempty"`
}

type TemplateVersion struct {
	Version   int    `json:"version"`
	Manifest  string `json:"manifest"`
	CreatedBy string `json:"createdBy"`
	CreatedAt string `json:"createdAt"`
}

type ApplicationTemplate struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Namespace   string              `json:"namespace"`
	Manifest    string              `json:"manifest"`
	Parameters  []TemplateParameter `json:"parameters"`
	Version     int                 `json:"version"`
	Disabled    bool                `json:"disabled"`
	CreatedBy   string              `json:"createdBy"`
	CreatedAt   string              `json:"createdAt"`
	UpdatedAt   string              `json:"updatedAt"`
	Versions    []TemplateVersion   `json:"versions,omitempty"`
}

type ApplicationFavorite struct {
	UserID      string `json:"userId,omitempty"`
	Namespace   string `json:"namespace"`
	Application string `json:"application"`
}

type ApplicationList struct {
	Items    []SparkApplication `json:"items"`
	Page     int                `json:"page"`
	PageSize int                `json:"pageSize"`
	Total    int                `json:"total"`
}

type BatchActionItem struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Allowed   bool   `json:"allowed"`
	Reason    string `json:"reason,omitempty"`
}

type BatchActionResult struct {
	Operation string           `json:"operation"`
	Items     []OperationAudit `json:"items"`
}

type AlertRule struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Type             string   `json:"type"`
	Namespaces       []string `json:"namespaces"`
	Enabled          bool     `json:"enabled"`
	ThresholdMinutes int      `json:"thresholdMinutes,omitempty"`
	ThresholdValue   float64  `json:"thresholdValue,omitempty"`
	MinimumRetries   int      `json:"minimumRetries,omitempty"`
	Severity         string   `json:"severity"`
	NotifyWebhook    bool     `json:"notifyWebhook"`
	CreatedBy        string   `json:"createdBy"`
	CreatedAt        string   `json:"createdAt"`
	UpdatedAt        string   `json:"updatedAt"`
}

type Alert struct {
	ID              string   `json:"id"`
	RuleID          string   `json:"ruleId"`
	RuleName        string   `json:"ruleName"`
	Namespace       string   `json:"namespace"`
	ApplicationName string   `json:"applicationName"`
	Fingerprint     string   `json:"fingerprint"`
	Severity        string   `json:"severity"`
	Status          string   `json:"status"`
	Summary         string   `json:"summary"`
	Evidence        []string `json:"evidence"`
	Confidence      string   `json:"confidence"`
	Recommendation  string   `json:"recommendation,omitempty"`
	FirstSeenAt     string   `json:"firstSeenAt"`
	LastSeenAt      string   `json:"lastSeenAt"`
	AcknowledgedBy  string   `json:"acknowledgedBy,omitempty"`
	AcknowledgedAt  string   `json:"acknowledgedAt,omitempty"`
	SilencedUntil   string   `json:"silencedUntil,omitempty"`
	RecoveredAt     string   `json:"recoveredAt,omitempty"`
}

type FailureFingerprint struct {
	Fingerprint    string `json:"fingerprint"`
	Code           string `json:"code"`
	Count          int    `json:"count"`
	LastSeenAt     string `json:"lastSeenAt"`
	SampleApp      string `json:"sampleApplication"`
	Namespace      string `json:"namespace"`
	Severity       string `json:"severity"`
	Recommendation string `json:"recommendation,omitempty"`
}

func NewAudit(id, namespace, application, operator, reason, result, message string) OperationAudit {
	return NewOperationAudit(id, namespace, application, operator, "KILL", reason, result, message)
}

func NewOperationAudit(id, namespace, application, operator, operation, reason, result, message string) OperationAudit {
	return OperationAudit{
		ID: id, Namespace: namespace, ApplicationName: application, Operator: operator,
		Operation: operation, Reason: reason, Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Result: result, Message: message,
	}
}
