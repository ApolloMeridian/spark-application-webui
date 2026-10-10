package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
)

func TestStorageGatewaySignsIdentityAndStreams(t *testing.T) {
	key := strings.Repeat("k", 32)
	calls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Spark-Other") != "" {
			t.Error("forwarded untrusted credentials")
		}
		parts := strings.Split(r.Header.Get("X-Spark-Storage-Identity"), ".")
		if len(parts) != 2 {
			t.Error("missing signature")
			return
		}
		sig, _ := base64.RawURLEncoding.DecodeString(parts[1])
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write([]byte(parts[0]))
		if !hmac.Equal(sig, mac.Sum(nil)) {
			t.Error("invalid signature")
		}
		raw, _ := base64.RawURLEncoding.DecodeString(parts[0])
		var claims map[string]any
		json.Unmarshal(raw, &claims)
		if claims["id"] != "user" || claims["tenant"] != "tenant" || claims["target"] != r.URL.RequestURI() || claims["method"] != r.Method {
			t.Error(claims)
		}
		if r.URL.Path != "/v1/uploads/part" || r.URL.Query().Get("key") != "a%2Fb" {
			t.Error(r.URL.String())
		}
		io.Copy(w, r.Body)
	}))
	defer target.Close()
	server := &Server{config: config.Config{ObjectStorageEnabled: true, ObjectStorageURL: target.URL, ObjectStorageSigningKey: key}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := httptest.NewRequest("PUT", "/v1/object-storage/uploads/part?key=a%252Fb", strings.NewReader("streamed"))
	r = r.WithContext(context.WithValue(r.Context(), principalContextKey{}, domain.User{ID: "user", TenantID: "tenant"}))
	r.Header.Set("Cookie", "secret")
	r.Header.Set("Authorization", "forged")
	r.Header.Set("X-Spark-Storage-Identity", "forged")
	r.Header.Set("X-Spark-Other", "forged")
	w := httptest.NewRecorder()
	server.objectStorageProxy(w, r)
	if w.Code != 200 || w.Body.String() != "streamed" || calls != 1 {
		t.Fatal(w.Code, w.Body.String(), calls)
	}
	server.config.ObjectStorageEnabled = false
	w = httptest.NewRecorder()
	server.objectStorageProxy(w, r)
	if w.Code != 404 || calls != 1 {
		t.Fatal("disabled gateway forwarded", w.Code, calls)
	}
}
