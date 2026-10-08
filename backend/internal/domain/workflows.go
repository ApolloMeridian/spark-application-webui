package domain

type Tenant struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
}
type Project struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenantId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedBy   string `json:"createdBy"`
	CreatedAt   string `json:"createdAt"`
}
type WorkflowNode struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Namespace       string              `json:"namespace"`
	Manifest        string              `json:"manifest"`
	TemplateID      string              `json:"templateId,omitempty"`
	TemplateVersion int                 `json:"templateVersion,omitempty"`
	Parameters      []TemplateParameter `json:"parameters"`
	Values          map[string]string   `json:"values"`
	DependsOn       []string            `json:"dependsOn"`
	Strategy        string              `json:"strategy"`
	NameSuffix      *bool               `json:"nameSuffix,omitempty"`
	Retries         int                 `json:"retries"`
	TimeoutMinutes  int                 `json:"timeoutMinutes"`
	X               float64             `json:"x"`
	Y               float64             `json:"y"`
}
type Workflow struct {
	ID             string         `json:"id"`
	ProjectID      string         `json:"projectId"`
	TenantID       string         `json:"tenantId"`
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Version        int            `json:"version"`
	Enabled        bool           `json:"enabled"`
	Strategy       string         `json:"strategy"`
	TimeZone       string         `json:"timeZone"`
	MaxParallel    int            `json:"maxParallel"`
	TimeoutMinutes int            `json:"timeoutMinutes"`
	Nodes          []WorkflowNode `json:"nodes"`
	CreatedBy      string         `json:"createdBy"`
	UpdatedAt      string         `json:"updatedAt"`
}
type WorkflowVersion struct {
	Version   int      `json:"version"`
	Snapshot  Workflow `json:"snapshot"`
	CreatedBy string   `json:"createdBy"`
	CreatedAt string   `json:"createdAt"`
}
type WorkflowRun struct {
	OwnerID     string    `json:"ownerId"`
	ID          string    `json:"id"`
	ProjectID   string    `json:"projectId"`
	WorkflowID  string    `json:"workflowId"`
	Name        string    `json:"name"`
	Version     int       `json:"version"`
	Status      string    `json:"status"`
	Trigger     string    `json:"trigger"`
	BackfillID  string    `json:"backfillId,omitempty"`
	ScheduledAt string    `json:"scheduledAt"`
	CreatedAt   string    `json:"createdAt"`
	StartedAt   string    `json:"startedAt,omitempty"`
	FinishedAt  string    `json:"finishedAt,omitempty"`
	CreatedBy   string    `json:"createdBy"`
	Message     string    `json:"message"`
	Snapshot    Workflow  `json:"snapshot"`
	Tasks       []TaskRun `json:"tasks"`
}
type TaskRun struct {
	CancelRequested bool   `json:"cancelRequested,omitempty"`
	TimedOut        bool   `json:"timedOut,omitempty"`
	ID              string `json:"id"`
	RunID           string `json:"runId"`
	ProjectID       string `json:"projectId"`
	NodeID          string `json:"nodeId"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	Attempt         int    `json:"attempt"`
	ApplicationName string `json:"applicationName"`
	Namespace       string `json:"namespace"`
	Manifest        string `json:"manifest"`
	StartedAt       string `json:"startedAt,omitempty"`
	FinishedAt      string `json:"finishedAt,omitempty"`
	NextAttemptAt   string `json:"nextAttemptAt,omitempty"`
	Message         string `json:"message"`
}
type WorkflowSchedule struct {
	ID         string `json:"id"`
	ProjectID  string `json:"projectId"`
	WorkflowID string `json:"workflowId"`
	Name       string `json:"name"`
	Cron       string `json:"cron"`
	TimeZone   string `json:"timeZone"`
	Enabled    bool   `json:"enabled"`
	Misfire    string `json:"misfire"`
	NextRunAt  string `json:"nextRunAt"`
	StartsAt   string `json:"startsAt,omitempty"`
	EndsAt     string `json:"endsAt,omitempty"`
	CreatedBy  string `json:"createdBy"`
}
