package localauth

import (
	"context"
	"spark-control-center/backend/internal/domain"
	"time"
)

type uiTicketStore interface {
	SaveSparkUITicket(context.Context, domain.SparkUITicket) error
	FindSparkUITicket(context.Context, string) (domain.SparkUITicket, error)
}

func (s *Service) IssueSparkUITicket(ctx context.Context, sessionToken, ns, name string) (string, error) {
	store, ok := s.store.(uiTicketStore)
	if !ok {
		return "", ErrUnauthorized
	}
	user, session, err := s.Authenticate(ctx, sessionToken)
	if err != nil {
		return "", err
	}
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	expires := time.Now().Add(15 * time.Minute)
	if session.ExpiresAt.Before(expires) {
		expires = session.ExpiresAt
	}
	tenant := user.TenantID
	if tenant == "" {
		tenant = "default"
	}
	err = store.SaveSparkUITicket(ctx, domain.SparkUITicket{TokenHash: hashToken(token), SessionHash: session.TokenHash, Namespace: ns, Application: name, TenantID: tenant, ExpiresAt: expires})
	return token, err
}

func (s *Service) AuthenticateSparkUITicket(ctx context.Context, token, ns, name string) (domain.User, error) {
	store, ok := s.store.(uiTicketStore)
	if !ok || token == "" || len(token) > 128 {
		return domain.User{}, ErrUnauthorized
	}
	ticket, err := store.FindSparkUITicket(ctx, hashToken(token))
	if err != nil || ticket.Namespace != ns || ticket.Application != name || !time.Now().Before(ticket.ExpiresAt) {
		return domain.User{}, ErrUnauthorized
	}
	user, _, err := s.store.FindSession(ctx, ticket.SessionHash, time.Now().UTC())
	if err != nil || user.Disabled {
		return domain.User{}, ErrUnauthorized
	}
	tenant := user.TenantID
	if tenant == "" {
		tenant = "default"
	}
	if tenant != ticket.TenantID {
		return domain.User{}, ErrUnauthorized
	}
	return s.permissions(ctx, user)
}
