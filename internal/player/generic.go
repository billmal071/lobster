package player

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

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
func (g *Generic) Play(stream *media.Stream, title string, startPos float64, subFiles []string) (float64, error) {
	args := []string{stream.URL}

	// Both iina and celluloid accept mpv-style flags
	args = append(args, "--force-media-title="+title)

	var headerFields []string
	var lavfHeaders strings.Builder
	if stream.Referer != "" {
		headerFields = append(headerFields, "Referer: "+stream.Referer)
		lavfHeaders.WriteString("Referer: " + stream.Referer + "\r\n")
	}
	if stream.UserAgent != "" {
		headerFields = append(headerFields, "User-Agent: "+stream.UserAgent)
		lavfHeaders.WriteString("User-Agent: " + stream.UserAgent + "\r\n")
	}
	if len(headerFields) > 0 {
		args = append(args, "--http-header-fields="+strings.Join(headerFields, ","))
		args = append(args, "--demuxer-lavf-o=headers="+lavfHeaders.String())
		args = append(args, "--tls-verify=no")
	}

	if startPos > 0 {
		args = append(args, fmt.Sprintf("--start=+%.0f", startPos))
	}

	for _, sf := range subFiles {
		args = append(args, "--sub-file="+sf)
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
