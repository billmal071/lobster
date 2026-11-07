package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"lobster/internal/doctor"
	"lobster/internal/provider"
	"lobster/internal/ui"
)

const (
	doctorDefaultQuery = "The Matrix"
	// Probed against AnimeOnsen, whose catalogue carries neither The Matrix nor Naruto.
	doctorAnimeOnsenQuery = "One Punch Man"
	// Probed against EZTV, which carries TV series only.
	doctorEZTVQuery = "Severance"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor [query]",
	Short: "Check which providers work, and where the others break",
	Long: `Probe every provider and report the stage at which each one fails.

Providers break constantly, and "it doesn't work" hides the difference between
a moved domain (a cheap fix) and a replaced player (a rewrite). This runs each
provider through search, servers and embed resolution and names the stage that
broke, so a repairable one is visible the moment it becomes repairable.

A well-known title is used by default so an empty result means the provider is
broken rather than the catalogue being thin.`,
	Args: cobra.MaximumNArgs(1),
	RunE: doctorRun,
}

func doctorRun(cmd *cobra.Command, args []string) error {
	query := doctorDefaultQuery
	if len(args) > 0 && args[0] != "" {
		query = args[0]
	}

	// Only sources newProvider or fallbackProviders can still reach; the
	// retired ones (config.retiredBases) would only ever read FAIL.
	providers := []doctor.Named{
		{Name: "VaPlayer", Provider: provider.NewVaPlayer()},
		{Name: "TBCPL", Provider: provider.NewTBCPL("tbcpl")},
		{Name: "FlixHQWS", Provider: provider.NewFlixHQWS("flixhq.ws")},
		// 1shows.org is the same TBCPL provider on a different base, and the two
		// domains fail independently — worth probing as its own entry.
		{Name: "TBCPL(1shows)", Provider: provider.NewTBCPL("1shows.org")},
		{Name: "YTS", Provider: provider.NewYTS()},
		// Anime catalogues need an anime title, or they report broken for being
		// asked about the wrong film.
		{Name: "AnimeOnsen", Provider: provider.NewAnimeOnsen(), Query: doctorAnimeOnsenQuery},
		{Name: "Nyaa", Provider: provider.NewNyaa(), Query: doctorAnimeOnsenQuery},
		// A TV-only catalogue needs a series title, for the same reason.
		{Name: "EZTV", Provider: provider.NewEZTV(), Query: doctorEZTVQuery},
	}

	stop := ui.StartSpinner(fmt.Sprintf("Probing %d providers with %q...", len(providers), query))
	results := doctor.CheckAll(providers, query)
	stop()

	// Report on stdout so it can be piped or grepped; only the spinner is
	// progress chatter and belongs on stderr.
	fmt.Printf("\nProvider health (query: %q)\n\n", query)
	fmt.Print(doctor.Format(results))

	// Exit non-zero when nothing works, so this is usable as a check.
	for _, r := range results {
		if r.OK {
			return nil
		}
	}
	return fmt.Errorf("no providers are usable")
}
