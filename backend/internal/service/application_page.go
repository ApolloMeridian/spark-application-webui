package service

import (
	"context"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/kube"
)

func (s *Service) QueryApplications(ctx context.Context, q domain.ApplicationQuery) (domain.ApplicationList, error) {
	q.Namespaces = allowedNamespaces(s.config.Namespaces, q.Namespaces)
	if index, ok := s.audits.(interface {
		QueryLiveApplications(context.Context, domain.ApplicationQuery) (domain.ApplicationList, error)
	}); ok {
		return index.QueryLiveApplications(ctx, q)
	}
	return domain.ApplicationList{}, &kube.APIError{StatusCode: 503, Message: "application query index is unavailable"}
}
