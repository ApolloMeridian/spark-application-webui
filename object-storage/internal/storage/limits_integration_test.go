package storage

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func quotaConfig(t *testing.T) Config {
	t.Helper()
	c := Config{Concurrent: 1, RequestsPerMinute: 600, Connections: []Connection{{ID: "demo", Name: "Fixture", Provider: "gcs", Buckets: []string{"bucket"}}}}
	c.Connections[0].GCS.AuthMode = "gkeWif"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	return c
}

func quotaUser() Identity {
	return Identity{ID: "admin", Tenant: "default", Username: "admin", Role: "admin"}
}

func TestPostgresGlobalBudgetsSurviveReplicasAndExpiry(t *testing.T) {
	s := dbFixture(t)
	ctx := context.Background()
	c := quotaConfig(t)
	a, err := s.reserveBudget(ctx, "replica-a", c, quotaUser(), "transfer", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.reserveBudget(ctx, "replica-b", c, quotaUser(), "transfer", ""); err == nil {
		t.Fatal("replica B bypassed global concurrency")
	}
	if s.Pool.Stat().AcquiredConns() != 0 {
		t.Fatal("lease retained a pool connection")
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE object_storage_budget_leases SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	b, err := s.reserveBudget(ctx, "replica-b-restarted", c, quotaUser(), "transfer", "")
	if err != nil {
		t.Fatal("expired replica prevented takeover", err)
	}
	if err = s.renewBudget(ctx, a); err == nil {
		t.Fatal("expired owner resurrected its lease")
	}
	if err = s.releaseBudget(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err = s.reserveBudget(ctx, "replica-c", c, quotaUser(), "transfer", ""); err == nil {
		t.Fatal("old cleanup released new owner's lease")
	}
	if err = s.releaseBudget(ctx, b); err != nil {
		t.Fatal(err)
	}

	// Global request admission stays bounded even with many independent nodes.
	var wg sync.WaitGroup
	leases := make(chan budgetLease, 20)
	errorsOut := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x, e := s.reserveBudget(ctx, newID(), c, quotaUser(), "request", "")
			if e == nil {
				leases <- x
			} else {
				var f *Failure
				if !errors.As(e, &f) || f.Status != 429 {
					errorsOut <- e
				}
			}
		}()
	}
	wg.Wait()
	close(leases)
	close(errorsOut)
	if len(leases) != 4 {
		t.Fatalf("global request budget admitted %d, expected 4", len(leases))
	}
	for err := range errorsOut {
		t.Error(err)
	}
	for l := range leases {
		if err = s.releaseBudget(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStorageRateLimitPersistsAcrossServersAndRestart(t *testing.T) {
	store := dbFixture(t)
	c := quotaConfig(t)
	c.RequestsPerMinute = 2
	// Keep this fixed database-minute-window test away from the boundary.
	var seconds float64
	if err := store.Pool.QueryRow(context.Background(), `SELECT date_part('second',clock_timestamp())`).Scan(&seconds); err != nil {
		t.Fatal(err)
	}
	if seconds > 58.5 {
		time.Sleep(time.Duration((60.1 - seconds) * float64(time.Second)))
	}
	key := []byte(strings.Repeat("k", 32))
	for i, want := range []int{200, 200, 429} {
		s := NewServer(c, store, key, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		w := signedBudgetRequest(s, key, context.Background(), "GET", "/v1/connections", nil)
		if w.Code != want {
			t.Fatalf("server/restart %d: status=%d want=%d body=%s", i, w.Code, want, w.Body.String())
		}
	}
}

func TestStorageRollingConfigUsesStrictestLiveBudget(t *testing.T) {
	s := dbFixture(t)
	ctx := context.Background()
	c := quotaConfig(t)
	c.Concurrent = 2
	first, err := s.reserveBudget(ctx, "old", c, quotaUser(), "transfer", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.reserveBudget(ctx, "old", c, quotaUser(), "transfer", "")
	if err != nil {
		t.Fatal(err)
	}
	strict := c
	strict.Concurrent = 1
	if _, err = s.reserveBudget(ctx, "new", strict, quotaUser(), "transfer", ""); err == nil {
		t.Fatal("lower live policy admitted excess work")
	}
	if err = s.releaseBudget(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err = s.reserveBudget(ctx, "old", c, quotaUser(), "transfer", ""); err == nil {
		t.Fatal("old node ignored newer lower policy")
	}
	if err = s.releaseBudget(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, err = s.reserveBudget(ctx, "old", c, quotaUser(), "transfer", ""); err != nil {
		t.Fatal(err)
	}
}

type waitingDownload struct {
	fakeProvider
	entered chan struct{}
	proceed chan struct{}
}

func (p *waitingDownload) Open(ctx context.Context, b, k, v string) (io.ReadCloser, error) {
	p.entered <- struct{}{}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.proceed:
		return io.NopCloser(strings.NewReader("data")), nil
	}
}

func signedBudgetRequest(s *Server, key []byte, ctx context.Context, method, target string, body io.Reader) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, body).WithContext(ctx)
	r.Header.Set("X-Spark-Storage-Identity", Sign(key, Claims{ID: "admin", Tenant: "default", Method: method, Target: r.URL.RequestURI(), Audience: "spark-object-storage", Expires: time.Now().Add(time.Minute).Unix()}))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestStorageTwoServersTransferCancellationAndLeaseLoss(t *testing.T) {
	for _, loss := range []bool{false, true} {
		t.Run(map[bool]string{false: "client_cancel", true: "lease_loss"}[loss], func(t *testing.T) {
			store := dbFixture(t)
			c := quotaConfig(t)
			key := []byte(strings.Repeat("k", 32))
			if err := store.Bootstrap(context.Background(), []Bootstrap{{Username: "admin", Tenant: "default", Connection: "demo", Bucket: "bucket", Prefix: "", Actions: Actions}}); err != nil {
				t.Fatal(err)
			}
			p := &waitingDownload{fakeProvider: fakeProvider{Data: map[string]string{"file.txt": "data"}, Version: map[string]int{"file.txt": 1}}, entered: make(chan struct{}, 2), proceed: make(chan struct{})}
			a := NewServer(c, store, key, map[string]Provider{"demo": p}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			b := NewServer(c, store, key, map[string]Provider{"demo": p}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan *httptest.ResponseRecorder, 1)
			url := "/v1/download?connection=demo&bucket=bucket&key=file.txt"
			go func() { done <- signedBudgetRequest(a, key, ctx, "GET", url, nil) }()
			select {
			case <-p.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("provider was not called")
			}
			if w := signedBudgetRequest(b, key, context.Background(), "GET", url, nil); w.Code != 429 {
				t.Fatal("second server bypassed transfer cap", w.Code, w.Body.String())
			}
			if store.Pool.Stat().AcquiredConns() != 0 {
				t.Fatal("provider RPC held a database connection")
			}
			if loss {
				if _, err := store.Pool.Exec(context.Background(), `DELETE FROM object_storage_budget_leases WHERE node_id=$1 AND kind='transfer'`, a.nodeID); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			select {
			case <-done:
			case <-time.After(8 * time.Second):
				t.Fatal("lost/cancelled lease did not cancel SDK request")
			}
			var count int
			if err := store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM object_storage_budget_leases`).Scan(&count); err != nil || count != 0 {
				t.Fatal("cancel/error leaked budget", count, err)
			}
			close(p.proceed)
			if w := signedBudgetRequest(b, key, context.Background(), "GET", url, nil); w.Code != 200 {
				t.Fatal("released budget was not reusable", w.Code, w.Body.String())
			}
		})
	}
}

func TestMultipartUploadLockIsDurableLeaseNotPinnedConnection(t *testing.T) {
	s := dbFixture(t)
	ctx := context.Background()
	c := quotaConfig(t)
	user := quotaUser()
	id, err := s.Begin(ctx, user, "UPLOAD", "demo", "bucket", "file", "")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.reserveBudget(ctx, "a", c, user, "upload", id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.reserveBudget(ctx, "b", c, user, "upload", id); err == nil {
		t.Fatal("upload mutex bypassed")
	}
	if s.Pool.Stat().AcquiredConns() != 0 {
		t.Fatal("upload mutex pinned database connection")
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE object_storage_budget_leases SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	b, err := s.reserveBudget(ctx, "b", c, user, "upload", id)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.renewBudget(ctx, a); err == nil {
		t.Fatal("abandoned upload mutex resurrected")
	}
	if err = s.releaseBudget(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(ctx, id, "cancelled"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.reserveBudget(ctx, "c", c, user, "upload", id); err == nil {
		t.Fatal("cancelled upload resumed")
	}
}
