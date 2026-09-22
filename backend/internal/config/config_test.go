package config

import (
	"strings"
	"testing"
)

func setRequiredTestEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_USERNAME", "spark")
	t.Setenv("DATABASE_PASSWORD", "secret")
	t.Setenv("KUBERNETES_API_URL", "https://kubernetes.example.test")
	t.Setenv("AUTH_INITIAL_ADMIN_PASSWORD", "")
}

func TestOIDCModeDoesNotRequireInitialAdministrator(t *testing.T) {
	setRequiredTestEnvironment(t)
	t.Setenv("AUTH_MODE", "oidc")
	t.Setenv("OIDC_ENABLED", "true")
	t.Setenv("OIDC_ISSUER_URL", "https://keycloak.example.test/realms/platform")
	t.Setenv("OIDC_CLIENT_ID", "spark-control-center")
	t.Setenv("OIDC_CLIENT_SECRET", "client-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("OIDC mode should load without initial administrator credentials: %v", err)
	}
	if cfg.AuthMode != "oidc" || !cfg.OIDCEnabled {
		t.Fatalf("unexpected auth configuration: mode=%q enabled=%v", cfg.AuthMode, cfg.OIDCEnabled)
	}
}

func TestLocalModeStillRequiresInitialAdministrator(t *testing.T) {
	setRequiredTestEnvironment(t)
	t.Setenv("AUTH_MODE", "local")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "AUTH_INITIAL_ADMIN") {
		t.Fatalf("expected missing local administrator error, got %v", err)
	}
}
