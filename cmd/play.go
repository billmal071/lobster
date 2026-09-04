package cmd

import (
	"github.com/spf13/cobra"

	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/provider"
)

// agentResolveAndPlay is the playback entry point, as a package var so tests
// can observe what selection reaches it without launching a player.
var agentResolveAndPlay = resolveAndPlay

// agentPlayerCheck reports whether the configured player binary is available,
// and its display name for the error message. A package var, seamed the same
// way as agentProvider and agentResolveAndPlay, so tests can force either
// outcome without depending on what is actually installed on the machine
// running the test.
//
// Without this precondition, a missing player binary reaches resolveAndPlay,
// which returns a plain error from player.NotFoundError; playRun's catch-all
// then maps it to "providers_failed" (exit 3), telling the agent the
// providers are down when the real problem is that mpv (or whichever player
// is configured) is not installed. Checking availability up front lets it
// report exit 4 with the right message instead.
var agentPlayerCheck = func() (available bool, name string) {
	if cfg == nil {
		return true, ""
	}
	p := player.New(cfg.Player, cfg.AudioLanguage)
	return p.Available(), p.Name()
}

var playCmd = &cobra.Command{
	Use:   "play --ref <REF>",
	Short: "Play a ref returned by lobster find (no prompts)",
	Long: `Play the exact title a ref identifies, without prompting.

The ref pins the selection — title, year, type and originating base. The
base is used as the resolution base unless --base is passed explicitly on
this invocation, which overrides it. Neither pins the stream: resolution
still runs through the fallback chain at play time, so if the original
source is down another provider's copy may be served.

For a series, both --season and --episode are required; without them playback
would fall through to the interactive picker and hang.

Only --detach honours the "stdout is JSON and nothing else" contract. Attached,
the player inherits this process's stdout — that is deliberate, so a human
running "lobster play --ref" still sees mpv's own output — but it means the
player's progress lines are interleaved with the JSON envelope. A caller
parsing stdout must pass --detach, which redirects the player's output to a log
file and reports that log's path in the envelope.`,
	Args: cobra.NoArgs,
	RunE: playRun,
}

func init() {
	markAgentCommand(playCmd)
	playCmd.Flags().StringVar(&flagRef, "ref", "", "Ref from lobster find (required)")
	playCmd.Flags().IntVar(&flagSeason, "season", 0, "Season number (required for TV)")
	playCmd.Flags().IntVar(&flagEpisode, "episode", 0, "Episode number (required for TV)")
	playCmd.Flags().BoolVar(&flagDetach, "detach", false, "Start playback in the background and return immediately")
	playCmd.Flags().BoolVar(&flagSupervised, "supervised", false, "Internal: marks the background supervisor process")
	_ = playCmd.Flags().MarkHidden("supervised")
}

// applyRefBase honors the ref's originating base unless the caller explicitly
// passed --base on this invocation. An ID found under `--base yts` is
// meaningless under another base (playRef doc comment, cmd/ref.go) — without
// this, a `find --base yts` result would silently resolve against whatever
// base is currently configured, losing the ID's exact-match bonus and letting
// a different provider's copy be served. An explicit --base is a deliberate
// override and must win.
//
// Shared by play and episodes rather than living in playRun: while only play
// applied it, a ref from `find --base flixhq.ws` played fine but listed
// nothing under episodes, because a MovieBox provider was handed a FlixHQ ID
// and reported "no seasons found". The two commands must agree on what one ref
// means.
func applyRefBase(cmd *cobra.Command, r playRef) {
	if r.Base != "" && cfg != nil && !cmd.Flags().Changed("base") {
		cfg.Base = r.Base
	}
}

// validateSeasonEpisode confirms the requested season and episode actually
// exist on p before any playback is dispatched.
//
// resolveAndPlay (cmd/search.go) initialises its seasonIdx/episodeIdx to 0 and
// only overwrites them on an exact Number match, so a request for a season or
// episode the show does not have fell through to index 0: `play --ref <show>
// --season 5 --episode 12` against a three-season show played S1E1 and
// reported success. `episodes` already answers exit 2 for exactly that input,
// so the two commands contradicted each other about whether a season exists.
//
// The distinction between the two failure modes is the point: a season that
// does not exist is exitNoResults (2) — pick a different number — while a
// provider that cannot answer is exitProvidersFailed (3) — run doctor. Mapping
// the second onto the first would tell the agent to fix the user's input when
// the sources are down.
func validateSeasonEpisode(p provider.Provider, r playRef, season, episode int) error {
	seasons, err := p.GetSeasons(r.ID)
	if err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "getting seasons for %q: %v", r.Title, err)
	}
	seasonID := ""
	found := false
	for _, s := range seasons {
		if s.Number == season {
			seasonID, found = s.ID, true
			break
		}
	}
	if !found {
		return emitErr("no_results", exitNoResults,
			"season %d not found for %q (list them with 'lobster episodes --ref ...')", season, r.Title)
	}

	eps, err := p.GetEpisodes(r.ID, seasonID)
	if err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "getting episodes for %q: %v", r.Title, err)
	}
	for _, e := range eps {
		if e.Number == episode {
			return nil
		}
	}
	return emitErr("no_results", exitNoResults,
		"season %d of %q has no episode %d", season, r.Title, episode)
}

func playRun(cmd *cobra.Command, args []string) error {
	r, err := decodeRef(flagRef)
	if err != nil {
		return emitErr("bad_ref", 1, "%v", err)
	}

	sel := r.searchResult()
	if sel.Type == media.TV && (flagSeason <= 0 || flagEpisode <= 0) {
		return emitErr("season_episode_required", 1,
			"%q is a series: pass --season and --episode (list them with 'lobster episodes --ref ...')", r.Title)
	}

	// Downloading walks a different, interactive path (batch range prompts), so
	// it is not supported here.
	if flagDownload != "" {
		return emitErr("unsupported", 1, "--download is not supported by 'play'; use the interactive CLI")
	}

	// Checked before the --detach dispatch so a detached invocation reports
	// exit 4 to the caller immediately, rather than forking a supervisor that
	// fails into a log file the agent has no reason to read.
	if available, name := agentPlayerCheck(); !available {
		return emitErr("player_unavailable", exitPlayerUnavailable, "%s is not installed or not on PATH", name)
	}

	// Both the base and the season/episode check must precede the --detach
	// dispatch: the base decides which provider is asked, and a rejection
	// after the fork would be written into a log the caller has no reason to
	// read while the parent had already reported success.
	applyRefBase(cmd, r)

	var p provider.Provider
	if sel.Type == media.TV {
		p = agentProvider()
		if err := validateSeasonEpisode(p, r, flagSeason, flagEpisode); err != nil {
			return err
		}
	}

	if flagDetach && !flagSupervised {
		return playDetached(cmd, r)
	}

	if p == nil {
		p = agentProvider()
	}
	if err := agentResolveAndPlay(p, sel, flagSeason, flagEpisode); err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "%v", err)
	}

	return emitJSON(map[string]any{
		"status": "finished",
		"title":  r.Title,
	})
}
