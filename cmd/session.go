package cmd

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

// cachedServerName is the last working server name, preferred for the next episode.
var cachedServerName string

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

	sess.SetEpisodes(sess.Episodes, sess.SeasonIdx, idx)
	debugf("selected: %s", sess.Title())
	return nil
}

// resolveStream fetches servers for the current episode and tries them in order.
// excludeNames lists server names to skip (e.g., servers whose streams failed during playback).
// If a server worked previously (cachedServerName), it's tried first.
// Returns the stream and the server name that provided it.
func resolveStream(sess *playlist.Session, excludeNames map[string]bool) (*media.Stream, string, error) {
	episodeID := sess.Current().ID

	// Always fetch fresh servers — IDs are episode-specific
	servers, err := sess.Provider.GetServers(sess.Content.ID, episodeID)
	if err != nil {
		return nil, "", fmt.Errorf("getting servers: %w", err)
	}
	if len(servers) == 0 {
		return nil, "", fmt.Errorf("no servers found for %s", sess.Title())
	}

	// Order servers: prefer cached server name, then user-configured provider, then rest
	ordered := orderServersWithCache(servers, cfg.Provider, cachedServerName)

	for _, srv := range ordered {
		if excludeNames[srv.Name] {
			debugf("skipping excluded server: %s (ID: %s)", srv.Name, srv.ID)
			continue
		}
		debugf("trying server: %s (ID: %s)", srv.Name, srv.ID)
		stream, err := tryServer(sess, &srv)
		if err != nil {
			debugf("server %s failed: %v", srv.Name, err)
			fmt.Fprintf(os.Stderr, "Server %s failed, trying next...\n", srv.Name)
			continue
		}
		cachedServerName = srv.Name
		return stream, srv.Name, nil
	}

	return nil, "", fmt.Errorf("all servers failed for %s", sess.Title())
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
// If playback fails mid-stream, it retries with a different source.
func playCurrentEpisode(sess *playlist.Session) error {
	title := sess.Title()
	excludeNames := make(map[string]bool)

	// JSON output mode — no retry needed
	if flagJSON {
		stream, _, err := resolveStream(sess, excludeNames)
		if err != nil {
			return err
		}
		return outputJSON(stream, title)
	}

	// Download mode — no retry needed
	if flagDownload != "" {
		stream, _, err := resolveStream(sess, excludeNames)
		if err != nil {
			return err
		}
		return downloadEpisode(stream, title)
	}

	// Normal playback with retry on failure
	p := player.New(cfg.Player)
	if !p.Available() {
		return fmt.Errorf("player %q not found in PATH", cfg.Player)
	}

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

	for {
		stream, serverName, err := resolveStream(sess, excludeNames)
		if err != nil {
			return err
		}

		subFile := resolveSubtitles(stream)

		lastPos, playErr := p.Play(stream, title, startPos, subFile)
		if playErr == nil {
			sess.LastPosition = lastPos
			return nil
		}

		// Playback failed — if user watched some of it, resume from there
		if lastPos > 0 {
			startPos = lastPos
			debugf("playback stopped at %.0fs, will resume from there", lastPos)
		}

		// Exclude this server and try the next one
		excludeNames[serverName] = true
		cachedServerName = ""
		fmt.Fprintf(os.Stderr, "Playback stopped, trying another source...\n")
		debugf("playback error: %v (server %s excluded)", playErr, serverName)
	}
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
