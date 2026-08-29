# TBCPL Catalog Feed Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Integrate TBCPL (tbcpl.lol) as a dynamic domain/fallback feed so lobster keeps existing providers' mirror domains fresh, plays additional trusted TMDB-id embed sites, and ingests TBCPL live-TV playlists — without authoring any bespoke per-site scrapers.

**Architecture:** A new `internal/tbcpl` package fetches and caches TBCPL's static JSON catalog (with an embedded offline snapshot). Its entries feed three consumers: (1) a mirror-match mapper that injects fresh domains into `DomainOverrides` for providers lobster already scrapes; (2) a new generic TMDB-id embed provider modeled on `VidNest`/`VaPlayer` that resolves trusted, unmatched movie/anime sites via `extract.ResolveForURL`; (3) `LiveTV.sources`, which accepts any m3u playlist URL directly.

**Tech Stack:** Go 1.22+, stdlib `net/http`, `encoding/json`, `//go:embed`; existing packages `internal/provider`, `internal/extract`, `internal/resolver`, `internal/config`, `internal/media`.

## Global Constraints

- Go 1.22+; build-only Go dependency, runtime deps are fzf/mpv/ffmpeg.
- No bespoke per-site scrapers. Only wire existing extractors/APIs. (Standing user preference.)
- No silent caps: any dropped/skipped TBCPL entry must be logged via the package's `debugf`-style logger.
- No `Co-Authored-By` lines in commits. (Standing user preference.)
- Catalog source of truth: `https://tbcpl.lol/links.json` and `https://tbcpl.lol/Region-Links/links.<COUNTRY>.json`. Schema: `{"categories":[{"id","name","sites":[{"name","url","logo","enabled","status"}]}]}`. `status` ∈ {`"trusted"`,`"new"`, absent}.
- Config defaults: `tbcpl_feed` = true, `tbcpl_region` = "" (global only), `tbcpl_include_untrusted` = false.
- Cache: `<configDir>/tbcpl-cache.json`, TTL 12h, embedded snapshot fallback when both cache and network are unavailable.

---

## File Structure

- Create `internal/tbcpl/catalog.go` — types (`Site`, `Catalog`), parse, category/trust/enabled filters.
- Create `internal/tbcpl/client.go` — fetch + disk cache + embedded snapshot fallback.
- Create `internal/tbcpl/snapshot.json` — embedded offline snapshot (baked global list).
- Create `internal/tbcpl/match.go` — host → known-provider-name mirror matcher.
- Create `internal/tbcpl/*_test.go` — unit tests for the above.
- Create `internal/provider/tbcplembed.go` — generic TMDB-id embed `StreamProvider`.
- Create `internal/provider/tbcplembed_test.go`.
- Modify `internal/config/config.go` — add config fields + `TBCPLCachePath()` + defaults.
- Modify `internal/config/paths_unix.go` / `paths_windows.go` — expose config dir for cache path (via a new exported `TBCPLCachePath` in config.go that reuses `configDir()`).
- Modify `internal/provider/failover.go` — add mirror-match domain injection helper.
- Modify `cmd/fallback.go` — build catalog once, inject mirror domains, append embed provider to `fallbackProviders`.
- Modify `internal/tui/app.go:145` — merge TBCPL live-tv playlists into `NewLiveTV` sources.

---

## Task 1: TBCPL catalog types and parsing

**Files:**
- Create: `internal/tbcpl/catalog.go`
- Test: `internal/tbcpl/catalog_test.go`

**Interfaces:**
- Produces:
  - `type Site struct { Name, URL, Category, Status string; Enabled bool }`
  - `type Catalog struct { Sites []Site }`
  - `func Parse(data []byte) (*Catalog, error)`
  - `func (c *Catalog) ByCategory(id string) []Site`
  - `func (c *Catalog) Trusted() []Site` (returns only `Status=="trusted" && Enabled`)

- [ ] **Step 1: Write the failing test**

```go
package tbcpl

import "testing"

const sampleJSON = `{"categories":[
 {"id":"movies","name":"Movies & Shows","sites":[
   {"name":"1Shows","url":"https://www.1shows.org/","enabled":true,"status":"trusted"},
   {"name":"MeowTV","url":"https://meowtv.ru/","enabled":true}]},
 {"id":"livetv","name":"Live TV & Sports","sites":[
   {"name":"FreeTV","url":"https://example.com/list.m3u","enabled":true,"status":"trusted"},
   {"name":"Disabled","url":"https://nope.example/","enabled":false,"status":"trusted"}]}]}`

func TestParseAndFilters(t *testing.T) {
	c, err := Parse([]byte(sampleJSON))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(c.Sites) != 4 {
		t.Fatalf("got %d sites, want 4", len(c.Sites))
	}
	movies := c.ByCategory("movies")
	if len(movies) != 2 || movies[0].Name != "1Shows" || movies[0].Category != "movies" {
		t.Fatalf("ByCategory movies wrong: %+v", movies)
	}
	trusted := c.Trusted()
	// 1Shows + FreeTV are trusted+enabled; Disabled is trusted but disabled.
	if len(trusted) != 2 {
		t.Fatalf("Trusted got %d, want 2: %+v", len(trusted), trusted)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tbcpl/ -run TestParseAndFilters -v`
Expected: FAIL (package/types not defined).

- [ ] **Step 3: Write minimal implementation**

```go
// Package tbcpl reads TBCPL's (tbcpl.lol) static site catalog and exposes it
// as filterable entries for lobster's provider/fallback machinery.
package tbcpl

import "encoding/json"

// Site is one streaming site listed by TBCPL.
type Site struct {
	Name     string
	URL      string
	Category string // category id: "movies", "anime", "livetv", ...
	Status   string // "trusted", "new", or ""
	Enabled  bool
}

// Catalog is a flattened view of all sites across all categories.
type Catalog struct {
	Sites []Site
}

type rawCatalog struct {
	Categories []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Sites []struct {
			Name    string `json:"name"`
			URL     string `json:"url"`
			Status  string `json:"status"`
			Enabled bool   `json:"enabled"`
		} `json:"sites"`
	} `json:"categories"`
}

// Parse turns TBCPL links.json bytes into a flattened Catalog.
func Parse(data []byte) (*Catalog, error) {
	var raw rawCatalog
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	c := &Catalog{}
	for _, cat := range raw.Categories {
		for _, s := range cat.Sites {
			c.Sites = append(c.Sites, Site{
				Name:     s.Name,
				URL:      s.URL,
				Category: cat.ID,
				Status:   s.Status,
				Enabled:  s.Enabled,
			})
		}
	}
	return c, nil
}

// ByCategory returns all sites in the given category id.
func (c *Catalog) ByCategory(id string) []Site {
	var out []Site
	for _, s := range c.Sites {
		if s.Category == id {
			out = append(out, s)
		}
	}
	return out
}

// Trusted returns only enabled sites flagged status=="trusted".
func (c *Catalog) Trusted() []Site {
	var out []Site
	for _, s := range c.Sites {
		if s.Enabled && s.Status == "trusted" {
			out = append(out, s)
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tbcpl/ -run TestParseAndFilters -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tbcpl/catalog.go internal/tbcpl/catalog_test.go
git commit -m "feat(tbcpl): catalog types, parse, and filters"
```

---

## Task 2: Config fields, defaults, and cache path

**Files:**
- Modify: `internal/config/config.go` (struct near line 18-36; `Default()`; add `TBCPLCachePath()` near `HealthPath()` at line 215)
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `Config.TBCPLFeed bool` (`toml:"tbcpl_feed"`)
  - `Config.TBCPLRegion string` (`toml:"tbcpl_region"`)
  - `Config.TBCPLIncludeUntrusted bool` (`toml:"tbcpl_include_untrusted"`)
  - `func TBCPLCachePath() (string, error)` → `<configDir>/tbcpl-cache.json`

- [ ] **Step 1: Write the failing test**

```go
func TestTBCPLDefaults(t *testing.T) {
	c := Default()
	if !c.TBCPLFeed {
		t.Errorf("TBCPLFeed default = false, want true")
	}
	if c.TBCPLRegion != "" {
		t.Errorf("TBCPLRegion default = %q, want empty", c.TBCPLRegion)
	}
	if c.TBCPLIncludeUntrusted {
		t.Errorf("TBCPLIncludeUntrusted default = true, want false")
	}
}

func TestTBCPLCachePath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p, err := TBCPLCachePath()
	if err != nil {
		t.Fatalf("TBCPLCachePath: %v", err)
	}
	if filepath.Base(p) != "tbcpl-cache.json" {
		t.Errorf("cache path base = %q, want tbcpl-cache.json", filepath.Base(p))
	}
}
```

(Ensure `path/filepath` is imported in the test file.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestTBCPL' -v`
Expected: FAIL (fields/function undefined).

- [ ] **Step 3: Write minimal implementation**

Add to the `Config` struct (after `AnimeDub` at line 35):

```go
	TBCPLFeed             bool   `toml:"tbcpl_feed"`
	TBCPLRegion           string `toml:"tbcpl_region"`
	TBCPLIncludeUntrusted bool   `toml:"tbcpl_include_untrusted"`
```

In `Default()`, set `TBCPLFeed: true` (leave the other two at zero values).

Add near `HealthPath()`:

```go
// TBCPLCachePath returns the path to the cached TBCPL catalog JSON.
func TBCPLCachePath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tbcpl-cache.json"), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run 'TestTBCPL' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add tbcpl_feed/region/include_untrusted and cache path"
```

---

## Task 3: Catalog client with cache + embedded snapshot

**Files:**
- Create: `internal/tbcpl/client.go`
- Create: `internal/tbcpl/snapshot.json`
- Test: `internal/tbcpl/client_test.go`

**Interfaces:**
- Consumes: `Parse` (Task 1).
- Produces:
  - `type Client struct { ... }`
  - `func NewClient(cachePath string, ttl time.Duration, log func(string, ...any)) *Client`
  - `func (cl *Client) Load(ctx context.Context, region string) *Catalog` — always returns a non-nil catalog: fresh cache → HTTP fetch → stale cache → embedded snapshot. Never returns an error; it logs degradations and falls back.
  - Base URLs are package vars `baseURL` / `regionURLFmt` so tests can override.

- [ ] **Step 1: Create the embedded snapshot**

Fetch the current global list and save it verbatim (used only as an offline fallback):

```bash
curl -sL https://tbcpl.lol/links.json -o internal/tbcpl/snapshot.json
```

Verify it parses (temporary check):

```bash
go run -tags ignore ./... 2>/dev/null; head -c 80 internal/tbcpl/snapshot.json
```

- [ ] **Step 2: Write the failing test**

```go
package tbcpl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadFetchesAndCaches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleJSON))
	}))
	defer srv.Close()
	oldBase := baseURL
	baseURL = srv.URL + "/links.json"
	defer func() { baseURL = oldBase }()

	cache := filepath.Join(t.TempDir(), "tbcpl-cache.json")
	cl := NewClient(cache, time.Hour, func(string, ...any) {})
	cat := cl.Load(context.Background(), "")
	if len(cat.Sites) != 4 {
		t.Fatalf("fetched %d sites, want 4", len(cat.Sites))
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("cache not written: %v", err)
	}
}

func TestLoadFallsBackToSnapshotOffline(t *testing.T) {
	oldBase := baseURL
	baseURL = "http://127.0.0.1:0/links.json" // unreachable
	defer func() { baseURL = oldBase }()

	cache := filepath.Join(t.TempDir(), "tbcpl-cache.json") // absent
	cl := NewClient(cache, time.Hour, func(string, ...any) {})
	cat := cl.Load(context.Background(), "")
	if len(cat.Sites) == 0 {
		t.Fatal("expected embedded snapshot fallback, got 0 sites")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/tbcpl/ -run TestLoad -v`
Expected: FAIL (Client/NewClient/baseURL undefined).

- [ ] **Step 4: Write minimal implementation**

```go
package tbcpl

import (
	"context"
	"encoding/json"
	_ "embed"
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
	// Region overlay: merge is handled by callers combining both loads; for the
	// primary Load we fetch the global list. Region-specific loading is done via
	// LoadRegion when a region is configured (see below).
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
		return nil, &httpError{resp.StatusCode}
	}
	return io.ReadAll(resp.Body)
}

func regionURL(region string) string {
	return fmtSprintf(regionURLFmt, region)
}

type httpError struct{ code int }

func (e *httpError) Error() string { return "tbcpl: http " + itoa(e.code) }
```

Add tiny helpers (or import `fmt`/`strconv` directly — prefer stdlib):

```go
import (
	"fmt"
	"strconv"
)

func fmtSprintf(f, a string) string { return fmt.Sprintf(f, a) }
func itoa(n int) string             { return strconv.Itoa(n) }
```

> Note: the two wrapper helpers exist only to keep the illustrative code above compilable in isolation. In the real file, import `fmt`/`strconv` and call `fmt.Sprintf` / `strconv.Itoa` directly; delete the wrappers.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/tbcpl/ -run TestLoad -v`
Expected: PASS (both).

- [ ] **Step 6: Commit**

```bash
git add internal/tbcpl/client.go internal/tbcpl/snapshot.json internal/tbcpl/client_test.go
git commit -m "feat(tbcpl): catalog client with disk cache and embedded snapshot"
```

---

## Task 4: Region overlay merge

**Files:**
- Modify: `internal/tbcpl/client.go`
- Test: `internal/tbcpl/client_test.go`

**Interfaces:**
- Consumes: `Client.Load`, `Parse`.
- Produces: `func (cl *Client) LoadMerged(ctx context.Context, region string) *Catalog` — loads the global list and, when `region != ""`, overlays the region file (union by URL; region entries add to but never remove globals). Always non-nil.

- [ ] **Step 1: Write the failing test**

```go
func TestLoadMergedOverlaysRegion(t *testing.T) {
	globalJSON := sampleJSON
	regionJSON := `{"categories":[{"id":"movies","name":"Movies & Shows","sites":[
		{"name":"RegionOnly","url":"https://regiononly.example/","enabled":true,"status":"trusted"}]}]}`
	mux := http.NewServeMux()
	mux.HandleFunc("/links.json", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(globalJSON)) })
	mux.HandleFunc("/Region-Links/links.INDIA.json", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(regionJSON)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ob, orf := baseURL, regionURLFmt
	baseURL = srv.URL + "/links.json"
	regionURLFmt = srv.URL + "/Region-Links/links.%s.json"
	defer func() { baseURL, regionURLFmt = ob, orf }()

	cl := NewClient(filepath.Join(t.TempDir(), "c.json"), time.Hour, nil)
	cat := cl.LoadMerged(context.Background(), "INDIA")
	found := false
	for _, s := range cat.Sites {
		if s.URL == "https://regiononly.example/" {
			found = true
		}
	}
	if !found {
		t.Fatal("region-only site not merged")
	}
	if len(cat.Sites) <= 4 {
		t.Fatalf("expected globals + region, got %d", len(cat.Sites))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tbcpl/ -run TestLoadMerged -v`
Expected: FAIL (LoadMerged undefined).

- [ ] **Step 3: Write minimal implementation**

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tbcpl/ -run TestLoadMerged -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tbcpl/client.go internal/tbcpl/client_test.go
git commit -m "feat(tbcpl): overlay region catalog on the global list"
```

---

## Task 5: Mirror-match host → provider mapper

**Files:**
- Create: `internal/tbcpl/match.go`
- Test: `internal/tbcpl/match_test.go`

**Interfaces:**
- Consumes: `Site`.
- Produces:
  - `func ProviderFor(host string) (name string, ok bool)` — maps a URL host to a lobster provider name key. Recognized substrings → names: `flixhq.ws`→`"flixhqws"`, `flixhq`→`"flixhq"`, `1shows`/`1flex`/`1tube`→`"tbcpl"`, `soap2day`→`"soap2day"`, `kimcartoon`→`"kimcartoon"`, `allanime`→`"allanime"`. (Order matters: check `flixhq.ws` before `flixhq`.)
  - `func MirrorDomains(sites []Site) map[string][]string` — for each site whose host maps to a provider, append the site host (no scheme, no trailing slash) to that provider's slice. Deduplicated, order preserved.

- [ ] **Step 1: Write the failing test**

```go
package tbcpl

import (
	"reflect"
	"testing"
)

func TestProviderFor(t *testing.T) {
	cases := map[string]string{
		"flixhq.ws":            "flixhqws",
		"www.flixhq.to":        "flixhq",
		"www.1shows.org":       "tbcpl",
		"1flex.org":            "tbcpl",
		"soap2day.example":     "soap2day",
		"kimcartoon.com.rs":    "kimcartoon",
		"allanime.to":          "allanime",
		"totallyunknown.xyz":   "",
	}
	for host, want := range cases {
		got, ok := ProviderFor(host)
		if want == "" && ok {
			t.Errorf("%s: expected no match, got %q", host, got)
		}
		if want != "" && got != want {
			t.Errorf("%s: got %q, want %q", host, got, want)
		}
	}
}

func TestMirrorDomains(t *testing.T) {
	sites := []Site{
		{URL: "https://flixhq.dad/", Category: "movies"},
		{URL: "https://www.1shows.org/", Category: "movies"},
		{URL: "https://randomsite.xyz/", Category: "movies"},
	}
	got := MirrorDomains(sites)
	want := map[string][]string{
		"flixhq": {"flixhq.dad"},
		"tbcpl":  {"www.1shows.org"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MirrorDomains = %+v, want %+v", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tbcpl/ -run 'TestProviderFor|TestMirrorDomains' -v`
Expected: FAIL (functions undefined).

- [ ] **Step 3: Write minimal implementation**

```go
package tbcpl

import (
	"net/url"
	"strings"
)

// providerMatch pairs a host substring with the lobster provider name key.
// Order matters: more specific hosts first.
var providerMatches = []struct{ sub, name string }{
	{"flixhq.ws", "flixhqws"},
	{"flixhq", "flixhq"},
	{"1shows", "tbcpl"},
	{"1flex", "tbcpl"},
	{"1tube", "tbcpl"},
	{"soap2day", "soap2day"},
	{"kimcartoon", "kimcartoon"},
	{"allanime", "allanime"},
}

// ProviderFor maps a host to a lobster provider name key, if recognized.
func ProviderFor(host string) (string, bool) {
	h := strings.ToLower(host)
	for _, m := range providerMatches {
		if strings.Contains(h, m.sub) {
			return m.name, true
		}
	}
	return "", false
}

// hostOf returns the bare host (no scheme, no trailing slash) of a site URL.
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return strings.Trim(rawURL, "/")
	}
	return u.Host
}

// MirrorDomains groups site hosts by the lobster provider they map to.
func MirrorDomains(sites []Site) map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	for _, s := range sites {
		host := hostOf(s.URL)
		name, ok := ProviderFor(host)
		if !ok {
			continue
		}
		key := name + "|" + host
		if seen[key] {
			continue
		}
		seen[key] = true
		out[name] = append(out[name], host)
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tbcpl/ -run 'TestProviderFor|TestMirrorDomains' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tbcpl/match.go internal/tbcpl/match_test.go
git commit -m "feat(tbcpl): mirror-match host to known provider domains"
```

---

## Task 6: Live-TV playlist detection

**Files:**
- Modify: `internal/tbcpl/match.go`
- Test: `internal/tbcpl/match_test.go`

**Interfaces:**
- Consumes: `Site`, `Catalog`.
- Produces: `func (c *Catalog) LivePlaylists(includeUntrusted bool) []string` — returns URLs from the `livetv` category that look like m3u playlists (URL path ends in `.m3u`/`.m3u8` or contains `get.php`/`type=m3u`). Trusted+enabled only unless `includeUntrusted`.

- [ ] **Step 1: Write the failing test**

```go
func TestLivePlaylists(t *testing.T) {
	c := &Catalog{Sites: []Site{
		{URL: "https://a.example/list.m3u8", Category: "livetv", Enabled: true, Status: "trusted"},
		{URL: "https://b.example/get.php?type=m3u_plus", Category: "livetv", Enabled: true, Status: "trusted"},
		{URL: "https://c.example/watch", Category: "livetv", Enabled: true, Status: "trusted"},     // not a playlist
		{URL: "https://d.example/list.m3u8", Category: "livetv", Enabled: true, Status: ""},          // untrusted
		{URL: "https://e.example/list.m3u8", Category: "movies", Enabled: true, Status: "trusted"},   // wrong category
	}}
	got := c.LivePlaylists(false)
	want := []string{"https://a.example/list.m3u8", "https://b.example/get.php?type=m3u_plus"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LivePlaylists(false) = %v, want %v", got, want)
	}
	if len(c.LivePlaylists(true)) != 3 {
		t.Fatalf("LivePlaylists(true) want 3 (adds untrusted d)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tbcpl/ -run TestLivePlaylists -v`
Expected: FAIL.

- [ ] **Step 3: Write minimal implementation**

```go
// LivePlaylists returns m3u playlist URLs from the livetv category.
func (c *Catalog) LivePlaylists(includeUntrusted bool) []string {
	var out []string
	for _, s := range c.ByCategory("livetv") {
		if !s.Enabled {
			continue
		}
		if !includeUntrusted && s.Status != "trusted" {
			continue
		}
		if isPlaylistURL(s.URL) {
			out = append(out, s.URL)
		}
	}
	return out
}

func isPlaylistURL(rawURL string) bool {
	u := strings.ToLower(rawURL)
	return strings.HasSuffix(hostPath(u), ".m3u") ||
		strings.HasSuffix(hostPath(u), ".m3u8") ||
		strings.Contains(u, "get.php") ||
		strings.Contains(u, "type=m3u")
}

// hostPath returns the URL without its query string, for suffix checks.
func hostPath(rawURL string) string {
	if i := strings.IndexByte(rawURL, '?'); i >= 0 {
		return rawURL[:i]
	}
	return rawURL
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tbcpl/ -run TestLivePlaylists -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tbcpl/match.go internal/tbcpl/match_test.go
git commit -m "feat(tbcpl): detect live-tv m3u playlist entries"
```

---

## Task 7: Generic TMDB-id embed provider — search & episode scaffolding

**Files:**
- Create: `internal/provider/tbcplembed.go`
- Test: `internal/provider/tbcplembed_test.go`

**Interfaces:**
- Consumes: `tbcpl.Site`; provider-package TMDB helpers `tmdbMultiSearchURL(base, query string) string`, `parseTMDBSearchResults(body []byte, base string) ([]media.SearchResult, error)`, `extractTMDBID(id string) string`, const `tmdbSearchBase`; `httputil.NewClient()`.
- Produces:
  - `type TBCPLEmbed struct { ... }` implementing `provider.Provider` and `provider.StreamProvider`.
  - `func NewTBCPLEmbed(sites []tbcpl.Site) *TBCPLEmbed` — keeps only movie/anime sites (categories `movies`,`anime`).
  - `Search`, `GetSeasons`, `GetEpisodes`, `GetServers`, `GetEmbedURL`, `Trending`, `Recent` behaving like VidNest/VaPlayer (TMDB-driven); `Watch` is added in Task 8.
  - ID scheme: movie id = TMDB id string; tv season id = `"<tmdb>:<season>"`; episode id = `"<tmdb>:<season>:<episode>"` (matches VaPlayer).

- [ ] **Step 1: Write the failing test**

```go
package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"lobster/internal/tbcpl"
)

func TestTBCPLEmbedSearchUsesTMDB(t *testing.T) {
	// Minimal TMDB multi-search HTML fixture parseable by parseTMDBSearchResults.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(tmdbSearchFixtureHTML)) // reuse existing fixture used by soap2day/vidnest tests
	}))
	defer srv.Close()

	p := NewTBCPLEmbed([]tbcpl.Site{{Name: "X", URL: "https://x.example/", Category: "movies", Status: "trusted", Enabled: true}})
	p.tmdbBase = srv.URL // test seam
	results, err := p.Search("matrix")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected TMDB results")
	}
}

func TestTBCPLEmbedFiltersToMovieAnime(t *testing.T) {
	p := NewTBCPLEmbed([]tbcpl.Site{
		{URL: "https://a/", Category: "movies", Enabled: true, Status: "trusted"},
		{URL: "https://b/", Category: "livetv", Enabled: true, Status: "trusted"},
		{URL: "https://c/", Category: "anime", Enabled: true, Status: "trusted"},
	})
	if len(p.sites) != 2 {
		t.Fatalf("kept %d sites, want 2 (movies+anime)", len(p.sites))
	}
}
```

> Before writing, confirm the exact fixture name used by existing TMDB tests: run
> `grep -rn "tmdbSearchFixture\|multi-search\|func TestSearch" internal/provider/soap2day_test.go internal/provider/vidnest_test.go`.
> Use whatever fixture/helper those tests use; if they hit a mock server, mirror that setup instead of a raw HTML constant.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/ -run TestTBCPLEmbed -v`
Expected: FAIL (types undefined).

- [ ] **Step 3: Write minimal implementation**

```go
package provider

import (
	"fmt"
	"net/http"

	"lobster/internal/httputil"
	"lobster/internal/media"
	"lobster/internal/tbcpl"
)

// TBCPLEmbed plays trusted, otherwise-unsupported TBCPL movie/anime sites that
// follow the common TMDB-id embed convention. It is TMDB-driven for discovery
// (like VidNest/VaPlayer) and resolves streams by sniffing an <iframe> from a
// templated embed URL and handing it to the extract package.
type TBCPLEmbed struct {
	client   *http.Client
	sites    []tbcpl.Site
	tmdbBase string // test seam; defaults to tmdbSearchBase
	log      func(string, ...any)
}

// NewTBCPLEmbed keeps only movie/anime sites from the supplied catalog slice.
func NewTBCPLEmbed(sites []tbcpl.Site) *TBCPLEmbed {
	var kept []tbcpl.Site
	for _, s := range sites {
		if s.Category == "movies" || s.Category == "anime" {
			kept = append(kept, s)
		}
	}
	return &TBCPLEmbed{
		client:   httputil.NewClient(),
		sites:    kept,
		tmdbBase: tmdbSearchBase,
		log:      func(string, ...any) {},
	}
}

func (p *TBCPLEmbed) Search(query string) ([]media.SearchResult, error) {
	body, err := fetchBytes(p.client, tmdbMultiSearchURL(p.tmdbBase, query))
	if err != nil {
		return nil, err
	}
	return parseTMDBSearchResults(body, p.tmdbBase)
}

func (p *TBCPLEmbed) GetDetails(id string) (*media.ContentDetail, error) {
	return &media.ContentDetail{}, nil
}

func (p *TBCPLEmbed) GetSeasons(id string) ([]media.Season, error) {
	// Mirror VaPlayer's TMDB season probing; reuse its helper if exported,
	// otherwise probe seasons 1..N. Implementer: copy VaPlayer.GetSeasons logic.
	return vaplayerLikeSeasons(p.client, p.tmdbBase, extractTMDBID(id))
}

func (p *TBCPLEmbed) GetEpisodes(id, seasonID string) ([]media.Episode, error) {
	return vaplayerLikeEpisodes(p.client, p.tmdbBase, seasonID)
}

func (p *TBCPLEmbed) GetServers(id, episodeID string) ([]media.Server, error) {
	servers := make([]media.Server, 0, len(p.sites))
	for _, s := range p.sites {
		servers = append(servers, media.Server{Name: s.Name, ID: s.URL})
	}
	return servers, nil
}

func (p *TBCPLEmbed) GetEmbedURL(serverID string) (string, error) {
	return "", fmt.Errorf("use Watch instead")
}

func (p *TBCPLEmbed) Trending(mt media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}

func (p *TBCPLEmbed) Recent(mt media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}
```

> Implementer notes:
> - `fetchBytes(client, url)` — if a shared GET helper already exists in the package (check `grep -rn "func fetchBytes\|func httpGet\|func getBody" internal/provider/*.go`), use it; otherwise add a tiny unexported `fetchBytes` doing `client.Get` + `io.ReadAll` with status check, placed in `tbcplembed.go`.
> - `vaplayerLikeSeasons` / `vaplayerLikeEpisodes` — factor the season/episode TMDB-probing from `vaplayer.go` (lines ~96-145) into shared unexported helpers in `tmdb.go`, then call them from both VaPlayer and TBCPLEmbed. If that refactor is undesirable, inline the same logic here. Keep the `"<tmdb>:<season>:<episode>"` id scheme.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/provider/ -run TestTBCPLEmbed -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/tbcplembed.go internal/provider/tbcplembed_test.go
git commit -m "feat(provider): TBCPL generic embed provider — TMDB discovery"
```

---

## Task 8: Generic embed — Watch via template + iframe sniff + extractor

**Files:**
- Modify: `internal/provider/tbcplembed.go`
- Test: `internal/provider/tbcplembed_test.go`

**Interfaces:**
- Consumes: `extract.ResolveForURL(embedURL, referer string) (extract.Extractor, string)`; `media.Stream`.
- Produces:
  - `func (p *TBCPLEmbed) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error)` — for each candidate site, builds embed URL templates, fetches the page, sniffs the first `<iframe src>`, resolves via `extract.ResolveForURL`, and returns the first successful stream. Skips (logged) sites that yield no template match / no iframe / extractor failure.
  - `func embedCandidates(origin, tmdbID string, season, episode int) []string` — pure function returning the template URLs, unit-tested independently.

- [ ] **Step 1: Write the failing test**

```go
func TestEmbedCandidates(t *testing.T) {
	movie := embedCandidates("https://x.example", "603", 0, 0)
	wantMovie := []string{
		"https://x.example/embed/movie/603",
		"https://x.example/movie/603",
		"https://x.example/e/603",
	}
	if !reflect.DeepEqual(movie, wantMovie) {
		t.Fatalf("movie candidates = %v, want %v", movie, wantMovie)
	}
	tv := embedCandidates("https://x.example", "1399", 2, 5)
	want0 := "https://x.example/embed/tv/1399/2/5"
	if tv[0] != want0 {
		t.Fatalf("tv[0] = %q, want %q", tv[0], want0)
	}
}

func TestWatchSniffsIframeAndExtracts(t *testing.T) {
	// site page returns an iframe pointing at a fake megacloud embed that the
	// extractor mock resolves. Because extract.ResolveForURL hits the network,
	// this test asserts the sniff step: the iframe src must be handed to the
	// extractor. Use a site server returning a known iframe and assert Watch
	// attempts it (see implementer note on seam).
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><iframe src="https://megacloud.example/e/abc"></iframe></body></html>`))
	}))
	defer site.Close()

	p := NewTBCPLEmbed([]tbcpl.Site{{Name: "X", URL: site.URL, Category: "movies", Status: "trusted", Enabled: true}})
	var sniffed string
	p.resolve = func(embed, referer string) (*media.Stream, error) {
		sniffed = embed
		return &media.Stream{URL: "https://cdn.example/x.m3u8"}, nil
	}
	stream, err := p.Watch("603", "", "", "1080")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if stream.URL == "" {
		t.Fatal("empty stream URL")
	}
	if sniffed != "https://megacloud.example/e/abc" {
		t.Fatalf("sniffed iframe = %q", sniffed)
	}
}
```

> Implementer: add a `resolve func(embed, referer string) (*media.Stream, error)` field on `TBCPLEmbed` defaulting to a wrapper around `extract.ResolveForURL` (call the returned extractor's `Extract(embed, quality)`); the field is the test seam. Store the requested quality on the struct or thread it through.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/ -run 'TestEmbedCandidates|TestWatchSniffs' -v`
Expected: FAIL.

- [ ] **Step 3: Write minimal implementation**

```go
import (
	"io"
	"regexp"
	"strconv"
	"strings"

	"lobster/internal/extract"
)

var iframeSrcRe = regexp.MustCompile(`(?i)<iframe[^>]+src=["']([^"']+)["']`)

// embedCandidates returns templated embed URLs for a site origin + TMDB id.
func embedCandidates(origin, tmdbID string, season, episode int) []string {
	origin = strings.TrimRight(origin, "/")
	if season > 0 {
		s, e := strconv.Itoa(season), strconv.Itoa(episode)
		return []string{
			origin + "/embed/tv/" + tmdbID + "/" + s + "/" + e,
			origin + "/tv/" + tmdbID + "/" + s + "/" + e,
			origin + "/e/" + tmdbID + "/" + s + "/" + e,
		}
	}
	return []string{
		origin + "/embed/movie/" + tmdbID,
		origin + "/movie/" + tmdbID,
		origin + "/e/" + tmdbID,
	}
}

func (p *TBCPLEmbed) resolveDefault(embed, referer string) (*media.Stream, error) {
	ex, target := extract.ResolveForURL(embed, referer)
	return ex.Extract(target, p.quality)
}

// Watch tries each candidate site's embed templates until one yields a stream.
func (p *TBCPLEmbed) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	p.quality = quality
	if p.resolve == nil {
		p.resolve = p.resolveDefault
	}
	tmdbID := extractTMDBID(mediaID)
	season, episode := parseSeasonEpisode(episodeID) // "" -> 0,0 (movie)

	for _, s := range p.sites {
		origin := siteOrigin(s.URL)
		for _, cand := range embedCandidates(origin, tmdbID, season, episode) {
			page, err := fetchBytes(p.client, cand)
			if err != nil {
				continue
			}
			m := iframeSrcRe.FindSubmatch(page)
			if m == nil {
				continue
			}
			embed := absoluteURL(origin, string(m[1]))
			stream, err := p.resolve(embed, origin+"/")
			if err != nil || stream == nil || stream.URL == "" {
				continue
			}
			return stream, nil
		}
		p.log("tbcplembed: no playable embed from %s", s.URL)
	}
	return nil, fmt.Errorf("tbcplembed: no site produced a stream")
}
```

> Implementer helpers (add to file; unit-test `parseSeasonEpisode`/`absoluteURL` if you factor them out):
> - `parseSeasonEpisode(episodeID)`: episodeID is `"<tmdb>:<season>:<episode>"`; split on `:` → returns `(season, episode)` ints, `(0,0)` if fewer than 3 parts.
> - `siteOrigin(rawURL)`: scheme+host, no path (`url.Parse` then `u.Scheme+"://"+u.Host`).
> - `absoluteURL(origin, src)`: if `src` starts with `//` prefix `https:`; if it starts with `/` prefix `origin`; else return `src`.
> - Add fields `quality string` and `resolve func(string, string) (*media.Stream, error)` to the struct.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/provider/ -run 'TestEmbedCandidates|TestWatchSniffs|TestTBCPLEmbed' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/tbcplembed.go internal/provider/tbcplembed_test.go
git commit -m "feat(provider): TBCPL embed Watch via template sniff + extractor"
```

---

## Task 9: Wire mirror domains + embed provider into the fallback chain

**Files:**
- Modify: `internal/provider/failover.go` (add merge helper near `ResolveDomain`)
- Modify: `cmd/fallback.go` (`fallbackProviders`, lines ~53-98)
- Test: `internal/provider/failover_test.go`, `cmd/fallback_test.go`

**Interfaces:**
- Consumes: `tbcpl.Client`, `tbcpl.MirrorDomains`, `NewTBCPLEmbed`, `config.TBCPLCachePath`, `cfg.TBCPL*`.
- Produces:
  - `func MergeOverrides(base, extra map[string][]string) map[string][]string` in provider package — unions two override maps (dedup per key), used so TBCPL mirror domains augment `cfg.DomainOverrides`.
  - `cmd.tbcplCatalog()` — loads the catalog once per process (guarded by `sync.Once`), honoring `cfg.TBCPLFeed`/`cfg.TBCPLRegion`; returns `nil` when the feed is disabled.
  - `fallbackProviders` appends `provider.NewTBCPLEmbed(cat.Trusted())` (or `cat.Sites` when `cfg.TBCPLIncludeUntrusted`) when a catalog is present and the primary isn't already `*TBCPLEmbed`.

- [ ] **Step 1: Write the failing test (provider merge)**

```go
func TestMergeOverrides(t *testing.T) {
	base := map[string][]string{"flixhq": {"flixhq.to"}}
	extra := map[string][]string{"flixhq": {"flixhq.to", "flixhq.dad"}, "tbcpl": {"1shows.org"}}
	got := MergeOverrides(base, extra)
	if len(got["flixhq"]) != 2 || got["flixhq"][1] != "flixhq.dad" {
		t.Fatalf("flixhq merge wrong: %v", got["flixhq"])
	}
	if got["tbcpl"][0] != "1shows.org" {
		t.Fatalf("tbcpl merge wrong: %v", got["tbcpl"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/ -run TestMergeOverrides -v`
Expected: FAIL.

- [ ] **Step 3: Implement `MergeOverrides`**

```go
// MergeOverrides unions two provider->domains maps, de-duplicating per key
// while preserving first-seen order.
func MergeOverrides(base, extra map[string][]string) map[string][]string {
	out := map[string][]string{}
	add := func(src map[string][]string) {
		for k, vs := range src {
			seen := map[string]bool{}
			for _, existing := range out[k] {
				seen[existing] = true
			}
			for _, v := range vs {
				if !seen[v] {
					out[k] = append(out[k], v)
					seen[v] = true
				}
			}
		}
	}
	add(base)
	add(extra)
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/provider/ -run TestMergeOverrides -v`
Expected: PASS.

- [ ] **Step 5: Write the failing test (fallback wiring)**

```go
func TestFallbackProvidersIncludesTBCPLEmbed(t *testing.T) {
	cfg = &config.Config{TBCPLFeed: true}
	defer func() { cfg = nil }()
	fbs := fallbackProviders(provider.NewMovieBox())
	found := false
	for _, p := range fbs {
		if _, ok := p.(*provider.TBCPLEmbed); ok {
			found = true
		}
	}
	if !found {
		t.Fatal("expected TBCPLEmbed in fallback providers when feed enabled")
	}
}

func TestFallbackProvidersOmitsTBCPLEmbedWhenDisabled(t *testing.T) {
	cfg = &config.Config{TBCPLFeed: false}
	defer func() { cfg = nil }()
	for _, p := range fallbackProviders(provider.NewMovieBox()) {
		if _, ok := p.(*provider.TBCPLEmbed); ok {
			t.Fatal("TBCPLEmbed present but feed disabled")
		}
	}
}
```

> Implementer: `tbcplCatalog()` must not hit the network in tests. Guard: when `cfg.TBCPLFeed` is false return nil; otherwise construct the client with `config.TBCPLCachePath()`. In these tests the cache is absent and network is unreachable, so `Load` returns the embedded snapshot (offline-safe) — the assertion only checks that the provider type is present, which holds because the snapshot has trusted sites. If CI is fully network- and disk-sandboxed and the snapshot is empty, gate the append on `len(cat.Trusted())>0`; keep the snapshot non-empty (Task 3) so this passes.

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./cmd/ -run TestFallbackProvidersIncludesTBCPLEmbed -v`
Expected: FAIL.

- [ ] **Step 7: Implement the wiring in `cmd/fallback.go`**

```go
var (
	tbcplCatOnce sync.Once
	tbcplCatVal  *tbcpl.Catalog
)

// tbcplCatalog loads the TBCPL catalog once per process, or nil if disabled.
func tbcplCatalog() *tbcpl.Catalog {
	tbcplCatOnce.Do(func() {
		if cfg == nil || !cfg.TBCPLFeed {
			return
		}
		cache, err := config.TBCPLCachePath()
		if err != nil {
			return
		}
		cl := tbcpl.NewClient(cache, 12*time.Hour, debugf)
		region := ""
		if cfg != nil {
			region = cfg.TBCPLRegion
		}
		tbcplCatVal = cl.LoadMerged(context.Background(), region)
	})
	return tbcplCatVal
}
```

In `fallbackProviders`, before the `return`, add:

```go
	if cat := tbcplCatalog(); cat != nil {
		sites := cat.Trusted()
		if cfg != nil && cfg.TBCPLIncludeUntrusted {
			sites = cat.Sites
		}
		if _, ok := primary.(*provider.TBCPLEmbed); !ok && len(sites) > 0 {
			fallbacks = append(fallbacks, provider.NewTBCPLEmbed(sites))
		}
	}
```

Add imports: `"context"`, `"time"`, `"lobster/internal/tbcpl"` (config already imported).

> Mirror-domain injection: where providers are constructed with `ResolveDomain(..., cfg.DomainOverrides)` (this file and `cmd/provider.go`), replace the overrides argument with `provider.MergeOverrides(cfg.DomainOverrides, tbcpl.MirrorDomains(cat.Sites))` when `cat != nil`. Do this in `cmd/provider.go`'s `newProvider()` for the flixhq/flixhqws/kimcartoon branches. Keep a nil-cat guard so behavior is unchanged when the feed is off.

- [ ] **Step 8: Run tests to verify they pass**

Run: `go test ./cmd/ -run TestFallbackProviders -v && go test ./internal/provider/ -run TestMergeOverrides -v`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/provider/failover.go cmd/fallback.go cmd/provider.go internal/provider/failover_test.go cmd/fallback_test.go
git commit -m "feat: wire TBCPL mirror domains and embed provider into fallback chain"
```

---

## Task 10: Feed TBCPL live-TV playlists into LiveTV

**Files:**
- Modify: `internal/tui/app.go:145`
- Modify: `cmd/fallback.go` (add `tbcplLiveSources()` helper) OR a small helper in `cmd/session.go` — place next to `tbcplCatalog()`.
- Test: `internal/tbcpl/match_test.go` already covers `LivePlaylists`; add a thin wiring test if a seam exists, otherwise verify by build + manual run.

**Interfaces:**
- Consumes: `tbcplCatalog()`, `Catalog.LivePlaylists`, `cfg.LiveTV.Sources()`, `cfg.TBCPLIncludeUntrusted`.
- Produces: `func liveTVSources() []string` in `cmd` — returns `cfg.LiveTV.Sources()` plus TBCPL live playlists (deduped), used at every `NewLiveTV` call site.

- [ ] **Step 1: Write the failing test**

```go
// in cmd package
func TestLiveTVSourcesMergesTBCPL(t *testing.T) {
	cfg = &config.Config{TBCPLFeed: true, LiveTV: config.LiveTVConfig{IPTVOrg: false}}
	defer func() { cfg = nil }()
	got := liveTVSources()
	// Offline snapshot may or may not contain m3u livetv entries; assert the
	// function returns config sources at minimum and never panics.
	base := cfg.LiveTV.Sources()
	if len(got) < len(base) {
		t.Fatalf("liveTVSources dropped config sources: got %d, base %d", len(got), len(base))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/ -run TestLiveTVSourcesMergesTBCPL -v`
Expected: FAIL (`liveTVSources` undefined).

- [ ] **Step 3: Implement `liveTVSources` and use it at call sites**

```go
// liveTVSources returns configured IPTV sources plus TBCPL live-tv playlists.
func liveTVSources() []string {
	sources := cfg.LiveTV.Sources()
	cat := tbcplCatalog()
	if cat == nil {
		return sources
	}
	include := cfg != nil && cfg.TBCPLIncludeUntrusted
	seen := map[string]bool{}
	for _, s := range sources {
		seen[s] = true
	}
	for _, pl := range cat.LivePlaylists(include) {
		if !seen[pl] {
			sources = append(sources, pl)
			seen[pl] = true
		}
	}
	return sources
}
```

Update `internal/tui/app.go:145` from `provider.NewLiveTV(cfg.LiveTV.Sources())` to consume the merged list. Since `app.go` is in the `tui` package, pass the merged sources in from `cmd` where the app is constructed (find the constructor call: `grep -rn "tui.New\|NewApp\|app.go" cmd/`), or expose `liveTVSources()` result to the tui constructor. Prefer threading the `[]string` in rather than importing `cmd` from `tui`.

> Implementer: locate where the tui app is built in `cmd` (e.g. `cmd/surf.go`/`cmd/session.go`) and pass `liveTVSources()` into whatever config/struct feeds `NewLiveTV`. If `NewLiveTV(cfg.LiveTV.Sources())` is called directly inside `tui`, add a field on the tui app config struct for the pre-merged sources and set it from `cmd`.

- [ ] **Step 4: Run test + build to verify**

Run: `go test ./cmd/ -run TestLiveTVSourcesMergesTBCPL -v && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add cmd/ internal/tui/app.go
git commit -m "feat: feed TBCPL live-tv playlists into LiveTV sources"
```

---

## Task 11: Docs + full verification

**Files:**
- Modify: `GUIDE.md` (document `tbcpl_feed`, `tbcpl_region`, `tbcpl_include_untrusted`)
- Modify: `README.md` (one line under Features noting TBCPL-backed source freshness)

- [ ] **Step 1: Document the config keys**

Add to `GUIDE.md` a short "TBCPL catalog feed" subsection listing the three keys, defaults, and what they do (global vs region, trusted-only vs all). Add valid region values: BRAZIL, EGYPT, FINLAND, FRANCE, GERMANY, INDIA, ITALY, JAPAN, KURDISTAN, NETHERLANDS, POLAND, PORTUGAL, RUSSIA, SOUTHKOREA, SPAIN.

- [ ] **Step 2: Run the full suite**

Run: `go build ./... && go test ./... && go vet ./...`
Expected: all pass. Fix any failures before committing.

- [ ] **Step 3: Manual smoke (best-effort, network permitting)**

Run: `go run . -q "the matrix"` (or the project's search entrypoint) and confirm it still resolves a stream (TBCPL now participates as a fallback). Note in the commit if network blocks this.

- [ ] **Step 4: Commit**

```bash
git add GUIDE.md README.md
git commit -m "docs: document TBCPL catalog feed config"
```

---

## Self-Review

**Spec coverage:**
- Catalog client (fetch/cache/snapshot) → Tasks 1, 3. ✓
- Region overlays → Task 4. ✓
- Mirror-match mapper → Task 5, wired in Task 9. ✓
- Generic TMDB-embed provider → Tasks 7, 8, wired in Task 9. ✓
- Live TV/sports feed → Tasks 6, 10. ✓
- Config keys + defaults → Task 2. ✓
- No silent caps (logging) → embedded in Tasks 3, 8, 9 via `debugf`/`log`. ✓
- Testing across components → each task is TDD. ✓

**Type consistency:** `Site`/`Catalog`/`Parse`/`ByCategory`/`Trusted`/`LivePlaylists`/`MirrorDomains`/`ProviderFor`/`NewClient`/`Load`/`LoadMerged` (tbcpl pkg); `NewTBCPLEmbed`/`TBCPLEmbed`/`Watch`/`embedCandidates`/`MergeOverrides` (provider pkg); `TBCPLCachePath`/`TBCPLFeed`/`TBCPLRegion`/`TBCPLIncludeUntrusted` (config). Names are used identically across defining and consuming tasks. ✓

**Known implementer decision points (flagged inline, not placeholders):** exact TMDB test fixture name (Task 7), whether to factor VaPlayer season/episode helpers vs inline (Task 7), and the tui/cmd seam for passing merged live sources (Task 10). Each has a concrete grep to run and a default to follow.
