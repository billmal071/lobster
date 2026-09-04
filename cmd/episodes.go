package cmd

import (
	"github.com/spf13/cobra"

	"lobster/internal/media"
)

var (
	flagRef     string
	flagSeason  int
	flagEpisode int
)

var episodesCmd = &cobra.Command{
	Use:   "episodes --ref <REF>",
	Short: "List seasons and episodes for a TV ref as JSON (no prompts)",
	Long: `List the episodes of a show without prompting.

Takes a ref emitted by "lobster find". With --season it lists that season's
episodes; without it, the first season's.`,
	Args: cobra.NoArgs,
	RunE: episodesRun,
}

func init() {
	markAgentCommand(episodesCmd)
	episodesCmd.Flags().StringVar(&flagRef, "ref", "", "Ref from lobster find (required)")
	episodesCmd.Flags().IntVar(&flagSeason, "season", 0, "Season number (default: first)")
}

func episodesRun(cmd *cobra.Command, args []string) error {
	r, err := decodeRef(flagRef)
	if err != nil {
		return emitErr("bad_ref", 1, "%v", err)
	}
	if r.Type != media.TV.String() {
		return emitErr("not_a_series", 1, "%q is a %s, which has no episodes", r.Title, r.Type)
	}

	p := agentProvider()
	seasons, err := p.GetSeasons(r.ID)
	if err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "getting seasons: %v", err)
	}
	if len(seasons) == 0 {
		return emitErr("no_results", exitNoResults, "no seasons found for %q", r.Title)
	}

	sel := seasons[0]
	if flagSeason > 0 {
		found := false
		for _, s := range seasons {
			if s.Number == flagSeason {
				sel, found = s, true
				break
			}
		}
		if !found {
			return emitErr("no_results", exitNoResults, "season %d not found for %q", flagSeason, r.Title)
		}
	}

	eps, err := p.GetEpisodes(r.ID, sel.ID)
	if err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "getting episodes: %v", err)
	}

	seasonNums := make([]int, 0, len(seasons))
	for _, s := range seasons {
		seasonNums = append(seasonNums, s.Number)
	}
	out := make([]map[string]any, 0, len(eps))
	for _, e := range eps {
		out = append(out, map[string]any{"number": e.Number, "title": e.Title})
	}

	return emitJSON(map[string]any{
		"title":    r.Title,
		"seasons":  seasonNums,
		"season":   sel.Number,
		"episodes": out,
	})
}
