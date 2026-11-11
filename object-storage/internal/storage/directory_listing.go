package storage

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	directoryTTL        = 15 * time.Second
	directoryCacheSize  = 4
	directoryEntryLimit = 50_000
	directoryByteLimit  = 8 << 20
)

type directoryKey struct{ connection, bucket, prefix string }

type directorySnapshot struct {
	objects []Object
	started time.Time
}

type directoryCache struct {
	mu      sync.Mutex
	entries map[directoryKey]directorySnapshot
	scans   chan struct{}
}

func newDirectoryCache() directoryCache {
	return directoryCache{
		entries: make(map[directoryKey]directorySnapshot),
		scans:   make(chan struct{}, 2),
	}
}

func (c *directoryCache) get(key directoryKey, since time.Time) (directorySnapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	snapshot, ok := c.entries[key]
	return snapshot, ok && snapshot.started.After(since) && time.Since(snapshot.started) < directoryTTL
}

func (c *directoryCache) put(key directoryKey, snapshot directorySnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if previous, ok := c.entries[key]; ok {
		if previous.started.After(snapshot.started) {
			return
		}
	} else if len(c.entries) >= directoryCacheSize {
		var oldestKey directoryKey
		var oldest time.Time
		for k, entry := range c.entries {
			if oldest.IsZero() || entry.started.Before(oldest) {
				oldestKey, oldest = k, entry.started
			}
		}
		delete(c.entries, oldestKey)
	}
	c.entries[key] = snapshot
}

func immediateObject(object Object, prefix string) bool {
	relative, matches := strings.CutPrefix(object.Key, prefix)
	relative = strings.TrimSuffix(relative, "/")
	return matches && relative != "" && ValidKey(object.Key) && !strings.Contains(relative, "/")
}

func scanDirectory(ctx context.Context, p Provider, key directoryKey) ([]Object, error) {
	var objects []Object
	bytesUsed := 0
	cursor := ""
	seen := map[string]bool{}
	for batch := 0; batch < 256; batch++ {
		page, err := p.List(ctx, key.bucket, key.prefix, cursor, 500)
		if err != nil {
			return nil, err
		}
		for _, object := range page.Objects {
			if !immediateObject(object, key.prefix) {
				return nil, fail(502, "invalid_provider_response")
			}
			bytesUsed += 96 + len(object.Key) + len(object.Version)
			if len(objects) >= directoryEntryLimit || bytesUsed > directoryByteLimit {
				return nil, fail(422, "directory_too_large")
			}
			objects = append(objects, object)
		}
		if page.Next == "" {
			return objects, nil
		}
		if seen[page.Next] {
			return nil, fail(502, "invalid_provider_response")
		}
		seen[page.Next] = true
		cursor = page.Next
	}
	return nil, fail(422, "directory_too_large")
}

type directoryPage struct {
	Objects []Object `json:"objects"`
	Total   int      `json:"total"`
	Page    int      `json:"page"`
}

func (s *Server) numberedPage(r *http.Request, provider Provider, key directoryKey, search string) (directoryPage, error) {
	q := r.URL.Query()
	page, err := strconv.Atoi(q.Get("page"))
	size, sizeErr := strconv.Atoi(q.Get("limit"))
	order, direction := q.Get("sort"), q.Get("direction")
	if err != nil || page < 1 || page > directoryEntryLimit || sizeErr != nil ||
		(size != 10 && size != 20 && size != 50 && size != 100) ||
		(order != "" && order != "name" && order != "size" && order != "modified") ||
		(direction != "" && direction != "asc" && direction != "desc") {
		return directoryPage{}, fail(400, "invalid_pagination")
	}
	var since time.Time
	if q.Get("refresh") == "true" {
		since = time.Now()
	}
	snapshot, ok := s.directories.get(key, since)
	if !ok {
		select {
		case s.directories.scans <- struct{}{}:
			defer func() { <-s.directories.scans }()
		case <-r.Context().Done():
			return directoryPage{}, r.Context().Err()
		}
		snapshot, ok = s.directories.get(key, since)
		if !ok {
			snapshot.started = time.Now()
			snapshot.objects, err = scanDirectory(r.Context(), provider, key)
			if errors.Is(err, context.DeadlineExceeded) {
				return directoryPage{}, fail(422, "directory_too_large")
			}
			if err != nil {
				return directoryPage{}, err
			}
			s.directories.put(key, snapshot)
		}
	}
	matches := make([]Object, 0, len(snapshot.objects))
	for _, object := range snapshot.objects {
		if search == "" || strings.Contains(strings.ToLower(strings.TrimPrefix(object.Key, key.prefix)), search) {
			matches = append(matches, object)
		}
	}
	if order != "" {
		slices.SortFunc(matches, func(a, b Object) int {
			return compareDirectoryObjects(a, b, order, direction == "desc")
		})
	}
	total := len(matches)
	page = min(page, max(1, (total+size-1)/size))
	start := (page - 1) * size
	return directoryPage{Objects: matches[start:min(start+size, total)], Total: total, Page: page}, nil
}

func compareDirectoryObjects(a, b Object, order string, descending bool) int {
	var result int
	switch order {
	case "size", "modified":
		// Virtual folders have no size or modification time. Keep them after files.
		if a.Directory != b.Directory {
			if a.Directory {
				return 1
			}
			return -1
		}
		if order == "size" {
			result = cmp.Compare(a.Size, b.Size)
		} else {
			result = a.Modified.Compare(b.Modified)
		}
	default:
		result = strings.Compare(a.Key, b.Key)
	}
	if result == 0 {
		return strings.Compare(a.Key, b.Key)
	}
	if descending {
		return -result
	}
	return result
}
