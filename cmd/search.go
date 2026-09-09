package cmd

import (
	"context"
	"encoding/json"
	"errors"
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
	"lobster/internal/playlist"
	"lobster/internal/poster"
	"lobster/internal/provider"
	"lobster/internal/subtitle"
	"lobster/internal/torrentstream"
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

		for {
			selected, lineup, startIdx, selectedProvider, err := tui.StartApp(p, cfg, liveTVSources(), mgr, fallbackSearchProviders(p)...)
			if err != nil {
				return err
			}
			if selected == nil {
				return nil // user quit the browser without selecting
			}
			if selectedProvider == nil {
				selectedProvider = p
			}
			if lineup != nil { // a Live TV channel was chosen -> surf the category
				if sp, ok := selectedProvider.(provider.StreamProvider); ok {
					if surfErr := playLiveSurf(sp, lineup, startIdx); errors.Is(surfErr, errSurfBackToList) {
						continue // reopen the browser
					} else {
						return surfErr
					}
				}
			}
			return resolveAndPlay(selectedProvider, *selected, 0, 0)
		}
	}

	debugf("searching for: %s", query)

	return playFlow(p, query)
}

// playFlow handles the full search -> select -> play flow.
func playFlow(p provider.Provider, query string) error {
	results, err := gatherSearchResults(p, fallbackSearchProviders(p), query)
	if err != nil {
		return err
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
	posterRows := posterCols * 3 / 4

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

// The TUI's download dialog cannot build the fallback chain itself: that needs
// the configured primary and the health store, both of which live in cmd.
// Without this wiring the dialog is an error message for any primary that
// cannot enumerate episodes.
//
// It is wired here rather than in searchRun because searchRun reopens the
// browser in a loop, and Bubble Tea does not wait for its Cmd goroutines at
// shutdown — a leaked fetchEpisodes could still be reading this var while the
// next iteration wrote it. The value was the same both times, so nothing
// misbehaved, but it was an unsynchronised write to a package var all the
// same. Assigning once, before any goroutine exists, removes the question.
func init() {
	tui.EpisodeListFallback = tuiEpisodeListFallback
}

// tuiEpisodeListFallback is tui.EpisodeListFallback: the chain's answer for a
// season the TUI's own provider could not list.
func tuiEpisodeListFallback(prov provider.Provider, item media.SearchResult, seasonNumber int) ([]media.Episode, error) {
	a := fallbackEpisodeList(prov, item, seasonNumber)
	if a == nil {
		return nil, fmt.Errorf("no fallback provider could list season %d of %q", seasonNumber, item.Title)
	}
	return a.episodes, nil
}

// episodeIndex is the position of the episode numbered n, or -1. It is the
// one lookup that decides whether a request is honoured or refused, so it is
// named rather than repeated: leaving the index at its zero value on a miss is
// exactly how a request for episode 47 came back as a successful play of
// episode 1.
func episodeIndex(episodes []media.Episode, n int) int {
	for i, ep := range episodes {
		if ep.Number == n {
			return i
		}
	}
	return -1
}

// seasonIndex is episodeIndex for seasons, or -1.
func seasonIndex(seasons []media.Season, n int) int {
	for i, s := range seasons {
		if s.Number == n {
			return i
		}
	}
	return -1
}

// selectItem is ui.Select as a package var, seamed like agentProvider and
// newPlayer so a test can drive the season and episode menus without a
// terminal. The menus are not decoration: a provider that cannot enumerate
// episodes used to fabricate a list, and the only way to check that the list
// now offered is a real one is to look at what the menu was handed.
var selectItem = ui.Select

// resolveAndPlay handles season/episode selection for TV and then plays.
func resolveAndPlay(p provider.Provider, selected media.SearchResult, season, episode int) error {
	episodeID := ""
	title := selected.Title

	// chainPrimary is the provider that was configured, kept apart from p
	// because the episode-list recovery below rebinds p to whichever chain
	// member could answer. The fallback chain is built by *excluding* its
	// argument (fallbackProviders, cmd/fallback.go), so passing the rebound p
	// to anything that builds one would drop the single provider proven to
	// have this show, this season and this episode list, and add back the
	// primary that could not list it.
	chainPrimary := p

	// Live channels are endless streams; ffmpeg-to-file would never finish.
	if _, isLive := p.(*provider.LiveTV); isLive && flagDownload != "" {
		return fmt.Errorf("live channels cannot be downloaded")
	}

	if selected.Type == media.TV {
		// Get seasons
		stopSeasons := ui.StartSpinner("Fetching seasons...")
		seasons, err := p.GetSeasons(selected.ID)
		stopSeasons()
		if err != nil || len(seasons) == 0 {
			// Primary provider can't resolve seasons — try fallback stream
			debugf("primary provider seasons failed: %v, trying fallbacks", err)
			fmt.Fprintf(os.Stderr, "Provider has no season data, trying fallbacks...\n")
			fbStream, fbErr := tryFallbackStream(p, selected, season, episode)
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
			// A requested number absent from a list the provider really did
			// return is an error, not a fallback to index 0. Leaving it at 0
			// played season one and reported success, so the caller was told
			// it got the season it asked for.
			seasonIdx = seasonIndex(seasons, season)
			if seasonIdx < 0 {
				return fmt.Errorf("season %d not found for %q (list them with 'lobster episodes --ref ...')", season, title)
			}
		} else {
			seasonItems := make([]string, len(seasons))
			for i, s := range seasons {
				seasonItems[i] = fmt.Sprintf("Season %d", s.Number)
			}

			// In download mode, offer multi-season batch options
			if flagDownload != "" && len(seasons) > 1 {
				batchItems := []string{
					"Download all seasons",
					"Download season range (e.g. 1-3)",
				}
				seasonItems = append(batchItems, seasonItems...)
			}

			seasonIdx, err = selectItem("Season", seasonItems)
			if err != nil {
				return err
			}

			// Handle multi-season batch options
			if flagDownload != "" && len(seasons) > 1 {
				if seasonIdx == 0 {
					return batchDownloadMultiSeason(p, selected, seasons)
				} else if seasonIdx == 1 {
					for {
						rangeInput, err := ui.Input("Season range")
						if err != nil {
							return err
						}
						matched, err := parseSeasonRange(rangeInput, seasons)
						if err != nil {
							fmt.Fprintf(os.Stderr, "Invalid range: %v\n", err)
							continue
						}
						if len(matched) == 0 {
							fmt.Fprintln(os.Stderr, "No seasons matched the range.")
							continue
						}
						return batchDownloadMultiSeason(p, selected, matched)
					}
				}
				// Offset index by 2 for injected batch options
				seasonIdx -= 2
			}
		}

		selectedSeason := seasons[seasonIdx]
		debugf("season: %d (ID: %s)", selectedSeason.Number, selectedSeason.ID)

		// providerID is the key p answers to. It starts as the work's own ID
		// and follows p if the episode-list recovery below moves playback to a
		// chain provider. selected.ID stays put: it is the identity history is
		// keyed on (cmd/session.go), so it has to mean the same work whichever
		// provider ended up answering this run.
		providerID := selected.ID

		// Get episodes
		stopEps := ui.StartSpinner("Fetching episodes...")
		episodes, err := p.GetEpisodes(providerID, selectedSeason.ID)
		stopEps()
		if err != nil || len(episodes) == 0 {
			// The primary has season data but cannot enumerate this season's
			// episodes. MovieBox and VidNest are exactly that shape: their
			// season counts are measured while their episode listings are
			// not available at all, and they now say so rather than
			// generating a list nobody can tell from a real one.
			//
			// Ask the chain for the list first, exactly as `episodes` does.
			// A list is what most of this function needs: interactive use
			// passes no --episode at all, so without one there is no menu to
			// offer and every show under such a primary is an error. It also
			// buys back playlist continuity — a session built from a real
			// list can go to the next episode, which a single resolved stream
			// cannot.
			//
			// The list is a chain provider's own, so playback moves to that
			// provider too: the numbers offered are numbers it will honour.
			// Only the provider-call key moves with it, not selected.ID.
			debugf("primary provider episodes failed: %v (%d episodes), trying fallbacks", err, len(episodes))
			fmt.Fprintf(os.Stderr, "Provider has no episode list, trying fallbacks...\n")

			answer := fallbackEpisodeList(p, selected, selectedSeason.Number)
			// A requested episode must be in the list before the session is
			// built on it. A chain provider can have a real but shorter list
			// than the show — a currently-airing season, say — and playing
			// the nearest entry instead is the silent-substitution bug this
			// whole path exists to prevent. When the number is absent the
			// resolver below still gets its turn, and it needs no list.
			if answer != nil && (episode == 0 || episodeIndex(answer.episodes, episode) >= 0) {
				debugf("episode list recovered from %T (%d episodes)", answer.hit.provider, len(answer.episodes))
				p = answer.hit.provider
				providerID = answer.hit.id
				seasons = answer.hit.seasons
				// The season came out of this very list, so the lookup
				// cannot miss; clamp anyway rather than index with -1.
				if seasonIdx = seasonIndex(seasons, answer.season.Number); seasonIdx < 0 {
					seasonIdx = 0
				}
				selectedSeason = answer.season
				episodes = answer.episodes
				err = nil
			} else if episode > 0 {
				// A caller who already knows which episode it wants does not
				// need a list: the fallback resolver reaches a StreamProvider
				// through Watch with an episode ID built arithmetically from
				// season and episode (tryStreamProviderFallback,
				// internal/resolver/probe.go), so this mirrors the branch
				// above for a primary that cannot enumerate seasons.
				fbStream, fbErr := tryFallbackStream(p, selected, selectedSeason.Number, episode)
				if fbErr == nil {
					return playStream(fbStream, title, selected, selectedSeason.Number, episode)
				}
				debugf("fallback stream failed: %v", fbErr)
			}
		}
		if err != nil || len(episodes) == 0 {
			// A list that arrived with an error is not a list: the provider
			// said it could not finish, and treating the part it managed as
			// the season is the fabrication bug in another form — a menu two
			// entries long for a 22-episode season, a playlist that ends
			// early, and success reported throughout. err is cleared above
			// when the chain supplies a real list, which is the only way past
			// this gate with something in hand.
			//
			// Out of options, so say who could not answer and what to do
			// instead. "episode listing unavailable" on its own leaves a user
			// under a MovieBox or VidNest primary with a dead end.
			//
			// `episodes` is not a second search — the recovery above already
			// asked the same chain — but it is a different report: it prints
			// the seasons and the episode numbers a chain provider does have
			// (fallbackSeasonHits/firstEpisodeList, cmd/episodes.go). That is
			// what a caller needs here, because this path refuses a requested
			// number the chain's list lacks rather than substituting a
			// neighbour, and a refusal is only actionable once you can see
			// which numbers exist.
			//
			// The --episode hint is only offered when no episode was
			// requested: with one, tryFallbackStream just failed above, so
			// suggesting it would send the caller back to what did not work.
			remedy := "list them with 'lobster episodes --ref ...', which asks every fallback provider"
			if episode == 0 {
				remedy += ", or pass --episode N to play a known episode without a list"
			}
			if err != nil {
				return fmt.Errorf("%s cannot list season %d of %q: %w (%s)", providerLabel(p), selectedSeason.Number, title, err, remedy)
			}
			return fmt.Errorf("%s returned no episodes for season %d of %q (%s)", providerLabel(p), selectedSeason.Number, title, remedy)
		}

		// Select episode (or use provided)
		episodeIdx := 0
		if episode > 0 {
			// Same rule as the season above, and the same bug: an episode
			// missing from a real list silently played episode one. The list
			// being unavailable is a different case entirely and was handled
			// above by handing the request to the fallback resolver, which
			// needs no list — so refusing here cannot break a provider that
			// never enumerates episodes.
			episodeIdx = episodeIndex(episodes, episode)
			if episodeIdx < 0 {
				return fmt.Errorf("season %d of %q has no episode %d (list them with 'lobster episodes --ref ...')", selectedSeason.Number, title, episode)
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

			episodeIdx, err = selectItem("Episode", episodeItems)
			if err != nil {
				return err
			}

			// Handle batch options
			if flagDownload != "" {
				if episodeIdx == 0 {
					// Download all episodes
					return batchDownload(chainPrimary, selected, episodes, selectedSeason)
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
						return batchDownload(chainPrimary, selected, matched, selectedSeason)
					}
				}
				// Offset index by 2 for injected batch options
				episodeIdx -= 2
			}
		}

		selectedEpisode := episodes[episodeIdx]
		debugf("episode: %d (ID: %s)", selectedEpisode.Number, selectedEpisode.ID)

		// Create a playlist session for continuous playback
		sess := playlist.NewWithProviderID(p, selected, providerID, seasons, episodes, seasonIdx, episodeIdx)
		sess.ChainPrimary = chainPrimary
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
			fbStream, fbErr := tryFallbackStream(p, selected, season, episode)
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
			fbStream, err := tryFallbackStream(p, selected, season, episode)
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
	fbStream, err := tryFallbackStream(p, selected, season, episode)
	stopStream()
	if err != nil {
		debugf("fallback failed: %v", err)
		hint := ""
		if _, isFlixHQ := p.(*provider.FlixHQ); isFlixHQ {
			hint = "\nTip: try --base moviebox or --base vaplayer for better stream availability"
		} else if _, isFlixHQWS := p.(*provider.FlixHQWS); isFlixHQWS {
			hint = "\nTip: try --base moviebox or --base vaplayer for better stream availability"
		}
		return fmt.Errorf("all providers failed for %s: %w%s", title, err, hint)
	}
	return playStream(fbStream, title, selected, season, episode)
}

// newPlayer constructs the player playStream launches. A package var, seamed
// like agentProvider and agentResolveAndPlay (cmd/play.go), so tests can
// exercise playStream's post-playback handling without a real media player.
var newPlayer = player.New

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
					f, err := subtitleDownload(tmpDir, sub, season, episode)
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

	// A magnet is not something a player or ffmpeg can open. Stand up the local
	// torrent server, which downloads pieces in reading order and serves the
	// film over loopback, then carry on with an ordinary HTTP URL.
	if torrentstream.IsMagnet(stream.URL) {
		fmt.Fprintln(os.Stderr, "Torrent source: joining swarm — your IP is visible to its peers.")
		ts, err := torrentstream.New("")
		if err != nil {
			return fmt.Errorf("starting torrent stream: %w", err)
		}
		defer func() { _ = ts.Close() }()

		stopT := ui.StartSpinner("Fetching torrent metadata...")
		localURL, err := ts.Serve(stream.URL)
		stopT()
		if err != nil {
			return fmt.Errorf("torrent stream: %w", err)
		}
		debugf("torrent serving at %s", localURL)
		// Copy rather than mutate: the caller's stream is reused for history.
		local := *stream
		local.URL = localURL
		local.Referer = ""
		stream = &local
		fmt.Fprintln(os.Stderr, "Buffering — playback starts once the first pieces arrive.")
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

	p2 := newPlayer(cfg.Player, cfg.AudioLanguage)
	if !p2.Available() {
		return player.NotFoundError(cfg.Player)
	}

	// Periodic checkpoints while playback runs: a hard shutdown (power cut,
	// kernel panic) kills the player and this process together, so waiting
	// for Play to return would lose the whole watch position.
	if cfg.History {
		if cp, ok := p2.(player.Checkpointer); ok {
			cp.SetCheckpoint(historyCheckpoint(selected.ID, selected.Title, selected.Type, season, episode))
		}
	}

	result, playErr := p2.Play(stream, title, startPos, subFiles)

	// Save to history before surfacing any player error: Play returns the
	// tracked position alongside the error, and an abnormal exit (killed,
	// crash) is exactly the watch whose resume point must not be lost. With no
	// tracked position there is nothing to keep, and writing 0 would clobber a
	// real position from an earlier watch of the same title.
	//
	// A result marked PositionUnknown carries a default rather than a
	// measurement, and the two reasons for that are not the same thing. A
	// player with no position tracking (vlc, iina, celluloid) also marks the
	// result PositionUntracked: the watch definitely happened, so it is
	// recorded, keeping whatever position history already holds. A tracked
	// player that was never heard from says nothing about whether playback
	// happened at all, so it writes nothing — even on a clean exit.
	if cfg.History && (playErr == nil || result.Position > 0) &&
		(!result.PositionUnknown || result.PositionUntracked) {
		entry := media.HistoryEntry{
			ID:       selected.ID,
			Title:    selected.Title,
			Type:     selected.Type,
			Season:   season,
			Episode:  episode,
			Position: result.Position,
			Duration: result.Duration,
		}
		save := history.Save
		if result.PositionUntracked {
			save = history.SaveKeepingPosition
		}
		if err := save(entry); err != nil {
			debugf("saving history failed: %v", err)
		}
	}

	if playErr != nil {
		return fmt.Errorf("playback failed: %w", playErr)
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
