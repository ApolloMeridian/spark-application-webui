package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"spark-control-center/backend/internal/localauth"
	"sync"
	"time"
)

type attemptWindow struct {
	count int
	until time.Time
}
type authGate struct {
	mu       sync.Mutex
	attempts map[string]attemptWindow
	active   int
}

func (g *authGate) allow(remote, account string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.attempts == nil {
		g.attempts = map[string]attemptWindow{}
	}
	now := time.Now()
	for k, v := range g.attempts {
		if !now.Before(v.until) {
			delete(g.attempts, k)
		}
	}
	ip, _, err := net.SplitHostPort(remote)
	if err != nil {
		ip = remote
	}
	sum := sha256.Sum256([]byte(account))
	keys := []string{"ip:" + ip, "account:" + hex.EncodeToString(sum[:])}
	limits := []int{120, 10}
	if len(g.attempts) > 10000 {
		return false
	}
	for i, key := range keys {
		if v := g.attempts[key]; v.count >= limits[i] && now.Before(v.until) {
			return false
		}
	}
	for _, key := range keys {
		v := g.attempts[key]
		if !now.Before(v.until) {
			v = attemptWindow{until: now.Add(time.Minute)}
		}
		v.count++
		g.attempts[key] = v
	}
	return true
}
func (g *authGate) acquire() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active >= 4 {
		return false
	}
	g.active++
	return true
}
func (g *authGate) release() { g.mu.Lock(); g.active--; g.mu.Unlock() }

func (s *Server) checkSharedAuthBudget(w http.ResponseWriter, r *http.Request, account string) bool {
	if err := s.auth.CheckAttempt(r.Context(), r.RemoteAddr, account); err != nil {
		if errors.Is(err, localauth.ErrRateLimited) {
			w.Header().Set("Retry-After", "60")
			s.writeError(w, 429, err)
		} else {
			s.logger.Warn("authentication budget unavailable")
			s.writeError(w, 503, fmt.Errorf("authentication temporarily unavailable"))
		}
		return false
	}
	return true
}
