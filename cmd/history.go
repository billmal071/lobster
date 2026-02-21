package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"lobster/internal/history"
	"lobster/internal/provider"
	"lobster/internal/ui"
)

var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "Resume from watch history",
	RunE:  historyRun,
}

func historyRun(cmd *cobra.Command, args []string) error {
	entries, err := history.Load()
	if err != nil {
		return fmt.Errorf("loading history: %w", err)
	}

	if len(entries) == 0 {
		fmt.Println("No history entries found.")
		return nil
	}

	// Show history in fzf
	items := history.FormatForDisplay(entries)
	idx, err := ui.Select("History", items)
	if err != nil {
		return err
	}

	selected := entries[idx]
	debugf("resuming: %s (ID: %s)", selected.Title, selected.ID)

	// Re-resolve and play from the saved position
	p := provider.NewFlixHQ(cfg.Base)

	// Search for the title to get fresh results
	results, err := p.Search(selected.Title)
	if err != nil {
		return fmt.Errorf("searching for %q: %w", selected.Title, err)
	}

	// Find matching result by ID
	for _, r := range results {
		if r.ID == selected.ID {
			// Override continue flag to resume
			flagContinue = true
			return resolveAndPlay(p, r, selected.Season, selected.Episode)
		}
	}

	// If exact ID not found, let user pick from search results
	return playFlow(p, selected.Title)
}
