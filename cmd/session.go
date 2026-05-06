package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"lobster/internal/download"
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

// resolveStream resolves a stream for the current episode via fallback providers.
// Primary provider (FlixHQ) is used for metadata only; streaming goes through
// Soap2Day and other fallbacks directly.
func resolveStream(sess *playlist.Session, excludeNames map[string]bool) (*media.Stream, string, error) {
	debugf("resolving stream via fallback providers for %s", sess.Title())
	stream, err := tryFallbackStream(
		sess.Provider,
		sess.Content.Title,
		sess.Content.Type,
		sess.CurrentSeason().Number,
		sess.Current().Number,
	)
	if err != nil {
		debugf("all fallbacks failed: %v", err)
		return nil, "", fmt.Errorf("all providers failed for %s: %w", sess.Title(), err)
	}
	return stream, "Fallback", nil
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
		return downloadEpisode(stream, sess, title)
	}

	// Normal playback with retry on failure
	p := player.New(cfg.Player)
	if !p.Available() {
		return player.NotFoundError(cfg.Player)
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

		subFiles := resolveSubtitles(stream, sess.Content.Title, sess.CurrentSeason().Number, sess.Current().Number)

		lastPos, playErr := p.Play(stream, title, startPos, subFiles)
		if playErr == nil {
			sess.LastPosition = lastPos
			return nil
		}

		// Playback failed — if user watched some of it, resume from there
		if lastPos > 0 {
			startPos = lastPos
			debugf("playback stopped at %.0fs, will resume from there", lastPos)
		}

		// If the fallback stream also failed, don't retry endlessly
		if serverName == "Fallback" {
			return fmt.Errorf("playback failed: %w", playErr)
		}

		// Exclude this server and try the next one
		excludeNames[serverName] = true
		cachedServerName = ""
		fmt.Fprintf(os.Stderr, "Playback stopped, trying another source...\n")
		debugf("playback error: %v (server %s excluded)", playErr, serverName)
	}
}

// resolveSubtitles downloads multiple subtitle files.
// Prefers SubDL over embedded. User can cycle tracks with 'j' in mpv.
func resolveSubtitles(stream *media.Stream, title string, season, episode int) []string {
	if flagNoSubs {
		return nil
	}

	subs := searchExternalSubs(title, season, episode)
	if len(subs) == 0 {
		subs = stream.Subtitles
	}

	if len(subs) == 0 {
		return nil
	}

	tmpDir, err := subtitle.NewTempDir()
	if err != nil {
		return nil
	}
	// Note: tmpDir cleanup happens when process exits; acceptable for a session

	var subFiles []string
	for _, sub := range subs {
		f, err := resolveAndDownloadSub(tmpDir, sub)
		if err != nil {
			debugf("subtitle download failed (%s): %v", sub.Label, err)
			continue
		}
		debugf("subtitle file: %s (%s)", f, sub.Label)
		subFiles = append(subFiles, f)
	}
	return subFiles
}

// downloadEpisode handles the download path.
func downloadEpisode(stream *media.Stream, sess *playlist.Session, title string) error {
	subFiles := resolveSubtitles(stream, sess.Content.Title, sess.CurrentSeason().Number, sess.Current().Number)
	dlSub := ""
	if len(subFiles) > 0 {
		dlSub = subFiles[0]
	}
	outputPath, err := download.Download(stream, title, flagDownload, dlSub)
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
