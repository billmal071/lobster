package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"lobster/internal/history"
	"lobster/internal/media"
	"lobster/internal/ui"
)

// historyCheckpoint returns a player checkpoint callback that persists the
// given watch's position to history mid-playback, so a hard shutdown loses at
// most one checkpoint interval of resume position. It writes through
// history.Save — the same update-in-place routine as the exit-time save — so
// a checkpoint row and a final row are structurally identical, and the
// exit-time save (which runs strictly after the last checkpoint; the player
// guarantees no callback after Play returns) simply overwrites it. Zero
// positions are skipped: writing 0 would clobber a real resume position from
// an earlier watch of the same title.
func historyCheckpoint(id, title string, mediaType media.MediaType, season, episode int) func(position, duration float64) {
	return func(position, duration float64) {
		if position <= 0 {
			return
		}
		entry := media.HistoryEntry{
			ID:       id,
			Title:    title,
			Type:     mediaType,
			Season:   season,
			Episode:  episode,
			Position: position,
			Duration: duration,
		}
		if err := history.Save(entry); err != nil {
			debugf("checkpoint history save failed: %v", err)
		}
	}
}

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
	p := newProvider()

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
