package localauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"strings"
)

var ErrRateLimited = errors.New("too many authentication attempts")

func (s *Service) CheckAttempt(ctx context.Context, remote, account string) error {
	store, ok := s.store.(interface {
		AllowAuthenticationAttempt(context.Context, string, string) (bool, error)
	})
	if !ok {
		return nil
	}
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	h := sha256.Sum256([]byte(host))
	a := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(account))))
	allowed, err := store.AllowAuthenticationAttempt(ctx, hex.EncodeToString(h[:]), hex.EncodeToString(a[:]))
	if err != nil {
		return err
	}
	if !allowed {
		return ErrRateLimited
	}
	return nil
}
