package cmd

import (
	"strings"

	"github.com/spf13/cobra"

	"lobster/internal/media"
)

var (
	flagFindType  string
	flagFindLimit int
)

// agentSearch is the search entry point, as a package var so tests can supply
// fixed results instead of reaching the network.
var agentSearch = gatherSearchResults

// agentProvider builds the primary provider. A package var so tests can supply
// a stub instead of one that reaches the network.
var agentProvider = newProvider

var findCmd = &cobra.Command{
	Use:   "find <query>",
	Short: "Search for a movie or TV show and print JSON (no prompts)",
	Long: `Search and print matching titles as JSON on stdout.

Unlike the interactive commands, find never opens fzf and never waits for
input, so it is safe to call from a script or an agent. Each result carries an
opaque "ref" which is the handle to pass to "lobster play --ref".`,
	Args:          cobra.MinimumNArgs(1),
	RunE:          findRun,
	SilenceErrors: true,
	SilenceUsage:  true,
}

func findRun(cmd *cobra.Command, args []string) error {
	query := strings.Join(args, " ")

	p := agentProvider()
	results, err := agentSearch(p, fallbackSearchProviders(p), query)
	if err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "search failed: %v", err)
	}

	if flagFindType != "" {
		want := media.Movie
		if flagFindType == "tv" {
			want = media.TV
		}
		filtered := results[:0:0]
		for _, r := range results {
			if r.Type == want {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}

	if len(results) == 0 {
		return emitErr("no_results", exitNoResults, "nothing matched %q", query)
	}

	if flagFindLimit > 0 && len(results) > flagFindLimit {
		results = results[:flagFindLimit]
	}

	base := ""
	if cfg != nil {
		base = cfg.Base
	}

	out := make([]map[string]any, 0, len(results))
	for i, r := range results {
		ref, err := encodeRef(playRef{
			ID:    r.ID,
			Title: r.Title,
			Year:  r.Year,
			Type:  r.Type.String(),
			Base:  base,
		})
		if err != nil {
			return emitErr("internal", 1, "encoding ref: %v", err)
		}
		out = append(out, map[string]any{
			"idx":   i,
			"ref":   ref,
			"title": r.Title,
			"year":  r.Year,
			"type":  r.Type.String(),
		})
	}
	return emitJSON(map[string]any{"results": out})
}

func init() {
	findCmd.Flags().StringVar(&flagFindType, "type", "", "Filter results: movie | tv")
	findCmd.Flags().IntVar(&flagFindLimit, "limit", 0, "Maximum results to print (0 = no limit)")
}
