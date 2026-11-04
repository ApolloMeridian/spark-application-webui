package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

// Claim using the database clock, without holding a transaction or connection
// across RPCs. Work is cancelled before the lease expires; crashed replicas
// become eligible for takeover after 90 seconds.
func (s *PostgresStore) RunAlertEvaluation(ctx context.Context, interval time.Duration, evaluate func(context.Context) error) error {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	owner := hex.EncodeToString(random[:])
	var claimed string
	err := s.pool.QueryRow(ctx, `UPDATE alert_evaluation_lease SET owner=$1,expires_at=now()+interval '90 seconds' WHERE id AND expires_at<=now() AND last_completed_at<=now()-($2*interval '1 second') RETURNING owner`, owner, interval.Seconds()).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	work, cancel := context.WithTimeout(ctx, 60*time.Second)
	err = evaluate(work)
	cancel()
	cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	_, releaseErr := s.pool.Exec(cleanup, `UPDATE alert_evaluation_lease SET expires_at=now(),last_completed_at=CASE WHEN $2 THEN now() ELSE last_completed_at END WHERE id AND owner=$1`, owner, err == nil)
	return errors.Join(err, releaseErr)
}
