package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"lobster/internal/download"
	"lobster/internal/extract"
	"lobster/internal/httputil"
	"lobster/internal/media"
	"lobster/internal/provider"
	"lobster/internal/subtitle"
	"lobster/internal/ui"
)

// parseEpisodeRange parses a range string like "1-5", "3,7,9", or "1-3,7,10-12"
// and returns matching episodes. Ranges refer to episode numbers, not list positions.
// Warns on stderr for requested numbers that don't exist in the episode list.
func parseEpisodeRange(input string, episodes []media.Episode) ([]media.Episode, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, fmt.Errorf("empty range")
	}

	// Build lookup map: episode number → episode
	byNum := make(map[int]media.Episode)
	for _, ep := range episodes {
		byNum[ep.Number] = ep
	}

	// Parse requested numbers from the range string
	requested := make(map[int]bool)
	parts := strings.Split(input, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if idx := strings.Index(part, "-"); idx >= 0 {
			// Range: "1-5"
			startStr := strings.TrimSpace(part[:idx])
			endStr := strings.TrimSpace(part[idx+1:])
			start, err := strconv.Atoi(startStr)
			if err != nil {
				return nil, fmt.Errorf("invalid range start %q", startStr)
			}
			end, err := strconv.Atoi(endStr)
			if err != nil {
				return nil, fmt.Errorf("invalid range end %q", endStr)
			}
			if start > end {
				return nil, fmt.Errorf("invalid range: %d > %d", start, end)
			}
			for n := start; n <= end; n++ {
				requested[n] = true
			}
		} else {
			// Single number: "5"
			n, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid episode number %q", part)
			}
			requested[n] = true
		}
	}

	if len(requested) == 0 {
		return nil, fmt.Errorf("no episode numbers in range")
	}

	// Match requested numbers against actual episodes
	var matched []media.Episode
	for num := range requested {
		if ep, ok := byNum[num]; ok {
			matched = append(matched, ep)
		} else {
			fmt.Fprintf(os.Stderr, "Warning: episode %d not found, skipping\n", num)
		}
	}

	// Sort by episode number
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Number < matched[j].Number
	})

	return matched, nil
}

// batchDownload downloads multiple episodes sequentially with skip-on-fail and retry.
func batchDownload(p provider.Provider, selected media.SearchResult, episodes []media.Episode, season media.Season) error {
	if flagJSON {
		return fmt.Errorf("--json is not supported with batch downloads")
	}

	// Resolve base download directory
	dir := flagDownload
	if dir == "" {
		var err error
		dir, err = cfg.ExpandDownloadDir()
		if err != nil {
			return fmt.Errorf("resolving download dir: %w", err)
		}
	}

	// Build nested output directory: <base>/<ShowTitle>/Season <NN>/
	showDir := httputil.SanitizeFilename(selected.Title)
	seasonDir := fmt.Sprintf("Season %02d", season.Number)
	outputDir := filepath.Join(dir, showDir, seasonDir)

	total := len(episodes)
	var failed []media.Episode

	for i, ep := range episodes {
		epLabel := formatEpisodeLabel(ep)
		fmt.Fprintf(os.Stderr, "[%d/%d] Downloading %s...\n", i+1, total, epLabel)

		if err := downloadSingleEpisode(p, selected, ep, season.Number, outputDir, epLabel); err != nil {
			fmt.Fprintf(os.Stderr, "  Failed: %v\n", err)
			failed = append(failed, ep)
		}
	}

	printBatchSummary(total, failed)

	// Retry loop
	for len(failed) > 0 {
		ok, err := ui.Confirm("Retry failed downloads?")
		if err != nil || !ok {
			break
		}

		retrying := failed
		failed = nil
		for i, ep := range retrying {
			epLabel := formatEpisodeLabel(ep)
			fmt.Fprintf(os.Stderr, "[%d/%d] Retrying %s...\n", i+1, len(retrying), epLabel)

			if err := downloadSingleEpisode(p, selected, ep, season.Number, outputDir, epLabel); err != nil {
				fmt.Fprintf(os.Stderr, "  Failed: %v\n", err)
				failed = append(failed, ep)
			}
		}

		printBatchSummary(len(retrying), failed)
	}

	return nil
}

// downloadSingleEpisode resolves and downloads one episode, trying fallback servers on failure.
func downloadSingleEpisode(p provider.Provider, selected media.SearchResult, ep media.Episode, seasonNum int, outputDir, title string) error {
	// Get servers
	servers, err := p.GetServers(selected.ID, ep.ID)
	if err != nil || len(servers) == 0 {
		if err != nil {
			debugf("GetServers failed: %v", err)
		}
		// Try fallback before giving up
		fmt.Fprintf(os.Stderr, "  Primary provider failed, trying fallback...\n")
		fbStream, fbErr := tryFallbackStream(p, selected.Title, selected.Type, seasonNum, ep.Number)
		if fbErr != nil {
			if err != nil {
				return fmt.Errorf("getting servers: %w", err)
			}
			return fmt.Errorf("no servers found")
		}
		_, dlErr := download.Download(fbStream, title, outputDir, "")
		return dlErr
	}

	// Order servers: preferred first, then the rest as fallbacks
	ordered := orderServers(servers, cfg.Provider)

	var lastErr error
	for _, srv := range ordered {
		debugf("trying server: %s (ID: %s)", srv.Name, srv.ID)

		err := tryDownloadFromServer(p, srv, selected.ID, ep.ID, title, outputDir)
		if err == nil {
			return nil
		}
		lastErr = err
		debugf("server %s failed: %v", srv.Name, err)
		if len(ordered) > 1 {
			fmt.Fprintf(os.Stderr, "  Server %s failed, trying next...\n", srv.Name)
		}
	}

	return lastErr
}

// tryDownloadFromServer attempts to resolve and download from a single server.
func tryDownloadFromServer(p provider.Provider, srv media.Server, mediaID, episodeID, title, outputDir string) error {
	var stream *media.Stream
	var err error

	// StreamProvider (consumet) can resolve streams directly
	if sp, ok := p.(provider.StreamProvider); ok {
		stream, err = sp.Watch(mediaID, episodeID, srv.Name, cfg.Quality)
		if err != nil {
			return fmt.Errorf("watch failed: %w", err)
		}
	} else {
		// Get embed URL
		embedURL, err := p.GetEmbedURL(srv.ID)
		if err != nil {
			return fmt.Errorf("getting embed URL: %w", err)
		}

		// Extract stream
		ext, resolvedURL := extract.ResolveForURL(embedURL)
		stream, err = ext.Extract(resolvedURL, cfg.Quality)
		if err != nil {
			return fmt.Errorf("extracting stream: %w", err)
		}
	}

	// Handle subtitles (explicit cleanup, no defer in loop)
	var subFile string
	var tmpDir *subtitle.TempDir
	if !flagNoSubs && len(stream.Subtitles) > 0 {
		best := subtitle.BestMatch(stream.Subtitles, cfg.SubsLanguage)
		if best != nil {
			tmpDir, err = subtitle.NewTempDir()
			if err == nil {
				subFile, err = tmpDir.Download(*best)
				if err != nil {
					debugf("subtitle download failed: %v", err)
					subFile = ""
				}
			}
		}
	}

	// Download
	_, dlErr := download.Download(stream, title, outputDir, subFile)

	// Clean up subtitle temp dir explicitly
	if tmpDir != nil {
		tmpDir.Cleanup()
	}

	return dlErr
}

// orderServers returns servers with the preferred one first, rest as fallbacks.
func orderServers(servers []media.Server, preferred string) []media.Server {
	ordered := make([]media.Server, 0, len(servers))
	var rest []media.Server

	for _, s := range servers {
		if strings.EqualFold(s.Name, preferred) {
			ordered = append([]media.Server{s}, ordered...)
		} else {
			rest = append(rest, s)
		}
	}

	return append(ordered, rest...)
}

// orderServersWithCache orders servers: cached name first, then preferred, then rest.
func orderServersWithCache(servers []media.Server, preferred, cached string) []media.Server {
	if cached == "" {
		return orderServers(servers, preferred)
	}
	ordered := make([]media.Server, 0, len(servers))
	var rest []media.Server
	for _, s := range servers {
		if strings.EqualFold(s.Name, cached) {
			ordered = append([]media.Server{s}, ordered...)
		} else if strings.EqualFold(s.Name, preferred) {
			ordered = append(ordered, s)
		} else {
			rest = append(rest, s)
		}
	}
	return append(ordered, rest...)
}

// formatEpisodeLabel creates a display label like "E01 - The Duel" or "E01".
func formatEpisodeLabel(ep media.Episode) string {
	if ep.Title != "" {
		return fmt.Sprintf("E%02d - %s", ep.Number, ep.Title)
	}
	return fmt.Sprintf("E%02d", ep.Number)
}

// printBatchSummary prints the download results summary.
func printBatchSummary(total int, failed []media.Episode) {
	succeeded := total - len(failed)
	if len(failed) == 0 {
		fmt.Fprintf(os.Stderr, "Downloaded %d/%d episodes.\n", succeeded, total)
	} else {
		labels := make([]string, len(failed))
		for i, ep := range failed {
			labels[i] = fmt.Sprintf("E%02d", ep.Number)
		}
		fmt.Fprintf(os.Stderr, "Downloaded %d/%d episodes. Failed: %s\n", succeeded, total, strings.Join(labels, ", "))
	}
}
