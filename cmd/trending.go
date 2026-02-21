package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"lobster/internal/media"
	"lobster/internal/provider"
	"lobster/internal/ui"
)

var trendingCmd = &cobra.Command{
	Use:   "trending [movies|tv]",
	Short: "Browse trending content",
	Args:  cobra.MaximumNArgs(1),
	RunE:  trendingRun,
}

func trendingRun(cmd *cobra.Command, args []string) error {
	mediaType := parseMediaTypeArg(args)

	p := provider.NewFlixHQ(cfg.Base)
	results, err := p.Trending(mediaType)
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

	idx, err := ui.Select("Trending", items)
	if err != nil {
		return err
	}

	return resolveAndPlay(p, results[idx], 0, 0)
}

var recentCmd = &cobra.Command{
	Use:   "recent [movies|tv]",
	Short: "Browse recently added content",
	Args:  cobra.MaximumNArgs(1),
	RunE:  recentRun,
}

func recentRun(cmd *cobra.Command, args []string) error {
	mediaType := parseMediaTypeArg(args)

	p := provider.NewFlixHQ(cfg.Base)
	results, err := p.Recent(mediaType)
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

	idx, err := ui.Select("Recent", items)
	if err != nil {
		return err
	}

	return resolveAndPlay(p, results[idx], 0, 0)
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
