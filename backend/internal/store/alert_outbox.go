package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"spark-control-center/backend/internal/domain"
)

// AlertWebhookDelivery is an immutable event snapshot. ID is also the receiver's
// idempotency key; a lease owner may never change a previously attempted payload.
type AlertWebhookDelivery struct {
	ID, Endpoint, LeaseToken string
	Payload                  []byte
	Attempts                 int
}

type AlertOutboxStore interface {
	UpsertAlertWithWebhook(context.Context, domain.Alert, string) (domain.Alert, bool, error)
	ClaimAlertWebhook(context.Context, string) (*AlertWebhookDelivery, error)
	FinishAlertWebhook(context.Context, AlertWebhookDelivery, bool, string) error
	PruneAlertWebhooks(context.Context) error
}

var ErrAlertOutboxFull = errors.New("alert webhook backlog is full; evaluation is backpressured")

const alertOutboxPendingLimit = 10000

func (s *PostgresStore) migrateAlertOutbox(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `
CREATE TABLE IF NOT EXISTS alert_webhook_outbox (
  id text PRIMARY KEY,
  alert_id text NOT NULL REFERENCES application_alerts(id) ON DELETE CASCADE,
  endpoint text NOT NULL,
  endpoint_hash text NOT NULL,
  payload jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  attempts integer NOT NULL DEFAULT 0,
  lease_token text NOT NULL DEFAULT '',
  lease_until timestamptz NOT NULL DEFAULT '-infinity',
  delivered_at timestamptz,
  last_error text NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS alert_webhook_pending_scope_idx ON alert_webhook_outbox(alert_id,endpoint_hash) WHERE delivered_at IS NULL;
CREATE INDEX IF NOT EXISTS alert_webhook_ready_idx ON alert_webhook_outbox(endpoint_hash,next_attempt_at,created_at) WHERE delivered_at IS NULL;
CREATE INDEX IF NOT EXISTS alert_webhook_lease_idx ON alert_webhook_outbox(lease_until) WHERE delivered_at IS NULL;
CREATE INDEX IF NOT EXISTS alert_webhook_delivered_idx ON alert_webhook_outbox(delivered_at) WHERE delivered_at IS NOT NULL;
`)
	return err
}

func endpointHash(endpoint string) string {
	hash := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(hash[:])
}

func outboxToken() (string, error) {
	var value [24]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

// The queue capacity check, observation and delivery intent commit together.
// Saturation returns an explicit error instead of acknowledging a lost intent.
func (s *PostgresStore) UpsertAlertWithWebhook(ctx context.Context, x domain.Alert, endpoint string) (domain.Alert, bool, error) {
	if endpoint == "" {
		return s.UpsertAlert(ctx, x)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return x, false, err
	}
	defer tx.Rollback(ctx)
	var tenant string
	var enabled, notify bool
	err = tx.QueryRow(ctx, `SELECT tenant_id,enabled,notify_webhook FROM alert_rules WHERE id=$1 AND ($2='' OR tenant_id=$2) FOR SHARE`, x.RuleID, domain.TenantScope(ctx)).Scan(&tenant, &enabled, &notify)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, false, ErrAlertNotFound
	}
	if err != nil {
		return x, false, err
	}
	// A single short lock coordinates only queue admissions, never HTTP I/O.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(14001403)`); err != nil {
		return x, false, err
	}
	evidence, err := json.Marshal(x.Evidence)
	if err != nil {
		return x, false, err
	}
	var first, last time.Time
	var created bool
	err = tx.QueryRow(ctx, `INSERT INTO application_alerts (id,rule_id,rule_name,namespace,application_name,fingerprint,severity,status,summary,evidence,confidence,recommendation,first_seen_at,last_seen_at,tenant_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,'active',$8,$9,$10,$11,now(),now(),$12)
ON CONFLICT (rule_id,namespace,application_name,fingerprint) DO UPDATE SET rule_name=EXCLUDED.rule_name,severity=EXCLUDED.severity,summary=EXCLUDED.summary,evidence=EXCLUDED.evidence,confidence=EXCLUDED.confidence,recommendation=EXCLUDED.recommendation,last_seen_at=EXCLUDED.last_seen_at,status=CASE WHEN application_alerts.status='recovered' OR (application_alerts.status='silenced' AND application_alerts.silenced_until<now()) THEN 'active' ELSE application_alerts.status END,recovered_at=NULL
RETURNING id,first_seen_at,last_seen_at,status,first_seen_at=last_seen_at`, x.ID, x.RuleID, x.RuleName, x.Namespace, x.ApplicationName, x.Fingerprint, x.Severity, x.Summary, evidence, x.Confidence, x.Recommendation, tenant).Scan(&x.ID, &first, &last, &x.Status, &created)
	if err != nil {
		return x, false, err
	}
	x.FirstSeenAt, x.LastSeenAt = first.UTC().Format(time.RFC3339Nano), last.UTC().Format(time.RFC3339Nano)
	if enabled && notify && x.Status != "silenced" {
		hash := endpointHash(endpoint)
		var pending bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM alert_webhook_outbox WHERE alert_id=$1 AND endpoint_hash=$2 AND delivered_at IS NULL)`, x.ID, hash).Scan(&pending); err != nil {
			return x, false, err
		}
		if !pending {
			var count int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM alert_webhook_outbox WHERE delivered_at IS NULL LIMIT $1) q`, alertOutboxPendingLimit).Scan(&count); err != nil {
				return x, false, err
			}
			if count >= alertOutboxPendingLimit {
				return x, false, ErrAlertOutboxFull
			}
			id, e := outboxToken()
			if e != nil {
				return x, false, e
			}
			payload, e := json.Marshal(x)
			if e != nil {
				return x, false, e
			}
			if _, err = tx.Exec(ctx, `INSERT INTO alert_webhook_outbox(id,alert_id,endpoint,endpoint_hash,payload) VALUES($1,$2,$3,$4,$5)`, id, x.ID, endpoint, hash, payload); err != nil {
				return x, false, err
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return x, false, err
	}
	return x, created, nil
}

// A 60-second DB-clock lease outlives the worker's maximum 30-second request.
// All replicas together may hold at most four live leases. Expired owners are
// fenced by a random token in FinishAlertWebhook, including late HTTP success.
func (s *PostgresStore) ClaimAlertWebhook(ctx context.Context, endpoint string) (*AlertWebhookDelivery, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(14001404)`); err != nil {
		return nil, err
	}
	var active int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM alert_webhook_outbox WHERE delivered_at IS NULL AND lease_until>now()`).Scan(&active); err != nil {
		return nil, err
	}
	if active >= 4 {
		return nil, nil
	}
	token, err := outboxToken()
	if err != nil {
		return nil, err
	}
	var value AlertWebhookDelivery
	err = tx.QueryRow(ctx, `UPDATE alert_webhook_outbox o SET lease_token=$2,lease_until=now()+interval '60 seconds',attempts=o.attempts+1 FROM
(SELECT id FROM alert_webhook_outbox WHERE endpoint_hash=$1 AND delivered_at IS NULL AND next_attempt_at<=now() AND lease_until<=now() ORDER BY next_attempt_at,created_at,id FOR UPDATE SKIP LOCKED LIMIT 1) q
WHERE o.id=q.id RETURNING o.id,o.endpoint,o.payload,o.lease_token,o.attempts`, endpointHash(endpoint), token).Scan(&value.ID, &value.Endpoint, &value.Payload, &value.LeaseToken, &value.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &value, nil
}

func AlertWebhookBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 9 {
		return 15 * time.Minute
	}
	delay := 5 * time.Second * time.Duration(1<<uint(attempt-1))
	if delay > 15*time.Minute {
		delay = 15 * time.Minute
	}
	return delay
}

func (s *PostgresStore) FinishAlertWebhook(ctx context.Context, value AlertWebhookDelivery, success bool, failure string) error {
	if len(failure) > 512 {
		failure = failure[:512]
	}
	_, err := s.pool.Exec(ctx, `UPDATE alert_webhook_outbox SET delivered_at=CASE WHEN $3 THEN now() ELSE NULL END,next_attempt_at=now()+($4*interval '1 second'),lease_until='-infinity',lease_token='',last_error=$5 WHERE id=$1 AND lease_token=$2 AND lease_until>now() AND delivered_at IS NULL`, value.ID, value.LeaseToken, success, AlertWebhookBackoff(value.Attempts).Seconds(), failure)
	return err
}

// Only completed deliveries are pruned. Unsent observations survive recovery,
// account changes and process replacement; old endpoint records are not sent to
// a newly configured endpoint. Operators can inspect them by endpoint_hash.
func (s *PostgresStore) PruneAlertWebhooks(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM alert_webhook_outbox WHERE id IN
(SELECT id FROM alert_webhook_outbox WHERE delivered_at<now()-interval '7 days' ORDER BY delivered_at LIMIT 1000)`)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM alert_webhook_outbox WHERE id IN
(SELECT id FROM alert_webhook_outbox WHERE delivered_at IS NOT NULL ORDER BY delivered_at DESC,id DESC OFFSET 100000 LIMIT 1000)`)
	if err != nil {
		return fmt.Errorf("prune completed alert deliveries: %w", err)
	}
	return nil
}
