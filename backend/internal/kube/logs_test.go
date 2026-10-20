package kube

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"spark-control-center/backend/internal/domain"
)

func TestDriverLogWindowsBeyondTailAndBounds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing authorization")
		}
		if strings.Contains(r.URL.Path, "missing") {
			w.WriteHeader(404)
			return
		}
		if strings.Contains(r.URL.Path, "large") {
			for i := 0; i < 200; i++ {
				fmt.Fprintf(w, "%s-%03d\n", strings.Repeat("x", 64<<10), i)
			}
			return
		}
		if strings.Contains(r.URL.Path, "long-line") {
			fmt.Fprintln(w, strings.Repeat("x", 2<<20))
			return
		}
		start := 1
		if tail := r.URL.Query().Get("tailLines"); tail != "" {
			n, _ := strconv.Atoi(tail)
			start = 12050 - n + 1
		}
		for i := start; i <= 12050; i++ {
			fmt.Fprintf(w, "2026-10-20T00:00:00Z INFO driver-line-%05d\n", i)
		}
	}))
	defer server.Close()
	client := &Client{baseURL: server.URL, token: "fixture", http: &http.Client{Timeout: time.Second * 5}, allowedLookup: map[string]bool{"spark": true}}
	tests := []struct {
		mode                           string
		start, end, limit, first, last int
		more                           bool
	}{
		{"tail", 0, 0, 2000, 10051, 12050, false},
		{"range", 1, 2000, 2000, 1, 2000, true},
		{"range", 6001, 8000, 2000, 6001, 8000, true},
		{"range", 12001, 14000, 2000, 12001, 12050, false},
	}
	for _, test := range tests {
		out, err := client.PodLogWindow(context.Background(), "spark", "driver", domain.DriverLogQuery{Mode: test.mode, StartLine: test.start, EndLine: test.end, Limit: test.limit})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Lines) != test.last-test.first+1 || !strings.HasSuffix(out.Lines[0], fmt.Sprintf("%05d", test.first)) || !strings.HasSuffix(out.Lines[len(out.Lines)-1], fmt.Sprintf("%05d", test.last)) || out.HasMore != test.more {
			t.Fatalf("incorrect window %+v: %+v", test, out)
		}
		if test.mode == "range" && (out.StartLine != test.first || out.EndLine != test.last) {
			t.Fatal("incorrect absolute line numbers", out)
		}
	}
	q := domain.DriverLogQuery{Mode: "range", StartLine: 13000, EndLine: 14000, Limit: 1001}
	out, err := client.PodLogWindow(context.Background(), "spark", "driver", q)
	if err != nil || len(out.Lines) != 0 || out.HasMore {
		t.Fatal(out, err)
	}
	q = domain.DriverLogQuery{Mode: "range", StartLine: 1, EndLine: 200, Limit: 200}
	out, err = client.PodLogWindow(context.Background(), "spark", "large", q)
	if err != nil || !out.ByteLimit || !out.HasMore || out.EndLine == 0 || len(strings.Join(out.Lines, "\n")) > domain.MaxDriverLogBytes {
		t.Fatal("unbounded response", out.EndLine, err)
	}
	tail, tailErr := client.PodLogWindow(context.Background(), "spark", "large", domain.DriverLogQuery{Mode: "tail", Limit: 200})
	if tailErr != nil || !tail.ByteLimit || !strings.HasSuffix(tail.Lines[len(tail.Lines)-1], "-199") || len(strings.Join(tail.Lines, "\n")) > domain.MaxDriverLogBytes {
		t.Fatal("tail byte cap did not retain newest lines", tailErr)
	}
	if _, err = client.PodLogWindow(context.Background(), "spark", "long-line", q); err == nil {
		t.Fatal("oversized line accepted")
	}
	if _, err = client.PodLogWindow(context.Background(), "other", "driver", q); err == nil {
		t.Fatal("namespace escaped")
	}
	if _, err = client.PodLogWindow(context.Background(), "spark", "missing", q); err == nil {
		t.Fatal("missing pod silently accepted")
	}
	q.EndLine = 20000
	if _, err = client.PodLogWindow(context.Background(), "spark", "driver", q); err == nil {
		t.Fatal("unbounded line window accepted")
	}
}

func TestDriverLogWindowClosesStreamEarly(t *testing.T) {
	closed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 10; i++ {
			fmt.Fprintln(w, "INFO line")
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		closed <- struct{}{}
	}))
	defer server.Close()
	client := &Client{baseURL: server.URL, token: "fixture", http: server.Client(), allowedLookup: map[string]bool{"spark": true}}
	out, err := client.PodLogWindow(context.Background(), "spark", "driver", domain.DriverLogQuery{Mode: "range", StartLine: 1, EndLine: 2, Limit: 2})
	if err != nil || len(out.Lines) != 2 {
		t.Fatal(out, err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("upstream stream was not cancelled")
	}
}
