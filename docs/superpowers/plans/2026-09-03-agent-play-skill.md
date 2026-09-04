# Agent-Driven Playback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a non-interactive CLI surface (`find`, `episodes`, `play`) plus a `lobster-play` agent skill, so a user can ask their coding agent to find and play a movie or TV episode.

**Architecture:** Three new Cobra subcommands emit JSON on stdout and never touch `fzf`. Selections round-trip through an opaque base64url `--ref` token carrying id/title/year/type/base, because provider IDs are not portable and the resolver re-searches by title. `play --detach` re-executes lobster as a background supervisor so the HLS proxy, torrent server and subtitle temp dir outlive the foreground process.

**Tech Stack:** Go 1.25, Cobra, standard library only for the new code (`encoding/json`, `encoding/base64`, `os/exec`, `syscall`).

## Global Constraints

- Build with `CGO_ENABLED=0`. Go toolchain lives at `/usr/local/go/bin`.
- Before every commit: `CGO_ENABLED=0 go build ./... && go vet ./... && go test ./...` must all pass.
- Do **not** add `Co-Authored-By` trailers to commits.
- Tests must make **no live network calls**. Follow the `TestMain` stubbing pattern in `cmd/fallback_providers_test.go`.
- CI runs ubuntu-latest, windows-latest and macos-latest (`.github/workflows/ci.yml`). Any `syscall.SysProcAttr` use must be behind build tags, following the existing `ipc_windows.go` / `fzf_path_windows.go` pattern.
- `schema` value is `1` in every JSON payload.
- Do not modify existing command behaviour. `lobster`, `lobster <query>`, `trending`, `recent`, `history` and `doctor` must work exactly as they do today.
- Work happens in the worktree `/tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill` on branch `feat/agent-play-skill`.

---

## File Structure

| File | Responsibility |
| ---- | -------------- |
| `cmd/agentjson.go` | JSON envelope writer, `exitError` type, exit-code mapping |
| `cmd/agentjson_test.go` | Envelope shape, error envelope, exit codes |
| `cmd/ref.go` | `ref` struct, encode/decode, conversion to `media.SearchResult` |
| `cmd/ref_test.go` | Round-trip, malformed input |
| `cmd/agenthelp_test.go` | Shared test helpers: hostile environment, stub provider |
| `cmd/find.go` | `lobster find` |
| `cmd/find_test.go` | find behaviour under hostile environment |
| `cmd/episodes.go` | `lobster episodes` |
| `cmd/episodes_test.go` | episodes behaviour |
| `cmd/play.go` | `lobster play` (attached path) + `newPlayer` seam wiring |
| `cmd/play_test.go` | play validation, exit codes |
| `cmd/detach.go` | Supervisor arg construction, liveness wait, log path |
| `cmd/detach_unix.go` | `Setpgid` spawn attrs (build tag `!windows`) |
| `cmd/detach_windows.go` | `CREATE_NEW_PROCESS_GROUP|DETACHED_PROCESS` spawn attrs |
| `cmd/detach_test.go` | Arg construction and liveness logic |
| `skills/lobster-play/SKILL.md` | The agent skill |
| `README.md` | Install instructions for the skill (modify) |

**Key reuse:** `resolveAndPlay(p, selected, season, episode)` at `cmd/search.go:223` is already non-interactive for movies, and for TV when `season > 0 && episode > 0` and `flagDownload == ""`. The four blocking calls (`cmd/search.go:279,290,351,364`) are all in `else` branches. `play` calls it directly rather than reimplementing playback.

---

### Task 1: JSON envelope and exit codes

**Files:**
- Create: `cmd/agentjson.go`
- Create: `cmd/agentjson_test.go`
- Modify: `cmd/root.go:44-48` (`Execute`)

**Interfaces:**
- Consumes: nothing
- Produces:
  - `func emitJSON(payload map[string]any) error`
  - `func emitErr(code string, exit int, format string, a ...any) error` — writes the error envelope to stdout and returns `*exitError`
  - `type exitError struct { code int; err error }` with `Error() string` and `Unwrap() error`
  - Constants `exitNoResults = 2`, `exitProvidersFailed = 3`, `exitPlayerUnavailable = 4`
  - `var agentOut io.Writer = os.Stdout` (test seam)

- [ ] **Step 1: Write the failing test**

Create `cmd/agentjson_test.go`:

```go
package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

// captureAgentOut redirects the agent JSON writer to a buffer for the test.
func captureAgentOut(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := agentOut
	agentOut = &buf
	t.Cleanup(func() { agentOut = prev })
	return &buf
}

func TestEmitJSONCarriesSchema(t *testing.T) {
	buf := captureAgentOut(t)
	if err := emitJSON(map[string]any{"results": []any{}}); err != nil {
		t.Fatalf("emitJSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, buf.String())
	}
	if got["schema"] != float64(1) {
		t.Fatalf("schema = %v, want 1", got["schema"])
	}
}

// Errors must be machine-readable on stdout too, so an agent can parse
// unconditionally instead of guessing whether a run produced JSON.
func TestEmitErrWritesEnvelopeAndCarriesExitCode(t *testing.T) {
	buf := captureAgentOut(t)
	err := emitErr("no_results", exitNoResults, "nothing matched %q", "the matirx")

	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("emitErr returned %T, want *exitError", err)
	}
	if ee.code != exitNoResults {
		t.Fatalf("exit code = %d, want %d", ee.code, exitNoResults)
	}

	var got struct {
		Schema int `json:"schema"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("error output is not valid JSON: %v (%q)", err, buf.String())
	}
	if got.Schema != 1 {
		t.Fatalf("schema = %d, want 1", got.Schema)
	}
	if got.Error.Code != "no_results" {
		t.Fatalf("code = %q, want no_results", got.Error.Code)
	}
	if got.Error.Message != `nothing matched "the matirx"` {
		t.Fatalf("message = %q", got.Error.Message)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./cmd/ -run 'TestEmit' -v`

Expected: FAIL to build — `undefined: agentOut`, `undefined: emitJSON`, `undefined: emitErr`, `undefined: exitError`, `undefined: exitNoResults`.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/agentjson.go`:

```go
package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// agentSchema versions the machine-readable contract. A skill written against a
// different lobster can detect the mismatch instead of silently misparsing.
const agentSchema = 1

// Exit codes for the agent-facing commands. 2 and 3 are deliberately distinct:
// "no such title" and "the title exists but every source is down" call for
// completely different advice, and on this repo the latter is the common one.
const (
	exitNoResults         = 2
	exitProvidersFailed   = 3
	exitPlayerUnavailable = 4
)

// agentOut is where JSON payloads go. A package var so tests can capture them
// without touching the real stdout.
var agentOut io.Writer = os.Stdout

// exitError carries a process exit code up to Execute. The JSON envelope has
// already been written by the time this is returned, so Execute must not print
// it again.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// emitJSON writes one payload to stdout with the schema marker attached.
//
// The schema is set after the payload is copied, not before: a caller passing
// a key named "schema" must not be able to override it, or the contract holds
// only by convention.
func emitJSON(payload map[string]any) error {
	out := make(map[string]any, len(payload)+1)
	for k, v := range payload {
		out[k] = v
	}
	out["schema"] = agentSchema
	enc := json.NewEncoder(agentOut)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// emitErr writes the error envelope and returns an *exitError carrying the
// process exit code.
func emitErr(code string, exit int, format string, a ...any) error {
	msg := fmt.Sprintf(format, a...)
	_ = emitJSON(map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
	return &exitError{code: exit, err: fmt.Errorf("%s: %s", code, msg)}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./cmd/ -run 'TestEmit' -v`

Expected: PASS (both tests).

- [ ] **Step 5: Wire the exit code into Execute**

Replace `Execute` in `cmd/root.go:44-48` with:

```go
// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		// Agent commands have already written a JSON error envelope to stdout
		// and carry their own exit code; printing again would corrupt it.
		var ee *exitError
		if errors.As(err, &ee) {
			os.Exit(ee.code)
		}
		os.Exit(1)
	}
}
```

Add `"errors"` to the import block in `cmd/root.go`.

Note: `SilenceErrors`/`SilenceUsage` are **not** set on the root command — that would change how errors look for existing human commands. They are set per-subcommand in Tasks 3-5.

- [ ] **Step 6: Verify the whole suite still passes**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go build ./... && go vet ./... && go test ./...`

Expected: all packages ok, no failures.

- [ ] **Step 7: Commit**

```bash
git add cmd/agentjson.go cmd/agentjson_test.go cmd/root.go
git commit -m "feat(agent): JSON envelope and exit codes for machine-readable output

Errors go to stdout as JSON too, so an agent can parse unconditionally
rather than guessing whether a run produced JSON. Exit codes 2 and 3 are
distinct because 'no such title' and 'every source is down' need
different advice, and the latter is the common case on this repo.

Execute now honours an exitError's code. SilenceErrors is deliberately
not set on the root command; it is set per-subcommand so existing human
commands keep their current error output."
```

---

### Task 2: The `ref` replay token

**Files:**
- Create: `cmd/ref.go`
- Create: `cmd/ref_test.go`

**Interfaces:**
- Consumes: Task 1 (`emitErr`, `exitNoResults`)
- Produces:
  - `type playRef struct { ID, Title, Year, Type, Base string }`
  - `func encodeRef(r playRef) (string, error)`
  - `func decodeRef(s string) (playRef, error)`
  - `func (r playRef) searchResult() media.SearchResult`

- [ ] **Step 1: Write the failing test**

Create `cmd/ref_test.go`:

```go
package cmd

import (
	"testing"

	"lobster/internal/media"
)

// A ref must survive the round trip intact. It is the only handle the agent
// has on a selection the user confirmed, so any field loss means playing
// something other than what was agreed.
func TestRefRoundTrip(t *testing.T) {
	want := playRef{
		ID:    "movie/watch-the-matrix-19724",
		Title: "The Matrix",
		Year:  "1999",
		Type:  "movie",
		Base:  "flixhq.ws",
	}
	tok, err := encodeRef(want)
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	got, err := decodeRef(tok)
	if err != nil {
		t.Fatalf("decodeRef: %v", err)
	}
	if got != want {
		t.Fatalf("round trip lost data:\n got %+v\nwant %+v", got, want)
	}
}

// A hand-mangled ref must produce a clean error, not a panic.
func TestDecodeRefRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "!!!not-base64!!!", "YWJj"} { // "abc" is valid b64, invalid JSON
		if _, err := decodeRef(in); err == nil {
			t.Fatalf("decodeRef(%q) succeeded, want an error", in)
		}
	}
}

// Year is a string throughout media.SearchResult and is frequently empty;
// the ref must preserve that rather than inventing a zero year.
func TestRefPreservesEmptyYear(t *testing.T) {
	tok, err := encodeRef(playRef{ID: "x", Title: "T", Type: "tv"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	got, err := decodeRef(tok)
	if err != nil {
		t.Fatalf("decodeRef: %v", err)
	}
	if got.Year != "" {
		t.Fatalf("Year = %q, want empty", got.Year)
	}
}

func TestRefSearchResultMapsType(t *testing.T) {
	if got := (playRef{Type: "tv"}).searchResult().Type; got != media.TV {
		t.Fatalf("tv mapped to %v, want media.TV", got)
	}
	if got := (playRef{Type: "movie"}).searchResult().Type; got != media.Movie {
		t.Fatalf("movie mapped to %v, want media.Movie", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./cmd/ -run 'TestRef|TestDecodeRef' -v`

Expected: FAIL to build — `undefined: playRef`, `undefined: encodeRef`, `undefined: decodeRef`.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/ref.go`:

```go
package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"lobster/internal/media"
)

// playRef is everything needed to replay a selection the user confirmed.
//
// An ID alone is not enough. media.SearchResult.ID is provider-specific
// (internal/media/types.go), and the resolver re-searches by title on every
// fallback provider — resolveWithProvider calls p.Search(req.Title), and its
// doc comment states that IDs are not portable across providers. Without Title
// and Year the provider is asked to Search("") and ranking collapses, which
// does not fail loudly: it plays the wrong film.
//
// Base is carried because the primary provider is flag/config-selected, and an
// ID found under --base yts is meaningless under the default base.
type playRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Year  string `json:"year,omitempty"`
	Type  string `json:"type"`
	Base  string `json:"base,omitempty"`
}

// encodeRef renders a ref as a base64url token. Opaque by contract, but plain
// base64 so it can be decoded by hand during support.
func encodeRef(r playRef) (string, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("encoding ref: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// decodeRef parses a token produced by encodeRef.
func decodeRef(s string) (playRef, error) {
	if s == "" {
		return playRef{}, fmt.Errorf("empty ref")
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return playRef{}, fmt.Errorf("ref is not valid base64url: %w", err)
	}
	var r playRef
	if err := json.Unmarshal(b, &r); err != nil {
		return playRef{}, fmt.Errorf("ref is not valid JSON: %w", err)
	}
	if r.ID == "" || r.Title == "" {
		return playRef{}, fmt.Errorf("ref is missing id or title")
	}
	return r, nil
}

// searchResult converts a ref back into the value the playback path expects.
func (r playRef) searchResult() media.SearchResult {
	t := media.Movie
	if r.Type == "tv" {
		t = media.TV
	}
	return media.SearchResult{
		ID:    r.ID,
		Title: r.Title,
		Year:  r.Year,
		Type:  t,
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./cmd/ -run 'TestRef|TestDecodeRef' -v`

Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add cmd/ref.go cmd/ref_test.go
git commit -m "feat(agent): opaque ref token for replaying a confirmed selection

An ID alone cannot replay a selection. Provider IDs are not portable
(internal/media/types.go:25) and the resolver re-searches by title
(internal/resolver/probe.go:84), so a bare ID leaves Title and Year empty
and ranking collapses to Search(\"\") — which plays the wrong film rather
than failing.

The ref carries id, title, year, type and base. Base matters because an
ID found under --base yts is meaningless under the default base."
```

---

### Task 3: Shared test helpers and `lobster find`

**Files:**
- Create: `cmd/agenthelp_test.go`
- Create: `cmd/find.go`
- Create: `cmd/find_test.go`
- Modify: `cmd/root.go` (register `findCmd` in `init`)

**Interfaces:**
- Consumes: Task 1 (`emitJSON`, `emitErr`, `exitNoResults`), Task 2 (`encodeRef`, `playRef`)
- Produces:
  - `var findCmd *cobra.Command`
  - `func hostileEnv(t *testing.T)` — test helper: closed stdin + an `fzf` shim on PATH that fails the test if executed
  - `type stubProvider struct { results []media.SearchResult; seasons []media.Season; episodes []media.Episode }` implementing `provider.Provider`
  - `var agentSearch = gatherSearchResults` — test seam

- [ ] **Step 1: Write the shared test helpers**

Create `cmd/agenthelp_test.go`:

```go
package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"lobster/internal/media"
)

// hostileEnv makes any attempt to prompt the user fail the test rather than
// hang it. Injecting ui.Select alone is not enough: ui.Input execs fzf
// directly, ui.SelectWithTimeout reads os.Stdin raw before reaching Select,
// and tui.StartApp is Bubble Tea rather than fzf. Closing stdin and shimming
// fzf on PATH catches all of those, including any added later.
func hostileEnv(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shim relies on a POSIX shell; the guarantee is covered on unix CI")
	}

	dir := t.TempDir()
	shim := filepath.Join(dir, "fzf")
	script := "#!/bin/sh\necho 'fzf was invoked by a non-interactive command' >&2\nexit 97\n"
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fzf shim: %v", err)
	}
	t.Setenv("PATH", dir)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	w.Close() // reads return EOF immediately instead of blocking
	prev := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = prev
		r.Close()
	})
}

// stubProvider is a provider.Provider that answers from fixed data and never
// touches the network.
type stubProvider struct {
	results  []media.SearchResult
	seasons  []media.Season
	episodes []media.Episode
	searchErr error
}

func (s *stubProvider) Search(string) ([]media.SearchResult, error) {
	return s.results, s.searchErr
}
func (s *stubProvider) GetDetails(string) (*media.ContentDetail, error) {
	return &media.ContentDetail{}, nil
}
func (s *stubProvider) GetSeasons(string) ([]media.Season, error) { return s.seasons, nil }
func (s *stubProvider) GetEpisodes(string, string) ([]media.Episode, error) {
	return s.episodes, nil
}
func (s *stubProvider) GetServers(string, string) ([]media.Server, error) {
	return []media.Server{{ID: "srv1", Name: "stub"}}, nil
}
func (s *stubProvider) GetEmbedURL(string) (string, error) { return "https://example.invalid/e", nil }
func (s *stubProvider) Trending(media.MediaType) ([]media.SearchResult, error) {
	return s.results, nil
}
func (s *stubProvider) Recent(media.MediaType) ([]media.SearchResult, error) {
	return s.results, nil
}
```

- [ ] **Step 2: Write the failing test for find**

Create `cmd/find_test.go`:

```go
package cmd

import (
	"encoding/json"
	"errors"
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/provider"
)

// find must complete without ever prompting. This is the whole point of the
// command: an agent that hits fzf hangs until it is killed.
func TestFindEmitsResultsWithoutPrompting(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	prevCfg := cfg
	cfg = &config.Config{Base: "flixhq.ws"}
	t.Cleanup(func() { cfg = prevCfg })

	prevSearch := agentSearch
	agentSearch = func(primary provider.Provider, fallbacks []provider.Provider, query string) ([]media.SearchResult, error) {
		return []media.SearchResult{
			{ID: "movie/watch-the-matrix-19724", Title: "The Matrix", Year: "1999", Type: media.Movie},
			{ID: "movie/watch-the-matrix-reloaded-19725", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie},
		}, nil
	}
	t.Cleanup(func() { agentSearch = prevSearch })

	if err := findRun(findCmd, []string{"the matrix"}); err != nil {
		t.Fatalf("findRun: %v", err)
	}

	var got struct {
		Schema  int `json:"schema"`
		Results []struct {
			Idx   int    `json:"idx"`
			Ref   string `json:"ref"`
			Title string `json:"title"`
			Year  string `json:"year"`
			Type  string `json:"type"`
		} `json:"results"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, buf.String())
	}
	if got.Schema != 1 {
		t.Fatalf("schema = %d, want 1", got.Schema)
	}
	if len(got.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(got.Results))
	}
	if got.Results[0].Title != "The Matrix" || got.Results[0].Year != "1999" {
		t.Fatalf("first result = %+v", got.Results[0])
	}
	if got.Results[0].Type != "movie" {
		t.Fatalf("type = %q, want movie", got.Results[0].Type)
	}

	// The ref must decode back to the same selection, including the base.
	ref, err := decodeRef(got.Results[0].Ref)
	if err != nil {
		t.Fatalf("emitted ref does not decode: %v", err)
	}
	if ref.ID != "movie/watch-the-matrix-19724" || ref.Title != "The Matrix" {
		t.Fatalf("ref = %+v", ref)
	}
	if ref.Base != "flixhq.ws" {
		t.Fatalf("ref.Base = %q, want flixhq.ws", ref.Base)
	}
}

// No results is a distinct, recoverable outcome and must not be conflated with
// "every provider is down".
func TestFindNoResultsExitsTwo(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	prevCfg := cfg
	cfg = &config.Config{Base: "flixhq.ws"}
	t.Cleanup(func() { cfg = prevCfg })

	prevSearch := agentSearch
	agentSearch = func(provider.Provider, []provider.Provider, string) ([]media.SearchResult, error) {
		return nil, nil
	}
	t.Cleanup(func() { agentSearch = prevSearch })

	err := findRun(findCmd, []string{"zzzznotathing"})
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("findRun returned %T (%v), want *exitError", err, err)
	}
	if ee.code != exitNoResults {
		t.Fatalf("exit code = %d, want %d", ee.code, exitNoResults)
	}
}

func TestFindLimitTruncates(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	prevCfg := cfg
	cfg = &config.Config{Base: "flixhq.ws"}
	t.Cleanup(func() { cfg = prevCfg })

	prevSearch := agentSearch
	agentSearch = func(provider.Provider, []provider.Provider, string) ([]media.SearchResult, error) {
		return []media.SearchResult{
			{ID: "a", Title: "A", Type: media.Movie},
			{ID: "b", Title: "B", Type: media.Movie},
			{ID: "c", Title: "C", Type: media.Movie},
		}, nil
	}
	t.Cleanup(func() { agentSearch = prevSearch })

	prevLimit := flagFindLimit
	flagFindLimit = 2
	t.Cleanup(func() { flagFindLimit = prevLimit })

	if err := findRun(findCmd, []string{"x"}); err != nil {
		t.Fatalf("findRun: %v", err)
	}
	var got struct {
		Results []struct{} `json:"results"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if len(got.Results) != 2 {
		t.Fatalf("got %d results, want 2 (limit)", len(got.Results))
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./cmd/ -run 'TestFind' -v`

Expected: FAIL to build — `undefined: findCmd`, `undefined: findRun`, `undefined: agentSearch`, `undefined: flagFindLimit`.

- [ ] **Step 4: Write minimal implementation**

Create `cmd/find.go`:

```go
package cmd

import (
	"strings"

	"github.com/spf13/cobra"

	"lobster/internal/media"
)

var (
	flagFindType  string
	flagFindLimit int
)

// agentSearch is the search entry point, as a package var so tests can supply
// fixed results instead of reaching the network.
var agentSearch = gatherSearchResults

var findCmd = &cobra.Command{
	Use:   "find <query>",
	Short: "Search for a movie or TV show and print JSON (no prompts)",
	Long: `Search and print matching titles as JSON on stdout.

Unlike the interactive commands, find never opens fzf and never waits for
input, so it is safe to call from a script or an agent. Each result carries an
opaque "ref" which is the handle to pass to "lobster play --ref".`,
	Args:          cobra.MinimumNArgs(1),
	RunE:          findRun,
	SilenceErrors: true,
	SilenceUsage:  true,
}

func findRun(cmd *cobra.Command, args []string) error {
	query := strings.Join(args, " ")

	p := newProvider()
	results, err := agentSearch(p, fallbackSearchProviders(p), query)
	if err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "search failed: %v", err)
	}

	if flagFindType != "" {
		want := media.Movie
		if flagFindType == "tv" {
			want = media.TV
		}
		filtered := results[:0:0]
		for _, r := range results {
			if r.Type == want {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}

	if len(results) == 0 {
		return emitErr("no_results", exitNoResults, "nothing matched %q", query)
	}

	if flagFindLimit > 0 && len(results) > flagFindLimit {
		results = results[:flagFindLimit]
	}

	base := ""
	if cfg != nil {
		base = cfg.Base
	}

	out := make([]map[string]any, 0, len(results))
	for i, r := range results {
		ref, err := encodeRef(playRef{
			ID:    r.ID,
			Title: r.Title,
			Year:  r.Year,
			Type:  r.Type.String(),
			Base:  base,
		})
		if err != nil {
			return emitErr("internal", 1, "encoding ref: %v", err)
		}
		out = append(out, map[string]any{
			"idx":   i,
			"ref":   ref,
			"title": r.Title,
			"year":  r.Year,
			"type":  r.Type.String(),
		})
	}
	return emitJSON(map[string]any{"results": out})
}
```

Note on `Base`, added after review: this block stamps the *configured* base on
every result, including ones the fallback chain supplied, whose IDs belong to a
different provider. That stamp is a starting point for resolution, not an
attribution, and it cannot be made exact — most fallback providers are not
addressable as a `--base` value at all (`newProvider`, `cmd/provider.go`). What
matters is that no consumer of a ref may assume its ID resolves against the
primary: `play` re-searches by title through the whole chain (`resolveAndPlay`,
`cmd/search.go`) and `episodes` does the same via `seasonSource`
(`cmd/episodes.go`) when the primary cannot enumerate the ref's ID. See Task 4.

Note the import block above deliberately omits `lobster/internal/provider`:
`newProvider()` and `fallbackSearchProviders()` are used through type
inference, so the package is never named in this file. Importing it would fail
`go vet`.

- [ ] **Step 5: Register the command**

In `cmd/root.go` `init()`, add after `rootCmd.AddCommand(doctorCmd)`:

```go
	rootCmd.AddCommand(findCmd)
```

And in `cmd/find.go`, add an `init()`:

```go
func init() {
	findCmd.Flags().StringVar(&flagFindType, "type", "", "Filter results: movie | tv")
	findCmd.Flags().IntVar(&flagFindLimit, "limit", 0, "Maximum results to print (0 = no limit)")
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./cmd/ -run 'TestFind' -v`

Expected: PASS (3 tests).

- [ ] **Step 7: Verify the whole suite**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go build ./... && go vet ./... && go test ./...`

Expected: all ok.

- [ ] **Step 8: Commit**

```bash
git add cmd/agenthelp_test.go cmd/find.go cmd/find_test.go cmd/root.go
git commit -m "feat(agent): add lobster find, a search that never prompts

Emits candidates as JSON with an opaque ref per result. No results exits
2, distinct from providers-down which exits 3.

Tests run under a hostile environment: stdin closed and an fzf shim on
PATH that fails the test if executed. Injecting ui.Select alone would not
be enough, because ui.Input execs fzf directly, ui.SelectWithTimeout
reads stdin raw before reaching Select, and the no-arg path is Bubble Tea
rather than fzf."
```

---

### Task 4: `lobster episodes`

**Files:**
- Create: `cmd/episodes.go`
- Create: `cmd/episodes_test.go`
- Modify: `cmd/root.go` (register `episodesCmd`)

**Interfaces:**
- Consumes: Task 1, Task 2 (`decodeRef`), Task 3 (`hostileEnv`, `stubProvider`)
- Produces:
  - `var episodesCmd *cobra.Command`
  - `var agentProvider = newProvider` — test seam so episodes/play can be given a stub

- [ ] **Step 1: Write the failing test**

Create `cmd/episodes_test.go`:

```go
package cmd

import (
	"encoding/json"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// An agent cannot guess episode numbers, so it needs a listing — and that
// listing must not prompt.
func TestEpisodesListsWithoutPrompting(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	prevProv := agentProvider
	agentProvider = func() provider.Provider {
		return &stubProvider{
			seasons:  []media.Season{{ID: "s1", Number: 1}, {ID: "s2", Number: 2}},
			episodes: []media.Episode{{ID: "e1", Number: 1, Title: "Pilot"}},
		}
	}
	t.Cleanup(func() { agentProvider = prevProv })

	ref, err := encodeRef(playRef{ID: "tv/show-1", Title: "Some Show", Type: "tv"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef := flagRef
	flagRef = ref
	t.Cleanup(func() { flagRef = prevRef })

	prevSeason := flagSeason
	flagSeason = 1
	t.Cleanup(func() { flagSeason = prevSeason })

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}

	var got struct {
		Schema   int `json:"schema"`
		Episodes []struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, buf.String())
	}
	if got.Schema != 1 {
		t.Fatalf("schema = %d, want 1", got.Schema)
	}
	if len(got.Episodes) != 1 || got.Episodes[0].Number != 1 || got.Episodes[0].Title != "Pilot" {
		t.Fatalf("episodes = %+v", got.Episodes)
	}
}

// Asking for episodes of a film is a caller mistake worth naming clearly.
func TestEpisodesRejectsMovieRef(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	ref, err := encodeRef(playRef{ID: "movie/x", Title: "A Film", Type: "movie"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef := flagRef
	flagRef = ref
	t.Cleanup(func() { flagRef = prevRef })

	if err := episodesRun(episodesCmd, nil); err == nil {
		t.Fatal("episodesRun accepted a movie ref, want an error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./cmd/ -run 'TestEpisodes' -v`

Expected: FAIL to build — `undefined: episodesCmd`, `undefined: episodesRun`, `undefined: agentProvider`, `undefined: flagRef`, `undefined: flagSeason`.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/episodes.go`:

```go
package cmd

import (
	"github.com/spf13/cobra"

	"lobster/internal/media"
)

var (
	flagRef     string
	flagSeason  int
	flagEpisode int
)

// agentProvider builds the primary provider. A package var so tests can supply
// a stub instead of one that reaches the network.
var agentProvider = newProvider

var episodesCmd = &cobra.Command{
	Use:   "episodes --ref <REF>",
	Short: "List seasons and episodes for a TV ref as JSON (no prompts)",
	Long: `List the episodes of a show without prompting.

Takes a ref emitted by "lobster find". With --season it lists that season's
episodes; without it, the first season's.`,
	Args:          cobra.NoArgs,
	RunE:          episodesRun,
	SilenceErrors: true,
	SilenceUsage:  true,
}

func init() {
	episodesCmd.Flags().StringVar(&flagRef, "ref", "", "Ref from lobster find (required)")
	episodesCmd.Flags().IntVar(&flagSeason, "season", 0, "Season number (default: first)")
}

func episodesRun(cmd *cobra.Command, args []string) error {
	r, err := decodeRef(flagRef)
	if err != nil {
		return emitErr("bad_ref", 1, "%v", err)
	}
	if r.Type != media.TV.String() {
		return emitErr("not_a_series", 1, "%q is a %s, which has no episodes", r.Title, r.Type)
	}

	p := agentProvider()
	seasons, err := p.GetSeasons(r.ID)
	if err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "getting seasons: %v", err)
	}
	if len(seasons) == 0 {
		return emitErr("no_results", exitNoResults, "no seasons found for %q", r.Title)
	}

	sel := seasons[0]
	if flagSeason > 0 {
		found := false
		for _, s := range seasons {
			if s.Number == flagSeason {
				sel, found = s, true
				break
			}
		}
		if !found {
			return emitErr("no_results", exitNoResults, "season %d not found for %q", flagSeason, r.Title)
		}
	}

	eps, err := p.GetEpisodes(r.ID, sel.ID)
	if err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "getting episodes: %v", err)
	}

	seasonNums := make([]int, 0, len(seasons))
	for _, s := range seasons {
		seasonNums = append(seasonNums, s.Number)
	}
	out := make([]map[string]any, 0, len(eps))
	for _, e := range eps {
		out = append(out, map[string]any{"number": e.Number, "title": e.Title})
	}

	return emitJSON(map[string]any{
		"title":    r.Title,
		"seasons":  seasonNums,
		"season":   sel.Number,
		"episodes": out,
	})
}
```

As in `find.go`, the import block deliberately omits
`lobster/internal/provider`: `agentProvider()` is used through type inference
and the package is never named here.

- [ ] **Step 4: Register the command**

In `cmd/root.go` `init()`, add:

```go
	rootCmd.AddCommand(episodesCmd)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./cmd/ -run 'TestEpisodes' -v`

Expected: PASS (2 tests).

- [ ] **Step 6: Verify the whole suite and commit**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go build ./... && go vet ./... && go test ./...`

```bash
git add cmd/episodes.go cmd/episodes_test.go cmd/root.go
git commit -m "feat(agent): add lobster episodes for non-interactive episode listing

An agent cannot guess episode numbers, so play --season/--episode is
unusable without a listing. Takes a ref from find and prints seasons plus
the selected season's episodes as JSON."
```

---

### Task 5: `lobster play` (attached path)

**Files:**
- Create: `cmd/play.go`
- Create: `cmd/play_test.go`
- Modify: `cmd/root.go` (register `playCmd`)

**Interfaces:**
- Consumes: Tasks 1-4 (`emitErr`, `decodeRef`, `agentProvider`, `flagRef`, `flagSeason`, `flagEpisode`, `hostileEnv`)
- Produces:
  - `var playCmd *cobra.Command`
  - `var agentResolveAndPlay = resolveAndPlay` — test seam
  - `func playRun(cmd *cobra.Command, args []string) error`

**Why this reuses existing code:** `resolveAndPlay` at `cmd/search.go:223` is already non-interactive for movies, and for TV when `season > 0 && episode > 0` and `flagDownload == ""`. Its four blocking calls (`cmd/search.go:279,290,351,364`) are all in `else` branches. Reimplementing playback would duplicate the fallback chain, subtitle handling and history writes for no benefit.

- [ ] **Step 1: Write the failing test**

Create `cmd/play_test.go`:

```go
package cmd

import (
	"errors"
	"fmt"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// A TV ref without both season and episode would fall through to the
// interactive season picker, which hangs. Refuse it with a clear message
// instead.
func TestPlayRejectsTVWithoutSeasonAndEpisode(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	ref, err := encodeRef(playRef{ID: "tv/show", Title: "Some Show", Type: "tv"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef, prevS, prevE := flagRef, flagSeason, flagEpisode
	flagRef, flagSeason, flagEpisode = ref, 0, 0
	t.Cleanup(func() { flagRef, flagSeason, flagEpisode = prevRef, prevS, prevE })

	if err := playRun(playCmd, nil); err == nil {
		t.Fatal("playRun accepted a TV ref with no season/episode, want an error")
	}
}

// The ref's title and year must reach the playback path. If they are dropped,
// the resolver searches for "" and ranking collapses — which plays the wrong
// film rather than failing.
func TestPlayPassesFullSelectionThrough(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	prevProv := agentProvider
	agentProvider = func() provider.Provider { return &stubProvider{} }
	t.Cleanup(func() { agentProvider = prevProv })

	var got media.SearchResult
	prevPlay := agentResolveAndPlay
	agentResolveAndPlay = func(p provider.Provider, sel media.SearchResult, season, episode int) error {
		got = sel
		return nil
	}
	t.Cleanup(func() { agentResolveAndPlay = prevPlay })

	ref, err := encodeRef(playRef{
		ID: "movie/watch-the-matrix-19724", Title: "The Matrix", Year: "1999", Type: "movie",
	})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef := flagRef
	flagRef = ref
	t.Cleanup(func() { flagRef = prevRef })

	if err := playRun(playCmd, nil); err != nil {
		t.Fatalf("playRun: %v", err)
	}
	if got.Title != "The Matrix" {
		t.Fatalf("Title = %q, want The Matrix", got.Title)
	}
	if got.Year != "1999" {
		t.Fatalf("Year = %q, want 1999 (the resolver ranks on it)", got.Year)
	}
	if got.ID != "movie/watch-the-matrix-19724" {
		t.Fatalf("ID = %q", got.ID)
	}
	if got.Type != media.Movie {
		t.Fatalf("Type = %v, want Movie", got.Type)
	}
}

// A total resolution failure is exit 3, so the agent knows to run doctor
// rather than suggest a spelling fix.
func TestPlayResolutionFailureExitsThree(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	prevProv := agentProvider
	agentProvider = func() provider.Provider { return &stubProvider{} }
	t.Cleanup(func() { agentProvider = prevProv })

	prevPlay := agentResolveAndPlay
	agentResolveAndPlay = func(provider.Provider, media.SearchResult, int, int) error {
		return fmt.Errorf("all providers failed")
	}
	t.Cleanup(func() { agentResolveAndPlay = prevPlay })

	ref, err := encodeRef(playRef{ID: "movie/x", Title: "X", Type: "movie"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef := flagRef
	flagRef = ref
	t.Cleanup(func() { flagRef = prevRef })

	err = playRun(playCmd, nil)
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("playRun returned %T (%v), want *exitError", err, err)
	}
	if ee.code != exitProvidersFailed {
		t.Fatalf("exit code = %d, want %d", ee.code, exitProvidersFailed)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./cmd/ -run 'TestPlay' -v`

Expected: FAIL to build — `undefined: playCmd`, `undefined: playRun`, `undefined: agentResolveAndPlay`.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/play.go`:

```go
package cmd

import (
	"github.com/spf13/cobra"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// agentResolveAndPlay is the playback entry point, as a package var so tests
// can observe what selection reaches it without launching a player.
var agentResolveAndPlay = resolveAndPlay

var playCmd = &cobra.Command{
	Use:   "play --ref <REF>",
	Short: "Play a ref returned by lobster find (no prompts)",
	Long: `Play the exact title a ref identifies, without prompting.

The ref pins the selection — title, year, type and originating base. It does
not pin the stream: resolution still runs through the fallback chain at play
time, so if the original source is down another provider's copy may be served.

For a series, both --season and --episode are required; without them playback
would fall through to the interactive picker and hang.`,
	Args:          cobra.NoArgs,
	RunE:          playRun,
	SilenceErrors: true,
	SilenceUsage:  true,
}

func init() {
	playCmd.Flags().StringVar(&flagRef, "ref", "", "Ref from lobster find (required)")
	playCmd.Flags().IntVar(&flagSeason, "season", 0, "Season number (required for TV)")
	playCmd.Flags().IntVar(&flagEpisode, "episode", 0, "Episode number (required for TV)")
}

func playRun(cmd *cobra.Command, args []string) error {
	r, err := decodeRef(flagRef)
	if err != nil {
		return emitErr("bad_ref", 1, "%v", err)
	}

	sel := r.searchResult()
	if sel.Type == media.TV && (flagSeason <= 0 || flagEpisode <= 0) {
		return emitErr("season_episode_required", 1,
			"%q is a series: pass --season and --episode (list them with 'lobster episodes --ref ...')", r.Title)
	}

	// Downloading walks a different, interactive path (batch range prompts), so
	// it is not supported here.
	if flagDownload != "" {
		return emitErr("unsupported", 1, "--download is not supported by 'play'; use the interactive CLI")
	}

	var p provider.Provider = agentProvider()
	if err := agentResolveAndPlay(p, sel, flagSeason, flagEpisode); err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "%v", err)
	}

	return emitJSON(map[string]any{
		"status": "finished",
		"title":  r.Title,
	})
}
```

- [ ] **Step 4: Register the command**

In `cmd/root.go` `init()`, add:

```go
	rootCmd.AddCommand(playCmd)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./cmd/ -run 'TestPlay' -v`

Expected: PASS (3 tests).

- [ ] **Step 6: Verify the whole suite and commit**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go build ./... && go vet ./... && go test ./...`

```bash
git add cmd/play.go cmd/play_test.go cmd/root.go
git commit -m "feat(agent): add lobster play --ref for non-interactive playback

Reuses resolveAndPlay, which is already non-interactive for movies and
for TV when season and episode are both set — its four blocking calls
(cmd/search.go:279,290,351,364) are all in else branches.

A TV ref without both --season and --episode is refused rather than
falling through to the season picker, which would hang. Resolution
failure exits 3 so the agent runs doctor instead of suggesting a
spelling fix."
```

---

### Task 5b: Regression test for the foreign-ID-namespace case

**Files:**
- Create: `internal/resolver/foreign_id_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks — this guards `internal/resolver`
- Produces: no production code

**Why this exists as its own task:** every other test in this plan uses a stub
provider that answers with the ID it was asked for, which is exactly the case
where a bare `--id` would have worked. That masks the problem `ref` was built
to solve. This test uses a provider whose IDs are from a *different namespace*,
which is the real situation across lobster's fallback chain — and proves that
carrying Title and Year is what saves the selection.

It lives in `internal/resolver` because `candidatesFor` is unexported, and
calling it directly keeps the test fast and network-free while still exercising
the real ranking code rather than a reimplementation.

- [ ] **Step 1: Write the failing test**

Create `internal/resolver/foreign_id_test.go`:

```go
package resolver

import (
	"testing"

	"lobster/internal/media"
)

// A fallback provider indexes its own catalogue, so the ID the user's
// selection came from means nothing to it. What still identifies the work is
// the title and year — which is why the agent-facing ref carries them rather
// than an ID alone.
//
// Without Title and Year the request degenerates and a franchise query returns
// whichever sequel the provider happens to rank first.
func TestCandidatesForPicksRightWorkWhenIDsAreForeign(t *testing.T) {
	// None of these IDs match the request: this provider uses its own scheme.
	results := []media.SearchResult{
		{ID: "yts/9001", Title: "The Matrix Resurrections", Year: "2021", Type: media.Movie},
		{ID: "yts/9002", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie},
		{ID: "yts/9003", Title: "The Matrix", Year: "1999", Type: media.Movie},
	}

	req := Request{
		ID:        "movie/watch-the-matrix-19724", // from a different provider
		Title:     "The Matrix",
		Year:      "1999",
		MediaType: media.Movie,
	}

	got := candidatesFor(results, req)
	if len(got) == 0 {
		t.Fatal("no candidates returned")
	}
	if got[0].Title != "The Matrix" || got[0].Year != "1999" {
		t.Fatalf("top candidate = %q (%s), want The Matrix (1999); "+
			"title+year ranking is what rescues a foreign ID", got[0].Title, got[0].Year)
	}
}

// The counter-case: strip Title and Year, as a bare --id would, and the
// ranking has nothing to work with. This documents why the ref carries more
// than an ID — if this ever starts passing, the ref could be simplified.
func TestCandidatesForCannotDisambiguateWithoutTitleOrYear(t *testing.T) {
	results := []media.SearchResult{
		{ID: "yts/9001", Title: "The Matrix Resurrections", Year: "2021", Type: media.Movie},
		{ID: "yts/9003", Title: "The Matrix", Year: "1999", Type: media.Movie},
	}

	req := Request{ID: "movie/watch-the-matrix-19724", MediaType: media.Movie}

	got := candidatesFor(results, req)
	if len(got) == 0 {
		t.Fatal("no candidates returned")
	}
	if got[0].Title == "The Matrix" && got[0].Year == "1999" {
		t.Fatal("an ID-only request picked the right film; if this is now " +
			"reliable, revisit whether playRef still needs Title and Year")
	}
}
```

- [ ] **Step 2: Run the tests**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./internal/resolver/ -run 'TestCandidatesFor.*Foreign|TestCandidatesForCannot' -v`

Expected: **both PASS immediately.** These characterise existing behaviour
rather than driving new code, so there is no red phase. If the first one fails,
stop — the premise behind `playRef` is wrong and the design needs revisiting
before any more of this plan is built.

This was measured against `origin/main` while writing the plan, and the result
is why `ref` carries more than an ID:

| Request | Top-ranked result |
| ------- | ----------------- |
| ID + Title + Year | `The Matrix` (1999) |
| ID only | `The Matrix Resurrections` (2021) |

A bare `--id` does not fail loudly — it plays the wrong film.

- [ ] **Step 3: Confirm the assertions actually bite**

Temporarily change `req` in the first test to drop `Title` and `Year`:

```go
	req := Request{ID: "movie/watch-the-matrix-19724", MediaType: media.Movie}
```

Run the first test again. Expected: **FAIL**, proving the assertion depends on
title/year ranking rather than passing by luck. Then revert the change and
re-run to confirm PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/resolver/foreign_id_test.go
git commit -m "test(resolver): pin title+year ranking when provider IDs are foreign

Every other test around the agent commands uses a stub that answers with
the ID it was asked for, which is exactly the case where a bare ID would
have worked — so it masks the problem the ref token exists to solve.

This uses results from a different ID namespace, as the fallback chain
really does, and pins that title+year is what identifies the work. The
paired counter-test documents why the ref carries more than an ID: if it
ever starts failing, the ref could be simplified."
```

---

### Task 6: `--detach` supervisor

**Files:**
- Create: `cmd/detach.go`
- Create: `cmd/detach_unix.go`
- Create: `cmd/detach_windows.go`
- Create: `cmd/detach_test.go`
- Modify: `cmd/play.go` (add `--detach`, hidden `--supervised`, branch in `playRun`)

**Interfaces:**
- Consumes: Tasks 1-5
- Produces:
  - `func supervisorArgs(exe string, ref string, season, episode int) []string`
  - `func detachLogPath(pid int) (string, error)`
  - `func detachSpawnAttr() *syscall.SysProcAttr` (per-platform)
  - `var flagDetach bool`, `var flagSupervised bool`

**Why re-exec rather than skipping `cmd.Wait()`:** playback depends on process-local resources that die when lobster exits — the de-obfuscating HLS proxy whose cleanup every player defers (`internal/player/mpv.go:41`, `vlc.go:33`, `generic.go:31`, used by AniPub), the torrent server at `cmd/search.go:518-522` (all YTS content is magnets), and the subtitle temp dir at `cmd/search.go:497-499`. It is also not mechanically possible: `vlc.go:57` and `generic.go:53` call `cmd.Run()`, with no `Start`/`Wait` to split.

- [ ] **Step 1: Write the failing test**

Create `cmd/detach_test.go`:

```go
package cmd

import (
	"strings"
	"testing"
)

// The supervisor must be told to actually play, and must be marked supervised
// so it does not recurse into spawning another supervisor.
func TestSupervisorArgsArePlayableAndNonRecursive(t *testing.T) {
	args := supervisorArgs("/usr/bin/lobster", "REF123", 2, 3)

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "play") {
		t.Fatalf("args %v do not invoke play", args)
	}
	if !strings.Contains(joined, "--ref REF123") {
		t.Fatalf("args %v do not carry the ref", args)
	}
	if !strings.Contains(joined, "--season 2") || !strings.Contains(joined, "--episode 3") {
		t.Fatalf("args %v lost season/episode", args)
	}
	if !strings.Contains(joined, "--supervised") {
		t.Fatalf("args %v missing --supervised; the child would spawn its own child forever", args)
	}
	if strings.Contains(joined, "--detach") {
		t.Fatalf("args %v still carry --detach; that is the recursion", args)
	}
}

// Season and episode are meaningless for a film and must not be passed as
// zeroes, which the play command would reject.
func TestSupervisorArgsOmitZeroSeasonEpisode(t *testing.T) {
	args := supervisorArgs("/usr/bin/lobster", "REF", 0, 0)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--season") || strings.Contains(joined, "--episode") {
		t.Fatalf("args %v pass zero season/episode", args)
	}
}

func TestDetachLogPathIsPerPID(t *testing.T) {
	a, err := detachLogPath(111)
	if err != nil {
		t.Fatalf("detachLogPath: %v", err)
	}
	b, err := detachLogPath(222)
	if err != nil {
		t.Fatalf("detachLogPath: %v", err)
	}
	if a == b {
		t.Fatalf("log paths collide: %q", a)
	}
	if !strings.Contains(a, "111") {
		t.Fatalf("log path %q does not identify the pid", a)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./cmd/ -run 'TestSupervisorArgs|TestDetachLogPath' -v`

Expected: FAIL to build — `undefined: supervisorArgs`, `undefined: detachLogPath`.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/detach.go`:

```go
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// supervisorArgs builds the argv for the background lobster that actually
// plays. It carries --supervised so the child does not spawn a child of its
// own, and drops --detach for the same reason.
func supervisorArgs(exe, ref string, season, episode int) []string {
	args := []string{exe, "play", "--ref", ref, "--supervised"}
	if season > 0 {
		args = append(args, "--season", strconv.Itoa(season))
	}
	if episode > 0 {
		args = append(args, "--episode", strconv.Itoa(episode))
	}
	return args
}

// detachLogPath is where a detached play writes its output. Per-pid so
// concurrent plays do not interleave, and so the JSON payload can point at a
// specific run.
func detachLogPath(pid int) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating cache dir: %w", err)
	}
	dir = filepath.Join(dir, "lobster")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	return filepath.Join(dir, fmt.Sprintf("play-%d.log", pid)), nil
}

// detachLiveness is how long the foreground waits before declaring success.
// cmd.Start succeeding only means the binary exec'd; mpv's own failure
// detection is "exited within 5s with no playback" (internal/player/mpv.go).
// This does not close the race, it removes the common case of reporting
// success for a stream that never started.
var detachLiveness = 1 * time.Second
```

- [ ] **Step 4: Write the platform spawn attributes**

Create `cmd/detach_unix.go`:

```go
//go:build !windows

package cmd

import "syscall"

// detachSpawnAttr puts the supervisor in its own process group so it survives
// the agent's shell exiting.
func detachSpawnAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
```

Create `cmd/detach_windows.go`:

```go
//go:build windows

package cmd

import "syscall"

// detachedProcess is Win32's DETACHED_PROCESS creation flag. Go's syscall
// package exports CREATE_NEW_PROCESS_GROUP but not this one, so it is spelled
// out here rather than left as a bare literal at the call site.
const detachedProcess = 0x00000008

// detachSpawnAttr detaches the supervisor from the console so it survives the
// agent's shell exiting. Setpgid does not exist on Windows.
func detachSpawnAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess,
	}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./cmd/ -run 'TestSupervisorArgs|TestDetachLogPath' -v`

Expected: PASS (3 tests).

- [ ] **Step 6: Verify the Windows build compiles**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 GOOS=windows go build ./... && GOOS=windows go vet ./cmd/`

Expected: no output (success). This is the step that catches `Setpgid` not existing on Windows.

- [ ] **Step 7: Wire `--detach` into play**

In `cmd/play.go`, add to the flag block in `init()`:

```go
	playCmd.Flags().BoolVar(&flagDetach, "detach", false, "Start playback in the background and return immediately")
	playCmd.Flags().BoolVar(&flagSupervised, "supervised", false, "Internal: marks the background supervisor process")
	_ = playCmd.Flags().MarkHidden("supervised")
```

Declare the vars in `cmd/detach.go`:

```go
var (
	flagDetach     bool
	flagSupervised bool
)
```

In `playRun`, insert this immediately after the `flagDownload` check and before the `agentProvider()` call:

```go
	if flagDetach && !flagSupervised {
		return playDetached(r)
	}
```

Add `playDetached` to `cmd/detach.go`:

```go
// playDetached re-executes lobster as a background supervisor and returns as
// soon as it looks alive. The child performs an ordinary attached play, so the
// HLS proxy, torrent server, subtitle temp dir and mpv IPC all behave exactly
// as they do interactively.
func playDetached(r playRef) error {
	exe, err := os.Executable()
	if err != nil {
		return emitErr("internal", 1, "locating lobster binary: %v", err)
	}

	argv := supervisorArgs(exe, flagRef, flagSeason, flagEpisode)

	c := exec.Command(argv[0], argv[1:]...)
	c.SysProcAttr = detachSpawnAttr()
	c.Stdin = nil // no TTY to inherit

	// The child's output must never reach the pipe the caller is parsing: all
	// three players set cmd.Stdout = os.Stdout, which would interleave with the
	// JSON envelope and corrupt it.
	tmpLog, err := detachLogPath(os.Getpid())
	if err != nil {
		return emitErr("internal", 1, "%v", err)
	}
	lf, err := os.Create(tmpLog)
	if err != nil {
		return emitErr("internal", 1, "creating log %s: %v", tmpLog, err)
	}
	defer lf.Close()
	c.Stdout = lf
	c.Stderr = lf

	if err := c.Start(); err != nil {
		return emitErr("player_unavailable", exitPlayerUnavailable, "starting background player: %v", err)
	}

	// Rename the log to the child's pid now that we know it, so the payload
	// points at a file the user can find from the pid alone.
	finalLog, err := detachLogPath(c.Process.Pid)
	if err == nil && os.Rename(tmpLog, finalLog) == nil {
		tmpLog = finalLog
	}

	time.Sleep(detachLiveness)
	if !processAlive(c.Process.Pid) {
		return emitErr("providers_failed", exitProvidersFailed,
			"playback exited immediately; see %s", tmpLog)
	}

	return emitJSON(map[string]any{
		"status":          "playing",
		"pid":             c.Process.Pid,
		"title":           r.Title,
		"log":             tmpLog,
		"resume_tracking": playerTracksPosition(),
	})
}

// playerTracksPosition reports whether the configured player reports playback
// position. Only mpv does: vlc.go and generic.go both return an empty
// PlayResult regardless of detach.
func playerTracksPosition() bool {
	if cfg == nil {
		return false
	}
	return strings.EqualFold(cfg.Player, "mpv")
}
```

Add `processAlive` to `cmd/detach_unix.go`:

```go
// processAlive reports whether the pid is still running. Signal 0 performs the
// permission and existence checks without delivering anything.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
```

Add `processAlive` to `cmd/detach_windows.go`:

```go
// processAlive reports whether the pid is still running. On Windows
// os.FindProcess fails outright for a dead pid, which is the check.
func processAlive(pid int) bool {
	_, err := os.FindProcess(pid)
	return err == nil
}
```

Add the needed imports: `os`, `os/exec`, `strings`, `time` in `cmd/detach.go`; `os`, `syscall` in `cmd/detach_unix.go`; `os`, `syscall` in `cmd/detach_windows.go`.

- [ ] **Step 8: Write a test for the recursion guard**

Append to `cmd/detach_test.go`:

```go
// A supervised process must play, not spawn another supervisor. Without this
// guard --detach forks forever.
func TestSupervisedPlayDoesNotDetachAgain(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	prevProv := agentProvider
	agentProvider = func() provider.Provider { return &stubProvider{} }
	t.Cleanup(func() { agentProvider = prevProv })

	called := false
	prevPlay := agentResolveAndPlay
	agentResolveAndPlay = func(provider.Provider, media.SearchResult, int, int) error {
		called = true
		return nil
	}
	t.Cleanup(func() { agentResolveAndPlay = prevPlay })

	ref, err := encodeRef(playRef{ID: "movie/x", Title: "X", Type: "movie"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef, prevD, prevS := flagRef, flagDetach, flagSupervised
	flagRef, flagDetach, flagSupervised = ref, true, true
	t.Cleanup(func() { flagRef, flagDetach, flagSupervised = prevRef, prevD, prevS })

	if err := playRun(playCmd, nil); err != nil {
		t.Fatalf("playRun: %v", err)
	}
	if !called {
		t.Fatal("supervised play did not reach playback; it re-detached instead")
	}
}
```

Add imports `"lobster/internal/media"` and `"lobster/internal/provider"` to `cmd/detach_test.go`.

- [ ] **Step 9: Run the detach tests**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./cmd/ -run 'TestSupervisor|TestDetach|TestSupervised' -v`

Expected: PASS (4 tests).

- [ ] **Step 10: Verify both platforms and the whole suite**

```bash
cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill
PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go build ./... && go vet ./... && go test ./...
PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 GOOS=windows go build ./...
PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 GOOS=darwin go build ./...
```

Expected: all succeed.

- [ ] **Step 11: Commit**

```bash
git add cmd/detach.go cmd/detach_unix.go cmd/detach_windows.go cmd/detach_test.go cmd/play.go
git commit -m "feat(agent): --detach re-execs lobster as a background supervisor

Skipping cmd.Wait() would have black-screened anime and all YTS
playback: the HLS deobfuscation proxy (player/*.go defer its cleanup),
the torrent server (search.go:522) and the subtitle temp dir
(search.go:497) are all torn down when lobster exits. vlc and generic
also call cmd.Run(), so there is no Wait to skip.

Re-exec means the background process does an ordinary attached play, so
every resource behaves as it does interactively and resume tracking is
preserved rather than sacrificed.

Setpgid is unix-only, so spawn attrs are split behind build tags
following the existing ipc_windows.go pattern. The foreground waits 1s
and checks liveness before reporting success, so the common case of a
stream that dies instantly is not reported as playing."
```

---

### Task 7: The `lobster-play` skill and docs

**Files:**
- Create: `skills/lobster-play/SKILL.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: Tasks 1-6 (the full command surface)
- Produces: no code

- [ ] **Step 1: Write the skill**

Create `skills/lobster-play/SKILL.md`:

```markdown
---
name: lobster-play
description: Use when the user asks to find, search for, or play a movie or TV episode — for example "put on Parasite", "find me something to watch", "play The Bear season 2 episode 3".
---

# Playing movies and TV with lobster

`lobster` is a terminal media streamer. It has a machine-readable mode that
never prompts, which is what you use. Its interactive commands will hang you
forever — see the do-not-run list below.

## Never run these

These block on `fzf` or open a full-screen TUI and will not return:

| Command | What happens |
| ------- | ------------ |
| `lobster` (no arguments) | opens a full-screen TUI |
| `lobster <query>` | blocks on an fzf picker |
| `lobster trending` / `lobster recent` | blocks on an fzf picker |
| `lobster history` | blocks on an fzf picker |

Use `find`, `episodes` and `play` instead. They print JSON on stdout and
never wait for input.

## The workflow

### 1. Find candidates

```
lobster find "the matrix" --limit 10
```

```json
{
  "schema": 1,
  "results": [
    {"idx": 0, "ref": "eyJpZCI6...", "title": "The Matrix", "year": "1999", "type": "movie"}
  ]
}
```

### 2. Show the user the candidates and stop

**Always ask which one before playing.** Do not pick for them, even when one
result looks obviously right. List the titles and years and wait for an answer.

### 3. Play the one they chose

```
lobster play --ref "eyJpZCI6..." --detach
```

```json
{"schema": 1, "status": "playing", "pid": 48213,
 "title": "The Matrix (1999)",
 "log": "/home/u/.cache/lobster/play-48213.log",
 "resume_tracking": true}
```

Always pass `--detach`. Without it the command blocks for the entire film and
your tool call will not return. Report the pid so the user can stop playback.

## TV series

A TV ref needs both `--season` and `--episode`. Get the numbers first:

```
lobster episodes --ref "eyJpZCI6..." --season 2
```

```json
{"schema": 1, "title": "Some Show", "seasons": [1, 2, 3], "season": 2,
 "episodes": [{"number": 1, "title": "Pilot"}]}
```

Then:

```
lobster play --ref "eyJpZCI6..." --season 2 --episode 3 --detach
```

## Rules

- **Never construct or edit a `ref`.** Only pass back one that `find` returned
  verbatim. Refs are opaque; their contents are not a stable interface.
- **Never reuse an `idx` from an earlier search.** It is only meaningful inside
  the payload it came from. Search results vary between runs.
- **Do not overclaim the source.** A ref pins the *selection*, not the stream.
  If the original provider is down, another provider's copy of the same title
  may be served. Say "playing The Matrix (1999)", not "playing it from X".
- **Check `schema`.** If it is not `1`, the installed lobster does not match
  this skill — tell the user instead of guessing at the output.

## When something fails

Errors are JSON on stdout too, so parse unconditionally:

```json
{"schema": 1, "error": {"code": "no_results", "message": "nothing matched \"the matirx\""}}
```

Branch on the exit code:

| Exit | Meaning | What to do |
| ---- | ------- | ---------- |
| 0 | success | — |
| 2 | no results | Suggest a spelling correction, or a different title |
| 3 | every provider failed | Run `lobster doctor` and report which sources are down. Do **not** suggest a spelling fix — the title was found, the sources are broken |
| 4 | player unavailable | mpv (or the configured player) is not installed |

Exit 3 is common. Providers break often; this usually means "try again later"
or "try a different title", not "you typed it wrong".

## Out of scope

Live TV channel listing and channel surfing are not available in this mode.
For those, tell the user to run `lobster` interactively themselves.
```

- [ ] **Step 2: Add install instructions to the README**

Add this section to `README.md`, immediately before the Configuration section:

```markdown
## Use it from a coding agent

`lobster` ships an agent skill so you can ask Claude Code (or another agent)
to find and play something for you.

```bash
mkdir -p ~/.claude/skills
cp -r skills/lobster-play ~/.claude/skills/
```

Then just ask: *"find me The Matrix and play it"*. The agent will show you the
matches and wait for you to pick one before starting playback.

Under the hood it uses three non-interactive commands, which are useful for
scripting on their own:

```bash
lobster find "the matrix" --limit 5      # JSON candidates, each with a ref
lobster episodes --ref <REF> --season 2  # JSON season/episode listing
lobster play --ref <REF> --detach        # start playback, return immediately
```

All three print JSON on stdout and never prompt.
```

- [ ] **Step 3: Verify the skill frontmatter parses**

Run: `cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill && head -5 skills/lobster-play/SKILL.md`

Expected: a `---` delimited block containing `name:` and `description:` on single lines.

- [ ] **Step 4: Verify the whole suite one last time**

```bash
cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill
PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go build ./... && go vet ./... && go test ./...
PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 GOOS=windows go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add skills/lobster-play/SKILL.md README.md
git commit -m "docs(agent): add the lobster-play skill and install instructions

The do-not-run list is the highest-value part: bare lobster opens a TUI
and the query/trending/recent/history commands all block on fzf, so an
agent that runs one hangs with no error, which reads to the user as the
agent freezing.

The skill instructs confirm-before-play rather than leaving it to model
judgement, and tells the agent not to overclaim the source, since a ref
pins the selection but not which provider ultimately serves it."
```

---

## Manual verification

After Task 7, verify by hand — the automated tests deliberately never touch the
network or launch a player, so the end-to-end path is unproven until someone
runs it.

```bash
cd /tmp/claude-1000/-home-williams-Documents-personal-lobster/2d217750-8ae7-47ec-be0b-d867c2ff5307/scratchpad/wt-agentskill
PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go build -o /tmp/lobster-agent .

# 1. find returns JSON and does not hang
/tmp/lobster-agent find "the matrix" --limit 3

# 2. the same, with stdin closed — must still return, not hang
/tmp/lobster-agent find "the matrix" --limit 3 < /dev/null

# 3. play the first result detached; must return in ~1s
REF=$(/tmp/lobster-agent find "the matrix" --limit 1 | grep -o '"ref": "[^"]*"' | cut -d'"' -f4)
time /tmp/lobster-agent play --ref "$REF" --detach

# 4. confirm the player is actually running and the log exists
#    (the pid and log path are in the JSON from step 3)

# 5. a nonsense query exits 2
/tmp/lobster-agent find "zzzznotarealfilmzzzz"; echo "exit=$?"
```

Expected: steps 1-2 print JSON immediately; step 3 returns in roughly a second
with `"status": "playing"`; step 4 shows mpv running; step 5 prints an error
envelope and `exit=2`.

## Sequencing note

This branch is based on `origin/main`. PR #36 (`feat/tbcpl-catalog-feed`) is
being resolved against `cmd/fallback.go` and `cmd/search.go`. Land #36 first,
then rebase or merge `main` into this branch before opening a PR, so the two
sets of changes to those files do not have to be untangled at once.
