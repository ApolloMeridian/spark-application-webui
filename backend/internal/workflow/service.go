package workflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/kube"
	"spark-control-center/backend/internal/localauth"
	"spark-control-center/backend/internal/service"
	"spark-control-center/backend/internal/store"
)

type Applications interface {
	SubmitApplication(context.Context, string, string, string) (domain.SparkApplication, error)
	DryRunApplication(context.Context, string, string, string) (domain.ManifestPreview, error)
	GetWorkflowApplication(context.Context, string, string) (domain.SparkApplication, error)
	KillApplication(context.Context, string, string, string, string) (domain.OperationAudit, error)
}
type Service struct {
	pool   *pgxpool.Pool
	store  *store.PostgresStore
	apps   Applications
	cfg    config.Config
	logger *slog.Logger
}

func New(pg *store.PostgresStore, apps Applications, cfg config.Config, logger *slog.Logger) *Service {
	return &Service{pg.WorkflowPool(), pg, apps, cfg, logger}
}
func id(prefix string) string {
	var data [12]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic(err)
	}
	return prefix + "-" + hex.EncodeToString(data[:])
}
func stamp(t time.Time) string     { return t.UTC().Format(time.RFC3339Nano) }
func invalid(message string) error { return &kube.APIError{StatusCode: 400, Message: message} }
func missing() error {
	return &kube.APIError{StatusCode: 404, Message: "resource not found in this tenant"}
}
func conflict(message string) error { return &kube.APIError{StatusCode: 409, Message: message} }
func tenant(user domain.User) string {
	if user.TenantID == "" {
		return "default"
	}
	return user.TenantID
}
func writable(user domain.User) error {
	if user.Disabled || user.Role != domain.RoleAdmin {
		return localauth.ErrForbidden
	}
	return nil
}
func (s *Service) namespace(user domain.User, namespace string) error {
	if !s.cfg.NamespaceAllowed(namespace) {
		return localauth.ErrNamespaceForbidden
	}
	if len(user.Namespaces) == 0 {
		return nil
	}
	for _, ns := range user.Namespaces {
		if ns == namespace {
			return nil
		}
	}
	return localauth.ErrNamespaceForbidden
}
func (s *Service) Audit(ctx context.Context, user domain.User, operation, resource, message string) error {
	audit := domain.NewOperationAudit(id("audit"), "", resource, user.Username, operation, "", "SUCCESS", message)
	audit.TenantID = tenant(user)
	return s.store.Insert(ctx, audit)
}
func auditTx(ctx context.Context, tx pgx.Tx, user, operation, resource, message string) error {
	_, err := tx.Exec(ctx, `INSERT INTO operation_audit(id,application_name,namespace,operator_name,operation,reason,occurred_at,result,message,tenant_id) VALUES($1,$2,'',$3,$4,'',now(),'SUCCESS',$5,COALESCE(
(SELECT tenant_id FROM projects WHERE id=$2),
(SELECT p.tenant_id FROM workflows w JOIN projects p ON p.id=w.project_id WHERE w.id=$2),
(SELECT p.tenant_id FROM workflow_runs r JOIN projects p ON p.id=r.project_id WHERE r.id=$2),
(SELECT p.tenant_id FROM workflow_schedules s JOIN projects p ON p.id=s.project_id WHERE s.id=$2),
(SELECT tenant_id FROM local_users WHERE username=$3),''))`, id("audit"), resource, user, operation, message)
	return err
}
func (s *Service) transaction(ctx context.Context, operation func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Config mutations and scheduler decisions use the same transaction lock.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(14001400)`); err != nil {
		return err
	}
	if err = operation(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) readTransaction(ctx context.Context, operation func(pgx.Tx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = operation(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) ListTenants(ctx context.Context, user domain.User) ([]domain.Tenant, error) {
	result := []domain.Tenant{}
	rows, err := s.pool.Query(ctx, `SELECT id,name,created_at FROM tenants WHERE $1 OR id=$2 ORDER BY name`, user.Role == domain.RoleAdmin, tenant(user))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var x domain.Tenant
		var at time.Time
		if err = rows.Scan(&x.ID, &x.Name, &at); err != nil {
			return nil, err
		}
		x.CreatedAt = stamp(at)
		result = append(result, x)
	}
	return result, rows.Err()
}
func (s *Service) SaveTenant(ctx context.Context, user domain.User, x domain.Tenant) (domain.Tenant, error) {
	if err := writable(user); err != nil {
		return x, err
	}
	x.Name = strings.TrimSpace(x.Name)
	if x.Name == "" || len(x.Name) > 100 {
		return x, invalid("tenant name is required (max 100 characters)")
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var at time.Time
		if x.ID == "" {
			x.ID = id("tenant")
			err := tx.QueryRow(ctx, `INSERT INTO tenants(id,name) VALUES($1,$2) RETURNING created_at`, x.ID, x.Name).Scan(&at)
			if err != nil {
				return err
			}
		} else {
			if x.ID == "default" && x.Name != "default" {
				return invalid("default tenant cannot be renamed")
			}
			if err := tx.QueryRow(ctx, `UPDATE tenants SET name=$2 WHERE id=$1 RETURNING created_at`, x.ID, x.Name).Scan(&at); err != nil {
				return err
			}
		}
		x.CreatedAt = stamp(at)
		return auditTx(ctx, tx, user.Username, "TENANT_SAVE", x.ID, x.Name)
	})
	return x, err
}
func (s *Service) ListProjects(ctx context.Context, user domain.User) ([]domain.Project, error) {
	result := []domain.Project{}
	rows, err := s.pool.Query(ctx, `SELECT id,tenant_id,name,description,created_by,created_at FROM projects WHERE tenant_id=$1 ORDER BY name`, tenant(user))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var x domain.Project
		var at time.Time
		if err = rows.Scan(&x.ID, &x.TenantID, &x.Name, &x.Description, &x.CreatedBy, &at); err != nil {
			return nil, err
		}
		x.CreatedAt = stamp(at)
		result = append(result, x)
	}
	return result, rows.Err()
}
func projectAccess(ctx context.Context, tx pgx.Tx, user domain.User, project string) error {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1 AND tenant_id=$2)`, project, tenant(user)).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return missing()
	}
	return nil
}
func (s *Service) SaveProject(ctx context.Context, user domain.User, x domain.Project) (domain.Project, error) {
	if err := writable(user); err != nil {
		return x, err
	}
	x.Name = strings.TrimSpace(x.Name)
	if x.Name == "" || len(x.Name) > 100 {
		return x, invalid("project name is required (max 100 characters)")
	}
	x.TenantID = tenant(user)
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var at time.Time
		if x.ID == "" {
			x.ID = id("project")
			x.CreatedBy = user.Username
			if err := tx.QueryRow(ctx, `INSERT INTO projects(id,tenant_id,name,description,created_by) VALUES($1,$2,$3,$4,$5) RETURNING created_at`, x.ID, x.TenantID, x.Name, x.Description, x.CreatedBy).Scan(&at); err != nil {
				return err
			}
		} else {
			if err := projectAccess(ctx, tx, user, x.ID); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `UPDATE projects SET name=$2,description=$3 WHERE id=$1 RETURNING created_by,created_at`, x.ID, x.Name, x.Description).Scan(&x.CreatedBy, &at); err != nil {
				return err
			}
		}
		x.CreatedAt = stamp(at)
		return auditTx(ctx, tx, user.Username, "PROJECT_SAVE", x.ID, x.Name)
	})
	return x, err
}
func (s *Service) DeleteProject(ctx context.Context, user domain.User, project string) error {
	if err := writable(user); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx pgx.Tx) error {
		if err := projectAccess(ctx, tx, user, project); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM workflows WHERE project_id=$1`, project).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return conflict("delete workflows and execution history before deleting project")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM projects WHERE id=$1`, project); err != nil {
			return err
		}
		return auditTx(ctx, tx, user.Username, "PROJECT_DELETE", project, "")
	})
}
func (s *Service) prepare(ctx context.Context, user domain.User, x *domain.Workflow) error {
	if err := writable(user); err != nil {
		return err
	}
	if strings.TrimSpace(x.Name) == "" || len(x.Name) > 100 {
		return invalid("workflow name is required (max 100 characters)")
	}
	if x.TimeZone == "" {
		x.TimeZone = s.cfg.WorkflowTimeZone
		if x.TimeZone == "" {
			x.TimeZone = "UTC"
		}
	}
	if _, err := time.LoadLocation(x.TimeZone); err != nil {
		return invalid("invalid workflow timezone")
	}
	if x.Strategy == "" {
		x.Strategy = "serial_wait"
	}
	if !strategy(x.Strategy) {
		return invalid("invalid workflow strategy")
	}
	if x.MaxParallel == 0 {
		x.MaxParallel = 10
	}
	if x.MaxParallel < 1 || x.MaxParallel > 100 || x.TimeoutMinutes < 0 || x.TimeoutMinutes > 525600 {
		return invalid("invalid workflow concurrency or timeout")
	}
	limit := s.cfg.WorkflowMaxNodes
	if limit == 0 {
		limit = 50
	}
	if len(x.Nodes) < 1 || len(x.Nodes) > limit {
		return invalid(fmt.Sprintf("workflow requires 1-%d tasks", limit))
	}
	seen := map[string]bool{}
	for i := range x.Nodes {
		n := &x.Nodes[i]
		if n.ID == "" || seen[n.ID] || len(n.ID) > 100 || n.Name == "" {
			return invalid("task IDs must be unique and task names are required")
		}
		seen[n.ID] = true
		if err := s.namespace(user, n.Namespace); err != nil {
			return err
		}
		if n.Strategy == "" {
			n.Strategy = "parallel"
		}
		if !strategy(n.Strategy) || n.Retries < 0 || n.Retries > 10 || n.TimeoutMinutes < 0 || n.TimeoutMinutes > 525600 {
			return invalid("invalid task strategy, retries or timeout")
		}
		if n.TemplateID != "" {
			defaults := map[string]string{}
			for _, p := range n.Parameters {
				defaults[p.Name] = p.Default
			}
			t, err := s.store.GetTemplate(ctx, n.TemplateID)
			if err != nil {
				return err
			}
			if t.Disabled || t.Namespace != n.Namespace {
				return invalid("template disabled or namespace mismatch")
			}
			if n.TemplateVersion == 0 {
				n.TemplateVersion = t.Version
			}
			found := false
			for _, v := range t.Versions {
				if v.Version == n.TemplateVersion {
					n.Manifest = v.Manifest
					n.Parameters = v.Parameters
					for j := range n.Parameters {
						if value, ok := defaults[n.Parameters[j].Name]; ok {
							n.Parameters[j].Default = value
						}
					}
					found = true
					break
				}
			}
			if !found {
				return missing()
			}
		}
		manifest, err := service.NormalizeManifest(n.Manifest, n.Namespace)
		if err != nil {
			return invalid(err.Error())
		}
		n.Manifest = manifest
		if err := service.ValidateWorkflowTemplate(n.Manifest, n.Namespace, n.Parameters); err != nil {
			return err
		}
		if n.DependsOn == nil {
			n.DependsOn = []string{}
		}
		if n.Parameters == nil {
			n.Parameters = []domain.TemplateParameter{}
		}
		if n.Values == nil {
			n.Values = map[string]string{}
		}
		if _, _, err := RenderNode(*n, time.Now(), x.TimeZone, "preview-12345678", 1); err != nil {
			return invalid(n.Name + ": " + err.Error())
		}
	}
	visiting := map[string]bool{}
	done := map[string]bool{}
	var visit func(string) error
	visit = func(key string) error {
		if visiting[key] {
			return invalid("workflow dependencies contain a cycle")
		}
		if done[key] {
			return nil
		}
		visiting[key] = true
		for _, n := range x.Nodes {
			if n.ID == key {
				deps := map[string]bool{}
				for _, dep := range n.DependsOn {
					if !seen[dep] || dep == key || deps[dep] {
						return invalid("invalid or duplicate task dependency")
					}
					deps[dep] = true
					if err := visit(dep); err != nil {
						return err
					}
				}
			}
		}
		visiting[key] = false
		done[key] = true
		return nil
	}
	for _, n := range x.Nodes {
		if err := visit(n.ID); err != nil {
			return err
		}
	}
	return nil
}
func strategy(value string) bool {
	return value == "parallel" || value == "serial_wait" || value == "serial_discard"
}
func readWorkflow(ctx context.Context, tx pgx.Tx, user domain.User, project, key string) (domain.Workflow, error) {
	var x domain.Workflow
	var data []byte
	if err := projectAccess(ctx, tx, user, project); err != nil {
		return x, err
	}
	err := tx.QueryRow(ctx, `SELECT definition FROM workflows WHERE id=$1 AND project_id=$2`, key, project).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, missing()
	}
	if err != nil {
		return x, err
	}
	err = json.Unmarshal(data, &x)
	if err == nil && !visible(user, x) {
		return x, localauth.ErrNamespaceForbidden
	}
	return x, err
}
func visible(user domain.User, x domain.Workflow) bool {
	if len(user.Namespaces) == 0 {
		return true
	}
	for _, n := range x.Nodes {
		allowed := false
		for _, ns := range user.Namespaces {
			if ns == n.Namespace {
				allowed = true
			}
		}
		if !allowed {
			return false
		}
	}
	return true
}
func (s *Service) ListWorkflows(ctx context.Context, user domain.User, project string) ([]domain.Workflow, error) {
	result := []domain.Workflow{}
	err := s.readTransaction(ctx, func(tx pgx.Tx) error {
		if err := projectAccess(ctx, tx, user, project); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT definition FROM workflows WHERE project_id=$1 ORDER BY updated_at DESC`, project)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var x domain.Workflow
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &x); err != nil {
				return err
			}
			if visible(user, x) {
				result = append(result, x)
			}
		}
		return rows.Err()
	})
	return result, err
}
func (s *Service) SaveWorkflow(ctx context.Context, user domain.User, x domain.Workflow) (domain.Workflow, error) {
	if err := s.prepare(ctx, user, &x); err != nil {
		return x, err
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if err := projectAccess(ctx, tx, user, x.ProjectID); err != nil {
			return err
		}
		x.TenantID = tenant(user)
		x.UpdatedAt = stamp(time.Now())
		if x.ID == "" {
			x.ID = id("flow")
			x.Version = 1
			x.CreatedBy = user.Username
		} else {
			prior, err := readWorkflow(ctx, tx, user, x.ProjectID, x.ID)
			if err != nil {
				return err
			}
			var maximum int
			if err := tx.QueryRow(ctx, `SELECT max_version FROM workflows WHERE id=$1 FOR UPDATE`, x.ID).Scan(&maximum); err != nil {
				return err
			}
			x.Version = maximum + 1
			x.CreatedBy = prior.CreatedBy
		}
		return saveVersion(ctx, tx, user, x, "WORKFLOW_SAVE")
	})
	return x, err
}
func saveVersion(ctx context.Context, tx pgx.Tx, user domain.User, x domain.Workflow, operation string) error {
	data, err := json.Marshal(x)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO workflows(id,project_id,current_version,definition) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET current_version=EXCLUDED.current_version,definition=EXCLUDED.definition,updated_at=now()`, x.ID, x.ProjectID, x.Version, data)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE workflows SET max_version=GREATEST(max_version,$2) WHERE id=$1`, x.ID, x.Version); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO workflow_versions(workflow_id,version,snapshot,created_by) VALUES($1,$2,$3,$4)`, x.ID, x.Version, data, user.Username)
	if err != nil {
		return err
	}
	return auditTx(ctx, tx, user.Username, operation, x.ID, fmt.Sprintf("version=%d tenant=%s project=%s", x.Version, x.TenantID, x.ProjectID))
}
func (s *Service) Versions(ctx context.Context, user domain.User, project, key string) ([]domain.WorkflowVersion, error) {
	result := []domain.WorkflowVersion{}
	err := s.readTransaction(ctx, func(tx pgx.Tx) error {
		if _, err := readWorkflow(ctx, tx, user, project, key); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT version,snapshot,created_by,created_at FROM workflow_versions WHERE workflow_id=$1 ORDER BY version DESC`, key)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x domain.WorkflowVersion
			var data []byte
			var at time.Time
			if err := rows.Scan(&x.Version, &data, &x.CreatedBy, &at); err != nil {
				return err
			}
			if err := json.Unmarshal(data, &x.Snapshot); err != nil {
				return err
			}
			if !visible(user, x.Snapshot) {
				continue
			}
			x.CreatedAt = stamp(at)
			result = append(result, x)
		}
		return rows.Err()
	})
	return result, err
}
func (s *Service) VersionAction(ctx context.Context, user domain.User, project, key string, version int, remove bool) (domain.Workflow, error) {
	var result domain.Workflow
	if err := writable(user); err != nil {
		return result, err
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		x, err := readWorkflow(ctx, tx, user, project, key)
		if err != nil {
			return err
		}
		if remove {
			if version == x.Version {
				return conflict("current version cannot be deleted; restore another version first")
			}
			tag, err := tx.Exec(ctx, `DELETE FROM workflow_versions WHERE workflow_id=$1 AND version=$2`, key, version)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return missing()
			}
			result = x
			return auditTx(ctx, tx, user.Username, "WORKFLOW_VERSION_DELETE", key, fmt.Sprint(version))
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT snapshot FROM workflow_versions WHERE workflow_id=$1 AND version=$2`, key, version).Scan(&raw); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return err
		}
		// Restore the frozen snapshot without resolving mutable template references again.
		for _, n := range result.Nodes {
			if err := s.namespace(user, n.Namespace); err != nil {
				return err
			}
		}
		result.Version = version
		result.UpdatedAt = stamp(time.Now())
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE workflows SET current_version=$2,definition=$3,updated_at=now() WHERE id=$1`, key, version, data); err != nil {
			return err
		}
		return auditTx(ctx, tx, user.Username, "WORKFLOW_RESTORE", key, fmt.Sprintf("from=%d to=%d", x.Version, version))
	})
	return result, err
}
func (s *Service) DeleteWorkflow(ctx context.Context, user domain.User, project, key string) error {
	if err := writable(user); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := readWorkflow(ctx, tx, user, project, key); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM workflow_runs WHERE workflow_id=$1`, key).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return conflict("delete execution history before deleting workflow")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM workflow_schedules WHERE workflow_id=$1`, key); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM workflows WHERE id=$1`, key); err != nil {
			return err
		}
		return auditTx(ctx, tx, user.Username, "WORKFLOW_DELETE", key, "")
	})
}

func (s *Service) TemplateVersionAction(ctx context.Context, user domain.User, key string, version int, remove bool) (domain.ApplicationTemplate, error) {
	x, err := s.store.GetTemplate(ctx, key)
	if err != nil {
		return x, err
	}
	if err = s.namespace(user, x.Namespace); err != nil {
		return x, err
	}
	if err = writable(user); err != nil {
		return x, err
	}
	err = s.transaction(ctx, func(tx pgx.Tx) error {
		var current int
		var namespace string
		if err := tx.QueryRow(ctx, `SELECT current_version,namespace FROM application_templates WHERE id=$1 FOR UPDATE`, key).Scan(&current, &namespace); err != nil {
			return err
		}
		if err := s.namespace(user, namespace); err != nil {
			return err
		}
		if remove {
			if current == version {
				return conflict("current template version cannot be deleted")
			}
			tag, err := tx.Exec(ctx, `DELETE FROM application_template_versions WHERE template_id=$1 AND version=$2`, key, version)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return missing()
			}
			return auditTx(ctx, tx, user.Username, "TEMPLATE_VERSION_DELETE", key, fmt.Sprint(version))
		}
		var manifest string
		var parameters []byte
		if err := tx.QueryRow(ctx, `SELECT manifest,parameters FROM application_template_versions WHERE template_id=$1 AND version=$2`, key, version).Scan(&manifest, &parameters); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return missing()
			}
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE application_templates SET current_version=$2,manifest=$3,parameters=$4,updated_at=now() WHERE id=$1`, key, version, manifest, parameters); err != nil {
			return err
		}
		return auditTx(ctx, tx, user.Username, "TEMPLATE_RESTORE", key, fmt.Sprintf("from=%d to=%d", current, version))
	})
	if err != nil {
		return x, err
	}
	return s.store.GetTemplate(ctx, key)
}
