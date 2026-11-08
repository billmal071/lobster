package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"lobster/internal/download"
	"lobster/internal/history"
	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/playlist"
	"lobster/internal/provider"
	"lobster/internal/subtitle"
	"lobster/internal/torrentstream"
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

// resolveStream resolves a stream for the current episode. A primary provider
// that implements StreamProvider (e.g. VaPlayer, TBCPL) is tried first with
// the session's native episode ID; otherwise (e.g. FlixHQWS, used for metadata
// only) resolution goes through the resilient fallback path.
func resolveStream(sess *playlist.Session, excludeNames map[string]bool) (*media.Stream, string, error) {
	// Primary StreamProvider gets first crack with the native episode ID from the
	// session — lets a provider with its own ID scheme (TBCPL, VaPlayer) reach
	// its own Watch verbatim, bypassing tmdbID:season:episode reconstruction.
	// Skip it once it's been excluded (its last stream failed playback), so a
	// dead Primary stream can't trap playCurrentEpisode in an infinite retry.
	var primaryMagnet *media.Stream
	if sp, ok := sess.Provider.(provider.StreamProvider); ok && !excludeNames["Primary"] {
		ep := sess.Current()
		if stream, err := sp.Watch(sess.ProviderKey(), ep.ID, "Default", cfg.Quality); err == nil && stream != nil {
			holdMagnet := flagJSON || recoveredTorrentSource(sess)
			if !holdMagnet || !torrentstream.IsMagnet(stream.URL) {
				return stream, "Primary", nil
			}
			primaryMagnet = stream
		}
	}

	if excludeNames["Fallback"] {
		if primaryMagnet != nil {
			return primaryMagnet, "Primary", nil
		}
		return nil, "", fmt.Errorf("no other source for %s", sess.Title())
	}

	debugf("resolving stream via fallback providers for %s", sess.Title())
	// The chain is built by excluding its argument, and sess.Provider is
	// not always the configured primary: the episode-list recovery moves
	// it to whichever chain member could list the season. Excluding that
	// one would remove the only provider known to have this show.
	chain := agentFallbackProviders(sess.ChainBase())
	if excludeNames["Primary"] {
		chain = withoutProvider(chain, sess.Provider)
	}
	if flagJSON {
		chain = withoutMagnetSources(chain)
	} else if primaryMagnet != nil {
		chain = withoutProvider(chain, sess.Provider)
	}
	stream, err := tryFallbackStreamFrom(
		chain,
		sess.Content,
		sess.CurrentSeason().Number,
		sess.Current().Number,
	)
	if err != nil {
		if primaryMagnet != nil {
			if flagJSON {
				return nil, "", errors.Join(jsonMagnetRefusal(sess.Title()),
					fmt.Errorf("all providers failed for %s: %w", sess.Title(), err))
			}
			return primaryMagnet, "Primary", nil
		}
		debugf("all fallbacks failed: %v", err)
		return nil, "", fmt.Errorf("all providers failed for %s: %w", sess.Title(), err)
	}
	return stream, "Fallback", nil
}

// jsonMagnetRefusalError is the failure for a title that resolved to a torrent
// under --json; play.go finds it with errors.As to emit its message.
type jsonMagnetRefusalError struct{ msg string }

func (e *jsonMagnetRefusalError) Error() string { return e.msg }

// jsonMagnetRefusal is the error for a title that resolved only to a torrent
// while --json is set, with the advice to play it without --json.
func jsonMagnetRefusal(title string) error {
	return &jsonMagnetRefusalError{msg: fmt.Sprintf("%s resolved to a torrent, which --json cannot express as a playable URL; "+
		"play it without --json, or fetch it first with the interactive CLI "+
		`(lobster --base yts "title" --download <dir>; 'play' has no --download)`, title)}
}

// recoveredTorrentSource reports whether the session's provider is a torrent
// source the episode-list recovery chose, as opposed to one the user selected.
func recoveredTorrentSource(sess *playlist.Session) bool {
	return sess.ChainPrimary != nil && sess.ChainPrimary != sess.Provider && servesMagnets(sess.Provider)
}

// heldMagnetRemains reports whether a recovered torrent source may still have
// a magnet to try: resolveStream holds one back for such a session, and
// neither the primary nor the fallback has been excluded yet. It does not know
// a magnet was actually held; when none was, the extra attempt costs at most
// one more Watch before the original error is returned.
func heldMagnetRemains(sess *playlist.Session, excludeNames map[string]bool) bool {
	return !flagJSON && recoveredTorrentSource(sess) && !excludeNames["Primary"] && !excludeNames["Fallback"]
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
		// Same refusal as playStream's JSON branch (cmd/search.go): a magnet
		// is not a URL a JSON consumer can open, and a loopback URL would die
		// with this process.
		if torrentstream.IsMagnet(stream.URL) {
			return jsonMagnetRefusal(title)
		}
		return outputJSON(stream, title)
	}

	// Download mode — no retry needed
	if flagDownload != "" {
		stream, _, err := resolveStream(sess, excludeNames)
		if err != nil {
			return err
		}
		stream, closeTorrent, err := serveTorrent(stream)
		if err != nil {
			return err
		}
		defer closeTorrent()
		return downloadEpisode(stream, sess, title)
	}

	// Normal playback with retry on failure
	p := newPlayer(cfg.Player, cfg.AudioLanguage)
	if !p.Available() {
		return player.NotFoundError(cfg.Player)
	}

	// Periodic checkpoints while the episode plays: a hard shutdown kills the
	// player and this process together, so the exit-time saveHistory in
	// runPlaybackLoop alone would lose the whole watch position.
	if cfg.History {
		if cp, ok := p.(player.Checkpointer); ok {
			cp.SetCheckpoint(historyCheckpoint(
				sess.Content.ID,
				sess.Content.Title,
				sess.Content.Type,
				sess.CurrentSeason().Number,
				sess.Current().Number,
			))
		}
	}

	var startPos float64
	if flagContinue && cfg.History {
		entries, _ := history.Load()
		for _, e := range entries {
			if e.ID == sess.Content.ID &&
				e.Season == sess.CurrentSeason().Number &&
				e.Episode == sess.Current().Number {
				startPos = resumePosition(e)
				if startPos == 0 && e.Position > 0 {
					debugf("stored position %.0fs is at the end of a %.0fs file; starting over", e.Position, e.Duration)
				} else {
					debugf("resuming from position: %.0fs", startPos)
				}
				break
			}
		}
	}

	var fallbackErr error
	for {
		stream, serverName, err := resolveStream(sess, excludeNames)
		if err != nil {
			if fallbackErr != nil {
				return fallbackErr
			}
			return err
		}

		subFiles, cleanupSubs := resolveSubtitles(stream, sess.Content.Title, sess.CurrentSeason().Number, sess.Current().Number)

		// A magnet (Nyaa, EZTV, or YTS via torrent_fallback) has to go
		// through the local torrent server before a player can open it.
		stream, closeTorrent, err := serveTorrent(stream)
		if err != nil {
			cleanupSubs()
			// A dead swarm is a failed source like any other: exclude it and
			// try the next one, exactly as a Play failure below does.
			if serverName == "Fallback" {
				if !heldMagnetRemains(sess, excludeNames) {
					return err
				}
				fallbackErr = err
				excludeNames["Fallback"] = true
				continue
			}
			excludeNames[serverName] = true
			cachedServerName = ""
			fmt.Fprintf(os.Stderr, "Torrent source failed, trying another source...\n")
			debugf("torrent error: %v (server %s excluded)", err, serverName)
			continue
		}
		result, playErr := p.Play(stream, title, startPos, subFiles)
		closeTorrent()
		cleanupSubs()
		if playErr == nil {
			sess.LastPosition = result.Position
			sess.LastDuration = result.Duration
			sess.LastPositionUnknown = result.PositionUnknown
			sess.LastPositionUntracked = result.PositionUntracked
			return nil
		}

		// Playback failed — if user watched some of it, resume from there
		if result.Position > 0 {
			startPos = resumePosition(media.HistoryEntry{Position: result.Position, Duration: result.Duration})
			debugf("playback stopped at %.0fs, will resume from %.0fs", result.Position, startPos)
		}

		// If the fallback stream also failed, don't retry endlessly
		if serverName == "Fallback" {
			fallbackErr = fmt.Errorf("playback failed: %w", playErr)
			if !heldMagnetRemains(sess, excludeNames) {
				return fallbackErr
			}
			excludeNames["Fallback"] = true
			continue
		}

		// Exclude this server and try the next one
		excludeNames[serverName] = true
		cachedServerName = ""
		fmt.Fprintf(os.Stderr, "Playback stopped, trying another source...\n")
		debugf("playback error: %v (server %s excluded)", playErr, serverName)
	}
}

// subtitleDownload is a seam so tests can exercise the staging-directory
// lifecycle without reaching the network.
var subtitleDownload = resolveAndDownloadSub

// resolveSubtitles downloads multiple subtitle files.
// User can cycle tracks with 'j' in mpv.
// The returned cleanup is never nil and must be called once the player has
// exited: subtitles are staged under $HOME, which nothing else reclaims.
func resolveSubtitles(stream *media.Stream, title string, season, episode int) ([]string, func()) {
	noCleanup := func() {}
	if flagNoSubs {
		return nil, noCleanup
	}

	external, asked := externalSubs(title, season, episode)
	subs := subtitle.FilterByEpisode(
		mergeSubtitles(
			subtitle.Filter(stream.Subtitles, cfg.SubsLanguage),
			external,
		),
		season, episode,
	)
	if len(subs) == 0 {
		reportNoSubtitles(title, asked)
		return nil, noCleanup
	}
	// Limit to 3 subtitle downloads to avoid stream URL expiry.
	if len(subs) > 3 {
		subs = subs[:3]
	}

	tmpDir, err := subtitle.NewTempDir()
	if err != nil {
		return nil, noCleanup
	}

	var subFiles []string
	for _, sub := range subs {
		f, err := subtitleDownload(tmpDir, sub, season, episode)
		if err != nil {
			debugf("subtitle download failed (%s): %v", sub.Label, err)
			continue
		}
		debugf("subtitle file: %s (%s)", f, sub.Label)
		subFiles = append(subFiles, f)
	}
	return subFiles, tmpDir.Cleanup
}

// downloadEpisode handles the download path.
func downloadEpisode(stream *media.Stream, sess *playlist.Session, title string) error {
	subFiles, cleanupSubs := resolveSubtitles(stream, sess.Content.Title, sess.CurrentSeason().Number, sess.Current().Number)
	defer cleanupSubs()
	dlSub := ""
	if len(subFiles) > 0 {
		dlSub = subFiles[0]
	}
	baseDir, err := resolveDownloadBaseDir(flagDownload)
	if err != nil {
		return fmt.Errorf("resolving download dir: %w", err)
	}
	outputDir := buildTVSeasonDownloadDir(baseDir, sess.Content.Title, sess.CurrentSeason().Number)
	outputPath, err := download.Download(stream, title, outputDir, dlSub)
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
	// When LastPositionUnknown is set, LastPosition is 0 by default rather
	// than by measurement, so it must never reach the episode's stored resume
	// point. The two ways that happens are handled differently.
	if sess.LastPositionUnknown && !sess.LastPositionUntracked {
		// A tracked player that was never heard from: nothing here says the
		// episode actually played, so record nothing at all.
		debugf("skipping history save: no position was observed for this episode")
		return
	}

	entry := media.HistoryEntry{
		ID:       sess.Content.ID,
		Title:    sess.Content.Title,
		Type:     sess.Content.Type,
		Season:   sess.CurrentSeason().Number,
		Episode:  sess.Current().Number,
		Position: sess.LastPosition,
		Duration: sess.LastDuration,
	}
	save := history.Save
	if sess.LastPositionUntracked {
		// The player cannot report a position at all, but the watch is real:
		// record it while keeping any position history already holds.
		save = history.SaveKeepingPosition
	}
	if err := save(entry); err != nil {
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
