package storage

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCrossTenantPolicyManagementPreservesDataBoundaries(t *testing.T) {
	store := dbFixture(t)
	ctx := context.Background()
	_, err := store.Pool.Exec(ctx, `INSERT INTO tenants VALUES('admin','Administrators','{"objectStorage":true}'),('viewer','Viewers','{"objectStorage":false}'); UPDATE local_users SET tenant_id='admin' WHERE id='admin'; UPDATE local_users SET tenant_id='viewer' WHERE id='viewer'; INSERT INTO local_users(id,tenant_id,role,username) VALUES('limited','admin','admin','limited'),('disabled','default','viewer','disabled'); UPDATE local_users SET disabled=true WHERE id='disabled'`)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Bootstrap(ctx, []Bootstrap{
		{Username: "admin", Tenant: "admin", Connection: "demo", Bucket: "bucket", Prefix: "team/a/", Actions: Actions},
		{Username: "limited", Tenant: "admin", Connection: "demo", Bucket: "bucket", Prefix: "team/a/", Actions: []string{"list", "read"}},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Connections: []Connection{{ID: "demo", Name: "Demo", Provider: "gcs", Buckets: []string{"bucket"}}}}
	cfg.Connections[0].GCS.AuthMode = "gkeWif"
	if err = cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{Data: map[string]string{"team/a/file.txt": "hello", "team/ab/private.txt": "secret"}, Version: map[string]int{"team/a/file.txt": 1}}
	key := []byte(strings.Repeat("s", 32))
	handler := NewServer(cfg, store, key, map[string]Provider{"demo": provider}, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
	send := func(user, tenant, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-Spark-Storage-Identity", Sign(key, Claims{ID: user, Tenant: tenant, Method: method, Target: r.URL.RequestURI(), Audience: "spark-object-storage", Expires: time.Now().Add(time.Minute).Unix()}))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	assertStatus := func(w *httptest.ResponseRecorder, want int) {
		t.Helper()
		if w.Code != want {
			t.Fatalf("want %d, got %d: %s", want, w.Code, w.Body.String())
		}
	}
	policies := func(user string) struct {
		Grants []Grant
		Users  []PolicyUser
	} { w := send(user, "admin", "GET", "/v1/policies", ""); assertStatus(w, 200); var p struct {
		Grants []Grant
		Users  []PolicyUser
	}; if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}; return p }
	p := policies("admin")
	found := false
	for _, u := range p.Users {
		if u.ID == "viewer" {
			found = true
			if u.TenantID != "viewer" || u.TenantName != "Viewers" || u.StorageEnabled {
				t.Fatal("incorrect target metadata", u)
			}
		}
		if u.ID == "disabled" {
			t.Fatal("disabled user listed")
		}
	}
	if !found {
		t.Fatal("cross-tenant viewer missing")
	}
	body := `{"grant":{"userId":"viewer","tenantId":"admin","connection":"demo","bucket":"bucket","prefix":"team/a/","actions":["list","read","download"]}}`
	assertStatus(send("admin", "admin", "POST", "/v1/policies", body), 400)
	if _, err = store.Pool.Exec(ctx, `UPDATE tenants SET permissions='{"objectStorage":true}' WHERE id='viewer'`); err != nil {
		t.Fatal(err)
	}
	assertStatus(send("admin", "admin", "POST", "/v1/policies", body), 200)
	var grant Grant
	for _, g := range policies("admin").Grants {
		if g.UserID == "viewer" {
			grant = g
		}
	}
	if grant.ID == "" || grant.Tenant != "viewer" {
		t.Fatal("grant not bound to target tenant", grant)
	}
	assertStatus(send("viewer", "viewer", "GET", "/v1/text?connection=demo&bucket=bucket&key=team/a/file.txt", ""), 200)
	assertStatus(send("viewer", "viewer", "GET", "/v1/text?connection=demo&bucket=bucket&key=team/ab/private.txt", ""), 403)
	assertStatus(send("viewer", "viewer", "GET", "/v1/policies", ""), 403)
	assertStatus(send("viewer", "viewer", "POST", "/v1/policies", body), 403)
	assertStatus(send("other", "other", "GET", "/v1/text?connection=demo&bucket=bucket&key=team/a/file.txt", ""), 403)
	assertStatus(send("viewer", "admin", "GET", "/v1/connections", ""), 401)
	owned, err := store.Grants(ctx, Identity{ID: "admin", Tenant: "admin", Role: "admin"}, false)
	if err != nil || len(owned) != 1 || owned[0].UserID != "admin" {
		t.Fatal("runtime grants leaked", owned, err)
	}
	if _, err = store.Grants(ctx, Identity{ID: "viewer", Tenant: "viewer", Role: "viewer"}, true); err == nil {
		t.Fatal("viewer read global policies")
	}
	// A narrower administrator must not overwrite or revoke a broader existing grant.
	narrow := strings.Replace(body, `"list","read","download"`, `"list","read"`, 1)
	assertStatus(send("limited", "admin", "POST", "/v1/policies", narrow), 403)
	assertStatus(send("limited", "admin", "DELETE", "/v1/policies/"+grant.ID, ""), 404)
	for _, g := range policies("limited").Grants {
		if g.UserID == "viewer" {
			t.Fatal("unmanageable broader grant exposed")
		}
	}
	for _, bad := range []string{
		strings.Replace(body, "team/a/", "team/ab/", 1),
		strings.Replace(body, `"bucket":"bucket"`, `"bucket":"unconfigured"`, 1),
		strings.Replace(body, `"download"`, `"unknown"`, 1),
	} {
		assertStatus(send("admin", "admin", "POST", "/v1/policies", bad), 403)
	}
	for _, id := range []string{"disabled", "missing"} {
		assertStatus(send("admin", "admin", "POST", "/v1/policies", strings.Replace(body, `"userId":"viewer"`, `"userId":"`+id+`"`, 1)), 400)
	}
	if err = store.ChangeGrant(ctx, Identity{ID: "viewer", Tenant: "viewer", Role: "admin"}, grant, false); err == nil {
		t.Fatal("forged actor role accepted")
	}
	wrong := grant
	wrong.Tenant = "admin"
	if err = store.ChangeGrant(ctx, Identity{ID: "admin", Tenant: "admin"}, wrong, false); err == nil {
		t.Fatal("forged target tenant accepted")
	}
	// Moving the recipient never makes an old tenant grant effective in the new tenant.
	if _, err = store.Pool.Exec(ctx, `UPDATE local_users SET tenant_id='default' WHERE id='viewer'`); err != nil {
		t.Fatal(err)
	}
	assertStatus(send("viewer", "default", "GET", "/v1/text?connection=demo&bucket=bucket&key=team/a/file.txt", ""), 403)
	assertStatus(send("admin", "admin", "POST", "/v1/policies", body), 200)
	var moved Grant
	for _, g := range policies("admin").Grants {
		if g.UserID == "viewer" && g.Tenant == "default" {
			moved = g
		}
	}
	if moved.ID == "" {
		t.Fatal("default tenant grant missing")
	}
	assertStatus(send("viewer", "default", "GET", "/v1/text?connection=demo&bucket=bucket&key=team/a/file.txt", ""), 200)
	if _, err = store.Pool.Exec(ctx, `UPDATE tenants SET permissions='{"objectStorage":false}' WHERE id='default'`); err != nil {
		t.Fatal(err)
	}
	assertStatus(send("viewer", "default", "GET", "/v1/text?connection=demo&bucket=bucket&key=team/a/file.txt", ""), 403)
	assertStatus(send("admin", "admin", "POST", "/v1/policies", body), 400)
	// Revocation remains possible even after tenant access or the account is disabled.
	if _, err = store.Pool.Exec(ctx, `UPDATE local_users SET disabled=true WHERE id='viewer'`); err != nil {
		t.Fatal(err)
	}
	assertStatus(send("admin", "admin", "DELETE", "/v1/policies/"+moved.ID, ""), 200)
	assertStatus(send("admin", "admin", "DELETE", "/v1/policies/"+grant.ID, ""), 200)
	var count int
	if err = store.Pool.QueryRow(ctx, `SELECT count(*) FROM object_storage_operations WHERE username='admin' AND tenant_id='admin' AND NULLIF(target_key,'')::jsonb->>'userId'='viewer'`).Scan(&count); err != nil || count != 4 {
		t.Fatal("cross-tenant audit attribution", count, err)
	}
	if err = store.Pool.QueryRow(ctx, `SELECT count(*) FROM object_storage_grants WHERE user_id='viewer'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("cross-tenant revoke did not persist", count, err)
	}
}
