package storage

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

type directoryProvider struct {
	fakeProvider
	calls int
}

func (p *directoryProvider) List(_ context.Context, bucket, prefix, cursor string, limit int) (Page, error) {
	if bucket != "bucket" || prefix != "team/" || limit != 500 {
		return Page{}, fmt.Errorf("unexpected directory request")
	}
	p.calls++
	if cursor == "" {
		return Page{
			Objects: []Object{{Key: "team/folder/", Directory: true}, {Key: "team/largest.txt", Size: 100}},
			Next:    "more",
		}, nil
	}
	if cursor != "more" {
		return Page{}, fmt.Errorf("unexpected cursor")
	}
	var objects []Object
	for i := 1; i <= 11; i++ {
		objects = append(objects, Object{
			Key: fmt.Sprintf("team/file-%02d.txt", i), Size: int64(i),
			Modified: time.Unix(int64(i), 0),
		})
	}
	return Page{Objects: objects}, nil
}

func (p *directoryProvider) ListRecursive(context.Context, string, string, string, int) (Page, error) {
	panic("numbered listings must use immediate directory pages")
}

func TestNumberedDirectoryListingSortsAllPagesAndCountsFolders(t *testing.T) {
	server := &Server{directories: newDirectoryCache()}
	provider := &directoryProvider{}
	list := func(query, search string) directoryPage {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/objects?limit=10&"+query, nil)
		page, err := server.numberedPage(r, provider, directoryKey{"demo", "bucket", "team/"}, search)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	first := list("page=1&sort=size&direction=desc", "")
	if first.Total != 13 || len(first.Objects) != 10 || first.Objects[0].Key != "team/largest.txt" || first.Objects[1].Size != 11 {
		t.Fatal(first)
	}
	second := list("page=2&sort=size&direction=desc", "")
	if second.Total != 13 || len(second.Objects) != 3 || second.Objects[0].Size != 2 || !second.Objects[2].Directory {
		t.Fatal(second)
	}
	latest := list("page=1&sort=modified&direction=desc", "")
	if latest.Objects[0].Key != "team/file-11.txt" {
		t.Fatal(latest)
	}
	search := list("page=12&sort=name&direction=asc", "file-01")
	if search.Total != 1 || search.Page != 1 || search.Objects[0].Key != "team/file-01.txt" || provider.calls != 2 {
		t.Fatal(search, provider.calls)
	}
	list("page=1&refresh=true", "")
	if provider.calls != 4 {
		t.Fatalf("refresh did not replace cached listing: %d", provider.calls)
	}
}
