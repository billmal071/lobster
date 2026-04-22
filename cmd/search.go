package cmd

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
	"lobster/internal/tui"
	"lobster/internal/ui"
)

// searchRun is the default command: lobster <query>
func searchRun(cmd *cobra.Command, args []string) error {
	query := strings.Join(args, " ")

	p := newProvider()

	if query == "" {
		// Launch the rich TUI Dashboard
		selected, err := tui.StartApp(p, cfg)
		if err != nil {
			return err
		}
		if selected != nil {
			return resolveAndPlay(p, *selected, 0, 0)
		}
		return nil
	}

	debugf("searching for: %s", query)

	return playFlow(p, query)
}

// playFlow handles the full search -> select -> play flow.
func playFlow(p provider.Provider, query string) error {
	// Search
	stop := ui.StartSpinner(fmt.Sprintf("Searching for %q...", query))
	results, err := p.Search(query)
	stop()
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	items := make([]string, len(results))
	for i, r := range results {
		items[i] = provider.FormatDisplayTitle(r)
	}

	for {
		// Select content
		idx, err := ui.Select("Select", items)
		if err != nil {
			return err
		}

		selected := results[idx]
		debugf("selected: %s (ID: %s, type: %s)", selected.Title, selected.ID, selected.Type)

		// Show details and confirm
		detail, err := p.GetDetails(selected.ID)
		if err != nil {
			debugf("could not fetch details: %v", err)
		} else {
			printDetail(selected, detail)
		}

		ok, err := ui.Confirm("Play this?")
		if err != nil {
			return err
		}
		if ok {
			return resolveAndPlay(p, selected, 0, 0)
		}
		// User declined — loop back to selection
		fmt.Fprintln(os.Stderr)
	}
}

// printDetail displays content metadata to stderr.
func printDetail(r media.SearchResult, d *media.ContentDetail) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s", r.Title)
	if r.Year != "" {
		fmt.Fprintf(os.Stderr, " (%s)", r.Year)
	}
	fmt.Fprintln(os.Stderr)

	if d.Rating != "" {
		fmt.Fprintf(os.Stderr, "  Rating:   %s\n", d.Rating)
	}
	if d.Duration != "" {
		fmt.Fprintf(os.Stderr, "  Duration: %s\n", d.Duration)
	} else if r.Duration != "" {
		fmt.Fprintf(os.Stderr, "  Duration: %s\n", r.Duration)
	}
	if r.Type == media.TV && r.Seasons > 0 {
		fmt.Fprintf(os.Stderr, "  Seasons:  %d (%d episodes)\n", r.Seasons, r.Episodes)
	}
	if len(d.Genre) > 0 {
		fmt.Fprintf(os.Stderr, "  Genre:    %s\n", strings.Join(d.Genre, ", "))
	}
	if d.Description != "" {
		fmt.Fprintf(os.Stderr, "\n  %s\n", d.Description)
	}
	fmt.Fprintln(os.Stderr)
}

// resolveAndPlay handles season/episode selection for TV and then plays.
func resolveAndPlay(p provider.Provider, selected media.SearchResult, season, episode int) error {
	episodeID := ""
	title := selected.Title

	if selected.Type == media.TV {
		// Get seasons
		stopSeasons := ui.StartSpinner("Fetching seasons...")
		seasons, err := p.GetSeasons(selected.ID)
		stopSeasons()
		if err != nil {
			return fmt.Errorf("getting seasons: %w", err)
		}

		if len(seasons) == 0 {
			return fmt.Errorf("no seasons found")
		}

		// Select season (or use provided)
		seasonIdx := 0
		if season > 0 {
			for i, s := range seasons {
				if s.Number == season {
					seasonIdx = i
					break
				}
			}
		} else {
			seasonItems := make([]string, len(seasons))
			for i, s := range seasons {
				seasonItems[i] = fmt.Sprintf("Season %d", s.Number)
			}
			seasonIdx, err = ui.Select("Season", seasonItems)
			if err != nil {
				return err
			}
		}

		selectedSeason := seasons[seasonIdx]
		debugf("season: %d (ID: %s)", selectedSeason.Number, selectedSeason.ID)

		// Get episodes
		stopEps := ui.StartSpinner("Fetching episodes...")
		episodes, err := p.GetEpisodes(selected.ID, selectedSeason.ID)
		stopEps()
		if err != nil {
			return fmt.Errorf("getting episodes: %w", err)
		}

		if len(episodes) == 0 {
			return fmt.Errorf("no episodes found")
		}

		// Select episode (or use provided)
		episodeIdx := 0
		if episode > 0 {
			for i, ep := range episodes {
				if ep.Number == episode {
					episodeIdx = i
					break
				}
			}
		} else {
			episodeItems := make([]string, len(episodes))
			for i, ep := range episodes {
				if ep.Title != "" {
					episodeItems[i] = fmt.Sprintf("Episode %d: %s", ep.Number, ep.Title)
				} else {
					episodeItems[i] = fmt.Sprintf("Episode %d", ep.Number)
				}
			}

			// In download mode, offer batch options
			if flagDownload != "" {
				batchItems := []string{"Download all episodes", "Download range (e.g., 1-5)"}
				episodeItems = append(batchItems, episodeItems...)
			}

			episodeIdx, err = ui.Select("Episode", episodeItems)
			if err != nil {
				return err
			}

			// Handle batch options
			if flagDownload != "" {
				if episodeIdx == 0 {
					// Download all episodes
					return batchDownload(p, selected, episodes, selectedSeason)
				} else if episodeIdx == 1 {
					// Download range
					for {
						rangeInput, err := ui.Input("Episode range")
						if err != nil {
							return err
						}
						matched, err := parseEpisodeRange(rangeInput, episodes)
						if err != nil {
							fmt.Fprintf(os.Stderr, "Invalid range: %v\n", err)
							continue
						}
						if len(matched) == 0 {
							fmt.Fprintln(os.Stderr, "No episodes matched the range.")
							continue
						}
						return batchDownload(p, selected, matched, selectedSeason)
					}
				}
				// Offset index by 2 for injected batch options
				episodeIdx -= 2
			}
		}

		selectedEpisode := episodes[episodeIdx]
		debugf("episode: %d (ID: %s)", selectedEpisode.Number, selectedEpisode.ID)

		// Create a playlist session for continuous playback
		sess := playlist.New(p, selected, seasons, episodes, seasonIdx, episodeIdx)
		cachedServerName = ""
		return runPlaybackLoop(sess)
	}


	// If provider supports direct streaming (consumet API), skip embed+extract step.
	if sp, ok := p.(provider.StreamProvider); ok {
		stopStream := ui.StartSpinner("Negotiating stream servers...")
		servers, err := p.GetServers(selected.ID, episodeID)
		stopStream()
		if err != nil {
			return fmt.Errorf("getting servers: %w", err)
		}
		if len(servers) == 0 {
			return fmt.Errorf("no servers found")
		}

		stopWatch := ui.StartSpinner(fmt.Sprintf("Fetching %s media stream...", title))
		defer stopWatch()

		ordered := orderServers(servers, cfg.Provider)
		var stream *media.Stream
		for _, srv := range ordered {
			debugf("trying server (watch): %s (ID: %s)", srv.Name, srv.ID)
			stream, err = sp.Watch(selected.ID, episodeID, srv.Name, cfg.Quality)
			if err != nil {
				debugf("server %s watch failed: %v", srv.Name, err)
				fmt.Fprintf(os.Stderr, "Server %s failed, trying next...\n", srv.Name)
				continue
			}
			debugf("stream URL: %s (server: %s)", stream.URL, srv.Name)
			break
		}
		if stream == nil {
			return fmt.Errorf("all servers failed for %s", title)
		}
		stopWatch()
		return playStream(stream, title, selected, season, episode)
	}

	// Get servers
	stopServer := ui.StartSpinner("Negotiating stream servers...")
	servers, err := p.GetServers(selected.ID, episodeID)
	stopServer()
	if err != nil {
		return fmt.Errorf("getting servers: %w", err)
	}

	if len(servers) == 0 {
		return fmt.Errorf("no servers found")
	}

	// Try servers in order: preferred first, then fallbacks
	stopExt := ui.StartSpinner(fmt.Sprintf("Extracting %s media stream...", title))
	defer stopExt() // Just defer to be safe when it breaks or returns

	ordered := orderServers(servers, cfg.Provider)
	var stream *media.Stream
	for _, srv := range ordered {
		debugf("trying server: %s (ID: %s)", srv.Name, srv.ID)

		embedURL, err := p.GetEmbedURL(srv.ID)
		if err != nil {
			debugf("server %s embed failed: %v", srv.Name, err)
			continue
		}
		debugf("embed URL: %s", embedURL)

		ext := extract.NewForURL(embedURL)
		stream, err = ext.Extract(embedURL, cfg.Quality)
		if err != nil {
			debugf("server %s extract failed: %v", srv.Name, err)
			fmt.Fprintf(os.Stderr, "Server %s failed, trying next...\n", srv.Name)
			continue
		}
		debugf("stream URL: %s (server: %s)", stream.URL, srv.Name)
		break
	}
	if stream == nil {
		return fmt.Errorf("all servers failed for %s", title)
	}

	// Stop extraction spinner before printing/playing
	stopExt()

	return playStream(stream, title, selected, season, episode)
}

// playStream handles all post-stream-resolution logic: JSON output, subtitle
// download, download mode, playback, and history saving.
func playStream(stream *media.Stream, title string, selected media.SearchResult, season, episode int) error {
	// JSON output mode
	if flagJSON {
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

	// Handle subtitles
	var subFile string
	if !flagNoSubs && len(stream.Subtitles) > 0 {
		best := subtitle.BestMatch(stream.Subtitles, cfg.SubsLanguage)
		if best != nil {
			tmpDir, err := subtitle.NewTempDir()
			if err == nil {
				defer tmpDir.Cleanup()
				subFile, err = tmpDir.Download(*best)
				if err != nil {
					debugf("subtitle download failed: %v", err)
					subFile = "" // Continue without subs
				} else {
					debugf("subtitle file: %s", subFile)
				}
			}
		}
	}

	// Download mode
	if flagDownload != "" {
		outputPath, err := download.Download(stream, title, flagDownload, subFile)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Downloaded: %s\n", outputPath)
		return nil
	}

	// Play
	var startPos float64
	if flagContinue && cfg.History {
		entries, _ := history.Load()
		for _, e := range entries {
			if e.ID == selected.ID && e.Season == season && e.Episode == episode {
				startPos = e.Position
				debugf("resuming from position: %.0fs", startPos)
				break
			}
		}
	}

	p2 := player.New(cfg.Player)
	if !p2.Available() {
		return fmt.Errorf("player %q not found in PATH", cfg.Player)
	}

	lastPos, err := p2.Play(stream, title, startPos, subFile)
	if err != nil {
		return fmt.Errorf("playback failed: %w", err)
	}

	// Save to history
	if cfg.History {
		entry := media.HistoryEntry{
			ID:       selected.ID,
			Title:    selected.Title,
			Type:     selected.Type,
			Season:   season,
			Episode:  episode,
			Position: lastPos,
		}
		if err := history.Save(entry); err != nil {
			debugf("saving history failed: %v", err)
		}
	}

	return nil
}
