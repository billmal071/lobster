package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"lobster/internal/ui"
)

// promptNumber asks for one number. A package var so tests can answer
// without fzf.
var promptNumber = ui.Input

// promptInteractive reports whether the user can be asked a question: not in
// --json mode and with a terminal on stdin.
var promptInteractive = func() bool {
	return !flagJSON && term.IsTerminal(int(os.Stdin.Fd()))
}

// askSeasonEpisode fills in whichever of season and episode is unset (0) for a
// TV title no provider could list. Without a prompt it returns a "season and
// episode are required" refusal, rather than letting a TV id reach a provider
// as a movie.
func askSeasonEpisode(title string, season, episode int) (int, int, error) {
	if season > 0 && episode > 0 {
		return season, episode, nil
	}
	if !promptInteractive() {
		return 0, 0, fmt.Errorf("%s: no provider could list seasons, so season and episode are required (pass --season and --episode)", title)
	}
	var err error
	if season == 0 {
		if season, err = askPositive("Season"); err != nil {
			return 0, 0, err
		}
	}
	if episode == 0 {
		if episode, err = askPositive("Episode"); err != nil {
			return 0, 0, err
		}
	}
	return season, episode, nil
}

func askPositive(label string) (int, error) {
	for {
		in, err := promptNumber(label)
		if err != nil {
			return 0, err
		}
		n, perr := strconv.Atoi(strings.TrimSpace(in))
		if perr != nil || n < 1 {
			fmt.Fprintf(os.Stderr, "%s must be a positive number.\n", label)
			continue
		}
		return n, nil
	}
}
