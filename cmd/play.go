package cmd

import (
	"github.com/spf13/cobra"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// agentResolveAndPlay is the playback entry point, as a package var so tests
// can observe what selection reaches it without launching a player.
var agentResolveAndPlay = resolveAndPlay

var playCmd = &cobra.Command{
	Use:   "play --ref <REF>",
	Short: "Play a ref returned by lobster find (no prompts)",
	Long: `Play the exact title a ref identifies, without prompting.

The ref pins the selection — title, year, type and originating base. It does
not pin the stream: resolution still runs through the fallback chain at play
time, so if the original source is down another provider's copy may be served.

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

	var p provider.Provider = agentProvider()
	if err := agentResolveAndPlay(p, sel, flagSeason, flagEpisode); err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "%v", err)
	}

	return emitJSON(map[string]any{
		"status": "finished",
		"title":  r.Title,
	})
}
