package sqlquery

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIdentityBindsAudienceMethodAndRequestURI(t *testing.T) {
	now := time.Now()
	key := "fixture signing secret"
	claim := actor{ID: "user", Tenant: "tenant", Method: "GET", Target: "/v1/table?name=polaris.demo.events", Audience: "spark-console-sql", Expires: now.Add(30 * time.Second).Unix()}
	sign := func(x actor) string {
		raw, _ := json.Marshal(x)
		payload := base64.RawURLEncoding.EncodeToString(raw)
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write([]byte(payload))
		return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}
	for _, tc := range []struct {
		method, path string
		valid        bool
	}{
		{"GET", claim.Target, true},
		{"POST", claim.Target, false},
		{"GET", "/v1/table?name=polaris.demo.private", false},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r.Header.Set("X-Spark-SQL-Identity", sign(claim))
		if _, ok := verify(r, key, now); ok != tc.valid {
			t.Fatalf("verify %s %s: %t", tc.method, tc.path, ok)
		}
	}
	r := httptest.NewRequest("GET", claim.Target, nil)
	r.Header.Set("X-Spark-SQL-Identity", sign(claim))
	if _, ok := verify(r, key, now.Add(time.Minute)); ok {
		t.Fatal("expired token accepted")
	}
	claim.Audience = "spark-object-storage"
	r.Header.Set("X-Spark-SQL-Identity", sign(claim))
	if _, ok := verify(r, key, now); ok {
		t.Fatal("storage token accepted in SQL gateway")
	}
}

func TestCSVWorksheetSafety(t *testing.T) {
	for input, wanted := range map[string]string{
		`"=1+1"`:        "'=1+1",
		`"  @sum(1,2)"`: "'  @sum(1,2)",
		`"text"`:        "text",
		"null":          "",
		"8192":          "8192",
	} {
		if got := csvCell(json.RawMessage(input)); got != wanted {
			t.Errorf("csvCell(%s) = %q, want %q", input, got, wanted)
		}
	}
}
