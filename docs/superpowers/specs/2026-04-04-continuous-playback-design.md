# Continuous Episode Playback

## Problem

After an episode finishes playing, lobster saves history and exits. Users must re-search, re-select the series, and manually pick the next episode. There is no way to continuously watch a series.

## Solution

Add a post-play loop that auto-plays the next episode after a 10-second countdown, with an interruptible menu for manual navigation (next, previous, replay, episode list, quit). Navigation crosses season boundaries seamlessly.

## Package: `internal/playlist`

New package containing a `Session` struct for episode navigation state.

### Types

```go
type Session struct {
    Provider    provider.Provider
    Content     media.SearchResult
    Seasons     []media.Season
    Episodes    []media.Episode   // episodes for current season
    SeasonIdx   int
    EpisodeIdx  int
    CachedServer *media.Server
}
```

### Methods

- `New(provider, content, seasons, episodes, seasonIdx, episodeIdx) *Session`
- `Current() media.Episode`
- `Next() (media.Episode, error)` — advances to next episode; if at end of season, loads next season's episodes via provider
- `Previous() (media.Episode, error)` — goes to previous episode; if at start of season, loads previous season's last episode via provider
- `HasNext() bool` — true unless at last episode of last season
- `HasPrevious() bool` — true unless at first episode of first season
- `SetEpisodes(episodes []media.Episode, seasonIdx int)` — for when user re-selects from episode list

The session owns navigation state only. It does not own playback, UI, or server resolution.

## Post-Play Loop

Lives in `cmd/session.go` as `runPlaybackLoop(session *playlist.Session, cfg *config.Config)`.

### Flow

```
Episode finishes playing
        │
        ▼
   Has next episode? ──no──▶ "End of series" → return
        │ yes
        ▼
   Show: "Next: S02E03 - Title"
   Show: "Playing in 10s... (press any key for menu)"
        │
        ├── 10s elapsed ──▶ Auto-play next episode
        │
        └── User interrupts ──▶ Show menu:
                                  1. Next episode
                                  2. Previous episode (if available)
                                  3. Replay current
                                  4. Episode list
                                  5. Quit
                                │
                                ▼
                          Execute choice, loop back
```

### Countdown Implementation

Uses fzf with `--timeout 10` via a new `ui.SelectWithTimeout(prompt, items, defaultIdx, timeout)` function. Menu items are pre-populated with "Next episode" as the default. If the timer expires, fzf exits with the default selection. If the user interacts, they pick from the full menu.

### Config

New field in `config.toml`:

```toml
auto_next = true  # default
```

When `false`, skips the countdown and shows the menu immediately (no timeout).

## Server Caching

The session struct holds a `CachedServer` reference. Resolution logic in `cmd/session.go`:

1. If cached server exists, try it first for the new episode
2. On success, play with that server
3. On failure, clear cache and do full resolution (get all servers, try in order)
4. Cache whichever server succeeds

Extracted as `resolveStream(session, cfg) (*media.Stream, error)` to avoid duplicating the server resolution block from `resolveAndPlay`.

## Cross-Season Navigation

- **Next at end of season:** `Session.Next()` detects last episode, increments `SeasonIdx`, calls `provider.GetEpisodes()` for the new season, sets `EpisodeIdx = 0`.
- **Previous at start of season:** `Session.Previous()` detects first episode, decrements `SeasonIdx`, calls `provider.GetEpisodes()` for the previous season, sets `EpisodeIdx` to last episode.
- **End of series:** `HasNext()` returns false at last episode of last season. Loop shows "End of series" and returns.
- **Start of series:** `HasPrevious()` returns false at first episode of first season. Menu hides "Previous episode" option.

## File Changes

### New Files

- `internal/playlist/session.go` — Session struct and navigation methods
- `internal/playlist/session_test.go` — Table-driven tests for navigation including cross-season
- `cmd/session.go` — `runPlaybackLoop` and `resolveStream` helper

### Modified Files

- `cmd/search.go` — `resolveAndPlay` creates a `playlist.Session` for TV shows and calls `runPlaybackLoop` instead of playing directly. Movies unchanged.
- `internal/config/config.go` — Add `AutoNext bool` field, default `true`.
- `internal/ui/ui.go` — Add `SelectWithTimeout(prompt, items, defaultIdx, timeout)` wrapping fzf `--timeout`.

### Unchanged

- `internal/player/` — no changes
- `internal/extract/` — no changes
- `internal/provider/` — no changes (session calls existing methods)
- `internal/download/` — no changes
- `cmd/batch.go` — no changes
- `cmd/history.go` — continues to work via `resolveAndPlay`, session loop kicks in after first episode
