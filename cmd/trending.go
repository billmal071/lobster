package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"lobster/internal/catalog"
	"lobster/internal/media"
	"lobster/internal/provider"
)

var trendingCmd = &cobra.Command{
	Use:   "trending [movies|tv]",
	Short: "Browse trending content",
	Args:  cobra.MaximumNArgs(1),
	RunE:  trendingRun,
}

func trendingRun(cmd *cobra.Command, args []string) error {
	mediaType := parseMediaTypeArg(args)

	p := agentProvider()
	results, err := listResults(p, mediaType, false)
	if err != nil {
		return fmt.Errorf("getting trending: %w", err)
	}

	if len(results) == 0 {
		fmt.Println("No trending content found.")
		return nil
	}

	items := make([]string, len(results))
	for i, r := range results {
		items[i] = provider.FormatDisplayTitle(r)
	}

	idx, err := selectItem("Trending", items)
	if err != nil {
		return err
	}

	return playSelected(p, resolveCatalogueRow(p, results[idx]), 0, 0)
}

var recentCmd = &cobra.Command{
	Use:   "recent [movies|tv]",
	Short: "Browse recently added content",
	Args:  cobra.MaximumNArgs(1),
	RunE:  recentRun,
}

func recentRun(cmd *cobra.Command, args []string) error {
	mediaType := parseMediaTypeArg(args)

	p := agentProvider()
	results, err := listResults(p, mediaType, true)
	if err != nil {
		return fmt.Errorf("getting recent: %w", err)
	}

	if len(results) == 0 {
		fmt.Println("No recently added content found.")
		return nil
	}

	items := make([]string, len(results))
	for i, r := range results {
		items[i] = provider.FormatDisplayTitle(r)
	}

	idx, err := selectItem("Recent", items)
	if err != nil {
		return err
	}

	return playSelected(p, resolveCatalogueRow(p, results[idx]), 0, 0)
}

var playSelected = resolveAndPlay

var newCatalogClient = catalog.New

func resolveCatalogueRow(p provider.Provider, r media.SearchResult) media.SearchResult {
	return catalog.Wrap(p, nil, catalog.General).ResolveRow(r)
}

// listResults reads the trending or recent list from the TMDB catalogue when
// tmdb_api_key is set, and from p otherwise or when the catalogue fails.
func listResults(p provider.Provider, mt media.MediaType, recent bool) ([]media.SearchResult, error) {
	var client *catalog.Client
	if cfg != nil {
		client = newCatalogClient(cfg.TMDBAPIKey)
	}
	cp := catalog.Wrap(p, client, catalog.General)
	var results []media.SearchResult
	var err error
	if recent {
		results, err = cp.Recent(mt)
	} else {
		results, err = cp.Trending(mt)
	}
	if n := cp.TakeNotice(); n != "" {
		warnf("%s", n)
	}
	return results, err
}

func parseMediaTypeArg(args []string) media.MediaType {
	if len(args) == 0 {
		return media.Movie // Default
	}
	switch strings.ToLower(args[0]) {
	case "tv", "shows", "series":
		return media.TV
	default:
		return media.Movie
	}
}
