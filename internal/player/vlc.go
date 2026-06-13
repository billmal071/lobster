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
	bin := vlcBinaryName()
	_, err := exec.LookPath(bin)
	if err != nil {
		// vlcBinaryName may return a full path on Windows
		_, err = os.Stat(bin)
	}
	return err == nil
}

// Play launches VLC. VLC doesn't have IPC position tracking like mpv,
// so we return zero position/duration.
func (v *VLC) Play(stream *media.Stream, title string, startPos float64, subFiles []string) (PlayResult, error) {
	args := []string{
		stream.URL,
		"--meta-title", title,
		"--play-and-exit",
	}

	if stream.Referer != "" {
		args = append(args, "--http-referrer", stream.Referer)
	}

	if startPos > 0 {
		args = append(args, fmt.Sprintf("--start-time=%.0f", startPos))
	}

	if len(subFiles) > 0 {
		args = append(args, "--sub-file", subFiles[0])
	}

	cmd := exec.Command(vlcBinaryName(), args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			_ = exitErr // VLC exits non-zero on user close
			return PlayResult{}, nil
		}
		return PlayResult{}, fmt.Errorf("running vlc: %w", err)
	}

	return PlayResult{}, nil
}
