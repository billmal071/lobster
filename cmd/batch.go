package cmd

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"lobster/internal/config"
	"lobster/internal/download"
	"lobster/internal/media"
	"lobster/internal/provider"
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

// batchDownload downloads multiple episodes sequentially with skip-on-fail and
// retry.
//
// chainPrimary is the configured primary, not whichever provider produced the
// episode list: downloads resolve their streams through the fallback chain,
// which is built by excluding its argument, so passing a provider the
// episode-list recovery moved to would exclude the one source proven to have
// these episodes.
func batchDownload(chainPrimary provider.Provider, selected media.SearchResult, episodes []media.Episode, season media.Season) error {
	if flagJSON {
		return fmt.Errorf("--json is not supported with batch downloads")
	}

	baseDir, err := resolveDownloadBaseDir(flagDownload)
	if err != nil {
		return fmt.Errorf("resolving download dir: %w", err)
	}
	outputDir := buildTVSeasonDownloadDir(baseDir, selected.Title, season.Number)

	total := len(episodes)
	var failed []media.Episode

	for i, ep := range episodes {
		epLabel := formatEpisodeLabel(ep)
		fmt.Fprintf(os.Stderr, "[%d/%d] Downloading %s...\n", i+1, total, epLabel)

		if err := downloadSingleEpisode(chainPrimary, selected, ep, season.Number, outputDir, epLabel); err != nil {
			fmt.Fprintf(os.Stderr, "  Failed: %v\n", err)
			failed = append(failed, ep)
		}
	}

	printBatchSummary(total, failed)

	// Retry loop
	for len(failed) > 0 {
		ok, err := ui.Confirm("Retry failed downloads?")
		if err != nil {
			return fmt.Errorf("retry prompt failed: %w", err)
		}
		if !ok {
			break
		}

		retrying := failed
		failed = nil
		for i, ep := range retrying {
			epLabel := formatEpisodeLabel(ep)
			fmt.Fprintf(os.Stderr, "[%d/%d] Retrying %s...\n", i+1, len(retrying), epLabel)

			if err := downloadSingleEpisode(chainPrimary, selected, ep, season.Number, outputDir, epLabel); err != nil {
				fmt.Fprintf(os.Stderr, "  Failed: %v\n", err)
				failed = append(failed, ep)
			}
		}

		printBatchSummary(len(retrying), failed)
	}

	return nil
}

// downloadSingleEpisode resolves and downloads one episode via fallback
// providers. chainPrimary is the provider to build that chain around — see
// batchDownload. A magnet is served through the local torrent server first,
// as the single-episode path does (playStream).
func downloadSingleEpisode(chainPrimary provider.Provider, selected media.SearchResult, ep media.Episode, seasonNum int, outputDir, title string) error {
	stream, closeTorrent, err := openBatchStream(chainPrimary, selected, ep, seasonNum)
	if err != nil {
		return err
	}
	defer closeTorrent()
	_, dlErr := download.Download(stream, title, outputDir, "")
	return dlErr
}

// openBatchStream resolves an episode and serves it if it is a magnet. A
// torrent that cannot be set up (no peers, metadata timeout) falls through to
// the streaming providers; torrent sources are left out of that chain so it
// cannot pick the same swarm again.
func openBatchStream(chainPrimary provider.Provider, selected media.SearchResult, ep media.Episode, seasonNum int) (*media.Stream, func(), error) {
	stream, err := resolveBatchStream(chainPrimary, selected, ep, seasonNum)
	if err != nil {
		return nil, nil, err
	}
	served, closeTorrent, err := serveTorrent(stream)
	if err == nil {
		return served, closeTorrent, nil
	}
	debugf("torrent setup failed for %s S%dE%d, trying streaming providers: %v", selected.Title, seasonNum, ep.Number, err)
	fb, fbErr := tryFallbackStreamFrom(withoutMagnetSources(agentFallbackProviders(chainPrimary)), selected, seasonNum, ep.Number)
	if fbErr != nil {
		return nil, nil, fmt.Errorf("%w; streaming fallback: %v", err, fbErr)
	}
	return serveTorrent(fb)
}

// resolveBatchStream asks the fallback chain for an episode's stream. An
// explicitly chosen torrent base (nyaa, eztv, 1337x) is asked first: the chain is
// built by excluding its primary, which would otherwise skip the one source
// the user named and the one proven to carry the episode list.
func resolveBatchStream(chainPrimary provider.Provider, selected media.SearchResult, ep media.Episode, seasonNum int) (*media.Stream, error) {
	if sp, ok := chainPrimary.(provider.StreamProvider); ok && cfg != nil && cfg.APIURL == "" && config.IsTorrentBase(cfg.Base) {
		stream, err := sp.Watch(selected.ID, ep.ID, "", cfgQuality())
		if err == nil && stream != nil && stream.URL != "" {
			return stream, nil
		}
		debugf("selected torrent source failed for %s episode %s: %v", selected.ID, ep.ID, err)
	}
	debugf("resolving stream via fallback providers for %s S%dE%d", selected.Title, seasonNum, ep.Number)
	stream, err := tryFallbackStream(chainPrimary, selected, seasonNum, ep.Number)
	if err != nil {
		return nil, fmt.Errorf("all providers failed: %w", err)
	}
	return stream, nil
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

// preferQualityServers moves servers whose name starts with the requested
// quality token ("1080p ...") ahead of the rest, keeping relative order. An
// unstocked quality promotes the highest stocked one below it instead;
// "best", an empty quality, or nothing at or below leaves the list untouched.
func preferQualityServers(servers []media.Server, quality string) []media.Server {
	want := strings.ToLower(strings.TrimSpace(quality))
	if want == "" || want == "best" {
		return servers
	}
	height, err := strconv.Atoi(strings.TrimSuffix(want, "p"))
	if err != nil {
		return servers
	}
	target := 0
	for _, s := range servers {
		h := serverQualityHeight(s.Name)
		if h == height {
			target = h
			break
		}
		if h < height && h > target {
			target = h
		}
	}
	if target == 0 {
		return servers
	}
	token := strconv.Itoa(target) + "p"
	var matched, rest []media.Server
	for _, s := range servers {
		name := strings.ToLower(s.Name)
		if name == token || strings.HasPrefix(name, token+" ") {
			matched = append(matched, s)
		} else {
			rest = append(rest, s)
		}
	}
	return append(matched, rest...)
}

// serverQualityHeight reads the "<n>p" token a torrent server name starts
// with (YTS, EZTV, Nyaa, 1337x), or 0 for a name that carries none.
func serverQualityHeight(name string) int {
	token, _, _ := strings.Cut(strings.ToLower(name), " ")
	if !strings.HasSuffix(token, "p") {
		return 0
	}
	h, err := strconv.Atoi(strings.TrimSuffix(token, "p"))
	if err != nil || h <= 0 {
		return 0
	}
	return h
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

// formatSeasonEpisodeLabel creates a label like "S01E03 - Episode Title".
func formatSeasonEpisodeLabel(seasonNum int, ep media.Episode) string {
	if ep.Title != "" {
		return fmt.Sprintf("S%02dE%02d - %s", seasonNum, ep.Number, ep.Title)
	}
	return fmt.Sprintf("S%02dE%02d", seasonNum, ep.Number)
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

// failedEpisode tracks a failed episode with its season number for multi-season reporting.
type failedEpisode struct {
	SeasonNum int
	Episode   media.Episode
}

// parseSeasonRange parses a range string like "1-3", "1,4,7", or "1-3,5"
// and returns matching seasons. Works the same way as parseEpisodeRange.
func parseSeasonRange(input string, seasons []media.Season) ([]media.Season, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, fmt.Errorf("empty range")
	}

	byNum := make(map[int]media.Season)
	for _, s := range seasons {
		byNum[s.Number] = s
	}

	requested := make(map[int]bool)
	parts := strings.Split(input, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if idx := strings.Index(part, "-"); idx >= 0 {
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
			n, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid season number %q", part)
			}
			requested[n] = true
		}
	}

	if len(requested) == 0 {
		return nil, fmt.Errorf("no season numbers in range")
	}

	var matched []media.Season
	for num := range requested {
		if s, ok := byNum[num]; ok {
			matched = append(matched, s)
		} else {
			fmt.Fprintf(os.Stderr, "Warning: season %d not found, skipping\n", num)
		}
	}

	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Number < matched[j].Number
	})

	return matched, nil
}

// seasonLister lists a season's episodes, asking the fallback chain when the
// provider cannot answer, and scanning that chain at most once however many
// seasons it is asked about.
//
// A primary can enumerate seasons and not episodes — MovieBox and VidNest both
// do — and a multi-season batch then recorded one synthetic failure per season
// and downloaded nothing, for a show the chain could list in full. The list is
// all that is needed here: downloads resolve their streams by title, season
// and episode number through makeStreamResolver, not by episode ID, so a list
// borrowed from another provider costs the download nothing.
//
// The scan is held across seasons because it is neither cheap nor stable. It
// is a search plus a GetSeasons call against every fallback provider, bounded
// at episodesFallbackTimeout, so a ten-season batch was ten of them — around a
// hundred seconds of scanning before the first byte. And a chain that answered
// differently between scans would hand different seasons of one show to
// different providers, which is not something the download summary would ever
// mention.
type seasonLister struct {
	primary  provider.Provider
	selected media.SearchResult
	hits     []*seasonHit
	scanned  bool
}

func newSeasonLister(p provider.Provider, selected media.SearchResult) *seasonLister {
	return &seasonLister{primary: p, selected: selected}
}

func (l *seasonLister) episodes(season media.Season) ([]media.Episode, error) {
	episodes, err := l.primary.GetEpisodes(l.selected.ID, season.ID)
	if err == nil && len(episodes) > 0 {
		return episodes, nil
	}
	if season.Number < 0 {
		return episodes, err
	}
	if !l.scanned {
		l.scanned = true
		l.hits = fallbackSeasonHits(l.primary, contentRequest(l.selected))
	}
	if a := firstEpisodeList(l.hits, season.Number); a != nil {
		debugf("batch: %T listed %d episodes of season %d", a.hit.provider, len(a.episodes), season.Number)
		return a.episodes, nil
	}
	return episodes, err
}

// batchDownloadMultiSeason downloads all episodes from multiple seasons.
//
// primary is the configured provider in both of its roles here: it is asked
// for each season's episode list, and it is the provider the download chain is
// built around. This path is reached from the season menu, before the
// episode-list recovery can move anything, so the two never diverge — unlike
// batchDownload, which is reached after it.
func batchDownloadMultiSeason(primary provider.Provider, selected media.SearchResult, seasons []media.Season) error {
	if flagJSON {
		return fmt.Errorf("--json is not supported with batch downloads")
	}

	baseDir, err := resolveDownloadBaseDir(flagDownload)
	if err != nil {
		return fmt.Errorf("resolving download dir: %w", err)
	}

	var totalEpisodes int
	var downloaded int
	var failed []failedEpisode

	lister := newSeasonLister(primary, selected)

	for _, season := range seasons {
		fmt.Fprintf(os.Stderr, "\n=== Season %d ===\n", season.Number)

		stopEps := ui.StartSpinner(fmt.Sprintf("Fetching Season %d episodes...", season.Number))
		episodes, err := lister.episodes(season)
		stopEps()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to fetch Season %d episodes: %v\n", season.Number, err)
			// Count each expected episode as failed. Since we can't know the
			// exact count, record one failure entry for the season.
			failed = append(failed, failedEpisode{SeasonNum: season.Number, Episode: media.Episode{Number: 0}})
			totalEpisodes++
			continue
		}
		if len(episodes) == 0 {
			fmt.Fprintf(os.Stderr, "Season %d has no episodes, skipping\n", season.Number)
			continue
		}

		outputDir := buildTVSeasonDownloadDir(baseDir, selected.Title, season.Number)
		totalEpisodes += len(episodes)

		for i, ep := range episodes {
			epLabel := formatSeasonEpisodeLabel(season.Number, ep)
			fmt.Fprintf(os.Stderr, "Downloading Season %d, Episode %d of %d: %s\n",
				season.Number, i+1, len(episodes), epLabel)

			if err := downloadSingleEpisode(primary, selected, ep, season.Number, outputDir, epLabel); err != nil {
				fmt.Fprintf(os.Stderr, "  Failed: %v\n", err)
				failed = append(failed, failedEpisode{SeasonNum: season.Number, Episode: ep})
			} else {
				downloaded++
			}
		}
	}

	// Print overall summary
	printMultiSeasonSummary(totalEpisodes, downloaded, failed)

	// Retry loop for failed episodes
	for len(failed) > 0 {
		ok, err := ui.Confirm("Retry failed downloads?")
		if err != nil {
			return fmt.Errorf("retry prompt failed: %w", err)
		}
		if !ok {
			break
		}

		retrying := failed
		failed = nil
		for i, fe := range retrying {
			epLabel := formatSeasonEpisodeLabel(fe.SeasonNum, fe.Episode)
			fmt.Fprintf(os.Stderr, "[%d/%d] Retrying %s...\n", i+1, len(retrying), epLabel)

			outputDir := buildTVSeasonDownloadDir(baseDir, selected.Title, fe.SeasonNum)
			if err := downloadSingleEpisode(primary, selected, fe.Episode, fe.SeasonNum, outputDir, epLabel); err != nil {
				fmt.Fprintf(os.Stderr, "  Failed: %v\n", err)
				failed = append(failed, fe)
			} else {
				downloaded++
			}
		}

		printMultiSeasonSummary(totalEpisodes, downloaded, failed)
	}

	return nil
}

// printMultiSeasonSummary prints the multi-season download results.
func printMultiSeasonSummary(total, downloaded int, failed []failedEpisode) {
	if len(failed) == 0 {
		fmt.Fprintf(os.Stderr, "\nDownloaded %d/%d episodes across all seasons.\n", downloaded, total)
	} else {
		labels := make([]string, len(failed))
		for i, fe := range failed {
			labels[i] = fmt.Sprintf("S%02dE%02d", fe.SeasonNum, fe.Episode.Number)
		}
		fmt.Fprintf(os.Stderr, "\nDownloaded %d/%d episodes. Failed: %s\n",
			downloaded, total, strings.Join(labels, ", "))
	}
}
