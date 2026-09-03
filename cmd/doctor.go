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
	// Probed against the anime providers, whose catalogues do not carry the
	// default title at all.
	doctorAnimeQuery = "Naruto"
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

	providers := []doctor.Named{
		{Name: "MovieBox", Provider: provider.NewMovieBox()},
		{Name: "VaPlayer", Provider: provider.NewVaPlayer()},
		{Name: "VidNest", Provider: provider.NewVidNest()},
		{Name: "Soap2Day", Provider: provider.NewSoap2Day()},
		{Name: "TBCPL", Provider: provider.NewTBCPL("tbcpl")},
		{Name: "FlixHQ", Provider: provider.NewFlixHQ("flixhq.to")},
		{Name: "FlixHQWS", Provider: provider.NewFlixHQWS("flixhq.ws")},
		{Name: "KimCartoon", Provider: provider.NewKimCartoon("kimcartoon.com.co")},
		// 1shows.org is the same TBCPL provider on a different base, and the two
		// domains fail independently — worth probing as its own entry.
		{Name: "TBCPL(1shows)", Provider: provider.NewTBCPL("1shows.org")},
		// Anime catalogues need an anime title, or they report broken for being
		// asked about the wrong film.
		{Name: "AllAnime", Provider: provider.NewAllAnime(false), Query: doctorAnimeQuery},
		{Name: "AniPub", Provider: provider.NewAniPub(), Query: doctorAnimeQuery},
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
