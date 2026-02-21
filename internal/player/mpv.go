package player

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lobster/internal/media"
)

// MPV implements the Player interface for mpv.
// Uses exec.Command with explicit args (no shell interpretation)
// and IPC via Unix socket at a randomized temp path.
type MPV struct{}

func (m *MPV) Name() string { return "mpv" }

func (m *MPV) Available() bool {
	_, err := exec.LookPath("mpv")
	return err == nil
}

// Play launches mpv with the given stream and returns the final playback position.
func (m *MPV) Play(stream *media.Stream, title string, startPos float64, subFile string) (float64, error) {
	// Create randomized IPC socket path (prevents symlink attacks)
	socketDir, err := os.MkdirTemp("", "lobster-mpv-*")
	if err != nil {
		return 0, fmt.Errorf("creating temp dir for mpv socket: %w", err)
	}
	defer os.RemoveAll(socketDir)

	socketPath := filepath.Join(socketDir, "socket")

	// Build args as explicit slice — each arg is separate, no shell interpretation
	args := []string{
		stream.URL,
		"--force-media-title=" + title,
		"--input-ipc-server=" + socketPath,
		"--really-quiet",
	}

	if startPos > 0 {
		args = append(args, fmt.Sprintf("--start=+%.0f", startPos))
	}

	if subFile != "" {
		args = append(args, "--sub-file="+subFile)
	} else {
		// If we have embedded subtitles from the stream, add them
		for _, sub := range stream.Subtitles {
			if sub.URL != "" {
				args = append(args, "--sub-file="+sub.URL)
				break // mpv handles multiple sub tracks, but add primary only
			}
		}
	}

	cmd := exec.Command("mpv", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("starting mpv: %w", err)
	}

	// Wait briefly for IPC socket to become available
	var lastPos float64
	go func() {
		lastPos = m.trackPosition(socketPath)
	}()

	if err := cmd.Wait(); err != nil {
		// mpv returns non-zero on user quit, which is normal
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 4 {
			return lastPos, nil
		}
	}

	return lastPos, nil
}

// trackPosition polls mpv's IPC socket for the current playback position.
func (m *MPV) trackPosition(socketPath string) float64 {
	var lastPos float64

	// Wait for socket to appear
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return 0
	}
	defer conn.Close()

	scanner := bufio.NewScanner(conn)

	// Start observing time-pos property
	cmd := map[string]interface{}{
		"command":    []interface{}{"observe_property", 1, "time-pos"},
		"request_id": 100,
	}
	data, _ := json.Marshal(cmd)
	data = append(data, '\n')
	conn.Write(data)

	for scanner.Scan() {
		line := scanner.Text()
		var event struct {
			Event string  `json:"event"`
			Name  string  `json:"name"`
			Data  float64 `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Name == "time-pos" && event.Data > 0 {
			lastPos = event.Data
		}
	}

	return lastPos
}

// formatDuration formats seconds as HH:MM:SS.
func formatDuration(seconds float64) string {
	s := int(seconds)
	h := s / 3600
	m := (s % 3600) / 60
	sec := s % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%d:%02d", m, sec)
}

// parseDuration parses HH:MM:SS or MM:SS into seconds.
func parseDuration(s string) float64 {
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 3:
		h, _ := strconv.ParseFloat(parts[0], 64)
		m, _ := strconv.ParseFloat(parts[1], 64)
		sec, _ := strconv.ParseFloat(parts[2], 64)
		return h*3600 + m*60 + sec
	case 2:
		m, _ := strconv.ParseFloat(parts[0], 64)
		sec, _ := strconv.ParseFloat(parts[1], 64)
		return m*60 + sec
	default:
		v, _ := strconv.ParseFloat(s, 64)
		return v
	}
}
