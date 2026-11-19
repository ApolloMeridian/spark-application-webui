package sqlquery

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type testTransport func(*http.Request) (*http.Response, error)

func (transport testTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return transport(r)
}

func TestKyuubiAuthCheckDistinguishesEndpointAndAuthenticationErrors(t *testing.T) {
	for _, tc := range []struct {
		status  int
		message string
	}{
		{http.StatusUnauthorized, ""},
		{http.StatusForbidden, ""},
		{http.StatusOK, "accepted invalid credentials"},
		{http.StatusNotFound, "returned HTTP 404"},
		{http.StatusBadGateway, "returned HTTP 502"},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			transport := testTransport(func(r *http.Request) (*http.Response, error) {
				_, password, ok := r.BasicAuth()
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sessions/count" || !ok || password != "invalid" {
					t.Error("unexpected Kyuubi authentication probe")
				}
				return &http.Response{StatusCode: tc.status, Body: http.NoBody}, nil
			})
			client := clients{http: &http.Client{Transport: transport}, cfg: Config{Kyuubi: "http://kyuubi.test"}}
			err := client.checkKyuubiAuth(t.Context(), Profile{KyuubiUser: "test-user"})
			if tc.message == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("expected %q, got %v", tc.message, err)
			}
		})
	}
}

func TestEmptyChildNamespacesSerializeAsJSONArray(t *testing.T) {
	transport := testTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("parent") != "analytics" {
			t.Error("expected analytics parent namespace")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"namespaces":[]}`)),
		}, nil
	})
	catalog := catalogSession{
		client: &clients{http: &http.Client{Transport: transport}, cfg: Config{Polaris: "http://polaris.test"}},
		prefix: "warehouse",
	}
	namespaces, err := catalog.namespaces(t.Context(), "polaris.analytics")
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(namespaces)
	if err != nil || string(body) != "[]" {
		t.Fatalf("expected an empty JSON array, got %s (%v)", body, err)
	}
}
