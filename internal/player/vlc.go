package player

import (
	"fmt"
	"os"
	"os/exec"

	"lobster/internal/media"
)

// VLC implements the Player interface for VLC media player.
type VLC struct{}

func (v *VLC) Name() string { return "vlc" }

func (v *VLC) Available() bool {
	_, err := exec.LookPath("vlc")
	return err == nil
}

// Play launches VLC. VLC doesn't have IPC position tracking like mpv,
// so we return 0 for position.
func (v *VLC) Play(stream *media.Stream, title string, startPos float64, subFile string) (float64, error) {
	args := []string{
		stream.URL,
		"--meta-title", title,
		"--play-and-exit",
	}

	if startPos > 0 {
		args = append(args, fmt.Sprintf("--start-time=%.0f", startPos))
	}

	if subFile != "" {
		args = append(args, "--sub-file", subFile)
	}

	cmd := exec.Command("vlc", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			_ = exitErr // VLC exits non-zero on user close
			return 0, nil
		}
		return 0, fmt.Errorf("running vlc: %w", err)
	}

	return 0, nil
}
