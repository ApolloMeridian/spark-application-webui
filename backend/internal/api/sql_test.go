package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
)

func TestSQLGatewaySignsOnlyTheAuthenticatedTenant(t *testing.T) {
	key := strings.Repeat("k", 32)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Forwarded-Host") != "" {
			t.Error("forwarded client authentication or proxy headers")
		}
		payload, encoded, ok := strings.Cut(r.Header.Get("X-Spark-SQL-Identity"), ".")
		if !ok {
			t.Error("SQL identity is missing")
			return
		}
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write([]byte(payload))
		signature, _ := base64.RawURLEncoding.DecodeString(encoded)
		if !hmac.Equal(signature, mac.Sum(nil)) {
			t.Error("SQL identity signature is invalid")
		}
		raw, _ := base64.RawURLEncoding.DecodeString(payload)
		var claim map[string]any
		_ = json.Unmarshal(raw, &claim)
		uri := "/v1/table?name=polaris.events.purchases"
		if claim["id"] != "user" || claim["tenant"] != "tenant" || claim["aud"] != "spark-console-sql" ||
			claim["method"] != http.MethodGet || claim["target"] != uri || r.URL.RequestURI() != uri {
			t.Error("SQL identity does not match the rewritten request", claim)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	s := &Server{config: config.Config{SQLEnabled: true, SQLURL: target.URL, SQLSigningKey: key}}
	x := domain.User{ID: "user", TenantID: "tenant", Role: domain.RoleViewer, Permissions: &domain.TenantPermissions{SQLQuery: true}}
	r := httptest.NewRequest(http.MethodGet, "/v1/sql/table?name=polaris.events.purchases", nil)
	r = r.WithContext(context.WithValue(r.Context(), principalContextKey{}, x))
	for _, header := range []string{"Cookie", "Authorization", "X-Forwarded-Host", "X-Spark-SQL-Identity"} {
		r.Header.Set(header, "untrusted")
	}
	w := httptest.NewRecorder()
	s.sqlProxy(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.config.SQLEnabled = false
	s.sqlProxy(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatal("disabled SQL gateway accepted request", w.Code)
	}
}
