package workflow

import (
	"context"
	"errors"
	"spark-control-center/backend/internal/localauth"
	"testing"
	"time"
)

func TestAuthenticationBudgetIsSharedAndExpires(t *testing.T) {
	s, _, _, _ := fixture(t)
	a := localauth.New(s.store, time.Hour, 4)
	b := localauth.New(s.store, time.Hour, 4)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		v := a
		if i%2 == 0 {
			v = b
		}
		if err := v.CheckAttempt(ctx, "127.0.0.1:99", "Alice"); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.CheckAttempt(ctx, "127.0.0.2:100", "alice"); !errors.Is(err, localauth.ErrRateLimited) {
		t.Fatal("account budget bypassed across replicas/source", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE authentication_rate_windows SET window_start=now()-interval '2 minutes'`); err != nil {
		t.Fatal(err)
	}
	if err := b.CheckAttempt(ctx, "127.0.0.1:99", "alice"); err != nil {
		t.Fatal("budget did not reset", err)
	}
	var plain int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM authentication_rate_windows WHERE key LIKE '%alice%' OR key LIKE '%127.0.0.1%'`).Scan(&plain); err != nil || plain != 0 {
		t.Fatal("raw identity persisted", err)
	}
}
