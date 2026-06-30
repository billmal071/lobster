package cmd

import (
	"errors"
	"fmt"
	"os"

	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/playlist"
	"lobster/internal/provider"
	"lobster/internal/ui"
)

// maxSurfFailures caps consecutive dead channels before surf gives up, so a
// fully-unreachable network can't loop forever.
const maxSurfFailures = 12

// errSurfBackToList signals the user chose "Back to channel list"; the caller
// (searchRun) reopens the browser.
var errSurfBackToList = errors.New("surf: back to channel list")

type surfAction int

const (
	surfNext surfAction = iota
	surfPrev
	surfBack
	surfQuit
)

// playLiveSurf surfs a Live TV category lineup starting at startIdx. On mpv it
// auto-advances past channels that fail to load (up to maxSurfFailures in a
// row); on any player it shows a Next/Previous/Back/Quit menu after a channel
// plays. Returns errSurfBackToList when the user picks "Back to channel list",
// nil on quit or when the failure cap is hit.
func playLiveSurf(p provider.StreamProvider, lineup []media.SearchResult, startIdx int) error {
	if len(lineup) == 0 {
		return nil
	}
	pl := player.New(cfg.Player)
	if !pl.Available() {
		return player.NotFoundError(cfg.Player)
	}
	autoSkip := pl.Name() == "mpv"
	if !autoSkip {
		fmt.Fprintf(os.Stderr, "Auto-skip of dead channels needs mpv; on %s use the menu to change channels.\n", pl.Name())
	}

	i := startIdx
	failCount := 0
	for {
		ch := lineup[i]
		res, perr := surfPlayOne(pl, p, ch)
		switch playlist.DecideSurf(res.Position, perr, autoSkip) {
		case playlist.SurfAdvance:
			failCount++
			if failCount >= maxSurfFailures {
				fmt.Fprintf(os.Stderr, "No playable channel found after %d tries.\n", failCount)
				return nil
			}
			fmt.Fprintf(os.Stderr, "%s unavailable, trying next...\n", ch.Title)
			i = playlist.NextIndex(i, len(lineup))
		default: // playlist.SurfMenu
			failCount = 0
			action, err := surfMenu(ch.Title)
			if err != nil {
				return nil // menu error (e.g. fzf ctrl-c) -> quit
			}
			switch action {
			case surfNext:
				i = playlist.NextIndex(i, len(lineup))
			case surfPrev:
				i = playlist.PrevIndex(i, len(lineup))
			case surfBack:
				return errSurfBackToList
			case surfQuit:
				return nil
			}
		}
	}
}

// surfPlayOne resolves and plays one channel. A resolve error is returned as a
// load failure (zero result + error) so it advances like a dead stream on mpv.
func surfPlayOne(pl player.Player, p provider.StreamProvider, ch media.SearchResult) (player.PlayResult, error) {
	stream, err := p.Watch(ch.ID, "", "LiveTV", cfg.Quality)
	if err != nil {
		return player.PlayResult{}, err
	}
	return pl.Play(stream, ch.Title, 0, nil)
}

// surfMenu shows the post-play channel menu and returns the chosen action.
func surfMenu(title string) (surfAction, error) {
	labels := []string{"Next channel", "Previous channel", "Back to channel list", "Quit"}
	idx, err := ui.Select("Now: "+title, labels)
	if err != nil {
		return surfQuit, err
	}
	return []surfAction{surfNext, surfPrev, surfBack, surfQuit}[idx], nil
}
