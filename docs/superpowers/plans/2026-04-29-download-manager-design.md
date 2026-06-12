# Download Manager — Implementation Plan

**Date:** 2026-04-29
**Spec:** [download-manager-design.md](../specs/2026-04-29-download-manager-design.md)
**Status:** Ready

## Overview

Implements a download manager with SQLite persistence, resumable HTTP/HLS engines, a goroutine worker pool, and a Downloads tab in the Bubbletea TUI. The plan is ordered so each step produces a testable, compilable unit.

## File Map

### New files
| File | Purpose |
|------|---------|
| `internal/dlmanager/store/store.go` | SQLite schema, migrations, CRUD operations |
| `internal/dlmanager/store/store_test.go` | Store unit tests (in-memory SQLite) |
| `internal/dlmanager/engine/engine.go` | Engine interface + shared types |
| `internal/dlmanager/engine/http.go` | HTTP byte-range download engine |
| `internal/dlmanager/engine/http_test.go` | HTTP engine tests (httptest server) |
| `internal/dlmanager/engine/hls.go` | HLS segment-based download engine |
| `internal/dlmanager/engine/hls_test.go` | HLS engine tests |
| `internal/dlmanager/manager.go` | Worker pool, queue dispatch, progress relay |
| `internal/dlmanager/manager_test.go` | Manager tests with mock engine/store |
| `internal/tui/downloads/model.go` | Downloads tab Bubbletea model |
| `internal/tui/downloads/view.go` | Downloads tab rendering (list + detail panes) |
| `internal/tui/downloads/keys.go` | Downloads tab keybindings |

### Modified files
| File | Change |
|------|--------|
| `internal/tui/app.go` | Add tab state, route keys to active tab, embed downloads model |
| `internal/tui/messages.go` | Add download-related message types |
| `internal/tui/commands.go` | Add download queue/control commands |
| `internal/config/config.go` | Add `MaxConcurrentDownloads` field (default 2) |
| `go.mod` | Add `modernc.org/sqlite` dependency |

---

## Step 1 — SQLite Store (`internal/dlmanager/store`)

**Goal:** Schema creation, CRUD for downloads and segments. All tests use in-memory SQLite (`:memory:`).

### 1a. Schema + Open/Close

Create `store.go` with:

```go
type Store struct { db *sql.DB }

func Open(path string) (*Store, error)   // opens SQLite, runs CREATE TABLE IF NOT EXISTS
func (s *Store) Close() error
```

Tables match the spec schema (downloads + segments). Use `modernc.org/sqlite` driver (pure Go, no CGO).

**Test (`store_test.go`):**
- `TestOpen` — open `:memory:`, verify tables exist via `sqlite_master` query
- `TestClose` — open then close, verify no error

### 1b. Download CRUD

```go
type Download struct {
    ID            int
    Title         string
    MediaTitle    string
    MediaType     string    // "movie" or "tv"
    Season        int
    Episode       int
    MediaID       string    // provider content ID
    EpisodeID     string    // provider episode ID
    StreamURL     string
    StreamType    string    // "hls" or "http"
    Referer       string
    OutputPath    string
    SubtitleURL   string
    Status        string    // pending, queued, downloading, paused, completed, failed
    Error         string
    TotalBytes    int64
    DoneBytes     int64
    TotalSegments int
    DoneSegments  int
    CreatedAt     time.Time
    UpdatedAt     time.Time
}

func (s *Store) InsertDownload(d *Download) (int, error)
func (s *Store) GetDownload(id int) (*Download, error)
func (s *Store) ListDownloads() ([]Download, error)
func (s *Store) UpdateStatus(id int, status, errMsg string) error
func (s *Store) UpdateProgress(id int, doneBytes int64, doneSegments int) error
func (s *Store) UpdateStreamInfo(id int, streamURL, streamType, referer string) error
func (s *Store) DeleteDownload(id int) error
func (s *Store) RecoverStale(maxAge time.Duration) ([]Download, error) // downloading + old updated_at → paused
func (s *Store) ClearCompleted() error
```

**Tests:**
- `TestInsertAndGet` — insert a download, get by ID, verify all fields
- `TestListDownloads` — insert 3, list, verify count and FIFO order
- `TestUpdateStatus` — insert, update to "downloading", verify
- `TestUpdateProgress` — insert, update bytes/segments, verify updated_at changes
- `TestUpdateStreamInfo` — insert pending, set stream URL/type, verify
- `TestDeleteDownload` — insert, delete, verify get returns not found
- `TestRecoverStale` — insert with status "downloading" and old updated_at, call recover, verify status becomes "paused"
- `TestClearCompleted` — insert completed + active, clear, verify only active remains

### 1c. Segment CRUD

```go
type Segment struct {
    ID         int
    DownloadID int
    Idx        int
    URL        string
    Completed  bool
}

func (s *Store) InsertSegments(downloadID int, segments []Segment) error
func (s *Store) GetSegments(downloadID int) ([]Segment, error)
func (s *Store) MarkSegmentDone(downloadID, idx int) error
func (s *Store) CountSegments(downloadID int) (total, done int, err error)
```

**Tests:**
- `TestInsertAndGetSegments` — insert 5 segments, get all, verify order and count
- `TestMarkSegmentDone` — mark segment 2 done, verify completed flag
- `TestCountSegments` — insert 10, mark 3 done, verify counts (10, 3)
- `TestSegmentsCascadeDelete` — delete parent download, verify segments gone

**Commit:** `feat(dlmanager): add SQLite store with download and segment CRUD`

---

## Step 2 — Engine Interface + HTTP Engine (`internal/dlmanager/engine`)

**Goal:** Define the engine contract and implement the HTTP byte-range downloader.

### 2a. Engine Interface

```go
type ProgressFunc func(doneBytes int64, totalBytes int64)

type Engine interface {
    // Download fetches the stream to outputPath. ctx cancellation = pause/cancel.
    // progressFn is called periodically (~500ms) with byte progress.
    Download(ctx context.Context, streamURL, outputPath, referer string, progressFn ProgressFunc) error

    // Resume continues a previously interrupted download.
    Resume(ctx context.Context, streamURL, outputPath, referer string, progressFn ProgressFunc) error

    // Type returns "http" or "hls".
    Type() string
}
```

### 2b. HTTP Engine

`http.go`:

```go
type HTTPEngine struct {
    client *http.Client
}
```

- `Download`: GET with full body, write to `<output>.part`, rename on completion
- `Resume`: stat `.part` file size, GET with `Range: bytes=<size>-`, append to `.part`
- Progress callback every 500ms via a counting writer wrapper
- Respects `context.Context` for cancellation
- Uses `httputil.NewClient()` base with custom transport for Range support
- 3 retries with exponential backoff (2s, 4s, 8s) on network errors

**Tests (httptest server):**
- `TestHTTPDownload` — serve 1MB of random data, download, verify file content + size
- `TestHTTPResume` — download half, cancel ctx, resume, verify complete file
- `TestHTTPProgress` — verify progressFn called with increasing byte counts
- `TestHTTPCancel` — start download, cancel ctx immediately, verify partial `.part` file exists
- `TestHTTPRetry` — server returns 503 twice then 200, verify succeeds after retries
- `TestHTTPRangeNotSupported` — server ignores Range header, engine restarts from beginning

**Commit:** `feat(dlmanager): add HTTP byte-range download engine with resume`

---

## Step 3 — HLS Engine (`internal/dlmanager/engine`)

**Goal:** Segment-based HLS downloader with resume via segment tracking.

### 3a. M3U8 Parser

Add to `hls.go`:

```go
func parseM3U8(body []byte, baseURL string) ([]string, error) // returns absolute segment URLs
```

Parses `#EXTINF` entries, resolves relative URLs against the playlist base URL. Handles both media playlists and master playlists (pick highest bandwidth variant).

### 3b. HLS Engine

```go
type HLSEngine struct {
    client *http.Client
    store  *store.Store // for segment tracking
}
```

- `Download`: fetch m3u8 → parse segments → insert into store → download each sequentially → mux with ffmpeg
- `Resume`: query store for incomplete segments → continue from first incomplete
- Each segment written to `<output>.parts/seg_NNN.ts`
- After all segments: `ffmpeg -i "concat:seg_000.ts|seg_001.ts|..." -c copy output.mkv`
- If subtitle URL provided, embed during mux step
- Progress: done_segments / total_segments reported via progressFn
- Cleans up `.parts/` directory on successful completion

**Tests:**
- `TestParseM3U8` — parse a sample playlist, verify segment URLs resolved correctly
- `TestParseM3U8Master` — parse master playlist, verify picks highest bandwidth variant
- `TestHLSDownload` — httptest serves m3u8 + 3 tiny .ts segments, verify all downloaded + concat file exists
- `TestHLSResume` — download 2 of 5 segments, cancel, resume, verify only remaining 3 fetched
- `TestHLSSegmentProgress` — verify progressFn called with segment counts
- `TestHLSCancel` — cancel mid-download, verify partial segments in `.parts/` dir

**Commit:** `feat(dlmanager): add HLS segment-based download engine with resume`

---

## Step 4 — Download Manager (`internal/dlmanager`)

**Goal:** Worker pool that pulls from the queue, dispatches to engines, and relays progress to the TUI.

### 4a. Manager Core

```go
type Manager struct {
    store      *store.Store
    httpEngine engine.Engine
    hlsEngine  engine.Engine
    workers    int
    progress   chan ProgressUpdate
    // internal: cancel funcs per download ID for pause/cancel
}

type ProgressUpdate struct {
    DownloadID    int
    Status        string
    DoneBytes     int64
    TotalBytes    int64
    DoneSegments  int
    TotalSegments int
    Speed         float64 // bytes/sec rolling 5s average
    Error         string
}

func New(store *store.Store, httpEngine, hlsEngine engine.Engine, workers int) *Manager
func (m *Manager) Start(ctx context.Context)           // launch worker goroutines
func (m *Manager) Stop()                                // cancel all, wait for workers
func (m *Manager) Progress() <-chan ProgressUpdate       // TUI reads from this
func (m *Manager) Queue(d store.Download) (int, error)  // insert + notify workers
func (m *Manager) Pause(id int) error                   // cancel ctx for download, set paused
func (m *Manager) Resume(id int) error                  // set queued, notify workers
func (m *Manager) Cancel(id int) error                  // cancel + set failed
func (m *Manager) Retry(id int) error                   // re-resolve stream, re-queue
func (m *Manager) Remove(id int) error                  // delete from store + partial files
```

### 4b. Worker Loop

Each worker goroutine:
1. Pull next `queued` download from store (FIFO by created_at)
2. If `status = pending` (batch download), resolve stream first via provider
3. Pick engine based on `stream_type` (hls vs http)
4. Call `engine.Download()` or `engine.Resume()` based on existing progress
5. Send `ProgressUpdate` to channel every 500ms
6. On completion: update store status, send final progress
7. On error: retry up to 3x, then mark failed

### 4c. Speed Calculator

Rolling 5-second window for speed calculation:
- Track `(timestamp, bytes)` samples
- Speed = `(latest_bytes - oldest_bytes) / time_diff`
- ETA = `(total - done) / speed`

**Tests (mock engine + in-memory store):**
- `TestManagerQueueAndProcess` — queue a download, verify engine.Download called, status transitions queued → downloading → completed
- `TestManagerPause` — start download, pause, verify engine ctx cancelled + status = paused
- `TestManagerResume` — pause then resume, verify engine.Resume called
- `TestManagerCancel` — cancel active download, verify status = failed
- `TestManagerConcurrency` — queue 4 downloads with 2 workers, verify max 2 concurrent
- `TestManagerRecoverOnStart` — insert stale downloads, start manager, verify recovered and re-queued
- `TestManagerProgressChannel` — queue download, read from Progress(), verify updates received
- `TestManagerRetryOnFailure` — engine returns error, verify retried up to 3x

**Commit:** `feat(dlmanager): add download manager with worker pool and progress relay`

---

## Step 5 — Config Extension (`internal/config`)

**Goal:** Add `max_concurrent_downloads` config field.

### 5a. Config Changes

In `config.go`:
- Add `MaxConcurrentDownloads int` field with `toml:"max_concurrent_downloads"` tag
- Default: 2 in `Default()`
- Validation: must be 1-5

**Tests (config_test.go):**
- `TestDefaultMaxConcurrent` — verify default is 2
- `TestValidateMaxConcurrent` — 0 and 6 fail, 1 and 5 pass

**Commit:** `feat(config): add max_concurrent_downloads setting`

---

## Step 6 — TUI Message Types (`internal/tui`)

**Goal:** Add download-related messages to the Bubbletea message system.

### 6a. New Messages

In `messages.go`, add:

```go
// downloadQueuedMsg is sent when a download is added to the queue.
type downloadQueuedMsg struct {
    downloadID int
    title      string
}

// downloadBatchQueuedMsg is sent when multiple downloads are queued.
type downloadBatchQueuedMsg struct {
    count int
    title string // e.g., "The Rookie Season 6"
}

// downloadProgressMsg relays progress from the download manager.
type downloadProgressMsg dlmanager.ProgressUpdate

// downloadCompleteMsg is sent when a download finishes.
type downloadCompleteMsg struct {
    downloadID int
    outputPath string
}

// downloadErrorMsg is sent when a download fails.
type downloadErrorMsg struct {
    downloadID int
    err        error
}

// downloadListUpdatedMsg signals the downloads list should refresh from store.
type downloadListUpdatedMsg struct{}
```

### 6b. New Commands

In `commands.go`, add:

```go
func queueDownloadCmd(mgr *dlmanager.Manager, d store.Download) tea.Cmd
func queueSeasonCmd(mgr *dlmanager.Manager, episodes []store.Download) tea.Cmd
func listenProgressCmd(mgr *dlmanager.Manager) tea.Cmd  // blocking read from Progress() channel, returns downloadProgressMsg
```

`listenProgressCmd` is a long-running command that reads one update from the progress channel and returns it as a message, then the TUI re-invokes it to keep listening.

**Commit:** `feat(tui): add download message types and commands`

---

## Step 7 — Downloads Tab Model (`internal/tui/downloads`)

**Goal:** Bubbletea model for the Downloads tab with list + detail panes.

### 7a. Model

`model.go`:

```go
type Model struct {
    store     *store.Store
    manager   *dlmanager.Manager
    downloads []store.Download
    selected  int
    width     int
    height    int
    focused   bool
}

func New(s *store.Store, m *dlmanager.Manager) Model
func (m Model) Init() tea.Cmd
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd)
func (m Model) View() string
func (m *Model) SetSize(w, h int)
func (m *Model) SetFocused(focused bool)
func (m *Model) Refresh() tea.Cmd  // reload downloads from store
```

### 7b. View

`view.go`:

Left pane — download list grouped by section:
- **Active** (downloading, queued, paused) — show progress bar + percentage + speed
- **Completed** — show checkmark + file size
- **Failed** — show error icon

Progress bar format: `[████████░░░░] 67% 2.3MB/s ETA 1:23`

Right pane — selected download detail:
- Title, Season/Episode (if TV)
- Status with color coding (green/purple/red/gray per spec)
- Progress: bytes or segments depending on engine type
- Speed + ETA (if active)
- Output path
- Stream URL (truncated)
- Subtitle source
- Error message (if failed)

### 7c. Keybindings

`keys.go`:

| Key | Handler |
|-----|---------|
| `j/k` or `↑/↓` | Navigate list |
| `p` | Pause/Resume selected |
| `x` | Cancel selected |
| `r` | Retry failed |
| `Backspace` | Remove + delete partial file |
| `Enter` | Play completed download |
| `c` | Clear all completed |

Each handler calls the appropriate `Manager` method and returns a `downloadListUpdatedMsg`.

**Commit:** `feat(tui): add Downloads tab model with list, detail, and keybindings`

---

## Step 8 — TUI Integration (`internal/tui/app.go`)

**Goal:** Wire the Downloads tab into the main app with tab switching.

### 8a. Tab State

Add to `AppModel`:

```go
type tab int
const (
    tabBrowse tab = iota
    tabDownloads
)

// New fields in AppModel:
activeTab      tab
downloadsModel downloads.Model
dlManager      *dlmanager.Manager
dlStore        *store.Store
```

### 8b. Initialization

In `StartApp`:
- Open SQLite store at `dataDir()/downloads.db`
- Create engines (HTTP + HLS)
- Create manager with config.MaxConcurrentDownloads workers
- Start manager
- Initialize downloads model
- On app exit: stop manager, close store

### 8c. Key Routing

In `Update()`:
- `Tab`, `1`, `2` — switch active tab (only when not in search mode)
- When `activeTab == tabBrowse`: existing key routing + new `d`/`D` keys
- When `activeTab == tabDownloads`: route to `downloadsModel.Update()`
- `downloadProgressMsg` — always forward to downloads model regardless of active tab

### 8d. Download Keybindings on Browse Tab

- `d` key: resolve stream for current item → queue single download → show toast "Queued: title"
- `D` key: queue all episodes in current season as pending (no stream resolution) → show toast "Queued N episodes"

Stream resolution for `d`: reuse existing server flow (GetServers → GetEmbedURL or Watch) in a tea.Cmd.

### 8e. View Changes

- Add tab bar above main content: `[1 Browse] [2 Downloads]` with active tab highlighted
- When `tabDownloads` active: render `downloadsModel.View()` instead of browse panes
- Footer updates based on active tab (download keybindings vs browse keybindings)

### 8f. Progress Listener

On `Init()`, start `listenProgressCmd` which feeds `downloadProgressMsg` into the update loop. Each time a message arrives, re-invoke to keep listening (self-scheduling pattern).

**Commit:** `feat(tui): integrate Downloads tab with tab switching and download keybindings`

---

## Step 9 — Dependency & Wiring

**Goal:** Add SQLite dependency, wire everything in `main.go` or `StartApp`.

### 9a. Go Module

```bash
go get modernc.org/sqlite
```

### 9b. Database Path

Store SQLite at the XDG data directory alongside history:
- Unix: `~/.local/share/lobster/downloads.db`
- Windows: `%LOCALAPPDATA%\lobster\downloads.db`

Add to `config/paths_unix.go` and `config/paths_windows.go`:
```go
func DownloadsDBPath() (string, error)
```

Or simply use the existing `dataDir()` + `"downloads.db"`.

**Commit:** `feat: wire SQLite dependency and database path`

---

## Step 10 — Integration Testing & Polish

**Goal:** End-to-end verification and edge case handling.

### 10a. Integration Test

- Start manager with in-memory store + httptest server engines
- Queue a download via manager
- Verify progress updates flow through channel
- Verify store reflects completed status
- Verify output file exists and matches source

### 10b. Edge Cases

- Empty download queue: Downloads tab shows "No downloads yet. Press d on any title to start."
- Network loss mid-download: engine retries 3x, then marks failed
- Disk full: engine detects write error, marks failed with clear message
- App crash recovery: on startup, RecoverStale() finds orphaned downloads, resets to paused

### 10c. Polish

- Toast notification system: brief message overlay when download queued (fades after 2s)
- Smooth progress bar animation via Bubbletea tick
- Tab indicator in header showing download count badge: `[Downloads (3)]`

**Commit:** `feat(dlmanager): add integration tests and polish`

---

## Execution Order

```
Step 1  → SQLite Store (no dependencies)
Step 2  → HTTP Engine (depends on store types only)
Step 3  → HLS Engine (depends on store for segment tracking)
Step 4  → Manager (depends on store + engines)
Step 5  → Config (independent, can parallel with 1-4)
Step 6  → TUI Messages (depends on manager types)
Step 7  → Downloads Tab (depends on store + manager)
Step 8  → TUI Integration (depends on everything)
Step 9  → Wiring (depends on everything)
Step 10 → Testing & Polish (depends on everything)
```

Steps 1-3 can be developed in sequence. Step 5 can be done in parallel with any step. Steps 6-8 are sequential. Step 9 ties it together.

## Risk Notes

- **modernc.org/sqlite** is a large dependency (~30MB compiled) but avoids CGO entirely, critical for cross-compilation
- **ffmpeg dependency** for HLS muxing is already present (used by existing download.go)
- **Token expiry** for batch downloads: the just-in-time resolution in Step 4b worker loop handles this
- **Concurrent SQLite writes**: modernc.org/sqlite supports WAL mode; enable `PRAGMA journal_mode=WAL` on open
