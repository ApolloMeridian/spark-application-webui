package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/localauth"
)

// The locked pre-change row and the audit share a transaction. Only a whitelist
// of administrative fields is recorded, never passwords, hashes or tokens.
func insertUserAudit(ctx context.Context, tx pgx.Tx, before, after *domain.User, passwordChanged bool) error {
	actor, tracked := localauth.AuditActor(ctx)
	if !tracked {
		return nil
	}
	target := after
	if target == nil {
		target = before
	}
	tenantLabel := func(id string) (string, error) {
		var name string
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id=$1`, id).Scan(&name); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s (%s)", name, id), nil
	}
	operation := "USER_UPDATE"
	changes := []string{fmt.Sprintf("user=%s; userId=%s", target.Username, target.ID)}
	if before == nil || after == nil {
		operation = "USER_CREATE"
		if after == nil {
			operation = "USER_DELETE"
		}
		label, err := tenantLabel(target.TenantID)
		if err != nil {
			return err
		}
		changes = append(changes, "tenant="+label, "role="+string(target.Role))
	} else {
		if before.TenantID != after.TenantID {
			operation = "USER_TENANT_CHANGE"
			from, err := tenantLabel(before.TenantID)
			if err != nil {
				return err
			}
			to, err := tenantLabel(after.TenantID)
			if err != nil {
				return err
			}
			changes = append([]string{fmt.Sprintf("tenant: %s -> %s", from, to)}, changes...)
		}
		if before.Role != after.Role {
			changes = append(changes, fmt.Sprintf("role: %s -> %s", before.Role, after.Role))
		}
		if before.Disabled != after.Disabled {
			changes = append(changes, fmt.Sprintf("disabled: %t -> %t", before.Disabled, after.Disabled))
		}
		if strings.Join(before.Namespaces, "\x00") != strings.Join(after.Namespaces, "\x00") {
			changes = append(changes, fmt.Sprintf("namespaces: %v -> %v", before.Namespaces, after.Namespaces))
		}
		if before.DisplayName != after.DisplayName {
			changes = append(changes, "displayName changed")
		}
		if before.Email != after.Email {
			changes = append(changes, "email changed")
		}
		if passwordChanged {
			changes = append(changes, "password changed")
		}
		if len(changes) == 1 {
			changes = append(changes, "no administrative field changes")
		}
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO operation_audit(id,application_name,namespace,operator_name,operation,reason,occurred_at,result,message,tenant_id)
VALUES($1,$2,'',$3,$4,'',now(),'SUCCESS',$5,$6)`, "user-audit-"+hex.EncodeToString(random), target.Username, actor.Username, operation, strings.Join(changes, "; "), actor.TenantID)
	return err
}
