package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"spark-control-center/backend/internal/store"
)

func (s *Service) startAlertWebhookWorkers(ctx context.Context) {
	outbox, ok := s.audits.(store.AlertOutboxStore)
	if !ok || s.config.AlertWebhookURL == "" {
		return
	}
	s.webhookOnce.Do(func() {
		for i := 0; i < 4; i++ {
			go func() {
				for ctx.Err() == nil {
					worked, err := s.DeliverAlertWebhook(ctx)
					if err != nil && ctx.Err() == nil {
						s.logger.Warn("alert outbox delivery failed", "error", err)
					}
					if !worked || err != nil {
						select {
						case <-ctx.Done():
							return
						case <-time.After(time.Second):
						}
					}
				}
			}()
		}
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				step, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := outbox.PruneAlertWebhooks(step)
				cancel()
				if err != nil && ctx.Err() == nil {
					s.logger.Warn("alert outbox retention failed", "error", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	})
}

// DeliverAlertWebhook processes one persisted intent. A transport timeout or a
// lost DB acknowledgement may duplicate delivery: receivers deduplicate using
// the stable ID header. A failed attempt leaves the same immutable record queued.
// The HTTP request never holds a PostgreSQL transaction or connection.
func (s *Service) DeliverAlertWebhook(ctx context.Context) (bool, error) {
	outbox, ok := s.audits.(store.AlertOutboxStore)
	if !ok || s.config.AlertWebhookURL == "" {
		return false, nil
	}
	claim, stop := context.WithTimeout(ctx, 3*time.Second)
	value, err := outbox.ClaimAlertWebhook(claim, s.config.AlertWebhookURL)
	stop()
	if err != nil || value == nil {
		return false, err
	}
	timeout := s.config.AlertWebhookTimeout
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	requestContext, cancel := context.WithTimeout(ctx, timeout)
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, value.Endpoint, bytes.NewReader(value.Payload))
	status, success := 0, false
	failure := "HTTP request construction failed"
	if err == nil {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Spark-Delivery-ID", value.ID)
		request.Header.Set("Idempotency-Key", value.ID)
		response, sendErr := s.webhookClient.Do(request)
		failure = "HTTP transport failed"
		if sendErr == nil {
			status = response.StatusCode
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
			_ = response.Body.Close()
			success = status >= 200 && status < 300
			failure = fmt.Sprintf("HTTP status %d", status)
		}
	}
	cancel()
	if success {
		failure = ""
	}
	// Cleanup gets its own short deadline even during shutdown; a failed cleanup
	// remains recoverable by lease expiry. Do not log endpoint URLs or payloads.
	finish, finishCancel := context.WithTimeout(context.Background(), 3*time.Second)
	err = outbox.FinishAlertWebhook(finish, *value, success, failure)
	finishCancel()
	if !success {
		s.logger.Warn("alert webhook queued for retry", "delivery_id", value.ID, "attempt", value.Attempts, "status", status)
	}
	return true, err
}

// DrainAlertWebhooks is useful for deterministic local/integration checks. The
// production evaluator uses long-lived workers, not one goroutine per event.
func (s *Service) DrainAlertWebhooks(ctx context.Context) error {
	var workers sync.WaitGroup
	var mu sync.Mutex
	var first error
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ctx.Err() == nil {
				worked, err := s.DeliverAlertWebhook(ctx)
				if err != nil {
					mu.Lock()
					if first == nil {
						first = err
					}
					mu.Unlock()
					return
				}
				if !worked {
					return
				}
			}
		}()
	}
	workers.Wait()
	return first
}
