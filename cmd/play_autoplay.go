package cmd

import (
	"lobster/internal/player"
	"lobster/internal/playlist"
)

var flagAutoplay bool

const autoplayEndSlackSeconds = 10

var playEpisode = playCurrentEpisode

// An unmeasured watch is never finished: an early quit looks the same.
func playFinished(res player.PlayResult) bool {
	if res.PositionUnknown || res.Duration <= 0 || res.Position <= 0 {
		return false
	}
	slack := min(autoplayEndSlackSeconds, res.Duration*finishedTailFraction)
	return res.Duration-res.Position <= slack
}

func sessionFinished(sess *playlist.Session) bool {
	return playFinished(player.PlayResult{
		Position:        sess.LastPosition,
		Duration:        sess.LastDuration,
		PositionUnknown: sess.LastPositionUnknown,
	})
}
