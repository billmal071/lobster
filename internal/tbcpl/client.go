package tbcpl

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

//go:embed snapshot.json
var snapshotJSON []byte

var (
	baseURL      = "https://tbcpl.lol/links.json"
	regionURLFmt = "https://tbcpl.lol/Region-Links/links.%s.json"
)

// Client loads the TBCPL catalog with a disk cache and embedded fallback.
type Client struct {
	cachePath string
	ttl       time.Duration
	http      *http.Client
	log       func(string, ...any)
}

// NewClient builds a catalog client. log may be nil.
func NewClient(cachePath string, ttl time.Duration, log func(string, ...any)) *Client {
	if log == nil {
		log = func(string, ...any) {}
	}
	return &Client{
		cachePath: cachePath,
		ttl:       ttl,
		http:      &http.Client{Timeout: 8 * time.Second},
		log:       log,
	}
}

// Load returns a non-nil catalog, preferring fresh cache, then network,
// then stale cache, then the embedded snapshot. It never returns nil.
func (cl *Client) Load(ctx context.Context, region string) *Catalog {
	// 1. Fresh cache.
	if data, ok := cl.readCacheIfFresh(); ok {
		if c, err := Parse(data); err == nil {
			return c
		}
	}
	// 2. Network.
	if data, err := cl.fetch(ctx, region); err == nil {
		if c, err := Parse(data); err == nil {
			cl.writeCache(data)
			return c
		}
	} else {
		cl.log("tbcpl: fetch failed, falling back: %v", err)
	}
	// 3. Stale cache.
	if data, err := os.ReadFile(cl.cachePath); err == nil {
		if c, err := Parse(data); err == nil {
			cl.log("tbcpl: using stale cache")
			return c
		}
	}
	// 4. Embedded snapshot.
	cl.log("tbcpl: using embedded snapshot")
	c, _ := Parse(snapshotJSON)
	if c == nil {
		c = &Catalog{}
	}
	return c
}

// LoadMerged loads the global catalog and, if region is non-empty, overlays the
// region file (union by URL). Always returns a non-nil catalog.
func (cl *Client) LoadMerged(ctx context.Context, region string) *Catalog {
	global := cl.Load(ctx, "")
	if region == "" {
		return global
	}
	data, err := cl.fetch(ctx, region)
	if err != nil {
		cl.log("tbcpl: region %q fetch failed: %v", region, err)
		return global
	}
	regionCat, err := Parse(data)
	if err != nil {
		cl.log("tbcpl: region %q parse failed: %v", region, err)
		return global
	}
	seen := make(map[string]bool, len(global.Sites))
	for _, s := range global.Sites {
		seen[s.URL] = true
	}
	merged := &Catalog{Sites: global.Sites}
	for _, s := range regionCat.Sites {
		if !seen[s.URL] {
			merged.Sites = append(merged.Sites, s)
			seen[s.URL] = true
		}
	}
	return merged
}

func (cl *Client) readCacheIfFresh() ([]byte, bool) {
	fi, err := os.Stat(cl.cachePath)
	if err != nil || time.Since(fi.ModTime()) > cl.ttl {
		return nil, false
	}
	data, err := os.ReadFile(cl.cachePath)
	if err != nil {
		return nil, false
	}
	return data, true
}

func (cl *Client) writeCache(data []byte) {
	if err := os.WriteFile(cl.cachePath, data, 0o644); err != nil {
		cl.log("tbcpl: cache write failed: %v", err)
	}
}

func (cl *Client) fetch(ctx context.Context, region string) ([]byte, error) {
	url := baseURL
	if region != "" {
		url = regionURL(region)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := cl.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tbcpl: http %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func regionURL(region string) string {
	return fmt.Sprintf(regionURLFmt, region)
}
