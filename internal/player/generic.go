package player

import (
	"fmt"
	"os"
	"os/exec"

	"lobster/internal/media"
)

// Generic implements the Player interface for players like iina and celluloid
// that accept mpv-compatible arguments.
type Generic struct {
	name string
}

func (g *Generic) Name() string { return g.name }

func (g *Generic) Available() bool {
	_, err := exec.LookPath(g.name)
	return err == nil
}

// Play launches the generic player. Position tracking is not supported.
func (g *Generic) Play(stream *media.Stream, title string, startPos float64, subFile string) (float64, error) {
	args := []string{stream.URL}

	// Both iina and celluloid accept mpv-style flags
	args = append(args, "--force-media-title="+title)

	if startPos > 0 {
		args = append(args, fmt.Sprintf("--start=+%.0f", startPos))
	}

	if subFile != "" {
		args = append(args, "--sub-file="+subFile)
	}

	cmd := exec.Command(g.name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return 0, nil
		}
		return 0, fmt.Errorf("running %s: %w", g.name, err)
	}

	return 0, nil
}
