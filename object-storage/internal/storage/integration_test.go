package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fakeProvider struct {
	Data     map[string]string
	Version  map[string]int
	HeadSize *int64
}

func (f *fakeProvider) ListRecursive(ctx context.Context, b, p, c string, n int) (Page, error) {
	return f.List(ctx, b, p, c, n)
}

func (f *fakeProvider) List(_ context.Context, b, p, c string, n int) (Page, error) {
	out := Page{Objects: []Object{}}
	for k, v := range f.Data {
		if strings.HasPrefix(k, p) {
			out.Objects = append(out.Objects, Object{Key: k, Size: int64(len(v)), Version: fmt.Sprint(f.Version[k])})
		}
	}
	return out, nil
}
func (f *fakeProvider) Head(_ context.Context, b, k string) (Object, error) {
	v, ok := f.Data[k]
	if !ok {
		return Object{}, fail(404, "object_not_found")
	}
	size := int64(len(v))
	if f.HeadSize != nil {
		size = *f.HeadSize
	}
	return Object{Key: k, Size: size, Version: fmt.Sprint(f.Version[k])}, nil
}
func (f *fakeProvider) Open(_ context.Context, b, k, v string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f.Data[k])), nil
}
func (f *fakeProvider) Put(_ context.Context, b, k string, r io.Reader, n int64, v string) (Object, error) {
	if _, exists := f.Data[k]; exists && v == "" {
		return Object{}, fail(409, "version_conflict")
	}
	if v != "" && v != fmt.Sprint(f.Version[k]) {
		return Object{}, fail(409, "version_conflict")
	}
	raw, e := io.ReadAll(r)
	if e != nil {
		return Object{}, e
	}
	f.Data[k] = string(raw)
	f.Version[k]++
	return Object{Key: k, Version: fmt.Sprint(f.Version[k]), Size: n}, nil
}
func (f *fakeProvider) Delete(_ context.Context, b, k, v string) error { delete(f.Data, k); return nil }
func (f *fakeProvider) Copy(_ context.Context, b, k, db, dk, v string) (Object, error) {
	f.Data[dk] = f.Data[k]
	return Object{Key: dk}, nil
}
func (f *fakeProvider) Start(context.Context, string, string, int64, string) (Upload, error) {
	return Upload{}, nil
}
func (f *fakeProvider) Part(context.Context, string, string, Upload, int32, io.Reader, int64) (Upload, error) {
	return Upload{}, nil
}
func (f *fakeProvider) Status(context.Context, string, string, Upload) (Upload, error) {
	return Upload{}, nil
}
func (f *fakeProvider) Complete(context.Context, string, string, Upload) (Object, error) {
	return Object{}, nil
}
func (f *fakeProvider) Abort(context.Context, string, string, Upload) error { return nil }
func dbFixture(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN required")
	}
	ctx := context.Background()
	pool, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := "storage_test_" + newID()
	if _, e = pool.Exec(ctx, `CREATE SCHEMA `+schema); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`); pool.Close() })
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	temp, e := pgxpool.New(ctx, u.String())
	if e != nil {
		t.Fatal(e)
	}
	_, e = temp.Exec(ctx, `CREATE TABLE tenants(id text PRIMARY KEY,name text NOT NULL,permissions jsonb); INSERT INTO tenants VALUES('default','Default',NULL),('other','Other',NULL); CREATE TABLE local_users(id text PRIMARY KEY,tenant_id text NOT NULL,role text NOT NULL,username text NOT NULL,disabled boolean NOT NULL DEFAULT false); INSERT INTO local_users(id,tenant_id,role,username) VALUES('admin','default','admin','admin'),('viewer','default','viewer','viewer'),('other','other','admin','other')`)
	temp.Close()
	if e != nil {
		t.Fatal(e)
	}
	store, e := OpenStore(ctx, u.String())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(store.Pool.Close)
	return store
}
func TestPostgresAuthorizationTextLimitsAuditAndRecovery(t *testing.T) {
	store := dbFixture(t)
	ctx := context.Background()
	boot := Bootstrap{Username: "admin", Tenant: "default", Connection: "demo", Bucket: "bucket", Prefix: "team/a/", Actions: Actions}
	if e := store.Bootstrap(ctx, []Bootstrap{boot}); e != nil {
		t.Fatal(e)
	}
	viewer := Grant{UserID: "viewer", Tenant: "default", Connection: "demo", Bucket: "bucket", Prefix: "team/a/", Actions: []string{"list", "read", "download"}}
	if e := store.ChangeGrant(ctx, Identity{ID: "admin", Tenant: "default", Username: "admin"}, viewer, false); e != nil {
		t.Fatal(e)
	}
	cfg := Config{Connections: []Connection{{ID: "demo", Name: "Mock", Provider: "gcs", Buckets: []string{"bucket"}}}}
	cfg.Connections[0].GCS.AuthMode = "gkeWif"
	if e := cfg.Validate(); e != nil {
		t.Fatal(e)
	}
	fake := &fakeProvider{Data: map[string]string{"team/a/config.yaml": "hello", "team/a/huge.txt": strings.Repeat("x", int(TextLimit+1)), "team/ab/private.txt": "secret"}, Version: map[string]int{"team/a/config.yaml": 1}}
	key := []byte(strings.Repeat("x", 32))
	handler := NewServer(cfg, store, key, map[string]Provider{"demo": fake}, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
	send := func(user, tenant, method, target, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("X-Spark-Storage-Identity", Sign(key, Claims{ID: user, Tenant: tenant, Method: method, Target: r.URL.RequestURI(), Audience: "spark-object-storage", Expires: time.Now().Add(time.Minute).Unix()}))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	textURL := "/v1/text?connection=demo&bucket=bucket&key=team/a/config.yaml"
	if w := send("viewer", "default", "GET", textURL, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send("viewer", "default", "PUT", textURL, `{"content":"evil","version":"1"}`); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send("admin", "default", "GET", "/v1/text?connection=demo&bucket=bucket&key=team/ab/private.txt", ""); w.Code != 403 {
		t.Fatal("prefix escape", w.Code)
	}
	if w := send("other", "other", "GET", textURL, ""); w.Code != 403 {
		t.Fatal("tenant escape", w.Code)
	}
	checkPreview := func() {
		w := send("admin", "default", "GET", "/v1/text?connection=demo&bucket=bucket&key=team/a/huge.txt", "")
		var result struct {
			Content      string
			Truncated    bool
			ReadOnly     bool
			PreviewBytes int64
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.Truncated || !result.ReadOnly || int64(len(result.Content)) > cfg.TextMaxBytes {
			t.Fatal("bounded read-only preview", w.Code)
		}
	}
	checkPreview()
	untrusted := int64(1)
	fake.HeadSize = &untrusted
	checkPreview()
	fake.HeadSize = nil
	if w := send("admin", "default", "PUT", "/v1/text?connection=demo&bucket=bucket&key=team/a/huge.txt", `{"content":"truncated","version":"0"}`); w.Code != 413 {
		t.Fatal("large file must stay read-only", w.Code)
	}
	createURL := "/v1/text?connection=demo&bucket=bucket&key=team/a/new.py"
	if w := send("viewer", "default", "POST", createURL, `{"content":"print(1)","version":""}`); w.Code != 403 {
		t.Fatal("ungranted viewer create", w.Code)
	}
	policy := `{"grant":{"userId":"viewer","connection":"demo","bucket":"bucket","prefix":"team/a/","actions":["list","read","download","upload","edit"]}}`
	if w := send("admin", "default", "POST", "/v1/policies", policy); w.Code != 200 {
		t.Fatal("grant viewer write", w.Code, w.Body.String())
	}
	if w := send("viewer", "default", "POST", createURL, `{"content":"print(1)","version":""}`); w.Code != 200 {
		t.Fatal("viewer create", w.Code, w.Body.String())
	}
	if w := send("viewer", "default", "POST", createURL, `{"content":"print(2)","version":""}`); w.Code != 409 {
		t.Fatal("create overwrite", w.Code)
	}
	if w := send("viewer", "default", "PUT", createURL, `{"content":"print(2)","version":"1"}`); w.Code != 200 {
		t.Fatal("viewer edit", w.Code)
	}
	if w := send("viewer", "default", "POST", "/v1/policies", policy); w.Code != 403 {
		t.Fatal("viewer manages policy", w.Code)
	}
	if w := send("viewer", "default", "GET", "/v1/download?connection=demo&bucket=bucket&key=team/a/new.py", ""); w.Code != 200 {
		t.Fatal("viewer download", w.Code)
	}
	if w := send("admin", "default", "PUT", textURL, `{"content":"updated","version":"1"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send("admin", "default", "PUT", textURL, `{"content":"stale","version":"1"}`); w.Code != 409 {
		t.Fatal(w.Code)
	}
	body := `{"bucket":"bucket","key":"team/ab/escape.txt","version":"2"}`
	if w := send("admin", "default", "POST", "/v1/copy?connection=demo&bucket=bucket&key=team/a/config.yaml", body); w.Code != 403 {
		t.Fatal("destination bypass", w.Code)
	}
	var count int
	store.Pool.QueryRow(ctx, `SELECT count(*) FROM object_storage_operations WHERE action='EDIT'`).Scan(&count)
	if count != 3 {
		t.Fatal("audit mutation count", count)
	}
	store.Pool.QueryRow(ctx, `SELECT count(*) FROM object_storage_operations WHERE action='DOWNLOAD'`).Scan(&count)
	if count != 1 {
		t.Fatal("download audit missing", count)
	}
	grants, e := store.Grants(ctx, Identity{ID: "admin", Tenant: "default"}, false)
	if e != nil {
		t.Fatal(e)
	}
	if e = store.ChangeGrant(ctx, Identity{ID: "admin", Tenant: "default", Username: "admin"}, grants[0], true); e != nil {
		t.Fatal(e)
	}
	if e = store.Bootstrap(ctx, []Bootstrap{boot}); e != nil {
		t.Fatal(e)
	}
	grants, e = store.Grants(ctx, Identity{ID: "admin", Tenant: "default"}, false)
	if e != nil || len(grants) != 0 {
		t.Fatal("bootstrap restored revoked grant", grants, e)
	}
	if w := send("admin", "default", "GET", textURL, ""); w.Code != 403 {
		t.Fatal("revocation not enforced", w.Code)
	}
	if _, e = store.Pool.Exec(ctx, `UPDATE tenants SET permissions='{"objectStorage":false}' WHERE id='default'`); e != nil {
		t.Fatal(e)
	}
	if w := send("viewer", "default", "GET", textURL, ""); w.Code != 403 {
		t.Fatal("tenant storage revocation not enforced", w.Code)
	}
	if _, e = store.Pool.Exec(ctx, `UPDATE local_users SET disabled=true WHERE id='viewer'`); e != nil {
		t.Fatal(e)
	}
	if w := send("viewer", "default", "GET", textURL, ""); w.Code != 401 {
		t.Fatal("disabled user", w.Code)
	}
}
func TestMinIOMultipartConditionalCopyAndRestart(t *testing.T) {
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("TEST_S3_ENDPOINT required")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ak"), []byte(os.Getenv("TEST_S3_ACCESS_KEY")), 0600)
	os.WriteFile(filepath.Join(dir, "sk"), []byte(os.Getenv("TEST_S3_SECRET_KEY")), 0600)
	c := Connection{ID: "s3", Name: "MinIO", Provider: "s3", Buckets: []string{"bucket"}}
	c.S3.Endpoint = endpoint
	c.S3.Region = "us-east-1"
	c.S3.ForcePathStyle = true
	c.S3.ConditionalWrites = true
	c.S3.AccessKeyFile = filepath.Join(dir, "ak")
	c.S3.SecretKeyFile = filepath.Join(dir, "sk")
	p, e := NewS3(c, 8<<20)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	bucket := "storage-test-" + newID()
	if _, e = p.Client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		list, _ := p.List(ctx, bucket, "", "", 500)
		for _, x := range list.Objects {
			if !x.Directory {
				p.Delete(ctx, bucket, x.Key, x.Version)
			}
		}
		p.Client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
	})
	obj, e := p.Put(ctx, bucket, "config.yaml", strings.NewReader("initial"), 7, "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Put(ctx, bucket, "config.yaml", strings.NewReader("overwrite"), 9, ""); e == nil {
		t.Fatal("create overwrote existing")
	}
	next, e := p.Put(ctx, bucket, "config.yaml", strings.NewReader("updated"), 7, obj.Version)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Put(ctx, bucket, "config.yaml", strings.NewReader("stale"), 5, obj.Version); e == nil {
		t.Fatal("stale edit accepted")
	}
	copied, e := p.Copy(ctx, bucket, "config.yaml", bucket, "copied.yaml", next.Version)
	if e != nil {
		t.Fatal(e)
	}
	reader, e := p.Open(ctx, bucket, copied.Key, copied.Version)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := io.ReadAll(reader)
	reader.Close()
	if string(raw) != "updated" {
		t.Fatal(string(raw))
	}
	if _, e = p.Copy(ctx, bucket, "config.yaml", bucket, "copied.yaml", next.Version); e == nil {
		t.Fatal("copy overwrote existing")
	}
	data := bytes.Repeat([]byte("z"), 10<<20)
	u, e := p.Start(ctx, bucket, "large.bin", int64(len(data)), "")
	if e != nil {
		t.Fatal(e)
	}
	u, e = p.Part(ctx, bucket, "large.bin", u, 1, bytes.NewReader(data[:8<<20]), 8<<20)
	if e != nil {
		t.Fatal(e)
	}
	// No in-memory upload state is carried into this new SDK client.
	restarted, e := NewS3(c, 8<<20)
	if e != nil {
		t.Fatal(e)
	}
	u, e = restarted.Status(ctx, bucket, "large.bin", u)
	if e != nil || u.Offset != 8<<20 {
		t.Fatal(u, e)
	}
	u, e = restarted.Part(ctx, bucket, "large.bin", u, 2, bytes.NewReader(data[8<<20:]), 2<<20)
	if e != nil {
		t.Fatal(e)
	}
	out, e := restarted.Complete(ctx, bucket, "large.bin", u)
	if e != nil || out.Size != 10<<20 {
		t.Fatal(out, e)
	}
	recovered, e := p.Status(ctx, bucket, "large.bin", u)
	if e != nil || !recovered.Complete || recovered.FinalVersion != out.Version {
		t.Fatal("completed multipart recovery must prove the upload marker", recovered, e)
	}
	wrong := u
	wrong.Marker = "different-upload"
	if _, e = p.Status(ctx, bucket, "large.bin", wrong); e == nil {
		t.Fatal("unrelated same-sized object accepted as completed upload")
	}
	cancelled, e := p.Start(ctx, bucket, "cancelled.bin", 1, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Abort(ctx, bucket, "cancelled.bin", cancelled); e != nil {
		t.Fatal(e)
	}
	empty, e := p.Put(ctx, bucket, "empty.txt", bytes.NewReader(nil), 0, "")
	if e != nil || empty.Size != 0 {
		t.Fatal(empty, e)
	}
	page, e := p.List(ctx, bucket, "", "", 1)
	if e != nil || page.Next == "" {
		t.Fatal(page, e)
	}
	second, e := p.List(ctx, bucket, "", page.Next, 1)
	if e != nil || len(second.Objects) != 1 || second.Objects[0].Key == page.Objects[0].Key {
		t.Fatal(second, e)
	}
	key := "子目录/100% 空格.yaml"
	special, e := p.Put(ctx, bucket, key, strings.NewReader("text"), 4, "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Copy(ctx, bucket, key, bucket, "special-copy.yaml", special.Version); e != nil {
		t.Fatal(e)
	}
	p.Delete(ctx, bucket, key, special.Version)
	if e = p.Delete(ctx, bucket, "config.yaml", obj.Version); e == nil {
		t.Fatal("stale delete accepted")
	}
	if e = p.Delete(ctx, bucket, "config.yaml", next.Version); e != nil {
		t.Fatal(e)
	}
}
