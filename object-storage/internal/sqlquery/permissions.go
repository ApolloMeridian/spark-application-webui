package sqlquery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var catalogActions = []string{"read", "write", "overwrite", "update", "deleteRows", "merge", "create", "delete", "purge", "metadataRead", "metadataWrite"}
var maintenanceActions = []string{"rewriteDataFiles", "rewriteManifests", "expireSnapshots", "previewOrphanFiles"}
var sqlActions = append(slices.Clone(catalogActions), maintenanceActions...)

const permissionSchema = `
CREATE TABLE IF NOT EXISTS console_sql_permissions (
 tenant text NOT NULL, user_id text NOT NULL, catalog text NOT NULL,
 scope text NOT NULL CONSTRAINT console_sql_permissions_scope_check CHECK(scope IN ('catalog','namespace','table','view')), resource text NOT NULL,
 table_uuid text NOT NULL DEFAULT '', actions text[] NOT NULL,
 grant_id uuid NOT NULL DEFAULT gen_random_uuid(),
 PRIMARY KEY(tenant,user_id,catalog,scope,resource)
);
CREATE TABLE IF NOT EXISTS console_sql_bootstrap_claims (
 tenant text NOT NULL, user_id text NOT NULL, catalog text NOT NULL,
 PRIMARY KEY(tenant,user_id,catalog)
);
ALTER TABLE console_sql_bootstrap_claims ADD COLUMN IF NOT EXISTS maintenance_defaults_applied boolean NOT NULL DEFAULT false;
ALTER TABLE console_sql_queries ADD COLUMN IF NOT EXISTS catalog text NOT NULL DEFAULT '';
ALTER TABLE console_sql_queries ADD COLUMN IF NOT EXISTS write_table text NOT NULL DEFAULT '';
ALTER TABLE console_sql_queries ADD COLUMN IF NOT EXISTS statement_plan jsonb;
DO $$ BEGIN
	IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='console_sql_permissions'::regclass
		AND conname='console_sql_permissions_scope_check' AND position('view' IN pg_get_constraintdef(oid))=0) THEN
		ALTER TABLE console_sql_permissions DROP CONSTRAINT console_sql_permissions_scope_check;
		ALTER TABLE console_sql_permissions ADD CONSTRAINT console_sql_permissions_scope_check
			CHECK(scope IN ('catalog','namespace','table','view'));
	END IF;
END $$;
UPDATE console_sql_permissions p
	SET actions=actions||ARRAY['overwrite','update','deleteRows','merge','purge'],grant_id=gen_random_uuid()
	WHERE scope='catalog' AND resource='polaris' AND actions @> ARRAY['read','write','create','delete','metadataRead','metadataWrite']
		AND NOT (actions && ARRAY['overwrite','update','deleteRows','merge','purge'])
		AND EXISTS (SELECT 1 FROM console_sql_bootstrap_claims b WHERE b.tenant=p.tenant AND b.user_id=p.user_id AND b.catalog=p.catalog);
`

func (c Config) catalogID() string {
	hash := sha256.Sum256([]byte(c.Polaris + "\n" + c.Realm + "\n" + c.Warehouse))
	return hex.EncodeToString(hash[:])
}

func engineProfile(x user) Profile {
	hash := sha256.Sum256([]byte(x.Tenant + "\n" + x.ID))
	return Profile{KyuubiUser: "console_" + hex.EncodeToString(hash[:16])}
}

func (s *Server) bootstrap(ctx context.Context) error {
	for _, bootstrap := range s.cfg.Bootstrap {
		_, err := s.db.Exec(ctx, `WITH claim AS (
			INSERT INTO console_sql_bootstrap_claims(tenant,user_id,catalog,maintenance_defaults_applied)
			SELECT tenant_id,id,$3,true FROM local_users
			WHERE tenant_id=$1 AND username_normalized=lower($2) AND role='admin' AND NOT disabled
			ON CONFLICT DO NOTHING RETURNING tenant,user_id,catalog
		), permission AS (
			INSERT INTO console_sql_permissions(tenant,user_id,catalog,scope,resource,actions)
			SELECT tenant,user_id,catalog,'catalog','polaris',$4 FROM claim
			ON CONFLICT DO NOTHING
		)
		UPDATE tenants SET permissions=COALESCE(permissions,'{}'::jsonb)||'{"sqlQuery":true,"sqlManage":true}'::jsonb
		WHERE id IN (SELECT tenant FROM claim)`, bootstrap.Tenant, bootstrap.Username, s.cfg.catalogID(), sqlActions)
		if err != nil {
			return err
		}
		// Upgrade an original full bootstrap grant once. Keep edited or revoked grants intact.
		_, err = s.db.Exec(ctx, `WITH upgraded AS (
			UPDATE console_sql_bootstrap_claims b SET maintenance_defaults_applied=true
			FROM local_users u WHERE b.tenant=$1 AND b.catalog=$3 AND b.tenant=u.tenant_id AND b.user_id=u.id
				AND u.username_normalized=lower($2) AND u.role='admin' AND NOT u.disabled
				AND NOT b.maintenance_defaults_applied
			RETURNING b.tenant,b.user_id,b.catalog
		)
		UPDATE console_sql_permissions p SET actions=$4,grant_id=gen_random_uuid()
		FROM upgraded b
		WHERE p.tenant=b.tenant AND p.user_id=b.user_id AND p.catalog=b.catalog AND p.scope='catalog'
			AND p.resource='polaris' AND p.actions @> $5::text[] AND p.actions <@ $5::text[]`,
			bootstrap.Tenant, bootstrap.Username, s.cfg.catalogID(), sqlActions, catalogActions)
		if err != nil {
			return err
		}
	}
	return nil
}

func validNamespace(name string) bool {
	parts := strings.Split(name, ".")
	if len(parts) < 2 || len(parts) > 7 || parts[0] != "polaris" {
		return false
	}
	for _, part := range parts[1:] {
		if !catalogName.MatchString(part) {
			return false
		}
	}
	return true
}

func namespaceOf(table string) string {
	return table[:strings.LastIndex(table, ".")]
}

func validAccess(scope, action, resource string) bool {
	if !slices.Contains(sqlActions, action) {
		return false
	}
	switch scope {
	case "catalog":
		return resource == "polaris"
	case "namespace":
		return validNamespace(resource)
	case "table":
		return validTable(resource) && action != "create"
	case "view":
		return validTable(resource) && slices.Contains([]string{"read", "delete", "metadataRead", "metadataWrite"}, action)
	default:
		return false
	}
}

func matchingGrant(row grant, scope, resource, action string) bool {
	if !slices.Contains(row.Actions, action) {
		return false
	}
	if row.Scope == "catalog" {
		return true
	}
	if row.Scope == "table" || row.Scope == "view" {
		return scope == row.Scope && resource == row.Table
	}
	return (scope == "namespace" && row.Table == resource) ||
		((scope == "table" || scope == "view") && namespaceOf(resource) == row.Table)
}

func (s *Server) grantsFor(ctx context.Context, x user, all bool) ([]grant, error) {
	rows, err := s.db.Query(ctx, `SELECT p.user_id,p.tenant,p.scope,p.resource,p.table_uuid,p.actions,p.grant_id::text
		FROM console_sql_permissions p JOIN local_users u ON u.id=p.user_id AND u.tenant_id=p.tenant
		WHERE p.catalog=$2 AND ((p.tenant=$1 AND p.user_id=$4) OR ($3 AND (p.tenant=$1 OR $5)))
		ORDER BY CASE WHEN p.scope IN ('table','view') THEN 0 WHEN p.scope='namespace' THEN 1 ELSE 2 END,p.resource,p.user_id LIMIT 5001`,
		x.Tenant, s.cfg.catalogID(), all, x.ID, x.CatalogManager)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []grant{}
	for rows.Next() {
		var g grant
		if err := rows.Scan(&g.User, &g.Tenant, &g.Scope, &g.Table, &g.UUID, &g.Actions, &g.version); err != nil {
			return nil, err
		}
		list = append(list, g)
	}
	return list, rows.Err()
}

func permits(grants []grant, scope, resource, action string) (grant, bool) {
	for _, g := range grants {
		if matchingGrant(g, scope, resource, action) {
			return g, true
		}
	}
	return grant{}, false
}

func (s *Server) authorize(ctx context.Context, x user, targets []objectAccess) ([]string, error) {
	grants, err := s.grantsFor(ctx, x, false)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(targets))
	for _, target := range targets {
		if !validAccess(target.Scope, target.Action, target.Resource) {
			return nil, errors.New("invalid catalog access")
		}
		g, ok := permits(grants, target.Scope, target.Resource, target.Action)
		if !ok {
			return nil, errors.New("missing " + target.Action + " permission on " + target.Resource)
		}
		ids = append(ids, g.UUID+":"+g.version)
	}
	return ids, nil
}

func (s *Server) changeGrant(w http.ResponseWriter, r *http.Request, x user, add bool) {
	if !x.Manager {
		reply(w, 403, nil, "SQL grant management permission required")
		return
	}
	var g grant
	if !decode(w, r, &g) {
		return
	}
	if g.User == "" || !validAccess(g.Scope, "metadataRead", g.Table) ||
		(add && (len(g.Actions) == 0 || len(g.Actions) > len(sqlActions))) {
		reply(w, 400, nil, "invalid SQL grant")
		return
	}
	if g.Scope == "catalog" && !x.CatalogManager {
		reply(w, 403, nil, "catalog administration permission required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	current, err := s.grantsFor(ctx, x, false)
	if err != nil {
		reply(w, 503, nil, "SQL permissions unavailable")
		return
	}
	authority, ok := permits(current, g.Scope, g.Table, "metadataWrite")
	if !ok {
		reply(w, 403, nil, "SQL grant is outside your scope")
		return
	}
	g.UUID = ""
	if add {
		for _, action := range g.Actions {
			_, ok := permits(current, g.Scope, g.Table, action)
			if !ok || !validAccess(g.Scope, action, g.Table) {
				reply(w, 403, nil, "SQL permission is outside your scope")
				return
			}
		}
		slices.Sort(g.Actions)
		g.Actions = slices.Compact(g.Actions)
		session, err := s.clients.catalog(ctx)
		if err != nil {
			reply(w, 503, nil, "Polaris catalog unavailable")
			return
		}
		if g.Scope == "table" || g.Scope == "view" {
			if g.Scope == "table" {
				info, err := session.table(ctx, g.Table)
				if err == nil {
					g.UUID = info.UUID
				}
			} else {
				info, err := session.view(ctx, g.Table)
				if err == nil {
					g.UUID = info.UUID
				}
			}
			if g.UUID == "" {
				reply(w, 404, nil, "catalog object unavailable")
				return
			}
			if authority.UUID != "" && authority.UUID != g.UUID {
				reply(w, 403, nil, "delegation applies to a previous table identity")
				return
			}
			for _, action := range g.Actions {
				owner, _ := permits(current, g.Scope, g.Table, action)
				if owner.UUID != "" && owner.UUID != g.UUID {
					reply(w, 403, nil, "delegation applies to a previous table identity")
					return
				}
			}
		} else if g.Scope == "namespace" && session.get(ctx, session.namespacePath(g.Table), &struct{}{}) != nil {
			reply(w, 404, nil, "namespace unavailable")
			return
		}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		reply(w, 503, nil, "SQL database unavailable")
		return
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `SELECT tenant_id FROM local_users
		WHERE id=$1 AND (tenant_id=$2 OR $3) AND (NOT $4 OR NOT disabled) FOR SHARE`,
		g.User, x.Tenant, x.CatalogManager, add).Scan(&g.Tenant)
	if errors.Is(err, pgx.ErrNoRows) {
		reply(w, 404, nil, "SQL user unavailable")
		return
	}
	if err != nil {
		reply(w, 503, nil, "SQL identities unavailable")
		return
	}
	action := "SQL_REVOKE"
	if add {
		action = "SQL_GRANT"
		_, err = tx.Exec(ctx, `INSERT INTO console_sql_permissions(tenant,user_id,catalog,scope,resource,table_uuid,actions)
			VALUES ($1,$7,$2,$3,$4,$5,$6)
			ON CONFLICT(tenant,user_id,catalog,scope,resource) DO UPDATE
			SET table_uuid=EXCLUDED.table_uuid,actions=EXCLUDED.actions,grant_id=gen_random_uuid()`,
			g.Tenant, s.cfg.catalogID(), g.Scope, g.Table, g.UUID, g.Actions, g.User)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM console_sql_permissions WHERE tenant=$1 AND catalog=$2 AND scope=$3 AND resource=$4 AND user_id=$5`,
			g.Tenant, s.cfg.catalogID(), g.Scope, g.Table, g.User)
	}
	if err == nil {
		err = audit(ctx, tx, x, g.Table, action, "SUCCESS", "user="+g.User+" tenant="+g.Tenant+" scope="+g.Scope+" privileges="+strings.Join(g.Actions, ","))
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		reply(w, 503, nil, "grant update failed")
		return
	}
	reply(w, 200, g, "")
}
