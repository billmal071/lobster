# Continuous Episode Playback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** After an episode finishes, auto-play the next episode with a 10-second interruptible countdown, with a full navigation menu (next/prev/replay/episode list/quit) and cross-season support.

**Architecture:** New `internal/playlist` package owns episode navigation state (current position, cross-season traversal). New `cmd/session.go` owns the interactive post-play loop (countdown, menu, server resolution with caching). `ui.SelectWithTimeout` implements the countdown using a Go timer + stdin reader since fzf lacks a native timeout flag.

**Tech Stack:** Go 1.22+, fzf (TUI), Cobra (CLI), TOML (config)

---

### Task 1: Add `AutoNext` config field

**Files:**
- Modify: `internal/config/config.go:17-25` (Config struct)
- Modify: `internal/config/config.go:28-39` (Default func)

- [ ] **Step 1: Add `AutoNext` field to Config struct**

In `internal/config/config.go`, add the `AutoNext` field to the `Config` struct:

```go
type Config struct {
	Base         string `toml:"base"`
	Player       string `toml:"player"`
	Provider     string `toml:"provider"`
	SubsLanguage string `toml:"subs_language"`
	Quality      string `toml:"quality"`
	History      bool   `toml:"history"`
	AutoNext     bool   `toml:"auto_next"`
	DownloadDir  string `toml:"download_dir"`
	Debug        bool   `toml:"debug"`
}
```

- [ ] **Step 2: Set default to `true` in `Default()`**

```go
func Default() *Config {
	return &Config{
		Base:         "flixhq.to",
		Player:       "mpv",
		Provider:     "Vidcloud",
		SubsLanguage: "english",
		Quality:      "1080",
		History:      true,
		AutoNext:     true,
		DownloadDir:  "~/Videos/lobster",
		Debug:        false,
	}
}
```

- [ ] **Step 3: Verify it compiles**

Run: `cd /home/williams/Documents/personal/lobster && go build ./...`
Expected: Clean build, no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go
git commit -m "feat: add auto_next config option for continuous playback"
```

---

### Task 2: Add `SelectWithTimeout` to UI package

**Files:**
- Modify: `internal/ui/ui.go`

The countdown cannot use fzf `--timeout` (not available in fzf 0.44). Instead, implement a Go-native countdown that reads a single keypress from stdin. If 10 seconds pass with no input, return the default index. If the user presses Enter, show the fzf menu.

- [ ] **Step 1: Add `SelectWithTimeout` function**

Append to `internal/ui/ui.go`. Uses a goroutine for stdin reading since `os.Stdin` does not support `SetReadDeadline` on all platforms:

```go
// SelectWithTimeout presents a countdown prompt. If the user does not press
// any key within the timeout, it returns defaultIdx. If the user presses any
// key, it falls through to a normal fzf Select with the given items.
// Pressing 'q' cancels and returns an error.
func SelectWithTimeout(prompt string, items []string, defaultIdx int, timeout time.Duration) (int, error) {
	if len(items) == 0 {
		return -1, fmt.Errorf("no items to select from")
	}
	if defaultIdx < 0 || defaultIdx >= len(items) {
		return -1, fmt.Errorf("default index %d out of range", defaultIdx)
	}

	// Put terminal into raw mode to catch single keypress
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		// Fallback: can't do raw mode, just show the menu
		return Select(prompt, items)
	}

	fmt.Fprintf(os.Stderr, "\n  %s: %s\n", prompt, items[defaultIdx])

	// Channel for keypress
	keyCh := make(chan byte, 1)
	go func() {
		buf := make([]byte, 1)
		n, _ := os.Stdin.Read(buf)
		if n > 0 {
			keyCh <- buf[0]
		}
	}()

	// Countdown with 1-second ticks
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	deadline := time.After(timeout)
	remaining := int(timeout.Seconds())

	fmt.Fprintf(os.Stderr, "\r  Playing in %ds — press Enter for menu, q to quit...", remaining)

	for {
		select {
		case key := <-keyCh:
			term.Restore(int(os.Stdin.Fd()), oldState)
			fmt.Fprintf(os.Stderr, "\r\033[K")
			if key == 'q' || key == 'Q' {
				return -1, fmt.Errorf("selection cancelled")
			}
			return Select(prompt, items)

		case <-ticker.C:
			remaining--
			if remaining > 0 {
				fmt.Fprintf(os.Stderr, "\r  Playing in %ds — press Enter for menu, q to quit...", remaining)
			}

		case <-deadline:
			term.Restore(int(os.Stdin.Fd()), oldState)
			fmt.Fprintf(os.Stderr, "\r\033[K")
			return defaultIdx, nil
		}
	}
}
```

- [ ] **Step 2: Add required imports**

Add `"time"` and `"golang.org/x/term"` to the import block in `ui.go`:

```go
import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/term"
)
```

- [ ] **Step 3: Add the `golang.org/x/term` dependency**

Run: `cd /home/williams/Documents/personal/lobster && go get golang.org/x/term`

- [ ] **Step 4: Verify it compiles**

Run: `cd /home/williams/Documents/personal/lobster && go build ./...`
Expected: Clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/ui.go go.mod go.sum
git commit -m "feat: add SelectWithTimeout for countdown episode menu"
```

---

### Task 3: Create `internal/playlist` package — Session struct and navigation

**Files:**
- Create: `internal/playlist/session.go`

- [ ] **Step 1: Create the session file with types and constructor**

Create `internal/playlist/session.go`:

```go
// Package playlist manages episode navigation state for continuous playback.
package playlist

import (
	"fmt"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// Session tracks the current position within a TV show's episodes,
// supporting navigation across episodes and seasons.
type Session struct {
	Provider     provider.Provider
	Content      media.SearchResult
	Seasons      []media.Season
	Episodes     []media.Episode // episodes for current season
	SeasonIdx    int
	EpisodeIdx   int
	LastPosition float64 // playback position from most recent play
}

// New creates a Session positioned at the given season and episode.
func New(p provider.Provider, content media.SearchResult, seasons []media.Season, episodes []media.Episode, seasonIdx, episodeIdx int) *Session {
	return &Session{
		Provider:   p,
		Content:    content,
		Seasons:    seasons,
		Episodes:   episodes,
		SeasonIdx:  seasonIdx,
		EpisodeIdx: episodeIdx,
	}
}

// Current returns the currently selected episode.
func (s *Session) Current() media.Episode {
	return s.Episodes[s.EpisodeIdx]
}

// CurrentSeason returns the currently selected season.
func (s *Session) CurrentSeason() media.Season {
	return s.Seasons[s.SeasonIdx]
}

// Title returns a formatted title like "Show S01E03".
func (s *Session) Title() string {
	ep := s.Current()
	sn := s.CurrentSeason()
	return fmt.Sprintf("%s S%02dE%02d", s.Content.Title, sn.Number, ep.Number)
}

// HasNext returns true if there is a next episode in this season or a next season.
func (s *Session) HasNext() bool {
	if s.EpisodeIdx < len(s.Episodes)-1 {
		return true
	}
	return s.SeasonIdx < len(s.Seasons)-1
}

// HasPrevious returns true if there is a previous episode in this season or a previous season.
func (s *Session) HasPrevious() bool {
	if s.EpisodeIdx > 0 {
		return true
	}
	return s.SeasonIdx > 0
}

// Next advances to the next episode. If at the end of a season, loads the
// next season's episodes from the provider. Returns an error if already at
// the last episode of the last season.
func (s *Session) Next() (media.Episode, error) {
	if s.EpisodeIdx < len(s.Episodes)-1 {
		s.EpisodeIdx++
		return s.Current(), nil
	}

	if s.SeasonIdx >= len(s.Seasons)-1 {
		return media.Episode{}, fmt.Errorf("no next episode: end of series")
	}

	// Cross to next season
	s.SeasonIdx++
	nextSeason := s.Seasons[s.SeasonIdx]
	episodes, err := s.Provider.GetEpisodes(s.Content.ID, nextSeason.ID)
	if err != nil {
		s.SeasonIdx-- // rollback
		return media.Episode{}, fmt.Errorf("loading season %d episodes: %w", nextSeason.Number, err)
	}
	if len(episodes) == 0 {
		s.SeasonIdx--
		return media.Episode{}, fmt.Errorf("season %d has no episodes", nextSeason.Number)
	}

	s.Episodes = episodes
	s.EpisodeIdx = 0
	return s.Current(), nil
}

// Previous moves to the previous episode. If at the start of a season, loads
// the previous season's episodes and positions at the last episode.
func (s *Session) Previous() (media.Episode, error) {
	if s.EpisodeIdx > 0 {
		s.EpisodeIdx--
		return s.Current(), nil
	}

	if s.SeasonIdx <= 0 {
		return media.Episode{}, fmt.Errorf("no previous episode: start of series")
	}

	// Cross to previous season
	s.SeasonIdx--
	prevSeason := s.Seasons[s.SeasonIdx]
	episodes, err := s.Provider.GetEpisodes(s.Content.ID, prevSeason.ID)
	if err != nil {
		s.SeasonIdx++ // rollback
		return media.Episode{}, fmt.Errorf("loading season %d episodes: %w", prevSeason.Number, err)
	}
	if len(episodes) == 0 {
		s.SeasonIdx++
		return media.Episode{}, fmt.Errorf("season %d has no episodes", prevSeason.Number)
	}

	s.Episodes = episodes
	s.EpisodeIdx = len(s.Episodes) - 1
	return s.Current(), nil
}

// SetEpisodes replaces the episode list (e.g., when user picks from episode list).
func (s *Session) SetEpisodes(episodes []media.Episode, seasonIdx, episodeIdx int) {
	s.Episodes = episodes
	s.SeasonIdx = seasonIdx
	s.EpisodeIdx = episodeIdx
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /home/williams/Documents/personal/lobster && go build ./...`
Expected: Clean build.

- [ ] **Step 3: Commit**

```bash
git add internal/playlist/session.go
git commit -m "feat: add playlist session for episode navigation"
```

---

### Task 4: Test `internal/playlist` — navigation and cross-season logic

**Files:**
- Create: `internal/playlist/session_test.go`

- [ ] **Step 1: Create test file with mock provider and table-driven tests**

Create `internal/playlist/session_test.go`:

```go
package playlist

import (
	"fmt"
	"testing"

	"lobster/internal/media"
)

// mockProvider implements provider.Provider for testing.
type mockProvider struct {
	episodes map[string][]media.Episode // keyed by seasonID
}

func (m *mockProvider) Search(query string) ([]media.SearchResult, error) {
	return nil, nil
}
func (m *mockProvider) GetDetails(id string) (*media.ContentDetail, error) {
	return nil, nil
}
func (m *mockProvider) GetSeasons(id string) ([]media.Season, error) { return nil, nil }
func (m *mockProvider) GetEpisodes(id string, seasonID string) ([]media.Episode, error) {
	eps, ok := m.episodes[seasonID]
	if !ok {
		return nil, fmt.Errorf("season %s not found", seasonID)
	}
	return eps, nil
}
func (m *mockProvider) GetServers(id string, episodeID string) ([]media.Server, error) {
	return nil, nil
}
func (m *mockProvider) GetEmbedURL(serverID string) (string, error) { return "", nil }
func (m *mockProvider) Trending(mt media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}
func (m *mockProvider) Recent(mt media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}

func newTestSession() *Session {
	mp := &mockProvider{
		episodes: map[string][]media.Episode{
			"s1": {
				{Number: 1, Title: "Pilot", ID: "e1"},
				{Number: 2, Title: "Second", ID: "e2"},
				{Number: 3, Title: "Third", ID: "e3"},
			},
			"s2": {
				{Number: 1, Title: "S2 Premiere", ID: "e4"},
				{Number: 2, Title: "S2 Second", ID: "e5"},
			},
			"s3": {
				{Number: 1, Title: "S3 Premiere", ID: "e6"},
			},
		},
	}

	seasons := []media.Season{
		{Number: 1, ID: "s1"},
		{Number: 2, ID: "s2"},
		{Number: 3, ID: "s3"},
	}

	episodes := []media.Episode{
		{Number: 1, Title: "Pilot", ID: "e1"},
		{Number: 2, Title: "Second", ID: "e2"},
		{Number: 3, Title: "Third", ID: "e3"},
	}

	content := media.SearchResult{
		ID:    "show-1",
		Title: "Test Show",
		Type:  media.TV,
	}

	return New(mp, content, seasons, episodes, 0, 0)
}

func TestNext(t *testing.T) {
	tests := []struct {
		name       string
		seasonIdx  int
		episodeIdx int
		wantEpNum  int
		wantSnNum  int
		wantErr    bool
	}{
		{
			name:       "next within season",
			seasonIdx:  0,
			episodeIdx: 0,
			wantEpNum:  2,
			wantSnNum:  1,
		},
		{
			name:       "next crosses to next season",
			seasonIdx:  0,
			episodeIdx: 2,
			wantEpNum:  1,
			wantSnNum:  2,
		},
		{
			name:       "next at end of series",
			seasonIdx:  2,
			episodeIdx: 0,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestSession()
			s.SeasonIdx = tt.seasonIdx
			s.EpisodeIdx = tt.episodeIdx

			// If crossing seasons, load the correct episodes for the starting season
			if tt.seasonIdx > 0 {
				seasonID := s.Seasons[tt.seasonIdx].ID
				eps, _ := s.Provider.GetEpisodes(s.Content.ID, seasonID)
				s.Episodes = eps
			}

			ep, err := s.Next()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ep.Number != tt.wantEpNum {
				t.Errorf("episode number = %d, want %d", ep.Number, tt.wantEpNum)
			}
			if s.CurrentSeason().Number != tt.wantSnNum {
				t.Errorf("season number = %d, want %d", s.CurrentSeason().Number, tt.wantSnNum)
			}
		})
	}
}

func TestPrevious(t *testing.T) {
	tests := []struct {
		name       string
		seasonIdx  int
		episodeIdx int
		wantEpNum  int
		wantSnNum  int
		wantErr    bool
	}{
		{
			name:       "previous within season",
			seasonIdx:  0,
			episodeIdx: 2,
			wantEpNum:  2,
			wantSnNum:  1,
		},
		{
			name:       "previous crosses to prior season last episode",
			seasonIdx:  1,
			episodeIdx: 0,
			wantEpNum:  3,
			wantSnNum:  1,
		},
		{
			name:       "previous at start of series",
			seasonIdx:  0,
			episodeIdx: 0,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestSession()
			s.SeasonIdx = tt.seasonIdx
			s.EpisodeIdx = tt.episodeIdx

			if tt.seasonIdx > 0 {
				seasonID := s.Seasons[tt.seasonIdx].ID
				eps, _ := s.Provider.GetEpisodes(s.Content.ID, seasonID)
				s.Episodes = eps
			}

			ep, err := s.Previous()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ep.Number != tt.wantEpNum {
				t.Errorf("episode number = %d, want %d", ep.Number, tt.wantEpNum)
			}
			if s.CurrentSeason().Number != tt.wantSnNum {
				t.Errorf("season number = %d, want %d", s.CurrentSeason().Number, tt.wantSnNum)
			}
		})
	}
}

func TestHasNextAndHasPrevious(t *testing.T) {
	tests := []struct {
		name        string
		seasonIdx   int
		episodeIdx  int
		wantNext    bool
		wantPrev    bool
	}{
		{"middle of season", 0, 1, true, true},
		{"first ep first season", 0, 0, true, false},
		{"last ep last season", 2, 0, false, true},
		{"last ep mid season", 0, 2, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestSession()
			s.SeasonIdx = tt.seasonIdx
			s.EpisodeIdx = tt.episodeIdx

			if tt.seasonIdx > 0 {
				seasonID := s.Seasons[tt.seasonIdx].ID
				eps, _ := s.Provider.GetEpisodes(s.Content.ID, seasonID)
				s.Episodes = eps
			}

			if got := s.HasNext(); got != tt.wantNext {
				t.Errorf("HasNext() = %v, want %v", got, tt.wantNext)
			}
			if got := s.HasPrevious(); got != tt.wantPrev {
				t.Errorf("HasPrevious() = %v, want %v", got, tt.wantPrev)
			}
		})
	}
}

func TestTitle(t *testing.T) {
	s := newTestSession()
	want := "Test Show S01E01"
	if got := s.Title(); got != want {
		t.Errorf("Title() = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run the tests**

Run: `cd /home/williams/Documents/personal/lobster && go test ./internal/playlist/ -v -race`
Expected: All tests PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/playlist/session_test.go
git commit -m "test: add playlist session navigation tests"
```

---

### Task 5: Create `cmd/session.go` — playback loop and server resolution

**Files:**
- Create: `cmd/session.go`

- [ ] **Step 1: Create the session playback loop file**

Create `cmd/session.go`:

```go
package cmd

import (
	"fmt"
	"os"
	"time"

	"lobster/internal/download"
	"lobster/internal/extract"
	"lobster/internal/history"
	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/playlist"
	"lobster/internal/subtitle"
	"lobster/internal/ui"
)

// cachedServer holds the last working server to reuse across episodes.
var cachedServer *media.Server

// runPlaybackLoop plays the current episode and then loops with a
// countdown/menu for continuous playback.
func runPlaybackLoop(sess *playlist.Session) error {
	for {
		if err := playCurrentEpisode(sess); err != nil {
			return err
		}

		// Save to history after each episode
		saveHistory(sess)

		// If no next episode, we're done
		if !sess.HasNext() {
			fmt.Fprintln(os.Stderr, "\n  End of series.")
			return nil
		}

		// Build menu items
		action, err := postPlayMenu(sess)
		if err != nil {
			return err
		}

		switch action {
		case actionNext:
			if _, err := sess.Next(); err != nil {
				return fmt.Errorf("advancing to next episode: %w", err)
			}
			debugf("next: %s", sess.Title())
		case actionPrevious:
			if _, err := sess.Previous(); err != nil {
				return fmt.Errorf("going to previous episode: %w", err)
			}
			debugf("previous: %s", sess.Title())
		case actionReplay:
			debugf("replaying: %s", sess.Title())
			// EpisodeIdx stays the same, just loop
		case actionEpisodeList:
			if err := episodeListMenu(sess); err != nil {
				return err
			}
		case actionQuit:
			return nil
		}
	}
}

type postPlayAction int

const (
	actionNext postPlayAction = iota
	actionPrevious
	actionReplay
	actionEpisodeList
	actionQuit
)

// postPlayMenu shows the countdown or immediate menu based on config.
func postPlayMenu(sess *playlist.Session) (postPlayAction, error) {
	items := buildMenuItems(sess)
	labels := make([]string, len(items))
	for i, item := range items {
		labels[i] = item.label
	}

	var idx int
	var err error

	if cfg.AutoNext {
		// Show countdown with auto-play next
		nextEp := peekNextTitle(sess)
		prompt := fmt.Sprintf("Next: %s", nextEp)
		idx, err = ui.SelectWithTimeout(prompt, labels, 0, 10*time.Second)
	} else {
		idx, err = ui.Select("Action", labels)
	}

	if err != nil {
		return actionQuit, err
	}

	return items[idx].action, nil
}

type menuItem struct {
	label  string
	action postPlayAction
}

// buildMenuItems creates the menu based on current session state.
func buildMenuItems(sess *playlist.Session) []menuItem {
	items := []menuItem{}

	if sess.HasNext() {
		items = append(items, menuItem{"Next episode", actionNext})
	}
	if sess.HasPrevious() {
		items = append(items, menuItem{"Previous episode", actionPrevious})
	}
	items = append(items, menuItem{"Replay current episode", actionReplay})
	items = append(items, menuItem{"Episode list", actionEpisodeList})
	items = append(items, menuItem{"Quit", actionQuit})

	return items
}

// peekNextTitle returns a display string for the next episode without advancing.
func peekNextTitle(sess *playlist.Session) string {
	ep := sess.EpisodeIdx
	sn := sess.SeasonIdx

	if ep < len(sess.Episodes)-1 {
		next := sess.Episodes[ep+1]
		season := sess.Seasons[sn]
		if next.Title != "" {
			return fmt.Sprintf("S%02dE%02d - %s", season.Number, next.Number, next.Title)
		}
		return fmt.Sprintf("S%02dE%02d", season.Number, next.Number)
	}
	if sn < len(sess.Seasons)-1 {
		nextSeason := sess.Seasons[sn+1]
		return fmt.Sprintf("Season %d Episode 1", nextSeason.Number)
	}
	return ""
}

// episodeListMenu lets the user re-select an episode from the current season.
func episodeListMenu(sess *playlist.Session) error {
	episodeItems := make([]string, len(sess.Episodes))
	for i, ep := range sess.Episodes {
		if ep.Title != "" {
			episodeItems[i] = fmt.Sprintf("Episode %d: %s", ep.Number, ep.Title)
		} else {
			episodeItems[i] = fmt.Sprintf("Episode %d", ep.Number)
		}
	}

	idx, err := ui.Select("Episode", episodeItems)
	if err != nil {
		return err
	}

	sess.EpisodeIdx = idx
	debugf("selected: %s", sess.Title())
	return nil
}

// resolveStream gets a playable stream, trying the cached server first.
func resolveStream(sess *playlist.Session) (*media.Stream, error) {
	episodeID := sess.Current().ID

	// Try cached server first
	if cachedServer != nil {
		debugf("trying cached server: %s", cachedServer.Name)
		stream, err := tryServer(sess, cachedServer)
		if err == nil {
			return stream, nil
		}
		debugf("cached server failed: %v", err)
		cachedServer = nil
	}

	// Full resolution: get all servers and try in order
	servers, err := sess.Provider.GetServers(sess.Content.ID, episodeID)
	if err != nil {
		return nil, fmt.Errorf("getting servers: %w", err)
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("no servers found for %s", sess.Title())
	}

	ordered := orderServers(servers, cfg.Provider)
	for _, srv := range ordered {
		debugf("trying server: %s (ID: %s)", srv.Name, srv.ID)
		stream, err := tryServer(sess, &srv)
		if err != nil {
			debugf("server %s failed: %v", srv.Name, err)
			fmt.Fprintf(os.Stderr, "Server %s failed, trying next...\n", srv.Name)
			continue
		}
		cachedServer = &srv
		return stream, nil
	}

	return nil, fmt.Errorf("all servers failed for %s", sess.Title())
}

// tryServer attempts to extract a stream from a single server.
func tryServer(sess *playlist.Session, srv *media.Server) (*media.Stream, error) {
	embedURL, err := sess.Provider.GetEmbedURL(srv.ID)
	if err != nil {
		return nil, fmt.Errorf("embed failed: %w", err)
	}
	debugf("embed URL: %s", embedURL)

	ext := extract.New()
	stream, err := ext.Extract(embedURL, cfg.Quality)
	if err != nil {
		return nil, fmt.Errorf("extract failed: %w", err)
	}
	debugf("stream URL: %s (server: %s)", stream.URL, srv.Name)
	return stream, nil
}

// playCurrentEpisode resolves the stream and plays the current episode.
func playCurrentEpisode(sess *playlist.Session) error {
	title := sess.Title()

	// JSON output mode — play once and return
	if flagJSON {
		stream, err := resolveStream(sess)
		if err != nil {
			return err
		}
		return outputJSON(stream, title)
	}

	// Download mode — download once and return
	if flagDownload != "" {
		stream, err := resolveStream(sess)
		if err != nil {
			return err
		}
		return downloadEpisode(stream, title)
	}

	// Normal playback
	stream, err := resolveStream(sess)
	if err != nil {
		return err
	}

	subFile := resolveSubtitles(stream)

	var startPos float64
	if flagContinue && cfg.History {
		entries, _ := history.Load()
		for _, e := range entries {
			if e.ID == sess.Content.ID &&
				e.Season == sess.CurrentSeason().Number &&
				e.Episode == sess.Current().Number {
				startPos = e.Position
				debugf("resuming from position: %.0fs", startPos)
				break
			}
		}
	}

	p := player.New(cfg.Player)
	if !p.Available() {
		return fmt.Errorf("player %q not found in PATH", cfg.Player)
	}

	lastPos, err := p.Play(stream, title, startPos, subFile)
	if err != nil {
		return fmt.Errorf("playback failed: %w", err)
	}

	sess.LastPosition = lastPos
	return nil
}

// resolveSubtitles downloads the best-match subtitle file.
func resolveSubtitles(stream *media.Stream) string {
	if flagNoSubs || len(stream.Subtitles) == 0 {
		return ""
	}

	best := subtitle.BestMatch(stream.Subtitles, cfg.SubsLanguage)
	if best == nil {
		return ""
	}

	tmpDir, err := subtitle.NewTempDir()
	if err != nil {
		return ""
	}
	// Note: tmpDir cleanup happens when process exits; acceptable for a session

	subFile, err := tmpDir.Download(*best)
	if err != nil {
		debugf("subtitle download failed: %v", err)
		return ""
	}
	debugf("subtitle file: %s", subFile)
	return subFile
}

// downloadEpisode handles the download path.
func downloadEpisode(stream *media.Stream, title string) error {
	subFile := resolveSubtitles(stream)
	outputPath, err := download.Download(stream, title, flagDownload, subFile)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Downloaded: %s\n", outputPath)
	return nil
}

// saveHistory persists the current episode to watch history.
func saveHistory(sess *playlist.Session) {
	if !cfg.History {
		return
	}

	entry := media.HistoryEntry{
		ID:       sess.Content.ID,
		Title:    sess.Content.Title,
		Type:     sess.Content.Type,
		Season:   sess.CurrentSeason().Number,
		Episode:  sess.Current().Number,
		Position: sess.LastPosition,
	}
	if err := history.Save(entry); err != nil {
		debugf("saving history failed: %v", err)
	}
}

// outputJSON writes stream metadata as JSON.
func outputJSON(stream *media.Stream, title string) error {
	// Reuse the existing JSON output logic from search.go
	out := map[string]interface{}{
		"title":     title,
		"url":       stream.URL,
		"quality":   stream.Quality,
		"subtitles": stream.Subtitles,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
```

- [ ] **Step 2: Add the `"encoding/json"` import**

The `outputJSON` function uses `json.NewEncoder`. Make sure the import block includes `"encoding/json"`:

```go
import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"lobster/internal/download"
	"lobster/internal/extract"
	"lobster/internal/history"
	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/playlist"
	"lobster/internal/subtitle"
	"lobster/internal/ui"
)
```

- [ ] **Step 3: Verify it compiles**

Run: `cd /home/williams/Documents/personal/lobster && go build ./...`
Expected: Clean build.

- [ ] **Step 4: Commit**

```bash
git add cmd/session.go
git commit -m "feat: add playback loop with countdown menu and server caching"
```

---

### Task 6: Integrate session into `resolveAndPlay`

**Files:**
- Modify: `cmd/search.go:112-347`

The key change: for TV shows, after selecting the episode, create a `playlist.Session` and delegate to `runPlaybackLoop` instead of doing inline playback. Movies keep the current behavior (no session needed — single play).

- [ ] **Step 1: Add playlist import to search.go**

Add `"lobster/internal/playlist"` to the imports in `cmd/search.go`:

```go
import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"lobster/internal/download"
	"lobster/internal/extract"
	"lobster/internal/history"
	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/playlist"
	"lobster/internal/provider"
	"lobster/internal/subtitle"
	"lobster/internal/ui"
)
```

- [ ] **Step 2: Replace the TV path in `resolveAndPlay`**

Replace the section from after episode selection (line 220) through the end of the function. After the episode is selected and `episodeID` and `title` are set, instead of doing inline server resolution and playback, create a session and call `runPlaybackLoop`:

The TV branch of `resolveAndPlay` (after the episode index is determined at line 220) should end with:

```go
		selectedEpisode := episodes[episodeIdx]
		episodeID = selectedEpisode.ID
		title = fmt.Sprintf("%s S%02dE%02d", selected.Title, selectedSeason.Number, selectedEpisode.Number)
		season = selectedSeason.Number
		episode = selectedEpisode.Number

		debugf("episode: %d (ID: %s)", selectedEpisode.Number, episodeID)

		// Create a playlist session for continuous playback
		sess := playlist.New(p, selected, seasons, episodes, seasonIdx, episodeIdx)
		cachedServer = nil // start fresh
		return runPlaybackLoop(sess)
	}

	// --- Movie path (unchanged below this point) ---
```

The movie path (everything from `// Get servers` on line 230 to the end of the function) stays exactly as-is.

- [ ] **Step 3: Remove unused `title`, `season`, `episode` assignments for TV path**

The variables `episodeID`, `title`, `season`, `episode` are no longer used after the session is created in the TV path. The `episodeID` declaration at line 114 and the `title` declaration at line 115 are still needed for the movie path. No changes needed — the compiler won't complain since these are assigned but the function returns before they'd be used elsewhere.

- [ ] **Step 4: Verify it compiles**

Run: `cd /home/williams/Documents/personal/lobster && go build ./...`
Expected: Clean build.

- [ ] **Step 5: Run all tests**

Run: `cd /home/williams/Documents/personal/lobster && go test ./... -race`
Expected: All tests PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/search.go
git commit -m "feat: integrate playlist session for continuous TV playback"
```

---

### Task 7: Manual integration test

**Files:** None (manual testing)

- [ ] **Step 1: Build the binary**

Run: `cd /home/williams/Documents/personal/lobster && go build -o lobster .`
Expected: Binary produced.

- [ ] **Step 2: Test continuous playback flow**

Run: `./lobster "breaking bad"` (or any known TV show)

Verify:
1. Search results appear in fzf
2. After selecting a show and confirming, season/episode selection works
3. After an episode finishes, the countdown appears: "Playing in 10s — press Enter for menu, q to quit..."
4. If countdown expires, next episode auto-plays
5. Pressing Enter shows the menu with: Next/Previous/Replay/Episode list/Quit
6. "q" during countdown quits cleanly
7. At the last episode of a season, next goes to the next season
8. "Episode list" shows the current season's episodes for re-selection

- [ ] **Step 3: Test with `auto_next = false`**

Add `auto_next = false` to `~/.config/lobster/config.toml` and verify the menu appears immediately without countdown.

- [ ] **Step 4: Test movie flow is unaffected**

Run: `./lobster "inception"` and verify movies play once and exit as before.

- [ ] **Step 5: Commit any fixes**

If any fixes were needed during testing, commit them.
