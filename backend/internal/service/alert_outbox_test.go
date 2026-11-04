package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/store"
)

type fakeDurableAlerts struct {
	fakeAlertStore
	mu    sync.Mutex
	queue []store.AlertWebhookDelivery
}

func (f *fakeDurableAlerts) UpsertAlertWithWebhook(ctx context.Context, a domain.Alert, endpoint string) (domain.Alert, bool, error) {
	a, created, err := f.UpsertAlert(ctx, a)
	if err != nil {
		return a, created, err
	}
	payload, _ := json.Marshal(a)
	f.mu.Lock()
	f.queue = append(f.queue, store.AlertWebhookDelivery{ID: fmt.Sprint(f.upserts), Endpoint: endpoint, Payload: payload, Attempts: 1})
	f.mu.Unlock()
	return a, created, nil
}
func (f *fakeDurableAlerts) ClaimAlertWebhook(context.Context, string) (*store.AlertWebhookDelivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queue) == 0 {
		return nil, nil
	}
	x := f.queue[0]
	f.queue = f.queue[1:]
	return &x, nil
}
func (f *fakeDurableAlerts) FinishAlertWebhook(context.Context, store.AlertWebhookDelivery, bool, string) error {
	return nil
}
func (f *fakeDurableAlerts) PruneAlertWebhooks(context.Context) error { return nil }

func outboxDB(t *testing.T) (*store.PostgresStore, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("outbox_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	pg, err := store.Open(ctx, u.String(), 8)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pg.Close()
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE")
		admin.Close()
	})
	return pg, pool
}

func outboxAlert(t *testing.T, pg *store.PostgresStore, ctx context.Context, id string) domain.Alert {
	t.Helper()
	rule, err := pg.SaveAlertRule(ctx, domain.AlertRule{Name: "Outbox", Type: "failure", Severity: "error", Enabled: true, NotifyWebhook: true, CreatedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return domain.Alert{ID: id, RuleID: rule.ID, RuleName: rule.Name, Namespace: "spark", ApplicationName: id, Fingerprint: id, Severity: "error", Summary: "failed", Evidence: []string{"state=FAILED"}, Confidence: "high"}
}

func TestAlertOutboxRetriesStableDeliveryAcrossReplicaReplacement(t *testing.T) {
	pg, pool := outboxDB(t)
	ctx := domain.WithTenant(context.Background(), "default")
	var requests atomic.Int32
	var mu sync.Mutex
	var ids []string
	var payloads [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		if r.Header.Get("Idempotency-Key") != r.Header.Get("X-Spark-Delivery-ID") || r.Header.Get("Idempotency-Key") == "" {
			t.Error("missing stable delivery key")
		}
		mu.Lock()
		ids = append(ids, r.Header.Get("Idempotency-Key"))
		payloads = append(payloads, payload)
		mu.Unlock()
		if requests.Add(1) == 1 {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	a := outboxAlert(t, pg, ctx, "alert-retry")
	saved, _, err := pg.UpsertAlertWithWebhook(ctx, a, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if saved.FirstSeenAt == "" || saved.LastSeenAt == "" {
		t.Fatal("missing persisted timestamps")
	}
	newReplica := func() *Service {
		return New(config.Config{AlertWebhookURL: server.URL, AlertWebhookTimeout: time.Second}, nil, nil, pg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}
	if worked, e := newReplica().DeliverAlertWebhook(ctx); e != nil || !worked {
		t.Fatal(worked, e)
	}
	if worked, e := newReplica().DeliverAlertWebhook(ctx); e != nil || worked {
		t.Fatal("backoff not respected", worked, e)
	}
	// New evaluations coalesce behind the immutable failed delivery, even if the
	// application has already recovered before the receiver becomes available.
	a.Summary = "updated observation"
	if _, _, err = pg.UpsertAlertWithWebhook(ctx, a, server.URL); err != nil {
		t.Fatal(err)
	}
	if err = pg.ResolveInactiveAlerts(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE alert_webhook_outbox SET next_attempt_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if worked, e := newReplica().DeliverAlertWebhook(ctx); e != nil || !worked {
		t.Fatal(worked, e)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ids) != 2 || ids[0] != ids[1] || string(payloads[0]) != string(payloads[1]) {
		t.Fatal("delivery changed across retry", ids)
	}
	var pending int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM alert_webhook_outbox WHERE delivered_at IS NULL`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal(pending, err)
	}
}

func TestAlertOutboxClaimsFenceCrashedOwnersAndBoundAllReplicas(t *testing.T) {
	pg, pool := outboxDB(t)
	ctx := domain.WithTenant(context.Background(), "default")
	endpoint := "https://receiver.example.test/"
	for i := 0; i < 5; i++ {
		a := outboxAlert(t, pg, ctx, fmt.Sprint("claim-", i))
		if _, _, err := pg.UpsertAlertWithWebhook(ctx, a, endpoint); err != nil {
			t.Fatal(err)
		}
	}
	claims := make(chan *store.AlertWebhookDelivery, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, e := pg.ClaimAlertWebhook(ctx, endpoint)
			if e != nil {
				t.Error(e)
			}
			if d != nil {
				claims <- d
			}
		}()
	}
	wg.Wait()
	close(claims)
	var values []*store.AlertWebhookDelivery
	for c := range claims {
		values = append(values, c)
	}
	if len(values) != 4 {
		t.Fatal("global concurrency exceeded", len(values))
	}
	old := values[0]
	if _, err := pool.Exec(ctx, `UPDATE alert_webhook_outbox SET lease_until=now()-interval '1 second' WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	// Fence other ready rows so takeover is deterministic.
	if _, err := pool.Exec(ctx, `UPDATE alert_webhook_outbox SET next_attempt_at=now()+interval '1 hour' WHERE id<>$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	takeover, err := pg.ClaimAlertWebhook(ctx, endpoint)
	if err != nil || takeover == nil || takeover.ID != old.ID || takeover.LeaseToken == old.LeaseToken {
		t.Fatal(takeover, err)
	}
	if err = pg.FinishAlertWebhook(ctx, *old, true, ""); err != nil {
		t.Fatal(err)
	}
	var delivered bool
	if err = pool.QueryRow(ctx, `SELECT delivered_at IS NOT NULL FROM alert_webhook_outbox WHERE id=$1`, old.ID).Scan(&delivered); err != nil || delivered {
		t.Fatal("old owner acknowledged new lease", delivered, err)
	}
	if err = pg.FinishAlertWebhook(ctx, *takeover, true, ""); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT delivered_at IS NOT NULL FROM alert_webhook_outbox WHERE id=$1`, old.ID).Scan(&delivered); err != nil || !delivered {
		t.Fatal(delivered, err)
	}
	if d, e := pg.ClaimAlertWebhook(ctx, "https://different.example.test/"); e != nil || d != nil {
		t.Fatal("delivered to changed endpoint", d, e)
	}
}

func TestAlertOutboxBackpressureRollsBackNewObservationAndRetainsUnsent(t *testing.T) {
	pg, pool := outboxDB(t)
	ctx := domain.WithTenant(context.Background(), "default")
	first := outboxAlert(t, pg, ctx, "capacity-seed")
	if _, _, err := pg.UpsertAlertWithWebhook(ctx, first, "https://receiver.example.test/"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO alert_webhook_outbox(id,alert_id,endpoint,endpoint_hash,payload) SELECT 'capacity-'||i,$1,'https://receiver.example.test/','hash-'||i,'{}'::jsonb FROM generate_series(1,10000) i`, first.ID); err != nil {
		t.Fatal(err)
	}
	blocked := outboxAlert(t, pg, ctx, "not-committed")
	if _, _, err := pg.UpsertAlertWithWebhook(ctx, blocked, "https://receiver.example.test/"); err != store.ErrAlertOutboxFull {
		t.Fatal("missing explicit backpressure", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM application_alerts WHERE id=$1`, blocked.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("alert and intent not atomic", count, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE alert_webhook_outbox SET created_at=now()-interval '30 days'`); err != nil {
		t.Fatal(err)
	}
	if err := pg.PruneAlertWebhooks(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM alert_webhook_outbox`).Scan(&count); err != nil || count != 10001 {
		t.Fatal("unsent observations were removed", count, err)
	}
}

func TestAlertOutboxBackoffIsBounded(t *testing.T) {
	if store.AlertWebhookBackoff(1) != 5*time.Second || store.AlertWebhookBackoff(100000) != 15*time.Minute {
		t.Fatal("invalid retry backoff")
	}
}
