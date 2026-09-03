# Revive Dead Providers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Re-add FlixHQ to the fallback chain behind a parallel health gate so it revives automatically when any mirror returns, and retire AllAnime from the chain.

**Architecture:** A new `FirstHealthyDomain` in `internal/provider/failover.go` probes all candidate domains in parallel (bounded by one probe timeout) and returns the first healthy one by list preference; a memoized wrapper caches the answer per provider name for the process lifetime. `cmd/fallback.go` uses it to conditionally add FlixHQ and drops AllAnime (Cloudflare bot-challenge + crypto-gated watch path; AniPub covers anime).

**Tech Stack:** Go 1.22+, stdlib only (`net/http`, `net`, `sync`, `httptest` for tests).

## Global Constraints

- No new scrapers — reuse the existing `FlixHQ` scraper (`NewFlixHQ(base string)`); sflix.to / myflixerz.to run the identical engine.
- Tests must never touch the live network: all probes hit `httptest` servers via the `healthURLFor` seam.
- No `Co-Authored-By` lines in commits.
- Run `go test ./...` (not just the package) before each commit.
- Spec: `docs/superpowers/specs/2026-09-01-revive-dead-providers-design.md`.

---

### Task 1: Parallel `FirstHealthyDomain` in the provider package

**Files:**
- Modify: `internal/provider/failover.go`
- Create: `internal/provider/failover_test.go`

**Interfaces:**
- Consumes: existing `checkDomainHealth(domain string) bool`, `knownDomains`.
- Produces: `func FirstHealthyDomain(providerName string, overrides map[string][]string) string` — probes all candidates in parallel, returns the first healthy domain in preference order (overrides first, then `knownDomains[providerName]`), or `""` if none respond. Package var `healthURLFor func(domain string) string` (test seam). Package var `probeTimeout = 3 * time.Second`.

- [ ] **Step 1: Write the failing tests**

```go
package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// pointProbesAt makes every probed "domain" hit the given handler by rewriting
// the probe URL. The domain string itself is ignored except when it matches a
// key in routes, which maps domain -> httptest server URL. Unrouted domains
// get an unreachable address so they fail fast.
func pointProbesAt(t *testing.T, routes map[string]string) {
	t.Helper()
	old := healthURLFor
	healthURLFor = func(domain string) string {
		if u, ok := routes[domain]; ok {
			return u
		}
		return "http://127.0.0.1:1/" // closed port: immediate refusal
	}
	t.Cleanup(func() { healthURLFor = old })
}

func TestFirstHealthyDomainPrefersListOrder(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	pointProbesAt(t, map[string]string{"b.example": ok.URL, "c.example": ok.URL})

	got := FirstHealthyDomain("testprov", map[string][]string{
		"testprov": {"a.example", "b.example", "c.example"},
	})
	if got != "b.example" {
		t.Fatalf("got %q, want b.example (first healthy in preference order)", got)
	}
}

func TestFirstHealthyDomainAllDeadReturnsEmpty(t *testing.T) {
	pointProbesAt(t, nil)
	if got := FirstHealthyDomain("testprov", map[string][]string{"testprov": {"a.example", "b.example"}}); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestFirstHealthyDomainHangingProbeDoesNotBlock(t *testing.T) {
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second)
	}))
	defer hang.Close()
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	pointProbesAt(t, map[string]string{"hang.example": hang.URL, "ok.example": ok.URL})

	oldTimeout := probeTimeout
	probeTimeout = 500 * time.Millisecond
	t.Cleanup(func() { probeTimeout = oldTimeout })

	start := time.Now()
	got := FirstHealthyDomain("testprov", map[string][]string{
		"testprov": {"hang.example", "ok.example"},
	})
	elapsed := time.Since(start)
	if got != "ok.example" {
		t.Fatalf("got %q, want ok.example", got)
	}
	// Parallel: total time ~ one probe timeout, not the sum of probes.
	if elapsed > 2*time.Second {
		t.Fatalf("gate took %s; probes are not parallel or timeout ignored", elapsed)
	}
}

func TestFirstHealthyDomainUsesKnownDomains(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	oldKnown := knownDomains["_fhd_test"]
	knownDomains["_fhd_test"] = []string{"known.example"}
	t.Cleanup(func() {
		if oldKnown == nil {
			delete(knownDomains, "_fhd_test")
		} else {
			knownDomains["_fhd_test"] = oldKnown
		}
	})
	pointProbesAt(t, map[string]string{"known.example": ok.URL})

	if got := FirstHealthyDomain("_fhd_test", nil); got != "known.example" {
		t.Fatalf("got %q, want known.example", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/provider/ -run TestFirstHealthyDomain -v`
Expected: FAIL to build — `healthURLFor`, `probeTimeout`, `FirstHealthyDomain` undefined.

- [ ] **Step 3: Implement in `internal/provider/failover.go`**

Refactor `checkDomainHealth` to use the seam and timeout var, then add the parallel gate. Replace the existing `checkDomainHealth` and add below `knownDomains`:

```go
// healthURLFor maps a domain to the URL probed for health. A package var so
// tests can point probes at httptest servers instead of the live network.
var healthURLFor = func(domain string) string { return "https://" + domain + "/" }

// probeTimeout bounds a single health probe. Package var for tests.
var probeTimeout = 3 * time.Second

// checkDomainHealth sends a HEAD request to the domain's health URL and
// returns true if the server responds with a non-5xx status in time.
func checkDomainHealth(domain string) bool {
	// Explicit dialer with Happy Eyeballs (FallbackDelay > 0): a broken IPv6
	// route must not make a live domain look dead — the dial races v6 and v4
	// and takes whichever connects. Go enables this by default; the explicit
	// dialer pins the behavior so a future custom transport can't lose it.
	client := &http.Client{
		Timeout: probeTimeout,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:       probeTimeout,
				FallbackDelay: 100 * time.Millisecond,
			}).DialContext,
		},
	}
	resp, err := client.Head(healthURLFor(domain))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

// candidateDomains returns the preference-ordered candidate list for a
// provider: config overrides first (case-insensitive key match), then the
// built-in known domains.
func candidateDomains(providerName string, overrides map[string][]string) []string {
	var candidates []string
	if overrides != nil {
		lowerName := strings.ToLower(providerName)
		for key, domains := range overrides {
			if strings.ToLower(key) == lowerName {
				candidates = append(candidates, domains...)
				break
			}
		}
	}
	return append(candidates, knownDomains[providerName]...)
}

// FirstHealthyDomain probes every candidate domain for a provider in parallel
// and returns the first healthy one in preference order, or "" if none
// respond. Wall-clock cost is roughly one probeTimeout regardless of how many
// candidates are dead, which is what makes gating a usually-dead provider
// affordable.
func FirstHealthyDomain(providerName string, overrides map[string][]string) string {
	candidates := candidateDomains(providerName, overrides)
	if len(candidates) == 0 {
		return ""
	}
	healthy := make([]bool, len(candidates))
	var wg sync.WaitGroup
	for i, d := range candidates {
		wg.Add(1)
		go func(i int, d string) {
			defer wg.Done()
			healthy[i] = checkDomainHealth(d)
		}(i, d)
	}
	wg.Wait()
	for i, ok := range healthy {
		if ok {
			return candidates[i]
		}
	}
	return ""
}
```

Add `"net"` to the imports of `failover.go`. Also update `ResolveDomain`'s inline candidate-building block (lines building `candidates` from overrides + knownDomains) to call `candidateDomains(providerName, overrides)` instead of duplicating it. Add `"sync"` to imports. `ResolveDomain`'s behavior is otherwise unchanged.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/provider/ -run 'TestFirstHealthyDomain|TestResolveDomain' -v` then `go test ./...`
Expected: PASS (the hanging-probe test takes ~0.5–1s, not 10s).

- [ ] **Step 5: Commit**

```bash
git add internal/provider/failover.go internal/provider/failover_test.go
git commit -m "feat(failover): parallel FirstHealthyDomain gate with test seam"
```

---

### Task 2: Session cache + seeded flixhq mirror list

**Files:**
- Modify: `internal/provider/failover.go`
- Modify: `internal/provider/failover_test.go` (append)

**Interfaces:**
- Consumes: `FirstHealthyDomain(providerName string, overrides map[string][]string) string` from Task 1.
- Produces: `func FirstHealthyDomainCached(providerName string, overrides map[string][]string) string` — memoizes the Task 1 result per provider name for the process lifetime (including a "" miss, so a dead provider is probed once per session, not once per search). `knownDomains["flixhq"]` seeded with the mirror list. Test helper `ResetDomainCache()` (exported for cmd tests in Task 3).

- [ ] **Step 1: Write the failing tests (append to `internal/provider/failover_test.go`)**

```go
func TestFirstHealthyDomainCachedProbesOnce(t *testing.T) {
	ResetDomainCache()
	t.Cleanup(ResetDomainCache)
	var probes int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&probes, 1)
	}))
	defer srv.Close()
	pointProbesAt(t, map[string]string{"a.example": srv.URL})
	ov := map[string][]string{"cachedprov": {"a.example"}}

	first := FirstHealthyDomainCached("cachedprov", ov)
	second := FirstHealthyDomainCached("cachedprov", ov)
	if first != "a.example" || second != "a.example" {
		t.Fatalf("got %q then %q, want a.example twice", first, second)
	}
	if n := atomic.LoadInt32(&probes); n != 1 {
		t.Fatalf("probed %d times, want 1 (cached)", n)
	}
}

func TestFirstHealthyDomainCachedCachesMiss(t *testing.T) {
	ResetDomainCache()
	t.Cleanup(ResetDomainCache)
	pointProbesAt(t, nil)
	ov := map[string][]string{"deadprov": {"a.example"}}
	if got := FirstHealthyDomainCached("deadprov", ov); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	// Second call must not re-probe; verify by making probes impossible to
	// distinguish — we just assert it still returns "" instantly.
	start := time.Now()
	if got := FirstHealthyDomainCached("deadprov", ov); got != "" {
		t.Fatalf("second call got %q, want empty", got)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("second call re-probed instead of using the cache")
	}
}

func TestKnownDomainsFlixhqSeeded(t *testing.T) {
	want := []string{"flixhq.to", "flixhq.click", "flixhq.pe", "flixhq.bz", "sflix.to", "myflixerz.to"}
	if !reflect.DeepEqual(knownDomains["flixhq"], want) {
		t.Fatalf("knownDomains[flixhq] = %v, want %v", knownDomains["flixhq"], want)
	}
}
```

Add `"reflect"` and `"sync/atomic"` to the test file imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/provider/ -run 'TestFirstHealthyDomainCached|TestKnownDomainsFlixhqSeeded' -v`
Expected: FAIL to build — `FirstHealthyDomainCached`, `ResetDomainCache` undefined; seed test fails on the old one-entry list.

- [ ] **Step 3: Implement**

In `internal/provider/failover.go`, change the `knownDomains` entry:

```go
	// flixhq.to's origin cluster has been down since ~Aug 2026 (Cloudflare 522).
	// sflix.to and myflixerz.to run the identical engine (same markup and /ajax/
	// endpoints), so the FlixHQ scraper works on them unchanged; they are listed
	// as revival candidates for whenever any of the family's origins return.
	"flixhq": {"flixhq.to", "flixhq.click", "flixhq.pe", "flixhq.bz", "sflix.to", "myflixerz.to"},
```

Add below `FirstHealthyDomain`:

```go
var (
	domainCacheMu sync.Mutex
	domainCache   = map[string]string{}
)

// FirstHealthyDomainCached memoizes FirstHealthyDomain per provider name for
// the process lifetime. A miss ("" — nothing healthy) is cached too: a dead
// provider costs one parallel probe per session, not one per search.
func FirstHealthyDomainCached(providerName string, overrides map[string][]string) string {
	domainCacheMu.Lock()
	if d, ok := domainCache[providerName]; ok {
		domainCacheMu.Unlock()
		return d
	}
	domainCacheMu.Unlock()

	d := FirstHealthyDomain(providerName, overrides)

	domainCacheMu.Lock()
	domainCache[providerName] = d
	domainCacheMu.Unlock()
	return d
}

// ResetDomainCache clears the memoized probe results. Test helper.
func ResetDomainCache() {
	domainCacheMu.Lock()
	domainCache = map[string]string{}
	domainCacheMu.Unlock()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/provider/ -v -run 'FirstHealthyDomain|KnownDomains'` then `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/failover.go internal/provider/failover_test.go
git commit -m "feat(failover): session-cached domain gate; seed flixhq mirror candidates"
```

---

### Task 3: Wire the gate into the fallback chain; retire AllAnime

**Files:**
- Modify: `cmd/fallback.go`
- Modify: `cmd/fallback_providers_test.go` (append)

**Interfaces:**
- Consumes: `provider.FirstHealthyDomainCached(name string, overrides map[string][]string) string`, `provider.ResetDomainCache()`, `provider.NewFlixHQ(base string) *FlixHQ` (existing).
- Produces: `fallbackProviders` includes `*provider.FlixHQ` iff a healthy domain exists, and never includes `*provider.AllAnime`. Package var `flixhqDomain = provider.FirstHealthyDomainCached` in `cmd/fallback.go` as the cmd-level test seam.

- [ ] **Step 1: Write the failing tests (append to `cmd/fallback_providers_test.go`)**

```go
// FlixHQ (the flixhq.to-engine scraper) is gated on a live health probe: it
// joins the chain only when some mirror in knownDomains/overrides answers, so
// a dead provider costs one parallel probe per session instead of a full
// request timeout on every search.
func TestFallbackProvidersIncludesFlixHQWhenDomainHealthy(t *testing.T) {
	prev := flixhqDomain
	flixhqDomain = func(name string, overrides map[string][]string) string { return "flixhq.to" }
	t.Cleanup(func() { flixhqDomain = prev })

	if !hasProvider[*provider.FlixHQ](fallbackProviders(nil)) {
		t.Fatal("FlixHQ missing from chain despite a healthy domain")
	}
}

func TestFallbackProvidersOmitsFlixHQWhenAllDomainsDead(t *testing.T) {
	prev := flixhqDomain
	flixhqDomain = func(name string, overrides map[string][]string) string { return "" }
	t.Cleanup(func() { flixhqDomain = prev })

	if hasProvider[*provider.FlixHQ](fallbackProviders(nil)) {
		t.Fatal("FlixHQ present in chain although no domain is healthy")
	}
}

// AllAnime is retired: its API sits behind a Cloudflare bot challenge and its
// watch path has been crypto-gated since mid-2026. AniPub covers anime.
func TestFallbackProvidersNeverIncludesAllAnime(t *testing.T) {
	if hasProvider[*provider.AllAnime](fallbackProviders(nil)) {
		t.Fatal("AllAnime should be retired from the fallback chain")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/ -run 'TestFallbackProviders' -v`
Expected: FAIL to build — `flixhqDomain` undefined (and once defined, AllAnime test fails until Step 3 completes).

- [ ] **Step 3: Implement in `cmd/fallback.go`**

Add near the top of the file (after the `sharedHealth` block):

```go
// flixhqDomain resolves a healthy FlixHQ mirror, memoized per session.
// Package var so tests can stub the probe.
var flixhqDomain = provider.FirstHealthyDomainCached
```

Replace the `// flixhq.to is gone —` comment block (cmd/fallback.go:95-98) with:

```go
	// The flixhq.to engine family (flixhq.to, sflix.to, myflixerz.to, ...) has
	// been origin-down since ~Aug 2026, so the scraper joins the chain only when
	// a health probe finds a live mirror. The probe runs in parallel across all
	// candidates and is cached for the session, so while everything is dead this
	// costs one probe timeout per run — and the provider revives automatically
	// the moment any mirror answers again.
	if _, ok := primary.(*provider.FlixHQ); !ok {
		var overrides map[string][]string
		if cfg != nil {
			overrides = cfg.DomainOverrides
		}
		if d := flixhqDomain("flixhq", overrides); d != "" {
			fallbacks = append(fallbacks, provider.NewFlixHQ(d))
		}
	}
```

Replace the AllAnime block (cmd/fallback.go:104-109, keeping the AniPub block that follows) with:

```go
	// AniPub is the anime path. AllAnime is retired: its API now sits behind a
	// Cloudflare bot challenge on top of the crypto-gated sources endpoint
	// (AA_CRYPTO_MISSING, mid-2026), so it can neither search nor stream. The
	// provider code stays for the day either gate lifts.
```

(The `if _, ok := primary.(*provider.AllAnime)` block is deleted; the `AniPub` block stays.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/ -run 'TestFallbackProviders' -v` then `go test ./...`
Expected: PASS, full suite green. Also build the binary: `go build ./...`.

- [ ] **Step 5: Verify the live behavior once**

Run: `go run . search --debug "breaking bad" 2>&1 | head -30` — expect no FlixHQ in the raced providers (all mirrors currently dead) and no multi-second stall attributable to flixhq probing beyond the single gate probe. This is an observation step; if the TUI blocks interactively, skip it and rely on the test suite.

- [ ] **Step 6: Commit**

```bash
git add cmd/fallback.go cmd/fallback_providers_test.go
git commit -m "feat(providers): health-gate FlixHQ revival, retire AllAnime from the chain"
```
