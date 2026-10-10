package storage

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrefixAuthorization(t *testing.T) {
	u := Identity{ID: "a", Tenant: "default", Role: "admin"}
	g := []Grant{{UserID: "a", Tenant: "default", Connection: "s3", Bucket: "bucket", Prefix: "team/a/", Actions: Actions}}
	for _, key := range []string{"team/a/file.yaml", "team/a/子目录/百分号%20.txt", "team/a//data.txt"} {
		if !Allows(u, g, "s3", "bucket", key, "read") {
			t.Fatal(key)
		}
	}
	for _, key := range []string{"team/ab/file", "team/a/../private", "team/a/./file", "/team/a/file", "team/a\\file", "team/a/\x00", "team/a/\xff"} {
		if Allows(u, g, "s3", "bucket", key, "read") {
			t.Fatal(key)
		}
	}
	u.Role = "viewer"
	if !Allows(u, g, "s3", "bucket", "team/a/file", "delete") {
		t.Fatal("explicitly authorized viewer write")
	}
	if Allows(u, g, "s3", "bucket", "team/ab/file", "delete") {
		t.Fatal("viewer prefix bypass")
	}
	if !Allows(u, g, "s3", "bucket", "team/a/file", "read") {
		t.Fatal("viewer read")
	}
	u.Tenant = "other"
	if Allows(u, g, "s3", "bucket", "team/a/file", "read") {
		t.Fatal("tenant leak")
	}
	u.Tenant = "default"
	u.Role = "admin"
	if Allows(u, nil, "s3", "bucket", "team/a/file", "read") {
		t.Fatal("admin bypass")
	}
}
func TestInternalIdentityAndEncryptedTickets(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	r := httptest.NewRequest("GET", "http://plugin/v1/objects?key=a%252Fb", nil)
	claims := Claims{ID: "user", Tenant: "default", Method: r.Method, Target: r.URL.RequestURI(), Audience: "spark-object-storage", Expires: time.Now().Add(time.Minute).Unix()}
	r.Header.Set("X-Spark-Storage-Identity", Sign(key, claims))
	if _, e := Verify(key, r); e != nil {
		t.Fatal(e)
	}
	r.URL.RawQuery = "key=a%2Fb"
	if _, e := Verify(key, r); e == nil {
		t.Fatal("target substitution")
	}
	r.URL.RawQuery = "key=a%252Fb"
	r.Method = "DELETE"
	if _, e := Verify(key, r); e == nil {
		t.Fatal("method substitution")
	}
	r.Method = "GET"
	claims.Expires = time.Now().Add(-time.Minute).Unix()
	r.Header.Set("X-Spark-Storage-Identity", Sign(key, claims))
	if _, e := Verify(key, r); e == nil {
		t.Fatal("expired identity")
	}
	u := Identity{ID: "user", Tenant: "default"}
	ticket := Ticket{User: u.ID, Tenant: u.Tenant, Kind: "upload", Upload: Upload{ID: "https://storage.googleapis.com/upload/secret-session"}, Expires: time.Now().Add(time.Hour).Unix()}
	sealed, e := seal(key, ticket)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(sealed, "secret-session") {
		t.Fatal("session leaked")
	}
	if _, e = unseal(key, sealed, u, "upload"); e != nil {
		t.Fatal(e)
	}
	u.ID = "other"
	if _, e = unseal(key, sealed, u, "upload"); e == nil {
		t.Fatal("ticket user substitution")
	}
}
func TestConfigurationFailClosed(t *testing.T) {
	c := Config{Connections: []Connection{{ID: "gcs", Name: "GCS", Provider: "gcs", Buckets: []string{"bucket"}}}}
	if e := c.Validate(); e == nil {
		t.Fatal("missing WIF")
	}
	c.Connections[0].GCS.AuthMode = "gkeWif"
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	if c.TextMaxBytes != TextLimit {
		t.Fatal("default text limit")
	}
	for _, size := range []int64{1024, 2097152, 8388608} {
		c.TextMaxBytes = size
		if err := c.Validate(); err != nil {
			t.Fatal("configurable text limit", err)
		}
	}
	for _, size := range []int64{-1, 1023, 8388609} {
		c.TextMaxBytes = size
		if err := c.Validate(); err == nil {
			t.Fatal("unsafe text limit accepted")
		}
	}
	c.TextMaxBytes = TextLimit
	c.PartSize = 5<<20 | 1
	if e := c.Validate(); e == nil {
		t.Fatal("invalid chunk alignment")
	}
}
