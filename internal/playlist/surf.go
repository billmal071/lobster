package playlist

// SurfDecision is what the Live TV surf loop should do after a play attempt.
type SurfDecision int

const (
	SurfAdvance SurfDecision = iota // stream failed to load -> try the next channel
	SurfMenu                        // channel played (or non-mpv) -> show the menu
)

// DecideSurf maps a play attempt's outcome to the next action. autoSkip is true
// only when the active player can report load failures (mpv): mpv returns a
// non-nil error with position 0 when a stream never loaded, vs a nil error with
// position > 0 when the user watched then quit. When autoSkip is false (VLC and
// other players give no usable signal), every outcome goes to the menu.
func DecideSurf(position float64, err error, autoSkip bool) SurfDecision {
	if autoSkip && err != nil && position == 0 {
		return SurfAdvance
	}
	return SurfMenu
}

// NextIndex returns the next index in a lineup of length n (n > 0), wrapping.
func NextIndex(i, n int) int { return (i + 1) % n }

// PrevIndex returns the previous index in a lineup of length n (n > 0), wrapping.
func PrevIndex(i, n int) int { return (i - 1 + n) % n }
