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
	"lobster/internal/provider"
	"lobster/internal/subtitle"
	"lobster/internal/ui"
)

// searchRun is the default command: lobster <query>
func searchRun(cmd *cobra.Command, args []string) error {
	query := strings.Join(args, " ")

	if query == "" {
		// Prompt for query via fzf
		var err error
		query, err = ui.Input("Search")
		if err != nil {
			return fmt.Errorf("no search query provided")
		}
	}

	debugf("searching for: %s", query)

	p := provider.NewFlixHQ(cfg.Base)
	return playFlow(p, query)
}

// playFlow handles the full search -> select -> play flow.
func playFlow(p provider.Provider, query string) error {
	// Search
	results, err := p.Search(query)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	// Select content
	items := make([]string, len(results))
	for i, r := range results {
		items[i] = provider.FormatDisplayTitle(r)
	}

	idx, err := ui.Select("Select", items)
	if err != nil {
		return err
	}

	selected := results[idx]
	debugf("selected: %s (ID: %s, type: %s)", selected.Title, selected.ID, selected.Type)

	return resolveAndPlay(p, selected, 0, 0)
}

// resolveAndPlay handles season/episode selection for TV and then plays.
func resolveAndPlay(p provider.Provider, selected media.SearchResult, season, episode int) error {
	episodeID := ""
	title := selected.Title

	if selected.Type == media.TV {
		// Get seasons
		seasons, err := p.GetSeasons(selected.ID)
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
		episodes, err := p.GetEpisodes(selected.ID, selectedSeason.ID)
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
			episodeIdx, err = ui.Select("Episode", episodeItems)
			if err != nil {
				return err
			}
		}

		selectedEpisode := episodes[episodeIdx]
		episodeID = selectedEpisode.ID
		title = fmt.Sprintf("%s S%02dE%02d", selected.Title, selectedSeason.Number, selectedEpisode.Number)
		season = selectedSeason.Number
		episode = selectedEpisode.Number

		debugf("episode: %d (ID: %s)", selectedEpisode.Number, episodeID)
	}

	// Get servers
	servers, err := p.GetServers(selected.ID, episodeID)
	if err != nil {
		return fmt.Errorf("getting servers: %w", err)
	}

	if len(servers) == 0 {
		return fmt.Errorf("no servers found")
	}

	// Find preferred server
	serverIdx := 0
	for i, s := range servers {
		if strings.EqualFold(s.Name, cfg.Provider) {
			serverIdx = i
			break
		}
	}
	debugf("using server: %s (ID: %s)", servers[serverIdx].Name, servers[serverIdx].ID)

	// Get embed URL
	embedURL, err := p.GetEmbedURL(servers[serverIdx].ID)
	if err != nil {
		return fmt.Errorf("getting embed URL: %w", err)
	}
	debugf("embed URL: %s", embedURL)

	// Extract stream from embed URL
	ext := extract.New()
	stream, err := ext.Extract(embedURL, cfg.Quality)
	if err != nil {
		return fmt.Errorf("decrypting stream: %w", err)
	}
	debugf("stream URL: %s", stream.URL)

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
		dir := flagDownload
		if dir == "" {
			var err error
			dir, err = cfg.ExpandDownloadDir()
			if err != nil {
				return fmt.Errorf("resolving download dir: %w", err)
			}
		}
		outputPath, err := download.Download(stream, title, dir, subFile)
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
