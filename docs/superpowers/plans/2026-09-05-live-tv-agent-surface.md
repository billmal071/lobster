# Live TV Agent Surface Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an agent list live TV channels and play one non-interactively, via a new `lobster channels` command and live refs accepted by `lobster play --ref`.

**Architecture:** Discovery gets its own command because the live backend has failure states `find`'s exit codes cannot express; playback reuses `play --ref` because the detach supervisor is built on that verb. A live ref carries `tvg-id` and its source playlist and is re-matched against freshly loaded playlists on every play, failing closed on absence *and* on ambiguity rather than falling through to the title-search path.

**Tech Stack:** Go 1.25, Cobra, no new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-05-live-tv-agent-surface-design.md`

## Global Constraints

- **The gate before any commit**, with `export` — a `VAR=x cmd1 && cmd2` prefix applies only to `cmd1`:
  ```bash
  export PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0
  go build ./... && go vet ./... && go test ./...
  ```
  Task 2 adds concurrency, so it additionally needs `CGO_ENABLED=1 go test ./internal/provider/ -race -count=2` (`-race` requires cgo and cannot run under the exported `CGO_ENABLED=0`).
- **No live network calls in tests.** Live TV fixtures are M3U files under `t.TempDir()`, reached through `fetch`'s `os.ReadFile` branch (`internal/provider/livetv.go:74`). Never add a network-capable seam for a test.
- **Never launch a real media player in a test.** Stub `agentPlayLive`, `agentPlayerCheck`, `agentResolveAndPlay`.
- **Watch every new test fail first, on an assertion.** A compile error is not a red — it proves a symbol is missing, not that the test detects the bug.
- **Restore package-level vars and the global `cfg` with `t.Cleanup`.**
- Never commit on a red suite. Never force-push. Never commit to `main`.
- **No AI attribution in commit messages** unless the session's active instructions require it; follow whatever attribution rule is in force at execution time, consistently across all commits in this branch.
- Agent-facing commands must be bounded. `liveLoadBudget` is the single named deadline; do not add a second timeout constant.
- Exit codes are fixed by `cmd/agentjson.go:23-26`: 1 usage, 2 no results, 3 providers failed, 4 player unavailable. **Do not add a fifth.**
- The ref type string for live is exactly `"live"`, lowercase.

---

### Task 1: `Channel.TVGID` and `Channel.Source`

Today `parseExtinf` collapses the two sources of channel identity into one field, so nothing downstream can tell an upstream `tvg-id` from a slug derived from a display name. And `doLoad` knows which playlist each channel came from but discards it. Both are needed by the ref design.

**Files:**
- Modify: `internal/provider/m3u.go:9-17` (the `Channel` struct), `internal/provider/m3u.go:56-72` (`parseExtinf`)
- Modify: `internal/provider/livetv.go:127-159` (`doLoad`)
- Test: `internal/provider/m3u_test.go`, `internal/provider/livetv_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `Channel.TVGID string` (the `tvg-id` attribute, `""` when the attribute is absent or empty) and `Channel.Source string` (the playlist source string the channel was loaded from, as it appears in `p.sources`).

- [ ] **Step 1: Write the failing test for `TVGID`**

Add to `internal/provider/m3u_test.go`:

```go
func TestParseM3USetsTVGIDOnlyWhenAttributePresent(t *testing.T) {
	data := []byte("#EXTM3U\n" +
		"#EXTINF:-1 tvg-id=\"bbc1.uk\" group-title=\"News\",BBC One\n" +
		"http://example.invalid/1.m3u8\n" +
		"#EXTINF:-1 group-title=\"News\",Sky News\n" +
		"http://example.invalid/2.m3u8\n")

	got := ParseM3U(data)
	if len(got) != 2 {
		t.Fatalf("ParseM3U returned %d channels, want 2", len(got))
	}
	if got[0].TVGID != "bbc1.uk" {
		t.Errorf("BBC One TVGID = %q, want %q", got[0].TVGID, "bbc1.uk")
	}
	// The channel with no tvg-id must report an empty TVGID, not its slug.
	// If TVGID fell back to the slug, the ref design could not tell an
	// upstream identifier from a name-derived one, and would treat a
	// display-name change as a different channel.
	if got[1].TVGID != "" {
		t.Errorf("Sky News TVGID = %q, want empty", got[1].TVGID)
	}
	// ID keeps its existing meaning: tvg-id when present, else slug.
	if got[0].ID != "bbc1.uk" || got[1].ID != "sky-news" {
		t.Errorf("IDs = %q, %q; want %q, %q", got[0].ID, got[1].ID, "bbc1.uk", "sky-news")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
export PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0
go test ./internal/provider/ -run TestParseM3USetsTVGIDOnlyWhenAttributePresent -v
```

Expected: a compile error `got[0].TVGID undefined`. **A compile error is not a red.** Add the field (Step 3's struct change) *first*, re-run, and confirm you get a real assertion failure — `BBC One TVGID = "", want "bbc1.uk"` — before writing the `parseExtinf` change.

- [ ] **Step 3: Add the fields and set `TVGID`**

In `internal/provider/m3u.go`, add to `Channel`:

```go
	TVGID      string   // the tvg-id attribute verbatim; "" when absent
	Source     string   // playlist this channel was loaded from; set by LiveTV.doLoad, not by the parser
```

In `parseExtinf`, replace the ID block:

```go
	if id := attrs["tvg-id"]; id != "" {
		c.TVGID = id
		c.ID = id
	} else {
		c.ID = slug(c.Name)
	}
```

`Source` is deliberately not set here: `ParseM3U(data []byte)` never sees the playlist URL.

- [ ] **Step 4: Run and confirm green**

```bash
go test ./internal/provider/ -run TestParseM3USetsTVGID -v
```
Expected: PASS.

- [ ] **Step 5: Write the failing test for `Source`**

Add to `internal/provider/livetv_test.go`:

```go
func TestDoLoadStampsSourceOnEveryChannel(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.m3u")
	b := filepath.Join(dir, "b.m3u")
	writeFile(t, a, "#EXTM3U\n#EXTINF:-1 tvg-id=\"bbc1.uk\",BBC One\nhttp://example.invalid/1.m3u8\n")
	writeFile(t, b, "#EXTM3U\n#EXTINF:-1 tvg-id=\"itv1.uk\",ITV1\nhttp://example.invalid/2.m3u8\n")

	p := NewLiveTV([]string{a, b})
	p.load()

	bySource := map[string]string{}
	for _, ch := range p.channels {
		bySource[ch.Name] = ch.Source
	}
	if bySource["BBC One"] != a {
		t.Errorf("BBC One Source = %q, want %q", bySource["BBC One"], a)
	}
	if bySource["ITV1"] != b {
		t.Errorf("ITV1 Source = %q, want %q", bySource["ITV1"], b)
	}
}

// writeFile is a helper; if internal/provider already has an equivalent,
// use that one instead of adding a second.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
```

- [ ] **Step 6: Run it and watch it fail**

```bash
go test ./internal/provider/ -run TestDoLoadStampsSource -v
```
Expected: FAIL with `BBC One Source = "", want "/tmp/.../a.m3u"`.

- [ ] **Step 7: Stamp `Source` in `doLoad`**

In `internal/provider/livetv.go`, inside the parse loop, immediately after the `if ch.URL == ""` guard:

```go
		for _, ch := range ParseM3U(data) {
			if ch.URL == "" {
				continue
			}
			ch.Source = src
			ch.ID = p.uniqueID(ch.ID)
			p.byID[ch.ID] = ch
			p.channels = append(p.channels, ch)
			for _, cat := range ch.Categories {
				p.byCat[cat] = append(p.byCat[cat], ch)
			}
		}
```

- [ ] **Step 8: Run the package suite**

```bash
export PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0
go build ./... && go vet ./... && go test ./...
```
Expected: all green. Both new fields are additive, so no existing struct literal or comparison should need updating — if one does, the compiler will say so.

- [ ] **Step 9: Commit**

```bash
git add internal/provider/m3u.go internal/provider/m3u_test.go internal/provider/livetv.go internal/provider/livetv_test.go
git commit -m "feat(livetv): record tvg-id and source playlist per channel"
```

---

### Task 2: Bound the load path

`doLoad` iterates sources serially (`livetv.go:132`), `fetch` retries the entire GET on the TLS-1.2 fallback client (`livetv.go:66-71`), and both clients carry `Timeout: 60s` (`livetv.go:51`). Worst case is *sources × 120s* — roughly twelve minutes for six sources — with no context anywhere. `liveTVSources()` appends every TBCPL live playlist (`cmd/fallback.go:50-71`), which is remote data with no cap, so the source count is not bounded either. CLAUDE.md forbids exactly this on agent-facing paths.

**Files:**
- Modify: `internal/provider/livetv.go` — `load`, `doLoad`, `fetch`, `httpGet`
- Test: `internal/provider/livetv_test.go`

**Interfaces:**
- Consumes: `Channel.Source` from Task 1.
- Produces:
  - `const LiveLoadBudget = 15 * time.Second`
  - `func (p *LiveTV) LoadContext(ctx context.Context) error`
  - `func (p *LiveTV) FailedSources() []string` — source strings that did not load this run, in declared order
  - `load()` retained, defined as `LoadContext(context.Background())`, so the TUI is unaffected.

- [ ] **Step 1: Write the failing determinism test**

Merge order must not vary with completion order, or category ordering and duplicate detection become nondeterministic. Add to `internal/provider/livetv_test.go`:

```go
func TestLoadContextMergesInDeclaredSourceOrder(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.m3u")
	b := filepath.Join(dir, "b.m3u")
	writeFile(t, a, "#EXTM3U\n#EXTINF:-1,Alpha\nhttp://example.invalid/1.m3u8\n")
	writeFile(t, b, "#EXTM3U\n#EXTINF:-1,Beta\nhttp://example.invalid/2.m3u8\n")

	// Run repeatedly: a parallel loader that appends on completion would
	// pass once and fail intermittently. Order must be pinned to p.sources.
	for i := 0; i < 20; i++ {
		p := NewLiveTV([]string{a, b})
		if err := p.LoadContext(context.Background()); err != nil {
			t.Fatalf("LoadContext: %v", err)
		}
		if len(p.channels) != 2 {
			t.Fatalf("got %d channels, want 2", len(p.channels))
		}
		if p.channels[0].Name != "Alpha" || p.channels[1].Name != "Beta" {
			t.Fatalf("iteration %d: order = %q, %q; want Alpha, Beta",
				i, p.channels[0].Name, p.channels[1].Name)
		}
	}
}
```

- [ ] **Step 2: Write the failing budget and partial-load tests**

```go
// blockingSource is a source string whose fetch blocks until ctx is done.
// It exercises the deadline without a real 15s wait and without a network.
func TestLoadContextRespectsDeadlineAndReportsFailedSources(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.m3u")
	writeFile(t, good, "#EXTM3U\n#EXTINF:-1,Alpha\nhttp://example.invalid/1.m3u8\n")
	// A path that does not exist fails fast and deterministically, standing
	// in for a source that did not load.
	bad := filepath.Join(dir, "missing.m3u")

	p := NewLiveTV([]string{good, bad})
	if err := p.LoadContext(context.Background()); err != nil {
		t.Fatalf("LoadContext with one good source must succeed, got %v", err)
	}
	if len(p.channels) != 1 {
		t.Fatalf("got %d channels, want 1", len(p.channels))
	}
	failed := p.FailedSources()
	if len(failed) != 1 || failed[0] != bad {
		t.Fatalf("FailedSources() = %v, want [%s]", failed, bad)
	}
}

func TestLoadContextAllSourcesFailingIsAnError(t *testing.T) {
	dir := t.TempDir()
	p := NewLiveTV([]string{filepath.Join(dir, "nope1.m3u"), filepath.Join(dir, "nope2.m3u")})
	if err := p.LoadContext(context.Background()); err == nil {
		t.Fatal("LoadContext with every source failing must return an error")
	}
	if len(p.FailedSources()) != 2 {
		t.Fatalf("FailedSources() = %v, want both", p.FailedSources())
	}
}

func TestLoadContextZeroSourcesIsNotAnError(t *testing.T) {
	// Load-bearing: cmd/channels.go distinguishes "no sources configured"
	// from "sources failed", and reports not_configured for the former.
	p := NewLiveTV(nil)
	if err := p.LoadContext(context.Background()); err != nil {
		t.Fatalf("zero sources must not be an error, got %v", err)
	}
	if len(p.FailedSources()) != 0 {
		t.Fatalf("FailedSources() = %v, want empty", p.FailedSources())
	}
}

func TestLoadContextCancelledContextDoesNotHang(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.m3u")
	writeFile(t, good, "#EXTM3U\n#EXTINF:-1,Alpha\nhttp://example.invalid/1.m3u8\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := NewLiveTV([]string{good})
	done := make(chan struct{})
	go func() { _ = p.LoadContext(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("LoadContext did not return on a cancelled context")
	}
}
```

- [ ] **Step 3: Run them and watch them fail**

```bash
export PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0
go test ./internal/provider/ -run 'TestLoadContext' -v
```
Expected: compile error `p.LoadContext undefined`. Add the method signatures returning `nil` and empty slices, re-run, and confirm **real assertion failures** on each before implementing.

- [ ] **Step 4: Implement the bounded parallel load**

In `internal/provider/livetv.go`, add the imports `context` and `sync` (already present) and:

```go
// LiveLoadBudget bounds a whole playlist load for the agent-facing commands.
// It is deliberately larger than find's 5s race: a single playlist can be
// several MB. One constant, not one per call site.
const LiveLoadBudget = 15 * time.Second

// liveLoadConcurrency caps simultaneous playlist fetches.
const liveLoadConcurrency = 4
```

Add the field `failed []string` to the `LiveTV` struct, then replace `load`/`doLoad`:

```go
// load fetches+parses+merges all sources once, without a deadline. Retained
// for the TUI, which is not an agent-facing path.
func (p *LiveTV) load() { _ = p.LoadContext(context.Background()) }

// LoadContext fetches every source once, in parallel and bounded by ctx, and
// merges the results in declared source order. A failed source is recorded in
// FailedSources and skipped; only if every source fails is an error returned.
// Zero sources is success with nothing, not an error — callers distinguish
// "nothing configured" themselves.
func (p *LiveTV) LoadContext(ctx context.Context) error {
	p.once.Do(func() { p.doLoadContext(ctx) })
	return p.loadErr
}

func (p *LiveTV) doLoadContext(ctx context.Context) {
	p.byID = map[string]Channel{}
	p.byCat = map[string][]Channel{}

	// Indexed by source position so the merge below is independent of
	// completion order.
	type result struct {
		data []byte
		err  error
	}
	results := make([]result, len(p.sources))

	sem := make(chan struct{}, liveLoadConcurrency)
	var wg sync.WaitGroup
	for i, src := range p.sources {
		wg.Add(1)
		go func(i int, src string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				results[i] = result{err: ctx.Err()}
				return
			}
			data, err := p.fetch(ctx, src)
			results[i] = result{data: data, err: err}
		}(i, src)
	}
	wg.Wait()

	var anyOK bool
	var lastErr error
	for i, src := range p.sources {
		if results[i].err != nil {
			lastErr = results[i].err
			p.failed = append(p.failed, src)
			continue
		}
		anyOK = true
		for _, ch := range ParseM3U(results[i].data) {
			if ch.URL == "" {
				continue
			}
			ch.Source = src
			ch.ID = p.uniqueID(ch.ID)
			p.byID[ch.ID] = ch
			p.channels = append(p.channels, ch)
			for _, cat := range ch.Categories {
				p.byCat[cat] = append(p.byCat[cat], ch)
			}
		}
	}
	if !anyOK && len(p.sources) > 0 {
		p.loadErr = fmt.Errorf("livetv: all %d source(s) failed: %w", len(p.sources), lastErr)
		return
	}
	for c := range p.byCat {
		p.cats = append(p.cats, c)
	}
	sort.Slice(p.cats, func(i, j int) bool { return liveCatLess(p.cats[i], p.cats[j]) })
}

// FailedSources returns the sources that did not load, in declared order.
// It is what lets a caller tell "this channel is gone" from "the playlist it
// lives in did not load" — two conditions that need different exit codes.
func (p *LiveTV) FailedSources() []string { return p.failed }
```

- [ ] **Step 5: Thread the context into `fetch` and `httpGet`**

Change the signatures and honour the remaining budget on the retry:

```go
func (p *LiveTV) fetch(ctx context.Context, src string) ([]byte, error) {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		data, err := p.httpGet(ctx, p.client, src)
		// The TLS 1.2 retry re-runs the whole GET, so it is attempted only
		// when the budget still allows it. Without this check a single
		// unreachable source costs two full client timeouts.
		if err != nil && p.fallback != nil && ctx.Err() == nil {
			if data2, err2 := p.httpGet(ctx, p.fallback, src); err2 == nil {
				return data2, nil
			}
		}
		return data, err
	}
	return os.ReadFile(src)
}
```

In `httpGet`, replace the request construction with `http.NewRequestWithContext(ctx, http.MethodGet, src, nil)`. Update the call site in `fetch` only — `httpGet` has no other callers.

- [ ] **Step 6: Run the tests, including the race detector**

```bash
export PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0
go build ./... && go vet ./... && go test ./...
CGO_ENABLED=1 go test ./internal/provider/ -race -count=2
```
Expected: all green, no race. `-race` requires cgo, so the second command must override the exported `CGO_ENABLED=0` or it fails with `-race requires cgo` and proves nothing.

- [ ] **Step 7: Check nothing else regressed on timing**

```bash
go test ./... -count=1 2>&1 | sort -k3 -n -r | head -5
```
If any package jumped to seconds, a test is reaching the network — find it before committing.

- [ ] **Step 8: Commit**

```bash
git add internal/provider/livetv.go internal/provider/livetv_test.go
git commit -m "feat(livetv): bounded parallel playlist load with per-source failure tracking"
```

---

### Task 3: Exported channel lookup

`cmd/` cannot see any of what the ref design needs: every exported accessor returns `media.SearchResult`, and `channelResult` (`livetv.go:188-190`) keeps only `{ID, Title, Type, Poster}`. `p.channels` and `p.byID` are unexported.

**Files:**
- Modify: `internal/provider/livetv.go`
- Test: `internal/provider/livetv_test.go`

**Interfaces:**
- Consumes: `Channel.TVGID`, `Channel.Source` (Task 1); `LoadContext` (Task 2).
- Produces:
  ```go
  type ChannelKey struct {
      TVGID  string // matched exactly when non-empty
      Name   string // matched case-folded and trimmed, only when TVGID is empty
      Source string // matched exactly when non-empty
  }
  func (p *LiveTV) AllChannels() []Channel
  func (p *LiveTV) Lookup(k ChannelKey) []Channel
  ```
  `Lookup` returns **every** match. Returning a slice rather than `(Channel, bool)` is deliberate: the caller must be able to detect ambiguity and refuse, and a signature that can only express one answer would force a silent first-wins pick.

- [ ] **Step 1: Write the failing tests**

```go
func liveTVFixture(t *testing.T, bodies map[string]string) (*LiveTV, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	paths := map[string]string{}
	var sources []string
	names := make([]string, 0, len(bodies))
	for n := range bodies {
		names = append(names, n)
	}
	sort.Strings(names) // deterministic source order
	for _, n := range names {
		p := filepath.Join(dir, n)
		writeFile(t, p, bodies[n])
		paths[n] = p
		sources = append(sources, p)
	}
	lt := NewLiveTV(sources)
	if err := lt.LoadContext(context.Background()); err != nil {
		t.Fatalf("LoadContext: %v", err)
	}
	return lt, paths
}

func TestLookupByTVGID(t *testing.T) {
	lt, paths := liveTVFixture(t, map[string]string{
		"a.m3u": "#EXTM3U\n#EXTINF:-1 tvg-id=\"bbc1.uk\",BBC One\nhttp://example.invalid/1.m3u8\n",
	})
	got := lt.Lookup(ChannelKey{TVGID: "bbc1.uk", Source: paths["a.m3u"]})
	if len(got) != 1 || got[0].Name != "BBC One" {
		t.Fatalf("Lookup = %v, want one BBC One", got)
	}
}

func TestLookupByNameIsCaseFoldedAndTrimmed(t *testing.T) {
	lt, paths := liveTVFixture(t, map[string]string{
		"a.m3u": "#EXTM3U\n#EXTINF:-1,Sky News\nhttp://example.invalid/1.m3u8\n",
	})
	got := lt.Lookup(ChannelKey{Name: "  sky news  ", Source: paths["a.m3u"]})
	if len(got) != 1 {
		t.Fatalf("Lookup = %v, want one match", got)
	}
}

func TestLookupReturnsEveryMatchSoCallersCanDetectAmbiguity(t *testing.T) {
	// Two channels, same name, no tvg-id, same playlist. The caller must be
	// able to see both and refuse. A (Channel, bool) signature would have
	// hidden this behind a first-wins pick — the playlist-order dependence
	// this whole design exists to escape.
	lt, paths := liveTVFixture(t, map[string]string{
		"a.m3u": "#EXTM3U\n" +
			"#EXTINF:-1,Sports HD\nhttp://example.invalid/1.m3u8\n" +
			"#EXTINF:-1,Sports HD\nhttp://example.invalid/2.m3u8\n",
	})
	got := lt.Lookup(ChannelKey{Name: "Sports HD", Source: paths["a.m3u"]})
	if len(got) != 2 {
		t.Fatalf("Lookup returned %d matches, want 2", len(got))
	}
}

func TestLookupSourceNarrowsTheMatch(t *testing.T) {
	lt, paths := liveTVFixture(t, map[string]string{
		"a.m3u": "#EXTM3U\n#EXTINF:-1,Sports HD\nhttp://example.invalid/1.m3u8\n",
		"b.m3u": "#EXTM3U\n#EXTINF:-1,Sports HD\nhttp://example.invalid/2.m3u8\n",
	})
	if got := lt.Lookup(ChannelKey{Name: "Sports HD"}); len(got) != 2 {
		t.Fatalf("unfiltered Lookup = %d matches, want 2", len(got))
	}
	got := lt.Lookup(ChannelKey{Name: "Sports HD", Source: paths["a.m3u"]})
	if len(got) != 1 || got[0].URL != "http://example.invalid/1.m3u8" {
		t.Fatalf("source-filtered Lookup = %v, want only the a.m3u entry", got)
	}
}

func TestAllChannelsIsInMergeOrder(t *testing.T) {
	lt, _ := liveTVFixture(t, map[string]string{
		"a.m3u": "#EXTM3U\n#EXTINF:-1,Alpha\nhttp://example.invalid/1.m3u8\n",
		"b.m3u": "#EXTM3U\n#EXTINF:-1,Beta\nhttp://example.invalid/2.m3u8\n",
	})
	got := lt.AllChannels()
	if len(got) != 2 || got[0].Name != "Alpha" || got[1].Name != "Beta" {
		t.Fatalf("AllChannels = %v, want Alpha then Beta", got)
	}
}
```

- [ ] **Step 2: Run and watch them fail**

```bash
export PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0
go test ./internal/provider/ -run 'TestLookup|TestAllChannels' -v
```
Expected: compile error first. Add stubs returning `nil`, re-run, confirm assertion failures like `Lookup = [], want one BBC One`.

- [ ] **Step 3: Implement**

```go
// ChannelKey identifies a channel across reloads. TVGID is the stable
// upstream identifier and wins when present; Name is the fallback for
// playlists that omit tvg-id. Source narrows both to one playlist.
type ChannelKey struct {
	TVGID  string
	Name   string
	Source string
}

// AllChannels returns every loaded channel in merge order.
func (p *LiveTV) AllChannels() []Channel {
	p.load()
	return p.channels
}

// Lookup returns every channel matching k. It returns all matches rather
// than one so the caller can detect ambiguity and refuse: picking the first
// would reintroduce playlist iteration order as the tie-break.
func (p *LiveTV) Lookup(k ChannelKey) []Channel {
	p.load()
	name := strings.ToLower(strings.TrimSpace(k.Name))
	var out []Channel
	for _, ch := range p.channels {
		if k.Source != "" && ch.Source != k.Source {
			continue
		}
		if k.TVGID != "" {
			if ch.TVGID == k.TVGID {
				out = append(out, ch)
			}
			continue
		}
		if name != "" && strings.ToLower(strings.TrimSpace(ch.Name)) == name {
			out = append(out, ch)
		}
	}
	return out
}
```

Note `AllChannels`/`Lookup` call `p.load()`, which is the no-deadline path. Callers that need the budget must call `LoadContext` **first**; `sync.Once` makes the later `load()` a no-op. Task 6's `resolveLiveRef` and Task 5's `channelsRun` both do this.

- [ ] **Step 4: Run and confirm green**

```bash
go test ./internal/provider/ -run 'TestLookup|TestAllChannels' -v
go build ./... && go vet ./... && go test ./...
```

- [ ] **Step 5: Commit**

```bash
git add internal/provider/livetv.go internal/provider/livetv_test.go
git commit -m "feat(livetv): export AllChannels and an ambiguity-preserving Lookup"
```

---

### Task 4: The live ref type

**Files:**
- Modify: `cmd/ref.go` (the `playRef` struct, `decodeRef`, `searchResult`)
- Modify: `cmd/play.go:163` (the one non-test caller of `searchResult`)
- Test: `cmd/ref_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `const liveRefType = "live"`
  - `playRef.TVGID string` (json `tvg_id,omitempty`), `playRef.Source string` (json `source,omitempty`)
  - `decodeRef` accepts `"live"` as a third `Type`
  - `func (r playRef) searchResult() (media.SearchResult, error)` — **errors on a live ref**

- [ ] **Step 1: Write the failing tests**

```go
func TestDecodeRefAcceptsLive(t *testing.T) {
	tok, err := encodeRef(playRef{
		ID: "bbc1.uk", Title: "BBC One", Type: liveRefType,
		TVGID: "bbc1.uk", Source: "https://example.invalid/uk.m3u",
	})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	got, err := decodeRef(tok)
	if err != nil {
		t.Fatalf("decodeRef rejected a live ref: %v", err)
	}
	if got.Type != liveRefType || got.TVGID != "bbc1.uk" ||
		got.Source != "https://example.invalid/uk.m3u" {
		t.Fatalf("round trip lost fields: %+v", got)
	}
}

func TestSearchResultRefusesLive(t *testing.T) {
	// The structural guard. play's live branch runs before this converter,
	// but the safety property must not depend on branch ordering: a future
	// edit that reorders playRun would otherwise silently hand a live ref to
	// the title-search path, where "BBC One" can resolve to a documentary.
	_, err := playRef{ID: "bbc1.uk", Title: "BBC One", Type: liveRefType}.searchResult()
	if err == nil {
		t.Fatal("searchResult must refuse a live ref")
	}
}

func TestSearchResultStillConvertsMovieAndTV(t *testing.T) {
	got, err := playRef{ID: "x", Title: "Parasite", Year: "2019", Type: media.Movie.String()}.searchResult()
	if err != nil {
		t.Fatalf("movie ref: %v", err)
	}
	if got.Type != media.Movie || got.Title != "Parasite" || got.Year != "2019" {
		t.Fatalf("movie conversion = %+v", got)
	}
	got, err = playRef{ID: "y", Title: "Severance", Type: media.TV.String()}.searchResult()
	if err != nil {
		t.Fatalf("tv ref: %v", err)
	}
	if got.Type != media.TV {
		t.Fatalf("tv conversion = %+v", got)
	}
}

func TestDecodeRefStillRejectsUnknownTypes(t *testing.T) {
	tok, err := encodeRef(playRef{ID: "x", Title: "T", Type: "Live"}) // wrong case
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	if _, err := decodeRef(tok); err == nil {
		t.Fatal("decodeRef must reject \"Live\": a ref is machine-produced, so case is corruption")
	}
}
```

- [ ] **Step 2: Run and watch them fail**

```bash
export PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0
go test ./cmd/ -run 'TestDecodeRefAcceptsLive|TestSearchResultRefusesLive|TestSearchResultStill|TestDecodeRefStillRejects' -v
```
Expected: compile error on `liveRefType`. Add the constant and the two fields, re-run, and confirm real failures — `decodeRef rejected a live ref: ref has unknown type "live"` and `searchResult must refuse a live ref`.

- [ ] **Step 3: Implement in `cmd/ref.go`**

```go
// liveRefType is the playRef.Type of a live channel. Lowercase and exact:
// a ref is machine-produced and opaque, so any other spelling is corruption.
const liveRefType = "live"
```

Add to `playRef`:

```go
	TVGID  string `json:"tvg_id,omitempty"` // live only: stable upstream id, "" when the playlist omits it
	Source string `json:"source,omitempty"` // live only: the playlist the channel was loaded from
```

In `decodeRef`, extend the type check:

```go
	if r.Type != media.Movie.String() && r.Type != media.TV.String() && r.Type != liveRefType {
		return playRef{}, fmt.Errorf("ref has unknown type %q (want %q, %q or %q)",
			r.Type, media.Movie.String(), media.TV.String(), liveRefType)
	}
```

Replace `searchResult`:

```go
// searchResult converts a ref back into the value the playback path expects.
//
// It refuses a live ref. media.MediaType has no Live, so a live channel could
// only be converted by mislabelling it Movie — and resolveAndPlay re-searches
// by title on every fallback provider, so "BBC One" would be handed to FlixHQ
// as a title query and could play a documentary instead. play's live branch
// runs before this function, but the refusal is what makes that property
// independent of branch ordering.
func (r playRef) searchResult() (media.SearchResult, error) {
	if r.Type == liveRefType {
		return media.SearchResult{}, fmt.Errorf("live ref %q cannot be converted to a search result", r.Title)
	}
	t := media.Movie
	if r.Type == media.TV.String() {
		t = media.TV
	}
	return media.SearchResult{ID: r.ID, Title: r.Title, Year: r.Year, Type: t}, nil
}
```

- [ ] **Step 4: Update the caller in `cmd/play.go`**

Replace line 163 (`sel := r.searchResult()`):

```go
	sel, err := r.searchResult()
	if err != nil {
		return emitErr("bad_ref", 1, "%v", err)
	}
```

`err` is already declared by the `decodeRef` line above, so use `=` not `:=` on the assignment if the compiler objects; write it as whichever form compiles cleanly.

- [ ] **Step 5: Run the full gate**

```bash
export PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0
go build ./... && go vet ./... && go test ./...
```
Expected: green. If another caller of `searchResult` surfaces, the compiler names it — update it the same way rather than reverting the signature.

- [ ] **Step 6: Commit**

```bash
git add cmd/ref.go cmd/ref_test.go cmd/play.go
git commit -m "feat(agent): accept live refs and make searchResult refuse them"
```

---

### Task 5: The `channels` command

**Files:**
- Create: `cmd/channels.go`
- Modify: `cmd/root.go:72-79` (registration)
- Test: `cmd/channels_test.go`

**Interfaces:**
- Consumes: `provider.LiveLoadBudget`, `LoadContext`, `FailedSources`, `AllChannels`, `Channel.TVGID/Source` (Tasks 1-3); `liveRefType`, `playRef.TVGID/Source`, `encodeRef` (Task 4).
- Produces:
  - `var agentLiveTV = func(sources []string) *provider.LiveTV { return provider.NewLiveTV(sources) }` — the test seam, named to match `agentProvider`/`agentSearch` (`cmd/find.go:18-22`)
  - `var agentLiveSources = liveTVSources` — seam for the configured source list
  - `func liveChannelRef(ch provider.Channel) (string, error)`

- [ ] **Step 1: Write the failing tests**

```go
func withLiveFixture(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.m3u")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	oldSources, oldTV := agentLiveSources, agentLiveTV
	agentLiveSources = func() []string { return []string{path} }
	t.Cleanup(func() { agentLiveSources, agentLiveTV = oldSources, oldTV })
}

const liveFixtureBody = "#EXTM3U\n" +
	"#EXTINF:-1 tvg-id=\"bbc1.uk\" group-title=\"News\",BBC One\nhttp://example.invalid/1.m3u8\n" +
	"#EXTINF:-1 group-title=\"Sports\",Sky Sports\nhttp://example.invalid/2.m3u8\n" +
	"#EXTINF:-1 group-title=\"News;Sports\",Euronews\nhttp://example.invalid/3.m3u8\n"

func TestChannelsListsCategoriesWithCounts(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	out := runAgentCmd(t, channelsCmd)
	cats := out["categories"].([]any)
	if len(cats) != 2 {
		t.Fatalf("categories = %v, want News and Sports", cats)
	}
	// Euronews is in both groups, so counts sum above the channel total.
	// That is intended: a count answers "how many rows --category X returns".
	byName := map[string]float64{}
	for _, c := range cats {
		m := c.(map[string]any)
		byName[m["name"].(string)] = m["channels"].(float64)
	}
	if byName["News"] != 2 || byName["Sports"] != 2 {
		t.Fatalf("counts = %v, want News:2 Sports:2", byName)
	}
}

func TestChannelsCategoryIsCaseInsensitive(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	// group-title is raw upstream text, so "news" and "News" are distinct
	// buckets there. find already folds case on --type; these must agree.
	out := runAgentCmd(t, channelsCmd, "--category", "news")
	rows := out["channels"].([]any)
	if len(rows) != 2 {
		t.Fatalf("--category news returned %d rows, want 2", len(rows))
	}
	if got := rows[0].(map[string]any)["category"].(string); got != "News" {
		t.Errorf("category echoed as %q, want the playlist's canonical %q", got, "News")
	}
}

func TestChannelsSearchAndCategoryIntersect(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	out := runAgentCmd(t, channelsCmd, "--category", "Sports", "--search", "euro")
	rows := out["channels"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["name"].(string) != "Euronews" {
		t.Fatalf("intersection = %v, want only Euronews", rows)
	}
}

func TestChannelsLimitCaps(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	out := runAgentCmd(t, channelsCmd, "--search", "", "--limit", "1")
	if rows := out["channels"].([]any); len(rows) != 1 {
		t.Fatalf("--limit 1 returned %d rows", len(rows))
	}
}

func TestChannelsEmitsRefNotID(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	out := runAgentCmd(t, channelsCmd, "--category", "Sports")
	row := out["channels"].([]any)[0].(map[string]any)
	if _, hasID := row["id"]; hasID {
		t.Error("channels must not print provider IDs; the ref is the only handle")
	}
	ref, ok := row["ref"].(string)
	if !ok || ref == "" {
		t.Fatal("row carries no ref")
	}
	got, err := decodeRef(ref)
	if err != nil {
		t.Fatalf("emitted ref does not decode: %v", err)
	}
	if got.Type != liveRefType {
		t.Errorf("ref type = %q, want %q", got.Type, liveRefType)
	}
}

func TestChannelsUnknownCategoryIsNoResults(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	err := runAgentCmdErr(t, channelsCmd, "--category", "Cooking")
	assertExit(t, err, exitNoResults)
}

func TestChannelsNoSourcesIsNotConfigured(t *testing.T) {
	old := agentLiveSources
	agentLiveSources = func() []string { return nil }
	t.Cleanup(func() { agentLiveSources = old })

	err := runAgentCmdErr(t, channelsCmd)
	// Exit 1, not 2. "You have no playlists configured" is a config fix,
	// not a query to rephrase — the distinction find could not express.
	assertExit(t, err, exitUsage)
	assertErrCode(t, "not_configured")
}

func TestChannelsRejectsPositionalArgs(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	// markAgentCommand installs ArbitraryArgs when a command sets none, so
	// without cobra.NoArgs `lobster channels Sports` would silently ignore
	// its argument and list categories instead.
	err := runAgentCmdErr(t, channelsCmd, "Sports")
	assertExit(t, err, exitUsage)
}
```

`runAgentCmd`, `runAgentCmdErr`, `assertExit` and `assertErrCode` are helpers. Check `cmd/find_test.go` and `cmd/agenterr_test.go` for existing equivalents and **reuse them**; only add what is genuinely missing, in `cmd/channels_test.go`, following the existing style of capturing `agentOut`.

- [ ] **Step 2: Run and watch them fail**

```bash
export PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0
go test ./cmd/ -run TestChannels -v
```
Expected: compile error on `channelsCmd`. Add the command with a `RunE` that returns `nil`, re-run, and confirm real assertion failures before implementing.

- [ ] **Step 3: Implement `cmd/channels.go`**

```go
package cmd

import (
	"context"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"lobster/internal/provider"
)

var (
	flagChannelsCategory string
	flagChannelsSearch   string
	flagChannelsLimit    int
)

// agentLiveTV builds the Live TV provider. A package var so tests can supply
// one over a temp-file playlist instead of one that reaches the network.
var agentLiveTV = func(sources []string) *provider.LiveTV { return provider.NewLiveTV(sources) }

// agentLiveSources is the configured source list, seamed for the same reason.
var agentLiveSources = liveTVSources

var channelsCmd = &cobra.Command{
	Use:   "channels",
	Short: "List live TV channels and print JSON (no prompts)",
	Long: `List live TV categories, or the channels in one, as JSON on stdout.

With no flags it prints the categories and how many channels each holds. With
--category or --search it prints matching channels, each carrying an opaque
"ref" which is the handle to pass to "lobster play --ref".

Categories come from the playlists' own group-title text, so --category matches
case-insensitively and the output echoes the playlist's spelling.
"Uncategorized" is synthesised for channels whose playlist gives no group. A
channel listed under several groups is counted in each, so the category counts
can sum to more than the number of distinct channels.`,
	Args: cobra.NoArgs, // markAgentCommand would otherwise install ArbitraryArgs
	RunE: channelsRun,
}

func init() {
	markAgentCommand(channelsCmd)
	channelsCmd.Flags().StringVar(&flagChannelsCategory, "category", "", "Only channels in this category (case-insensitive)")
	channelsCmd.Flags().StringVar(&flagChannelsSearch, "search", "", "Only channels whose name contains this text (case-insensitive)")
	channelsCmd.Flags().IntVar(&flagChannelsLimit, "limit", 0, "Maximum channels to print (0 = no limit)")
}

func channelsRun(cmd *cobra.Command, args []string) error {
	sources := agentLiveSources()
	if len(sources) == 0 {
		return emitErr("not_configured", exitUsage,
			"no live TV sources configured; add one under [live_tv] in the config, or enable the TBCPL feed")
	}

	p := agentLiveTV(sources)
	ctx, cancel := context.WithTimeout(context.Background(), provider.LiveLoadBudget)
	defer cancel()
	if err := p.LoadContext(ctx); err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "%v", err)
	}

	listing := flagChannelsCategory != "" || cmd.Flags().Changed("search")
	if !listing {
		return emitChannelCategories(p)
	}
	return emitChannelRows(p)
}

func emitChannelCategories(p *provider.LiveTV) error {
	counts := map[string]int{}
	for _, ch := range p.AllChannels() {
		for _, c := range ch.Categories {
			counts[c]++
		}
	}
	names := make([]string, 0, len(counts))
	for n := range counts {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]map[string]any, 0, len(names))
	for _, n := range names {
		out = append(out, map[string]any{"name": n, "channels": counts[n]})
	}
	return emitJSON(map[string]any{"categories": out})
}

func emitChannelRows(p *provider.LiveTV) error {
	wantCat := strings.ToLower(strings.TrimSpace(flagChannelsCategory))
	wantName := strings.ToLower(strings.TrimSpace(flagChannelsSearch))

	out := []map[string]any{}
	for _, ch := range p.AllChannels() {
		matchedCat := ""
		if wantCat == "" {
			if len(ch.Categories) > 0 {
				matchedCat = ch.Categories[0]
			}
		} else {
			for _, c := range ch.Categories {
				if strings.ToLower(strings.TrimSpace(c)) == wantCat {
					matchedCat = c
					break
				}
			}
			if matchedCat == "" {
				continue
			}
		}
		if wantName != "" && !strings.Contains(strings.ToLower(ch.Name), wantName) {
			continue
		}
		ref, err := liveChannelRef(ch)
		if err != nil {
			return emitErr("internal", 1, "encoding ref: %v", err)
		}
		out = append(out, map[string]any{
			"name":     ch.Name,
			"category": matchedCat,
			"logo":     ch.Logo,
			"ref":      ref,
		})
		if flagChannelsLimit > 0 && len(out) >= flagChannelsLimit {
			break
		}
	}
	if len(out) == 0 {
		return emitErr("no_results", exitNoResults, "no channel matched")
	}
	return emitJSON(map[string]any{"channels": out})
}

// liveChannelRef mints the ref for one channel. It carries TVGID and Source so
// play can re-match the channel after a reload rather than trusting an ID that
// depends on playlist order.
func liveChannelRef(ch provider.Channel) (string, error) {
	return encodeRef(playRef{
		ID:     ch.ID,
		Title:  ch.Name,
		Type:   liveRefType,
		TVGID:  ch.TVGID,
		Source: ch.Source,
	})
}
```

- [ ] **Step 4: Register the command**

In `cmd/root.go`, after line 74 (`rootCmd.AddCommand(episodesCmd)`):

```go
	rootCmd.AddCommand(channelsCmd)
```

- [ ] **Step 5: Run and confirm green**

```bash
export PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0
go test ./cmd/ -run TestChannels -v
go build ./... && go vet ./... && go test ./...
```

- [ ] **Step 6: Commit**

```bash
git add cmd/channels.go cmd/channels_test.go cmd/root.go
git commit -m "feat(agent): lobster channels lists live TV categories and channels"
```

---

### Task 6: Live playback in `play --ref`

**Files:**
- Create: `cmd/liveref.go`
- Modify: `cmd/play.go:157-163` (the branch, immediately after `decodeRef`)
- Test: `cmd/liveref_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1-5, plus the existing `agentPlayerCheck` (`cmd/play.go:29`), `playDetached` (`cmd/detach.go:174`), `flagDetach`, `flagSupervised`, `flagDownload`, `flagSeason`, `flagEpisode`.
- Produces:
  - `func playLiveRef(cmd *cobra.Command, r playRef) error`
  - `func resolveLiveRef(p *provider.LiveTV, r playRef) (provider.Channel, error)`
  - `var agentPlayLive = func(stream *media.Stream, title string) error` — the seam that stands in for the player, so no test launches mpv.

- [ ] **Step 1: Write the failing tests**

```go
func TestPlayLiveRejectsSeasonAndEpisode(t *testing.T) {
	// Must fire before the provider is built: assert the seam is never called.
	called := false
	old := agentLiveTV
	agentLiveTV = func(sources []string) *provider.LiveTV { called = true; return nil }
	t.Cleanup(func() { agentLiveTV = old })

	err := runAgentCmdErr(t, playCmd, "--ref", liveRefFor(t, "bbc1.uk", "BBC One", "src.m3u"), "--season", "1")
	assertExit(t, err, exitUsage)
	if called {
		t.Error("provider was constructed before the season/episode rejection")
	}
}

func TestPlayLiveResolvesByTVGIDAfterPlaylistReorder(t *testing.T) {
	// The ID-drift case. The ref is minted against playlist A; playlist B has
	// the same two channels in the opposite order, so the ref's ID now names
	// a DIFFERENT channel. Feeding an empty playlist here would only prove
	// the empty case and let the real bug ship under a passing test.
	orderA := "#EXTM3U\n" +
		"#EXTINF:-1 tvg-id=\"dup\",Alpha\nhttp://example.invalid/alpha.m3u8\n" +
		"#EXTINF:-1 tvg-id=\"dup\",Beta\nhttp://example.invalid/beta.m3u8\n"
	orderB := "#EXTM3U\n" +
		"#EXTINF:-1 tvg-id=\"dup\",Beta\nhttp://example.invalid/beta.m3u8\n" +
		"#EXTINF:-1 tvg-id=\"dup\",Alpha\nhttp://example.invalid/alpha.m3u8\n"
	_ = orderA // minted against A in the helper below

	var played string
	stubLivePlayer(t, &played)
	ref := refFromPlaylist(t, orderA, "Alpha")
	usePlaylist(t, orderB)

	if err := runAgentCmdErr(t, playCmd, "--ref", ref); err != nil {
		t.Fatalf("play: %v", err)
	}
	if played != "http://example.invalid/alpha.m3u8" {
		t.Fatalf("played %q, want Alpha's URL", played)
	}
}

func TestPlayLiveAmbiguousMatchRefuses(t *testing.T) {
	body := "#EXTM3U\n" +
		"#EXTINF:-1,Sports HD\nhttp://example.invalid/1.m3u8\n" +
		"#EXTINF:-1,Sports HD\nhttp://example.invalid/2.m3u8\n"
	var played string
	stubLivePlayer(t, &played)
	ref := refFromPlaylist(t, body, "Sports HD")
	usePlaylist(t, body)

	err := runAgentCmdErr(t, playCmd, "--ref", ref)
	assertExit(t, err, exitNoResults)
	assertErrCode(t, "ambiguous_channel")
	if played != "" {
		t.Fatal("an ambiguous ref must not play anything")
	}
}

func TestPlayLiveAbsentChannelIsNoResultsAndNeverResolves(t *testing.T) {
	// The second assertion is the one that matters: it proves a live ref
	// cannot leak into the title-search path.
	resolved := false
	oldResolve := agentResolveAndPlay
	agentResolveAndPlay = func(p provider.Provider, sel media.SearchResult, s, e int) error {
		resolved = true
		return nil
	}
	t.Cleanup(func() { agentResolveAndPlay = oldResolve })

	ref := refFromPlaylist(t, "#EXTM3U\n#EXTINF:-1 tvg-id=\"gone.uk\",Gone\nhttp://example.invalid/x.m3u8\n", "Gone")
	usePlaylist(t, "#EXTM3U\n#EXTINF:-1 tvg-id=\"other.uk\",Other\nhttp://example.invalid/y.m3u8\n")

	err := runAgentCmdErr(t, playCmd, "--ref", ref)
	assertExit(t, err, exitNoResults)
	if resolved {
		t.Fatal("a live ref reached resolveAndPlay — the title-search path")
	}
}

func TestPlayLiveDistinguishesDownPlaylistFromMissingChannel() // see Step 2
```

Write `TestPlayLiveDistinguishesDownPlaylistFromMissingChannel` as: mint a ref whose `Source` points at a path, then delete that file before playing, and assert `exitProvidersFailed` — not `exitNoResults`. The channel is not gone; its playlist is down, and those need different advice.

Helpers to write in `cmd/liveref_test.go`: `usePlaylist(t, body)` (writes a temp M3U and points `agentLiveSources`/`agentLiveTV` at it, restoring both with `t.Cleanup`), `refFromPlaylist(t, body, name)` (loads the body, finds the channel by name, returns `liveChannelRef(ch)`), `liveRefFor(t, tvgID, title, source)` (mints a ref directly), and `stubLivePlayer(t, &played)` (replaces `agentPlayLive` with one that records `stream.URL`, restoring it with `t.Cleanup`).

- [ ] **Step 2: Run and watch them fail**

```bash
export PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0
go test ./cmd/ -run TestPlayLive -v
```
Expected: compile errors first. Add `playLiveRef` returning `nil` plus the seams, re-run, and confirm each test fails on its assertion — particularly that the drift test fails by playing Beta's URL, which is the actual bug.

- [ ] **Step 3: Implement `cmd/liveref.go`**

```go
package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/provider"
)

// agentPlayLive hands a resolved live stream to the player. A package var so
// tests can observe what would be played without launching mpv.
var agentPlayLive = func(stream *media.Stream, title string) error {
	pl := player.New(cfg.Player, cfg.AudioLanguage)
	_, err := pl.Play(stream, title, 0, nil)
	return err
}

// playLiveRef plays a live channel ref.
//
// It never enters resolveAndPlay. That path re-searches by title on every
// fallback provider, so a live ref would be handed to FlixHQ as a title query
// and could play a documentary named after the channel.
//
// No history or resume entry is written, and none needs to be suppressed:
// history is written only in playStream (cmd/search.go:598) and saveHistory
// (cmd/session.go:351-366), and this path enters neither. --continue is inert
// for the same reason — its resume lookup keys on e.ID == selected.ID
// (cmd/search.go:565-573), which is never reached. If a later change routes
// live through playStream, both of those become live problems: live IDs are
// the unstable positional ones, so a resume would seek to a stranger's
// position.
func playLiveRef(cmd *cobra.Command, r playRef) error {
	if flagSeason > 0 || flagEpisode > 0 {
		return emitErr("usage", exitUsage,
			"%q is a live channel: --season and --episode do not apply", r.Title)
	}
	if flagDownload != "" {
		return emitErr("unsupported", exitUsage,
			"--download is not supported for live channels")
	}
	if available, name := agentPlayerCheck(); !available {
		return emitErr("player_unavailable", exitPlayerUnavailable,
			"%s is not installed or not on PATH", name)
	}

	// Fork before loading any playlist. supervisorArgs forwards only the ref
	// (cmd/detach.go:86-96), so the child reloads regardless; resolving here
	// would do the work twice and block the parent, breaking the "returns
	// immediately" contract --detach promises. The !flagSupervised guard
	// mirrors cmd/play.go:208 — without it the child forks a child of its own.
	if flagDetach && !flagSupervised {
		return playDetached(cmd, r)
	}

	sources := agentLiveSources()
	if len(sources) == 0 {
		return emitErr("not_configured", exitUsage,
			"no live TV sources configured; add one under [live_tv] in the config, or enable the TBCPL feed")
	}
	p := agentLiveTV(sources)
	ctx, cancel := context.WithTimeout(context.Background(), provider.LiveLoadBudget)
	defer cancel()
	if err := p.LoadContext(ctx); err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "%v", err)
	}

	ch, err := resolveLiveRef(p, r)
	if err != nil {
		return err
	}

	stream, err := p.Watch(ch.ID, "", "LiveTV", cfgQuality())
	if err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "%v", err)
	}
	if err := agentPlayLive(stream, ch.Name); err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "%v", err)
	}
	return emitJSON(map[string]any{"status": "finished", "title": ch.Name})
}

// resolveLiveRef re-matches a live ref against the freshly loaded playlists.
//
// It fails closed at every branch. An ID is never authoritative: uniqueID
// (internal/provider/livetv.go:161-172) disambiguates by playlist order, so an
// ID means a position, not a channel. Ambiguity fails like absence — picking
// the first match would reintroduce exactly the ordering dependence this
// design exists to escape.
func resolveLiveRef(p *provider.LiveTV, r playRef) (provider.Channel, error) {
	matches := p.Lookup(provider.ChannelKey{TVGID: r.TVGID, Name: r.Title, Source: r.Source})

	switch {
	case len(matches) == 1:
		return matches[0], nil
	case len(matches) > 1:
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, m.URL)
		}
		return provider.Channel{}, emitErr("ambiguous_channel", exitNoResults,
			"%q matches %d channels in the same playlist (%s); it cannot be identified unambiguously",
			r.Title, len(matches), strings.Join(names, ", "))
	}

	// No match. Distinguish "the channel is gone" from "its playlist is down":
	// they call for completely different advice.
	for _, f := range p.FailedSources() {
		if f == r.Source {
			return provider.Channel{}, emitErr("providers_failed", exitProvidersFailed,
				"the playlist %q came from could not be loaded; the channel may still exist",
				r.Title)
		}
	}
	return provider.Channel{}, emitErr("no_results", exitNoResults,
		"%q is no longer in your playlists; re-run 'lobster channels' to get a current ref", r.Title)
}

var _ = fmt.Sprintf // remove if unused after implementation
```

Delete the `fmt` blank identifier line if `fmt` ends up unused; `go vet` will flag it either way.

- [ ] **Step 4: Add the branch in `cmd/play.go`**

Immediately after the `decodeRef` block (currently lines 158-161), **before** `searchResult`:

```go
	// Live refs take a completely separate path: they resolve against the
	// playlists, never through resolveAndPlay's title search. This must
	// precede searchResult, which now refuses a live ref outright.
	if r.Type == liveRefType {
		return playLiveRef(cmd, r)
	}
```

- [ ] **Step 5: Run and confirm green**

```bash
export PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0
go test ./cmd/ -run TestPlayLive -v
go build ./... && go vet ./... && go test ./...
CGO_ENABLED=1 go test ./cmd/ -race -count=2
```

- [ ] **Step 6: Verify no test started reaching the network**

```bash
go test ./... -count=1 2>&1 | sort -k3 -n -r | head -5
```
`./cmd/` and `./internal/provider/` must stay sub-second. A jump to seconds means a fixture is hitting a real URL.

- [ ] **Step 7: Commit**

```bash
git add cmd/liveref.go cmd/liveref_test.go cmd/play.go
git commit -m "feat(agent): play live channel refs, re-matched and fail-closed"
```

---

### Task 7: Skill and documentation

`skills/lobster-play/SKILL.md` currently tells the agent under "Out of scope" that live TV is unavailable and to send the user to the interactive CLI. Shipping Tasks 1-6 without this leaves a surface the agent is instructed not to use. Its exit-code table also defines exit 1 as "You called it wrong… Fix the command; do not retry it unchanged" without ever mentioning `error.code`, so an agent receiving `not_configured` would conclude it malformed its own command.

**Files:**
- Modify: `skills/lobster-play/SKILL.md`
- Modify: `README.md` (the agent-commands section, if one exists — check with `grep -n "lobster find" README.md`)
- Test: `cmd/agenthelp_test.go` (an existing test asserts the agent commands' help text; extend it rather than adding a parallel one)

**Interfaces:**
- Consumes: the command surface from Tasks 5 and 6.
- Produces: no code symbols.

- [ ] **Step 1: Extend the help test**

Read `cmd/agenthelp_test.go` first and follow its existing shape. Add `channelsCmd` to whatever table it iterates, and add:

```go
func TestSkillDocumentsChannels(t *testing.T) {
	body, err := os.ReadFile("../skills/lobster-play/SKILL.md")
	if err != nil {
		t.Fatalf("reading SKILL.md: %v", err)
	}
	s := string(body)
	// The skill previously told the agent live TV was unavailable. Leaving
	// that in place would ship a command the agent is instructed not to call.
	if strings.Contains(s, "Live TV channel listing and channel surfing are not available") {
		t.Error("SKILL.md still declares live TV out of scope")
	}
	for _, want := range []string{"lobster channels", "not_configured", "error.code"} {
		if !strings.Contains(s, want) {
			t.Errorf("SKILL.md does not mention %q", want)
		}
	}
}
```

- [ ] **Step 2: Run and watch it fail**

```bash
export PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0
go test ./cmd/ -run TestSkillDocumentsChannels -v
```
Expected: FAIL on all four assertions.

- [ ] **Step 3: Rewrite the skill's live TV coverage**

Replace the "Out of scope" live TV paragraph with a `channels` section documenting: the three modes (categories, `--category`, `--search`), `--limit`, that `--category` is case-insensitive, that `Uncategorized` is synthesised, that category counts can exceed the channel total, and that a channel is played with `lobster play --ref` exactly like a film — with `--detach` recommended, since a live stream never ends.

Add to the exit-code table's row for exit 1: *"Read `error.code`: `not_configured` means the user has no live TV playlists set up — tell them to add one under `[live_tv]` rather than retrying."* Add a row or note for `ambiguous_channel` (exit 2): the ref matches more than one channel and cannot be resolved; re-run `lobster channels` and pick a different one.

State plainly that a live ref can go stale — playlists change upstream — and that exit 2 on `play` means re-running `channels` for a current ref, while exit 3 means the playlist itself is down.

- [ ] **Step 4: Run and confirm green**

```bash
go test ./cmd/ -run 'TestSkill|TestAgentHelp' -v
```

- [ ] **Step 5: Full gate, including cross-compilation**

`channels` touches no syscalls or paths beyond what already existed, but the branch as a whole does, and CI runs three OSes:

```bash
export PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0
go build ./... && go vet ./... && go test ./...
GOOS=windows go build ./... && GOOS=windows go vet ./cmd/
GOOS=darwin  go build ./...
```

- [ ] **Step 6: Commit**

```bash
git add skills/lobster-play/SKILL.md cmd/agenthelp_test.go README.md
git commit -m "docs(agent): document the live TV surface in the lobster-play skill"
```

---

## Self-review notes

**Spec coverage.** Bounded load → Task 2. `Channel.TVGID`/`Source` → Task 1. Exported provider surface → Task 3. Live ref fields, `decodeRef`, `searchResult` refusal → Task 4. `channels` with `--limit`, case-insensitive `--category`, `NoArgs`, no printed IDs → Task 5. `playLiveRef`, fail-closed re-match, ambiguity, absent-vs-down, detach ordering, history → Task 6. SKILL.md and `error.code` → Task 7. Every error-table row is asserted by a test in the task that introduces it, except `player_unavailable`, which is the existing `agentPlayerCheck` behaviour reused unchanged.

**Known limitation carried from the spec, not implemented here:** `playDetached` sets `c.Stdout = c.Stderr = lf` (`cmd/detach.go:194-195`) and nothing rotates `~/.cache/lobster/play-*.log`. A live channel left running for days grows that log without bound. Capping it affects movies equally and belongs in its own commit — do not fold it into this branch.

**Type consistency check performed:** `ChannelKey` is spelled the same in Tasks 3 and 6; `LiveLoadBudget` is exported and used in Tasks 5 and 6; `liveRefType` is defined in Task 4 and consumed in Tasks 5 and 6; `agentLiveTV`/`agentLiveSources` are defined in Task 5 and reused in Task 6's tests; `agentPlayLive` is defined in Task 6 only.
