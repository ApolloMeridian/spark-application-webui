package storage

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGCSResumableProtocol(t *testing.T) {
	ctx := context.Background()
	step := 0
	const session = "https://storage.googleapis.com/upload/storage/v1/b/bucket/o?upload_id=private"
	p := &GCS{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		step++
		response := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}
		switch step {
		case 1:
			if r.Method != "POST" || r.URL.Query().Get("name") != "team/中文%.txt" || r.URL.Query().Get("ifGenerationMatch") != "0" || r.Header.Get("X-Upload-Content-Length") != "6" {
				t.Fatal("invalid conditional initiation", r.URL)
			}
			response.Header.Set("Location", session)
		case 2:
			data, _ := io.ReadAll(r.Body)
			if r.Method != "PUT" || r.ContentLength != 3 || r.Header.Get("Content-Range") != "bytes 0-2/6" || string(data) != "abc" {
				t.Fatal("invalid first part")
			}
			response.StatusCode = 308
			response.Header.Set("Range", "bytes=0-2")
		case 3:
			if r.Header.Get("Content-Range") != "bytes */6" || r.ContentLength != 0 {
				t.Fatal("invalid recovery probe")
			}
			response.StatusCode = 308
			response.Header.Set("Range", "bytes=0-2")
		case 4:
			data, _ := io.ReadAll(r.Body)
			if r.Header.Get("Content-Range") != "bytes 3-5/6" || string(data) != "def" {
				t.Fatal("invalid last part")
			}
			response.Body = io.NopCloser(strings.NewReader(`{"generation":"42","size":"6"}`))
		case 5:
			if r.Header.Get("Content-Range") != "bytes */6" {
				t.Fatal("invalid completion probe")
			}
			response.Body = io.NopCloser(strings.NewReader(`{"generation":"42","size":"6"}`))
		case 6:
			if r.Method != "DELETE" {
				t.Fatal("invalid abort")
			}
			response.StatusCode = 499
		default:
			t.Fatal("unexpected request")
		}
		return response, nil
	})}}
	u, err := p.Start(ctx, "bucket", "team/中文%.txt", 6, "")
	if err != nil || u.ID != session {
		t.Fatal(u, err)
	}
	u, err = p.Part(ctx, "bucket", "team/中文%.txt", u, 1, strings.NewReader("abc"), 3)
	if err != nil || u.Offset != 3 || u.Complete {
		t.Fatal(u, err)
	}
	// A replacement stateless provider recovers the offset using the same encrypted-ticket session.
	replacement := &GCS{HTTP: p.HTTP}
	u.Offset = 0
	u, err = replacement.Status(ctx, "bucket", "team/中文%.txt", u)
	if err != nil || u.Offset != 3 {
		t.Fatal(u, err)
	}
	u, err = replacement.Part(ctx, "bucket", "team/中文%.txt", u, 2, strings.NewReader("def"), 3)
	if err != nil || !u.Complete || u.FinalVersion != "42" {
		t.Fatal(u, err)
	}
	object, err := replacement.Complete(ctx, "bucket", "team/中文%.txt", u)
	if err != nil || object.Version != "42" || object.Size != 6 {
		t.Fatal(object, err)
	}
	if err = replacement.Abort(ctx, "bucket", "team/中文%.txt", u); err != nil {
		t.Fatal(err)
	}
	if _, err = replacement.Status(ctx, "bucket", "key", Upload{ID: "http://169.254.169.254/upload/"}); err == nil {
		t.Fatal("session endpoint injection accepted")
	}
}

func TestGCSRejectsUnsafeProviderResponsesAndCredentials(t *testing.T) {
	for _, body := range []string{`{"generation":"42","size":"999"}`, `{"size":"6"}`, `not-json`} {
		p := &GCS{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
		})}}
		if _, err := p.Status(context.Background(), "bucket", "key", Upload{ID: "https://storage.googleapis.com/upload/session", Size: 6}); err == nil {
			t.Fatal("invalid response accepted", body)
		}
	}
	for _, document := range []string{
		`{"type":"service_account","private_key":"not-a-real-key"}`,
		`{"type":"external_account","token_url":"http://attacker/token","credential_source":{"file":"/var/run/secrets/storage-wif/gcs/token"}}`,
		`{"type":"external_account","token_url":"https://sts.googleapis.com/v1/token","credential_source":{"url":"http://attacker/token"}}`,
	} {
		file := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(file, []byte(document), 0600); err != nil {
			t.Fatal(err)
		}
		c := Connection{ID: "gcs"}
		c.GCS.AuthMode = "kubernetesWif"
		c.GCS.CredentialFile = file
		if _, err := NewGCS(context.Background(), c, 8<<20); err == nil {
			t.Fatal("unsafe credentials accepted")
		}
	}
	if validImpersonationURL("https://attacker/v1/projects/-/serviceAccounts/test:generateAccessToken") {
		t.Fatal("unsafe impersonation endpoint")
	}
	if !validImpersonationURL("https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/test@example.iam.gserviceaccount.com:generateAccessToken") {
		t.Fatal("valid impersonation endpoint rejected")
	}
}
