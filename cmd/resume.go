package cmd

import "lobster/internal/media"

// A watch within finishedTailSeconds of the end, or past finishedFraction of
// the runtime, counts as finished. mpv stores the file length as the final
// position of a watch that ran to the end, and restoring it opens at EOF.
// The tail is capped at finishedTailFraction of the runtime so a short clip
// stopped early is still resumable.
const (
	finishedTailSeconds  = 60
	finishedTailFraction = 0.1
	finishedFraction     = 0.98
)

// resumePosition returns the position to start a history entry at, or 0 when
// the stored position is at the end of the file. An unknown duration (0)
// trusts the position as-is.
func resumePosition(e media.HistoryEntry) float64 {
	if e.Position <= 0 {
		return 0
	}
	if e.Duration > 0 {
		tail := float64(finishedTailSeconds)
		if t := e.Duration * finishedTailFraction; t < tail {
			tail = t
		}
		if e.Duration-e.Position <= tail || e.Position/e.Duration >= finishedFraction {
			return 0
		}
	}
	return e.Position
}
