package cmd

import (
	"fmt"
	"strings"

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
			// No flagContinue override here: resuming is the registered
			// default (root.go), and forcing it would override the one
			// caller who said --continue=false, i.e. "replay this from the
			// start" — a coherent request even when picking off the history
			// list.
			return resolveAndPlay(p, r, selected.Season, selected.Episode)
		}
	}

	// The saved ID belongs to whatever source was default when the watch was
	// recorded, so after a change of default it is absent from these results
	// by construction. Fall back to identity by title and type before making
	// the user pick the title they have already picked.
	if r, ok := resultForHistoryEntry(results, selected); ok {
		debugf("history: %q not found by ID; matched %s by title and type", selected.Title, r.ID)
		return resolveAndPlay(p, r, selected.Season, selected.Episode)
	}

	// Nothing conclusive: let the user pick from search results.
	return playFlow(p, selected.Title)
}

// titleKey is the comparison key for the identity fallbacks below: case and
// surrounding whitespace differ between catalogues for the same work, nothing
// else is normalised away. Deliberately strict — every character it does not
// fold is one more thing that has to agree before two rows are called the
// same film, and the cost of a miss is a watch that starts from zero while
// the cost of a false hit is a watch that starts in the middle of a film the
// user has not seen.
func titleKey(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// resumePosition returns the stored position for this watch, and whether one
// was found.
//
// The exact ID wins outright. But IDs are provider-specific — "movie/
// free-the-matrix-hd-75043" from a scraper, "yts/1745" from YTS — so every
// row written under a previous default source misses it, and a change of
// default silently loses every watch position on disk. That has now happened
// three times.
//
// So when the ID misses, fall back to identity by (title, type, season,
// episode). This is a read, and the conservative direction is to decline:
// the fallback requires exactly ONE row to match, because two works can share
// a title (Dune 1984 and Dune 2021) and a history row carries no year to tell
// them apart — media.HistoryEntry and the 7-column TSV it is written to hold
// id, title, type, season, episode, position and duration, and nothing else.
// A year condition would therefore be unevaluable for precisely the legacy
// rows this exists to rescue; uniqueness is the check that can actually be
// made against them, and it refuses in the same ambiguous case a year would
// have caught. When it declines, playback starts from the beginning, which is
// what happened before this fallback existed.
func resumePosition(entries []media.HistoryEntry, sel media.SearchResult, season, episode int) (float64, bool) {
	for _, e := range entries {
		if e.ID == sel.ID && e.Season == season && e.Episode == episode {
			return e.Position, true
		}
	}
	var match media.HistoryEntry
	found := 0
	key := titleKey(sel.Title)
	if key == "" {
		return 0, false
	}
	for _, e := range entries {
		if titleKey(e.Title) == key && e.Type == sel.Type &&
			e.Season == season && e.Episode == episode {
			match = e
			found++
		}
	}
	if found != 1 {
		return 0, false
	}
	return match.Position, true
}

// resultForHistoryEntry finds the row of a fresh search that is the work this
// history entry records, for the same reason and under the same rule as
// resumePosition: the saved ID belongs to whichever source was default when
// the watch happened, so after a change of default it cannot appear in the
// results at all and `lobster history` re-prompts for the title the user has
// just picked.
//
// Season and episode play no part here: a search returns one row per work,
// and the episode is selected downstream from the entry's own Season and
// Episode. Ambiguity is refused exactly as it is there — with two same-titled
// films in the results, the honest answer is the picker the caller falls back
// to.
func resultForHistoryEntry(results []media.SearchResult, e media.HistoryEntry) (media.SearchResult, bool) {
	key := titleKey(e.Title)
	if key == "" {
		return media.SearchResult{}, false
	}
	var match media.SearchResult
	found := 0
	for _, r := range results {
		if titleKey(r.Title) == key && r.Type == e.Type {
			match = r
			found++
		}
	}
	if found != 1 {
		return media.SearchResult{}, false
	}
	return match, true
}
