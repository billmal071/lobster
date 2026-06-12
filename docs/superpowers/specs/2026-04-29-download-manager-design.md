# Download Manager — Design Spec

**Date:** 2026-04-29
**Status:** Approved

## Overview

A download manager integrated as a second tab in lobster's Bubbletea TUI. Supports resumable downloads with SQLite persistence, real-time progress bars, and concurrent download workers.

## Architecture

Single-process, goroutine-based. Downloads run as goroutines coordinated by a download manager with a configurable worker pool (default: 2 concurrent). Progress updates flow through Bubbletea's message system. SQLite stores download state for resume across restarts.

```
┌─────────────────────────────────────────────┐
│  Bubbletea TUI                              │
│  ┌───────────┐  ┌────────────────────────┐  │
│  │ Browse Tab │  │ Downloads Tab          │  │
│  │ (existing) │  │ - Queue list           │  │
│  │            │  │ - Progress bars        │  │
│  │            │  │ - Status/controls      │  │
│  └───────────┘  └────────────────────────┘  │
│         │                    ▲               │
│         │ queue              │ progress msgs │
│         ▼                    │               │
│  ┌──────────────────────────────────┐       │
│  │  Download Manager (goroutine)    │       │
│  │  - Worker pool (2 concurrent)    │       │
│  │  - Queue management              │       │
│  │  - Resume logic                  │       │
│  └──────────┬───────────────────────┘       │
│             │                                │
│         ┌───┴───┐                            │
│         ▼       ▼                            │
│  ┌──────────┐ ┌──────────┐                  │
│  │ HTTP     │ │ HLS      │                  │
│  │ Downldr  │ │ Downldr  │                  │
│  │(byte-rng)│ │(segments)│                  │
│  └──────────┘ └──────────┘                  │
└─────────────────────────────────────────────┘
          │
          ▼
    ┌──────────┐
    │  SQLite   │
    │ downloads │
    │ segments  │
    └──────────┘
```

## New Packages

| Package | Purpose |
|---------|---------|
| `internal/dlmanager` | Download manager, worker pool, queue logic |
| `internal/dlmanager/engine` | HTTP and HLS download engines with resume |
| `internal/dlmanager/store` | SQLite schema, queries, state persistence |
| `internal/tui/downloads` | Downloads tab Bubbletea model + view |

## Existing File Changes

| File | Change |
|------|--------|
| `internal/tui/app.go` | Add tab switching, route keys to active tab |
| `internal/tui/messages.go` | Add download progress/complete/error messages |
| `internal/config/config.go` | Add `max_concurrent_downloads` field |
| `go.mod` | Add `modernc.org/sqlite` (pure Go, no CGO) |

## Download Engines

### Direct HTTP (mp4, mkv URLs)

- Standard HTTP GET with `Range: bytes=X-` header for resume
- On start: check SQLite for existing progress, send Range request from last byte offset
- Write to `<filename>.part`, rename to final name on completion
- Progress metric: bytes downloaded / Content-Length

### HLS (m3u8 — majority of content)

- Parse m3u8 playlist to get list of `.ts` segment URLs
- Download segments sequentially, track each in SQLite (segment index + completed flag)
- On resume: skip completed segments, continue from first incomplete
- Write segments to `<output>.parts/seg_000.ts`, `seg_001.ts`, etc.
- After all segments: mux to MKV via `ffmpeg -i concat:... -c copy`
- Embed subtitle during mux if available
- Clean up `.parts/` directory on completion
- Progress metric: completed segments / total segments

### Resume Flow on Restart

1. App starts, query SQLite for downloads with status `downloading` or `paused`
2. Stale detection: if `status = 'downloading'` but `updated_at` older than 60 seconds, treat as crashed (resumable)
3. For HTTP: check `.part` file size, resume from that byte offset
4. For HLS: query completed segments, continue from next incomplete
5. Downloads with status `queued` stay in queue, processed by worker pool in FIFO order

## SQLite Schema

```sql
CREATE TABLE downloads (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    title       TEXT NOT NULL,
    media_title TEXT NOT NULL,
    media_type  TEXT NOT NULL,
    season      INTEGER DEFAULT 0,
    episode     INTEGER DEFAULT 0,
    media_id    TEXT NOT NULL DEFAULT '',  -- provider-specific content ID (for lazy stream resolution)
    episode_id  TEXT NOT NULL DEFAULT '',  -- provider-specific episode ID (for lazy stream resolution)
    stream_url  TEXT NOT NULL DEFAULT '',  -- empty until stream is resolved
    stream_type TEXT NOT NULL DEFAULT '',  -- hls or http; empty until resolved
    referer     TEXT DEFAULT '',
    output_path TEXT NOT NULL,
    subtitle_url TEXT DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'queued',
    error       TEXT DEFAULT '',
    total_bytes INTEGER DEFAULT 0,
    done_bytes  INTEGER DEFAULT 0,
    total_segments INTEGER DEFAULT 0,
    done_segments  INTEGER DEFAULT 0,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE segments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    download_id INTEGER NOT NULL REFERENCES downloads(id) ON DELETE CASCADE,
    idx         INTEGER NOT NULL,
    url         TEXT NOT NULL,
    completed   BOOLEAN DEFAULT 0,
    UNIQUE(download_id, idx)
);
```

### Status Transitions

```
pending → queued (stream resolved) → downloading → completed
                                                 → paused → downloading (resume)
                                                 → failed → queued (retry, re-resolves stream)
```

`pending`: metadata only, stream not yet resolved (used for batch `D` downloads).
`queued`: stream resolved, waiting for a worker.

### Stale Detection

On app startup, any download with `status = 'downloading'` and `updated_at` older than 60 seconds is treated as crashed mid-download. These are reset to `paused` and become resumable.

## TUI Design

### Tab Switching

The TUI has two tabs: Browse (existing) and Downloads (new). Only one is visible at a time.

| Key | Action |
|-----|--------|
| `Tab` | Toggle between Browse and Downloads |
| `1` | Switch to Browse tab |
| `2` | Switch to Downloads tab |

### Downloads Tab Layout

Same 2-pane layout as Browse tab for consistency:

- **Left pane**: Download queue list grouped by Active/Completed. Each entry shows title and a compact progress bar with percentage, speed, and ETA.
- **Right pane**: Full details for the selected download — show name, season/episode, status, progress (bytes or segments), speed, ETA, output path, subtitle source, stream source.

Color coding:
- Green (`#50fa7b`): downloading / completed
- Purple (`#bd93f9`): queued
- Red (`#ff5555`): failed
- Gray (`#888888`): completed items in list

### Downloads Tab Keybindings

| Key | Action |
|-----|--------|
| `↑/↓` or `j/k` | Navigate download list |
| `p` | Pause / Resume selected download |
| `x` | Cancel selected download (keeps partial file) |
| `r` | Retry failed download |
| `Backspace` | Remove from list and delete partial file |
| `Enter` | Play completed download in media player |
| `c` | Clear all completed downloads from list |

### Browse Tab Additions

New keybindings on the Browse tab:

| Key | Action |
|-----|--------|
| `d` | Download selected movie/episode (queue it) |
| `D` | Download entire season (queue all episodes) |

When `d` is pressed: resolve stream → queue download → toast notification "Queued: S06E01" → stay on Browse tab.
When `D` is pressed: confirm prompt → queue all episodes with metadata (no stream resolution yet) → toast "Queued 22 episodes". Each worker resolves the stream just before downloading to avoid token expiry.

### Post-Selection Menu Enhancement

After selecting content and picking an episode, the action menu shows:
- **Play** (default, Enter)
- **Download**
- **Download Season**

## Download Manager Internals

### Worker Pool

- 2 concurrent download workers by default (configurable via `max_concurrent_downloads` in config.toml)
- Workers pull from queue in FIFO order
- Each worker sends progress updates to TUI via a channel every 500ms

### Progress Reporting

```go
type ProgressUpdate struct {
    DownloadID    int
    Status        string  // downloading, paused, completed, failed
    DoneBytes     int64
    TotalBytes    int64
    DoneSegments  int
    TotalSegments int
    Speed         float64 // bytes per second, rolling 5-second average
}
```

Flow: engine goroutine → channel → download manager → `tea.Program.Send()` → TUI rerender

### Error Handling

- Network errors: retry 3 times with exponential backoff (2s, 4s, 8s)
- CDN 403/429: mark as failed, user retries manually
- Partial segment corruption: re-download that segment on resume
- ffmpeg mux failure: keep segments intact, mark failed, user can retry mux step

### Stream Resolution Timing

For single downloads (`d` key), streams are resolved at queue time for immediate feedback. For batch downloads (`D` key), only metadata is queued — each worker resolves the stream just before downloading to avoid token expiry. On retry of any failed download, a fresh stream is always re-resolved.

### Subtitle Handling

- Subtitles are resolved at queue time (SubDL → OpenSubtitles fallback)
- The resolved subtitle URL is stored in SQLite alongside the download record
- During the final ffmpeg mux step, the subtitle is embedded into the MKV container

## File Organization

Output follows the existing batch download convention:

```
~/Videos/lobster/
  The Rookie/
    Season 06/
      S06E01 - Strike Back.mkv
      S06E02 - The Hammer.mkv
  Inception (2010).mkv
```

Partial files during download:
```
~/Videos/lobster/
  The Rookie/
    Season 06/
      S06E01 - Strike Back.mkv.part        # HTTP direct download
      S06E01 - Strike Back.mkv.parts/      # HLS segments directory
        seg_000.ts
        seg_001.ts
        ...
```

## Configuration

New config.toml fields:

```toml
max_concurrent_downloads = 2    # number of parallel download workers
```

## Dependencies

- `modernc.org/sqlite` — pure Go SQLite driver (no CGO required, cross-platform)
