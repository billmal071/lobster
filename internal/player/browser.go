package player

import (
	"errors"
	"net/url"
	"os/exec"
	"runtime"
)

// OpenBrowser hands a page to the user's browser. It is used for sources whose
// own JavaScript player is required to render the live stream.
func OpenBrowser(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return errors.New("invalid browser playback URL")
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-a", "Google Chrome", u.String())
		if err := cmd.Run(); err == nil {
			return nil
		}
		return exec.Command("open", u.String()).Run()
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u.String())
	case "linux":
		cmd = exec.Command("xdg-open", u.String())
	default:
		return errors.New("opening browser playback is not supported on this platform")
	}
	return cmd.Run()
}
