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
would fall through to the interactive picker and hang.`,
	Args:          cobra.NoArgs,
	RunE:          playRun,
	SilenceErrors: true,
	SilenceUsage:  true,
}

func init() {
	playCmd.Flags().StringVar(&flagRef, "ref", "", "Ref from lobster find (required)")
	playCmd.Flags().IntVar(&flagSeason, "season", 0, "Season number (required for TV)")
	playCmd.Flags().IntVar(&flagEpisode, "episode", 0, "Episode number (required for TV)")
	playCmd.Flags().BoolVar(&flagDetach, "detach", false, "Start playback in the background and return immediately")
	playCmd.Flags().BoolVar(&flagSupervised, "supervised", false, "Internal: marks the background supervisor process")
	_ = playCmd.Flags().MarkHidden("supervised")
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

	if flagDetach && !flagSupervised {
		return playDetached(cmd, r)
	}

	// Honor the ref's originating base unless the caller explicitly passed
	// --base on this invocation. An ID found under `--base yts` is meaningless
	// under another base (playRef doc comment, cmd/ref.go) — without this, a
	// `find --base yts` result would silently resolve against whatever base is
	// currently configured, losing the ID's exact-match bonus and letting a
	// different provider's copy be served. An explicit --base is a deliberate
	// override and must win.
	if r.Base != "" && cfg != nil && !cmd.Flags().Changed("base") {
		cfg.Base = r.Base
	}

	var p provider.Provider = agentProvider()
	if err := agentResolveAndPlay(p, sel, flagSeason, flagEpisode); err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "%v", err)
	}

	return emitJSON(map[string]any{
		"status": "finished",
		"title":  r.Title,
	})
}
