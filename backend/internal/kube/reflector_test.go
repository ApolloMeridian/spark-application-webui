package kube

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"spark-control-center/backend/internal/domain"
)

func TestReflectorUsesConsistentPagedListAndWatchAndCopies(t *testing.T) {
	var lists atomic.Int32
	seen := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") == "true" {
			if r.URL.Query().Get("resourceVersion") != "10" {
				t.Error("watch did not resume from listed resourceVersion")
			}
			fmt.Fprintln(w, `{"type":"MODIFIED","object":{"metadata":{"name":"a","uid":"u","resourceVersion":"11"},"value":"new"}}`)
			w.(http.Flusher).Flush()
			<-release
			fmt.Fprintln(w, `{"type":"ERROR","object":{"code":410}}`)
			return
		}
		lists.Add(1)
		if r.URL.Query().Get("limit") != "500" {
			t.Error("unbounded list")
		}
		if r.URL.Query().Get("continue") == "next" {
			fmt.Fprintln(w, `{"metadata":{"resourceVersion":"10"},"items":[{"metadata":{"name":"b","uid":"b"}}]}`)
		} else {
			fmt.Fprintln(w, `{"metadata":{"resourceVersion":"10","continue":"next"},"items":[{"metadata":{"name":"a","uid":"u"},"value":"old"}]}`)
		}
	}))
	defer server.Close()
	client := &Client{baseURL: server.URL, http: server.Client(), allowedLookup: map[string]bool{"spark": true}}
	done := make(chan error, 1)
	go func() {
		done <- client.WatchSparkApplications(context.Background(), "spark", func(change domain.ApplicationChange) {
			if change.Type == "MODIFIED" {
				close(seen)
			}
		})
	}()
	<-seen
	for i := 0; i < 2; i++ {
		apps, err := client.ListSparkApplications(context.Background(), "spark")
		if err != nil || len(apps) != 2 || lists.Load() != 2 {
			t.Fatal(apps, err, lists.Load())
		}
		for _, app := range apps {
			if String(app, "metadata", "name") == "a" {
				if app["value"] != "new" {
					t.Fatal(app)
				}
				app["value"] = "forged"
			}
		}
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("410 treated as success")
	}
	if _, ready := client.reflectedApplications("spark"); ready {
		t.Fatal("disconnected cache still trusted")
	}
}
