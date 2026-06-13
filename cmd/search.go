package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"lobster/internal/config"
	"lobster/internal/dlmanager"
	"lobster/internal/dlmanager/engine"
	"lobster/internal/dlmanager/store"
	"lobster/internal/download"
	"lobster/internal/history"
	"lobster/internal/httputil"
	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/poster"
	"lobster/internal/playlist"
	"lobster/internal/provider"
	"lobster/internal/subtitle"
	"lobster/internal/tui"
	"lobster/internal/ui"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// searchRun is the default command: lobster <query>
func searchRun(cmd *cobra.Command, args []string) error {
	query := strings.Join(args, " ")

	p := newProvider()

	if query == "" {
		// Launch the rich TUI Dashboard with download manager.
		mgr, cleanup, err := initDownloadManager(cfg)
		if err != nil {
			debugf("download manager init failed (continuing without): %v", err)
		}
		if cleanup != nil {
			defer cleanup()
		}

		selected, selectedProvider, err := tui.StartApp(p, cfg, mgr)
		if err != nil {
			return err
		}
		if selected != nil {
			if selectedProvider == nil {
				selectedProvider = p
			}
			return resolveAndPlay(selectedProvider, *selected, 0, 0)
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

// Detail pane styles.
var (
	detailTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F8F8F2"))
	detailYear  = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4"))
	detailType  = lipgloss.NewStyle().Foreground(lipgloss.Color("#BD93F9"))
	detailDot   = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4")).SetString(" • ")
	detailStar  = lipgloss.NewStyle().Foreground(lipgloss.Color("#F1FA8C"))
	detailLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4"))
	detailValue = lipgloss.NewStyle().Foreground(lipgloss.Color("#F8F8F2"))
	detailDesc  = lipgloss.NewStyle().Foreground(lipgloss.Color("#BFBFBF"))
)

// printDetail displays content metadata to stderr with poster on the left
// and details on the right. Layout is responsive to terminal width.
func printDetail(r media.SearchResult, d *media.ContentDetail) {
	termWidth := 80
	if w, _, err := term.GetSize(int(os.Stderr.Fd())); err == nil && w > 0 {
		termWidth = w
	}

	// Poster: 35% of width, capped at 40 cols
	posterCols := termWidth * 35 / 100
	if posterCols > 40 {
		posterCols = 40
	}
	if posterCols < 15 {
		posterCols = 15
	}
	posterRows := posterCols * 7 / 10

	textWidth := termWidth - posterCols - 8
	if r.Poster == "" {
		textWidth = termWidth - 4
	}
	if textWidth < 30 {
		textWidth = 30
	}

	// Build styled text lines
	var lines []string

	// Title + year
	title := detailTitle.Render(r.Title)
	if r.Year != "" {
		title += " " + detailYear.Render("("+r.Year+")")
	}
	lines = append(lines, title)

	// Type badge
	var typeLine string
	if r.Type == media.TV {
		typeLine = detailType.Render("TV Series")
		if r.Seasons > 0 {
			typeLine += detailDot.String() + fmt.Sprintf("%d Seasons", r.Seasons)
		}
		if r.Episodes > 0 {
			typeLine += detailDot.String() + fmt.Sprintf("%d Episodes", r.Episodes)
		}
	} else {
		typeLine = detailType.Render("Movie")
		dur := d.Duration
		if dur == "" {
			dur = r.Duration
		}
		if dur != "" {
			typeLine += detailDot.String() + dur
		}
	}
	lines = append(lines, typeLine, "")

	// Rating
	if d.Rating != "" {
		lines = append(lines, detailStar.Render("★ "+d.Rating))
	}

	// Metadata
	if len(d.Genre) > 0 {
		lines = append(lines, detailLabel.Render("Genre:")+" "+detailValue.Render(strings.Join(d.Genre, ", ")))
	}
	if d.Released != "" {
		lines = append(lines, detailLabel.Render("Released:")+" "+detailValue.Render(d.Released))
	}
	if d.Country != "" {
		lines = append(lines, detailLabel.Render("Country:")+" "+detailValue.Render(d.Country))
	}

	// Description (word-wrapped)
	if d.Description != "" {
		lines = append(lines, "")
		desc := detailDesc.Width(textWidth).Render(d.Description)
		lines = append(lines, strings.Split(desc, "\n")...)
	}

	// Render poster + text side by side (Kitty or half-block)
	fmt.Fprintln(os.Stderr)
	output := poster.RenderSideBySide(r.Poster, posterCols, posterRows, lines)
	fmt.Fprintln(os.Stderr, " "+output)
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
		if err != nil || len(seasons) == 0 {
			// Primary provider can't resolve seasons — try fallback stream
			debugf("primary provider seasons failed: %v, trying fallbacks", err)
			fmt.Fprintf(os.Stderr, "Provider has no season data, trying fallbacks...\n")
			fbStream, fbErr := tryFallbackStream(p, selected.Title, selected.Type, season, episode)
			if fbErr != nil {
				if err != nil {
					return fmt.Errorf("getting seasons: %w", err)
				}
				return fmt.Errorf("no seasons found")
			}
			return playStream(fbStream, title, selected, season, episode)
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

	// If provider supports direct streaming, skip embed+extract step.
	if sp, ok := p.(provider.StreamProvider); ok {
		debugf("primary provider: %T (StreamProvider)", p)
		stopStream := ui.StartSpinner("Negotiating stream servers...")
		servers, err := p.GetServers(selected.ID, episodeID)
		stopStream()
		if err != nil || len(servers) == 0 {
			if err != nil {
				debugf("GetServers failed: %v", err)
			}
			// Try fallback immediately
			fmt.Fprintf(os.Stderr, "Primary provider failed, trying fallback...\n")
			fbStream, fbErr := tryFallbackStream(p, selected.Title, selected.Type, season, episode)
			if fbErr != nil {
				if err != nil {
					return fmt.Errorf("getting servers: %w", err)
				}
				return fmt.Errorf("no servers found")
			}
			return playStream(fbStream, title, selected, season, episode)
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
			stopWatch()
			// Try fallback providers
			fmt.Fprintf(os.Stderr, "Primary provider failed, trying fallback...\n")
			fbStream, err := tryFallbackStream(p, selected.Title, selected.Type, season, episode)
			if err != nil {
				return fmt.Errorf("all servers failed for %s", title)
			}
			stream = fbStream
		} else {
			stopWatch()
		}
		return playStream(stream, title, selected, season, episode)
	}

	// Skip primary provider's embed+extract (unreliable) and go straight to
	// fallback StreamProviders (Soap2Day, etc.) for stream resolution.
	debugf("resolving stream via fallback providers for %s", title)
	stopStream := ui.StartSpinner(fmt.Sprintf("Fetching %s media stream...", title))
	fbStream, err := tryFallbackStream(p, selected.Title, selected.Type, season, episode)
	stopStream()
	if err != nil {
		debugf("fallback failed: %v", err)
		hint := ""
		if _, isFlixHQ := p.(*provider.FlixHQ); isFlixHQ {
			hint = "\nTip: try --base soap2day or --base vaplayer for better stream availability"
		} else if _, isFlixHQWS := p.(*provider.FlixHQWS); isFlixHQWS {
			hint = "\nTip: try --base soap2day or --base vaplayer for better stream availability"
		}
		return fmt.Errorf("all providers failed for %s: %w%s", title, err, hint)
	}
	return playStream(fbStream, title, selected, season, episode)
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

	// Download multiple tracks so the user can cycle with 'j' in mpv.
	var subFiles []string
	if !flagNoSubs {
		subs := subtitle.FilterByEpisode(
			mergeSubtitles(
				subtitle.Filter(stream.Subtitles, cfg.SubsLanguage),
				searchExternalSubs(selected.Title, season, episode),
			),
			season, episode,
		)
		// Limit to 3 subtitle downloads to avoid stream URL expiry.
		if len(subs) > 3 {
			subs = subs[:3]
		}
		if len(subs) > 0 {
			tmpDir, err := subtitle.NewTempDir()
			if err == nil {
				defer tmpDir.Cleanup()
				for _, sub := range subs {
					f, err := resolveAndDownloadSub(tmpDir, sub, season, episode)
					if err != nil {
						debugf("subtitle download failed (%s): %v", sub.Label, err)
						continue
					}
					debugf("subtitle file: %s (%s)", f, sub.Label)
					subFiles = append(subFiles, f)
				}
			}
		}
	}

	// Download mode
	if flagDownload != "" {
		dlSub := ""
		if len(subFiles) > 0 {
			dlSub = subFiles[0]
		}
		baseDir, err := resolveDownloadBaseDir(flagDownload)
		if err != nil {
			return fmt.Errorf("resolving download dir: %w", err)
		}
		outputDir := resolveDownloadOutputDir(baseDir, selected, season)
		outputPath, err := download.Download(stream, title, outputDir, dlSub)
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
		return player.NotFoundError(cfg.Player)
	}

	lastPos, err := p2.Play(stream, title, startPos, subFiles)
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

// resolveAndDownloadSub handles downloading a subtitle, resolving provider-specific
// URL schemes (opensubtitles:, subdl:) to actual files.
func resolveAndDownloadSub(tmpDir *subtitle.TempDir, sub media.Subtitle, season, episode int) (string, error) {
	if strings.HasPrefix(sub.URL, "subdl:") {
		zipURL := strings.TrimPrefix(sub.URL, "subdl:")
		client := subtitle.NewSubDL(cfg.SubDLAPIKey)
		return client.DownloadAndExtract(zipURL, tmpDir, season, episode)
	}
	if strings.HasPrefix(sub.URL, "opensubtitles:") {
		var fileID int
		fmt.Sscanf(sub.URL, "opensubtitles:%d", &fileID)
		osClient := subtitle.NewOpenSubtitles(cfg.OSAPIKey)
		downloadURL, err := osClient.ResolveDownloadURL(fileID)
		if err != nil {
			return "", fmt.Errorf("resolving OpenSubtitles download: %w", err)
		}
		sub.URL = downloadURL
	}
	return tmpDir.Download(sub)
}

// searchExternalSubs tries SubDL first, then OpenSubtitles as fallback.
func searchExternalSubs(title string, season, episode int) []media.Subtitle {
	var all []media.Subtitle
	if cfg.SubDLAPIKey != "" {
		debugf("trying SubDL subtitles...")
		subs, err := subtitle.NewSubDL(cfg.SubDLAPIKey).Search(
			title, cfg.SubsLanguage, season, episode,
		)
		if err != nil {
			debugf("SubDL search failed: %v", err)
		} else if len(subs) > 0 {
			all = append(all, subs...)
		}
	}
	if cfg.OSAPIKey != "" {
		debugf("trying OpenSubtitles fallback...")
		subs, err := subtitle.NewOpenSubtitles(cfg.OSAPIKey).Search(
			title, cfg.SubsLanguage, season, episode,
		)
		if err != nil {
			debugf("OpenSubtitles search failed: %v", err)
		} else if len(subs) > 0 {
			all = append(all, subs...)
		}
	}
	return all
}

func mergeSubtitles(groups ...[]media.Subtitle) []media.Subtitle {
	var merged []media.Subtitle
	seen := make(map[string]bool)
	for _, group := range groups {
		for _, sub := range group {
			if sub.URL == "" || seen[sub.URL] {
				continue
			}
			seen[sub.URL] = true
			merged = append(merged, sub)
		}
	}
	return merged
}

// initDownloadManager sets up the download manager with SQLite store and engines.
// Returns a cleanup function that must be called on exit.
func initDownloadManager(c *config.Config) (*dlmanager.Manager, func(), error) {
	dbPath, err := config.DownloadsDBPath()
	if err != nil {
		return nil, nil, fmt.Errorf("getting db path: %w", err)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("opening downloads db: %w", err)
	}

	client := httputil.NewClient()
	httpEng := &engine.HTTPEngine{Client: client, MaxRetries: c.MaxRetries}
	hlsEng := &engine.HLSEngine{Client: client, Store: s, MaxRetries: c.MaxRetries}

	workers := c.MaxConcurrentDownloads
	if workers < 1 {
		workers = 2
	}

	stallTimeout := time.Duration(c.StallTimeout) * time.Second
	mgr := dlmanager.New(s, httpEng, hlsEng, workers, stallTimeout)
	mgr.SetResolver(makeStreamResolver(newProvider()))
	ctx := context.Background()
	mgr.Start(ctx)

	cleanup := func() {
		mgr.Stop()
		s.Close()
	}

	return mgr, cleanup, nil
}
