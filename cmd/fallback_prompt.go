package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/provider"
	"lobster/internal/resolver"
	"lobster/internal/ui"
)

// promptNumber asks for one number. A package var so tests can answer
// without fzf.
var promptNumber = ui.Input

// stdinIsTerminal is the one place the terminal is probed, so tests can drive
// promptInteractive's real gating (--json, then the TTY) without replacing it.
var stdinIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// promptInteractive reports whether the user can be asked a question: not in
// --json mode and with a terminal on stdin.
func promptInteractive() bool {
	return !flagJSON && stdinIsTerminal()
}

// askSeasonEpisode fills in what is missing for a TV title no provider could
// list. An episode of 0 is missing; a season of 0 is missing only alongside it,
// because season 0 with an episode is a Specials request (TMDB season 0) and is
// left alone. So both 0 asks for both, season N with episode 0 asks only for
// the episode, and anything with an episode is returned as given.
//
// Where nobody can be asked (--json, no terminal) it refuses with an error
// that wraps resolver.ErrSeasonEpisodeRequired and says how to run the title
// from a script, rather than let the TV id be requested as a movie.
func askSeasonEpisode(p provider.Provider, sel media.SearchResult, season, episode int) (int, int, error) {
	if episode > 0 {
		return season, episode, nil
	}
	if !promptInteractive() {
		return 0, 0, seasonEpisodeRefusal{fmt.Sprintf("%q is a TV title and no season or episode was chosen: run without --json in a terminal, or use `lobster play --ref %s --season N --episode M`",
			sel.Title, refFor(p, sel))}
	}
	var err error
	if season == 0 {
		if season, err = askPositive("Season"); err != nil {
			return 0, 0, err
		}
	}
	if episode, err = askPositive("Episode"); err != nil {
		return 0, 0, err
	}
	return season, episode, nil
}

// seasonEpisodeRefusal is the readable refusal; it unwraps to
// resolver.ErrSeasonEpisodeRequired so callers can tell it from a provider failure.
type seasonEpisodeRefusal struct{ msg string }

func (e seasonEpisodeRefusal) Error() string { return e.msg }
func (e seasonEpisodeRefusal) Unwrap() error { return resolver.ErrSeasonEpisodeRequired }

// refFor encodes the ref `lobster find` would have printed for sel, or a
// placeholder when it cannot be built.
func refFor(p provider.Provider, sel media.SearchResult) string {
	configured := ""
	if cfg != nil {
		configured = config.NormalizeBase(cfg.Base)
	}
	ref, err := encodeRef(playRef{
		ID:    sel.ID,
		Title: sel.Title,
		Year:  sel.Year,
		Type:  sel.Type.String(),
		Base:  refBaseFor(sel, searchProviderBase(p), configured),
	})
	if err != nil {
		return "<ref from lobster find>"
	}
	return ref
}

// askPositive asks until it gets a whole number of at least 1. A cancelled
// prompt (ui.Input errors on empty input, Esc and Ctrl-C) returns that error.
func askPositive(label string) (int, error) {
	for {
		in, err := promptNumber(label)
		if err != nil {
			return 0, err
		}
		n, perr := strconv.Atoi(strings.TrimSpace(in))
		if perr != nil || n < 1 {
			fmt.Fprintf(os.Stderr, "%s must be a whole number of 1 or more.\n", label)
			continue
		}
		return n, nil
	}
}
