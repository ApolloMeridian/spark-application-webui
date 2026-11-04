package service

import (
	"context"
	"spark-control-center/backend/internal/domain"
	"time"
)

type applicationCache struct {
	apps    []domain.SparkApplication
	expires time.Time
	flight  chan struct{}
	version uint64
}

func (s *Service) ListApplicationsForNamespaces(ctx context.Context, namespaces []string) ([]domain.SparkApplication, error) {
	result := []domain.SparkApplication{}
	for _, ns := range allowedNamespaces(s.config.Namespaces, namespaces) {
		for {
			s.cacheMu.Lock()
			entry := s.listCache[ns]
			if entry == nil {
				entry = &applicationCache{}
				s.listCache[ns] = entry
			}
			if entry.flight != nil {
				wait := entry.flight
				s.cacheMu.Unlock()
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-wait:
				}
				continue
			}
			if time.Now().Before(entry.expires) {
				result = append(result, entry.apps...)
				s.cacheMu.Unlock()
				break
			}
			entry.flight = make(chan struct{})
			version := entry.version
			s.cacheMu.Unlock()
			apps, err := s.listApplicationsUncached(ctx, []string{ns})
			s.cacheMu.Lock()
			if err == nil {
				entry.apps = apps
				if version == entry.version {
					entry.expires = time.Now().Add(3 * time.Second)
				} else {
					entry.expires = time.Time{}
				}
			}
			close(entry.flight)
			entry.flight = nil
			s.cacheMu.Unlock()
			if err != nil {
				return nil, err
			}
			result = append(result, apps...)
			break
		}
	}
	if filter, ok := s.audits.(interface {
		FilterTenantApplications(context.Context, []domain.SparkApplication) ([]domain.SparkApplication, error)
	}); ok {
		return filter.FilterTenantApplications(ctx, result)
	}
	return result, nil
}

// Collection, not GET traffic, owns periodic history persistence. Never persist
// a tenant-filtered list as the cluster's authoritative snapshot.
func (s *Service) collectHistory(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		step, cancel := context.WithTimeout(ctx, 30*time.Second)
		var err error
		if index, ok := s.audits.(interface {
			CollectApplicationNamespace(context.Context, string, func(context.Context) ([]domain.SparkApplication, error)) error
		}); ok {
			for _, ns := range s.config.Namespaces {
				if e := index.CollectApplicationNamespace(step, ns, func(ctx context.Context) ([]domain.SparkApplication, error) {
					return s.listApplicationsWithManifests(ctx, []string{ns}, true)
				}); e != nil {
					err = e
					s.logger.Warn("namespace snapshot collection failed", "namespace", ns, "error", e)
				}
			}
		} else {
			var apps []domain.SparkApplication
			apps, err = s.ListApplicationsForNamespaces(step, nil)
			if err == nil {
				err = s.audits.UpsertApplications(step, apps)
			}
		}
		cancel()
		if err != nil && ctx.Err() == nil {
			s.logger.Warn("history collection failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
