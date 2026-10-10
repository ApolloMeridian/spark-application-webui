package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/localauth"
	"spark-control-center/backend/internal/workflow"
)

func (s *Server) registerWorkflows(mux *http.ServeMux) {
	patterns := []string{"GET /v1/tenants", "POST /v1/tenants", "PATCH /v1/tenants/{id}", "GET /v1/projects", "POST /v1/projects", "PATCH /v1/projects/{project}", "DELETE /v1/projects/{project}",
		"GET /v1/projects/{project}/workflows", "POST /v1/projects/{project}/workflows", "PATCH /v1/projects/{project}/workflows/{id}", "DELETE /v1/projects/{project}/workflows/{id}",
		"GET /v1/projects/{project}/workflows/{id}/versions", "POST /v1/projects/{project}/workflows/{id}/versions/{version}/restore", "DELETE /v1/projects/{project}/workflows/{id}/versions/{version}",
		"POST /v1/projects/{project}/workflows/{id}/trigger", "POST /v1/projects/{project}/workflows/{id}/dry-run", "POST /v1/projects/{project}/workflows/{id}/backfill",
		"GET /v1/projects/{project}/backfills", "POST /v1/projects/{project}/backfills/{id}/cancel",
		"GET /v1/projects/{project}/schedules", "POST /v1/projects/{project}/schedules", "PATCH /v1/projects/{project}/schedules/{id}", "DELETE /v1/projects/{project}/schedules/{id}",
		"GET /v1/projects/{project}/runs", "GET /v1/projects/{project}/task-runs", "POST /v1/projects/{project}/runs/{id}/{action}", "POST /v1/workflows/cron-preview", "GET /v1/workflows/scheduler",
		"POST /v1/templates/{id}/versions/{version}/restore", "DELETE /v1/templates/{id}/versions/{version}"}
	for _, pattern := range patterns {
		mux.HandleFunc(pattern, s.workflowRequest)
	}
}
func (s *Server) workflowRequest(w http.ResponseWriter, r *http.Request) {
	if s.flows == nil {
		s.writeError(w, 503, fmt.Errorf("workflow persistence unavailable"))
		return
	}
	if !s.config.WorkflowsEnabled && !strings.HasPrefix(r.URL.Path, "/v1/tenants") && !strings.HasPrefix(r.URL.Path, "/v1/templates/") {
		s.writeError(w, 404, fmt.Errorf("workflows disabled"))
		return
	}
	user := currentUser(r)
	project := r.PathValue("project")
	key := r.PathValue("id")
	version, _ := strconv.Atoi(r.PathValue("version"))
	ctx := r.Context()
	path := r.URL.Path
	var result any
	var err error
	decode := func(value any) error {
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		if err := json.NewDecoder(r.Body).Decode(value); err != nil {
			return &requestError{"invalid JSON body"}
		}
		return nil
	}
	switch {
	case strings.HasPrefix(path, "/v1/templates/"):
		result, err = s.flows.TemplateVersionAction(ctx, user, key, version, r.Method == http.MethodDelete)
	case strings.HasPrefix(path, "/v1/tenants"):
		if r.Method == http.MethodGet {
			result, err = s.flows.ListTenants(ctx, user)
		} else {
			var x domain.Tenant
			if err = decode(&x); err == nil {
				x.ID = key
				result, err = s.flows.SaveTenant(ctx, user, x)
			}
		}
	case path == "/v1/workflows/scheduler":
		result, err = s.flows.Status(ctx)
	case path == "/v1/workflows/cron-preview":
		var body struct {
			Cron     string `json:"cron"`
			TimeZone string `json:"timeZone"`
			From     string `json:"from"`
			StartsAt string `json:"startsAt"`
			EndsAt   string `json:"endsAt"`
			Count    int    `json:"count"`
		}
		if err = decode(&body); err == nil {
			at := time.Now()
			if body.From != "" {
				at, err = time.Parse(time.RFC3339Nano, body.From)
			}
			if body.Count == 0 {
				body.Count = 10
			}
			if body.Count < 1 || body.Count > 10 {
				err = &requestError{"preview count must be 1-10"}
			}
			var start, end time.Time
			if err == nil && body.StartsAt != "" {
				start, err = time.Parse(time.RFC3339Nano, body.StartsAt)
			}
			if err == nil && body.EndsAt != "" {
				end, err = time.Parse(time.RFC3339Nano, body.EndsAt)
			}
			if err == nil {
				result, err = workflow.CronPreviewWindow(body.Cron, body.TimeZone, at, start, end, body.Count)
				if err != nil {
					err = &requestError{err.Error()}
				}
			}
		}
	case path == "/v1/projects":
		if r.Method == http.MethodGet {
			result, err = s.flows.ListProjects(ctx, user)
		} else {
			var x domain.Project
			if err = decode(&x); err == nil {
				x.ID = ""
				result, err = s.flows.SaveProject(ctx, user, x)
			}
		}
	case strings.HasSuffix(path, "/versions"):
		result, err = s.flows.Versions(ctx, user, project, key)
	case r.PathValue("version") != "":
		result, err = s.flows.VersionAction(ctx, user, project, key, version, r.Method == http.MethodDelete)
	case strings.Contains(path, "/backfills"):
		if r.Method == http.MethodGet {
			result, err = s.flows.ListBackfills(ctx, user, project)
		} else {
			err = s.flows.CancelBackfill(ctx, user, project, key)
		}
	case strings.HasSuffix(path, "/trigger") || strings.HasSuffix(path, "/dry-run") || strings.HasSuffix(path, "/backfill"):
		var body struct {
			ScheduledAt   string                       `json:"scheduledAt"`
			Values        map[string]map[string]string `json:"values"`
			Cron          string                       `json:"cron"`
			TimeZone      string                       `json:"timeZone"`
			From          string                       `json:"from"`
			To            string                       `json:"to"`
			Confirm       bool                         `json:"confirm"`
			Direction     string                       `json:"direction"`
			FailurePolicy string                       `json:"failurePolicy"`
		}
		if err = decode(&body); err == nil {
			at := time.Now()
			if body.ScheduledAt != "" {
				at, err = time.Parse(time.RFC3339Nano, body.ScheduledAt)
			}
			if err == nil {
				switch {
				case strings.HasSuffix(path, "/trigger"):
					result, err = s.flows.Trigger(ctx, user, project, key, at, body.Values)
				case strings.HasSuffix(path, "/dry-run"):
					result, err = s.flows.DryRun(ctx, user, project, key, at)
				default:
					var from, to time.Time
					from, err = time.Parse(time.RFC3339Nano, body.From)
					if err == nil {
						to, err = time.Parse(time.RFC3339Nano, body.To)
					}
					if err == nil {
						result, err = s.flows.Backfill(ctx, user, project, key, body.Cron, body.TimeZone, from, to, body.Confirm, workflow.BackfillOptions{Direction: body.Direction, FailurePolicy: body.FailurePolicy})
					}
				}
			}
		}
	case strings.Contains(path, "/schedules"):
		if r.Method == http.MethodGet {
			result, err = s.flows.ListSchedules(ctx, user, project)
		} else if r.Method == http.MethodDelete {
			err = s.flows.DeleteSchedule(ctx, user, project, key)
		} else {
			var x domain.WorkflowSchedule
			if err = decode(&x); err == nil {
				x.ID = key
				x.ProjectID = project
				result, err = s.flows.SaveSchedule(ctx, user, x)
			}
		}
	case strings.HasSuffix(path, "/task-runs"):
		result, err = s.flows.ListRuns(ctx, user, project, true)
	case strings.Contains(path, "/runs"):
		if r.Method == http.MethodGet {
			result, err = s.flows.ListRuns(ctx, user, project, false)
		} else {
			err = s.flows.RunAction(ctx, user, project, key, r.PathValue("action"))
		}
	case strings.Contains(path, "/workflows"):
		if r.Method == http.MethodGet {
			result, err = s.flows.ListWorkflows(ctx, user, project)
		} else if r.Method == http.MethodDelete {
			err = s.flows.DeleteWorkflow(ctx, user, project, key)
		} else {
			var x domain.Workflow
			if err = decode(&x); err == nil {
				x.ID = key
				x.ProjectID = project
				result, err = s.flows.SaveWorkflow(ctx, user, x)
			}
		}
	default:
		if r.Method == http.MethodDelete {
			err = s.flows.DeleteProject(ctx, user, project)
		} else {
			var x domain.Project
			if err = decode(&x); err == nil {
				x.ID = project
				result, err = s.flows.SaveProject(ctx, user, x)
			}
		}
	}
	if err != nil {
		s.workflowError(w, err)
		return
	}
	if result == nil {
		result = map[string]bool{"ok": true}
	}
	s.writeJSON(w, 200, result)
}

type requestError struct{ message string }

func (e *requestError) Error() string { return e.message }
func (s *Server) workflowError(w http.ResponseWriter, err error) {
	var input *requestError
	var pgerr *pgconn.PgError
	var timeError *time.ParseError
	switch {
	case errors.Is(err, localauth.ErrForbidden), errors.Is(err, localauth.ErrNamespaceForbidden):
		s.writeAuthError(w, err)
	case errors.Is(err, pgx.ErrNoRows):
		s.writeError(w, 404, fmt.Errorf("resource not found"))
	case errors.As(err, &input):
		s.writeError(w, 400, err)
	case errors.As(err, &timeError):
		s.writeError(w, 400, fmt.Errorf("invalid timestamp"))
	case errors.As(err, &pgerr) && pgerr.Code == "23505":
		s.writeError(w, 409, fmt.Errorf("name already exists"))
	default:
		s.writeServiceError(w, err)
	}
}

// Only legacy configuration mutations need this wrapper; the workflow service
// writes its audits in the same PostgreSQL transaction. Bodies (including user
// passwords) are never recorded. Buffering prevents successful replies before
// audit persistence has been checked.
type auditBuffer struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (w *auditBuffer) Header() http.Header            { return w.header }
func (w *auditBuffer) WriteHeader(code int)           { w.code = code }
func (w *auditBuffer) Write(data []byte) (int, error) { return w.body.Write(data) }
func (s *Server) auditMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// User/profile changes persist a semantic audit with the user mutation.
		tracked := strings.HasPrefix(path, "/v1/alert-rules") || strings.HasPrefix(path, "/v1/alerts/") || strings.HasPrefix(path, "/v1/templates") || strings.HasSuffix(path, "/favorite")
		if s.flows == nil || r.Method == "GET" || !tracked || strings.Contains(path, "/versions/") {
			next.ServeHTTP(w, r)
			return
		}
		buffer := &auditBuffer{header: make(http.Header), code: 200}
		next.ServeHTTP(buffer, r)
		if buffer.code < 400 {
			if err := s.flows.Audit(r.Context(), currentUser(r), "CONFIG_"+r.Method, path, "configuration action"); err != nil {
				s.writeError(w, 500, fmt.Errorf("operation applied but audit persistence failed"))
				return
			}
		}
		for key, values := range buffer.header {
			w.Header()[key] = values
		}
		w.WriteHeader(buffer.code)
		_, _ = w.Write(buffer.body.Bytes())
	})
}
