package cmd

import (
	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/playlist"
)

var flagAutoplay bool

var playEpisode = playCurrentEpisode

// An unmeasured watch is never finished: an early quit looks the same.
func playFinished(res player.PlayResult) bool {
	if res.PositionUnknown || res.Duration <= 0 || res.Position <= 0 {
		return false
	}
	return resumePosition(media.HistoryEntry{Position: res.Position, Duration: res.Duration}) == 0
}

func sessionFinished(sess *playlist.Session) bool {
	return playFinished(player.PlayResult{
		Position:        sess.LastPosition,
		Duration:        sess.LastDuration,
		PositionUnknown: sess.LastPositionUnknown,
	})
}
