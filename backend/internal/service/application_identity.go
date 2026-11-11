package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/kube"
	"spark-control-center/backend/internal/store"
)

// UID references are immutable Kubernetes resource identities, not Spark run IDs.
// A same-name replacement must never receive an old instance's reads or writes.
func (s *Service) resolveApplication(ctx context.Context, ns, ref string) (kube.Object, *domain.SparkApplication, error) {
	if !s.config.NamespaceAllowed(ns) {
		return nil, nil, &kube.APIError{StatusCode: 403, Message: "namespace is not configured"}
	}
	explicit := strings.HasPrefix(ref, "uid:")
	id := strings.TrimPrefix(ref, "uid:")
	var saved *domain.SparkApplication
	if index, ok := s.audits.(interface {
		GetApplicationSnapshotByID(context.Context, string, string) (domain.SparkApplication, error)
	}); ok {
		app, err := index.GetApplicationSnapshotByID(ctx, ns, id)
		if err == nil {
			saved = &app
		} else if !errors.Is(err, store.ErrSnapshotNotFound) {
			return nil, nil, err
		}
	}
	name := ref
	if saved != nil {
		name = saved.Name
	} else if explicit {
		return nil, nil, &kube.APIError{StatusCode: 404, Message: "application instance not found"}
	}
	object, err := s.kubernetes.GetSparkApplication(ctx, ns, name)
	if err != nil && HTTPStatus(err) != 404 {
		return nil, nil, err
	}
	if saved != nil && (HTTPStatus(err) == 404 || kube.String(object, "metadata", "uid") != saved.ID) {
		markArchived(saved)
		return nil, saved, nil
	}
	if err != nil {
		app, e := s.audits.GetApplicationSnapshot(ctx, ns, name)
		if e == nil {
			markArchived(&app)
			return nil, &app, nil
		}
		if !errors.Is(e, store.ErrSnapshotNotFound) {
			return nil, nil, e
		}
		return nil, nil, err
	}
	if filter, ok := s.audits.(interface {
		FilterTenantApplications(context.Context, []domain.SparkApplication) ([]domain.SparkApplication, error)
	}); ok {
		apps, e := filter.FilterTenantApplications(ctx, []domain.SparkApplication{s.mapApplication(object, nil, nil)})
		if e != nil {
			return nil, nil, e
		}
		if len(apps) == 0 {
			return nil, nil, &kube.APIError{StatusCode: 404, Message: "application instance not found"}
		}
	}
	return object, nil, nil
}

func markArchived(app *domain.SparkApplication) {
	app.Historical = true
	app.SparkUIAvailable = false
	if !terminalState(app.State) {
		app.State = "DELETED"
		app.Lifecycle = buildLifecycle(*app)
	}
}

func (s *Service) PurgeApplicationHistory(ctx context.Context, namespace, id, operator, reason string) (domain.OperationAudit, error) {
	object, snapshot, err := s.resolveApplication(ctx, namespace, "uid:"+id)
	if err != nil {
		return domain.OperationAudit{}, err
	}
	if object != nil || snapshot == nil {
		return domain.OperationAudit{}, &kube.APIError{StatusCode: 409, Message: "only archived application instances can be removed"}
	}
	history, ok := s.audits.(interface {
		PurgeApplicationSnapshot(context.Context, string, domain.OperationAudit) error
	})
	if !ok {
		return domain.OperationAudit{}, &kube.APIError{StatusCode: 503, Message: "application history is unavailable"}
	}
	audit := domain.NewOperationAudit(newID(), namespace, snapshot.Name, operator, "PURGE_HISTORY", reason, "SUCCESS", "Archived SparkApplication snapshot removed: "+snapshot.ID)
	audit.TenantID = domain.TenantScope(ctx)
	if err := history.PurgeApplicationSnapshot(ctx, snapshot.ID, audit); err != nil {
		if errors.Is(err, store.ErrSnapshotNotFound) {
			return domain.OperationAudit{}, &kube.APIError{StatusCode: 404, Message: "archived application instance not found"}
		}
		return domain.OperationAudit{}, err
	}
	return audit, nil
}

func archivedWriteError() error {
	return &kube.APIError{StatusCode: 409, Message: "this application instance is archived; the same-name live resource will not be modified"}
}

// Pod names can be reused too. Historical queries stay inside the observed
// instance lifetime; Kubernetes reads use saved logs rather than a reused Pod.
func historicalRange(app domain.SparkApplication, from, to time.Time) (time.Time, time.Time) {
	if t, e := time.Parse(time.RFC3339Nano, app.CreatedAt); e == nil && from.Before(t) {
		from = t
	}
	end := app.FinishedAt
	if end == "" {
		end = app.LastObservedAt
	}
	if t, e := time.Parse(time.RFC3339Nano, end); e == nil && to.After(t) {
		to = t
	}
	return from, to
}
