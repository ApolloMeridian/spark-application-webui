package oidcauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/localauth"
)

type Store interface {
	SaveOIDCLoginState(context.Context, domain.OIDCLoginState) error
	ConsumeOIDCLoginState(context.Context, string, time.Time) (domain.OIDCLoginState, error)
}

type Config struct {
	IssuerURL     string
	ClientID      string
	ClientSecret  string
	Scopes        []string
	GroupsClaim   string
	AdminGroups   []string
	UsernameClaim string
	AutoCreate    bool
	StateTTL      time.Duration
}

type Service struct {
	config     Config
	store      Store
	accounts   *localauth.Service
	oauth      oauth2.Config
	verifier   *oidc.IDTokenVerifier
	httpClient *http.Client
}

func New(ctx context.Context, cfg Config, store Store, accounts *localauth.Service, httpClient *http.Client) (*Service, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	discoveryContext := oidc.ClientContext(ctx, httpClient)
	provider, err := oidc.NewProvider(discoveryContext, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider: %w", err)
	}
	return &Service{
		config: cfg, store: store, accounts: accounts, httpClient: httpClient,
		oauth:    oauth2.Config{ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, Endpoint: provider.Endpoint(), Scopes: cfg.Scopes},
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
	}, nil
}

func (s *Service) Begin(ctx context.Context, redirectURL, returnURL string) (string, error) {
	state, err := randomValue(32)
	if err != nil {
		return "", err
	}
	nonce, err := randomValue(24)
	if err != nil {
		return "", err
	}
	codeVerifier := oauth2.GenerateVerifier()
	loginState := domain.OIDCLoginState{
		StateHash: hashValue(state), Nonce: nonce, CodeVerifier: codeVerifier,
		RedirectURL: redirectURL, ReturnURL: returnURL, ExpiresAt: time.Now().UTC().Add(s.config.StateTTL),
	}
	if err := s.store.SaveOIDCLoginState(ctx, loginState); err != nil {
		return "", err
	}
	config := s.oauth
	config.RedirectURL = redirectURL
	return config.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(codeVerifier)), nil
}

func (s *Service) Callback(ctx context.Context, stateValue, code string) (domain.User, string, time.Time, string, error) {
	if strings.TrimSpace(stateValue) == "" || strings.TrimSpace(code) == "" {
		return domain.User{}, "", time.Time{}, "", localauth.ErrUnauthorized
	}
	loginState, err := s.store.ConsumeOIDCLoginState(ctx, hashValue(stateValue), time.Now().UTC())
	if err != nil {
		return domain.User{}, "", time.Time{}, "", err
	}
	config := s.oauth
	config.RedirectURL = loginState.RedirectURL
	exchangeContext := oidc.ClientContext(ctx, s.httpClient)
	token, err := config.Exchange(exchangeContext, code, oauth2.VerifierOption(loginState.CodeVerifier))
	if err != nil {
		return domain.User{}, "", time.Time{}, "", fmt.Errorf("exchange OIDC authorization code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return domain.User{}, "", time.Time{}, "", fmt.Errorf("OIDC token response did not include an ID token")
	}
	idToken, err := s.verifier.Verify(exchangeContext, rawIDToken)
	if err != nil {
		return domain.User{}, "", time.Time{}, "", fmt.Errorf("verify OIDC ID token: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(loginState.Nonce)) != 1 {
		return domain.User{}, "", time.Time{}, "", fmt.Errorf("OIDC nonce mismatch")
	}
	claims := map[string]any{}
	if err := idToken.Claims(&claims); err != nil {
		return domain.User{}, "", time.Time{}, "", fmt.Errorf("decode OIDC claims: %w", err)
	}
	username := stringClaim(claims, s.config.UsernameClaim)
	if username == "" {
		return domain.User{}, "", time.Time{}, "", fmt.Errorf("OIDC username claim %q is missing", s.config.UsernameClaim)
	}
	groups := stringListClaim(claims, s.config.GroupsClaim)
	role := domain.RoleViewer
	if hasMappedGroup(groups, s.config.AdminGroups) {
		role = domain.RoleAdmin
	}
	user, sessionToken, expiresAt, err := s.accounts.LoginOIDC(ctx, localauth.OIDCIdentity{
		Issuer: s.config.IssuerURL, Subject: idToken.Subject, Username: username,
		DisplayName: firstNonEmpty(stringClaim(claims, "name"), username), Email: stringClaim(claims, "email"),
		Role: role, AutoCreate: s.config.AutoCreate,
	})
	if err != nil {
		return domain.User{}, "", time.Time{}, "", err
	}
	return user, sessionToken, expiresAt, loginState.ReturnURL, nil
}

func stringClaim(claims map[string]any, path string) string {
	value := claimValue(claims, path)
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func stringListClaim(claims map[string]any, path string) []string {
	value := claimValue(claims, path)
	result := make([]string, 0)
	switch items := value.(type) {
	case []any:
		for _, item := range items {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				result = append(result, strings.TrimSpace(text))
			}
		}
	case []string:
		for _, item := range items {
			if strings.TrimSpace(item) != "" {
				result = append(result, strings.TrimSpace(item))
			}
		}
	case string:
		for _, item := range strings.Split(items, ",") {
			if strings.TrimSpace(item) != "" {
				result = append(result, strings.TrimSpace(item))
			}
		}
	}
	return result
}

func claimValue(claims map[string]any, path string) any {
	var current any = claims
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[part]
	}
	return current
}

func hasMappedGroup(groups, configured []string) bool {
	for _, actual := range groups {
		actual = strings.Trim(strings.TrimSpace(actual), "/")
		for _, expected := range configured {
			if actual != "" && actual == strings.Trim(strings.TrimSpace(expected), "/") {
				return true
			}
		}
	}
	return false
}

func randomValue(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate OIDC random value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func hashValue(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
