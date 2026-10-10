package store

import (
	"context"
	"encoding/json"
	"spark-control-center/backend/internal/domain"
)

func (s *PostgresStore) ApplyTenantPermissions(ctx context.Context, user domain.User) (domain.User, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT permissions FROM tenants WHERE id=$1`, user.TenantID).Scan(&raw)
	if err != nil {
		return user, err
	}
	if len(raw) == 0 {
		return user, nil
	}
	var p domain.TenantPermissions
	if err = json.Unmarshal(raw, &p); err != nil {
		return user, err
	}
	user.Permissions = &p
	if p.Namespaces == nil {
		p.Namespaces = []string{}
	}
	user.Namespaces = append([]string{}, p.Namespaces...)
	if p.AllNamespaces {
		user.Namespaces = []string{}
	} else if len(user.Namespaces) == 0 {
		// Existing services interpret an empty list as all namespaces. This sentinel
		// cannot be a Kubernetes namespace and therefore safely represents no access.
		user.Namespaces = []string{"__no_namespaces__"}
	}
	return user, nil
}
