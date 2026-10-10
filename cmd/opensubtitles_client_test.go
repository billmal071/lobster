package cmd

import (
	"strconv"
	"strings"
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/subtitle"
)

// osResolveCall is one trip through the OpenSubtitles resolve seam. The
// credential values here come from the test's own fake config and are never
// sent anywhere: the seam is stubbed, so no request leaves the process.
type osResolveCall struct {
	apiKey   string
	username string
	password string
	fileID   int
}

// The three config fields the download path reads are read in one expression,
// and nothing above this test ever checked that the right three arrive in the
// right order — a swapped username and password would log in as nobody and
// degrade silently to the anonymous quota, which is exactly the failure this
// branch exists to remove.
//
// It also pins that the path resolves through the shared seam rather than
// building its own client: the reuse that makes a season log in once lives
// behind this call (subtitle.OpenSubtitlesFor), and a call site that
// constructed a client itself would bypass it.
func TestResolveAndDownloadSubPassesOpenSubtitlesCredentials(t *testing.T) {
	prevCfg := cfg
	t.Cleanup(func() { cfg = prevCfg })
	cfg = &config.Config{
		OSAPIKey:   "stub-api-key",
		OSUsername: "stub-user",
		OSPassword: "stub-pass",
	}

	prevResolve := osResolveDownloadURL
	t.Cleanup(func() { osResolveDownloadURL = prevResolve })
	var calls []osResolveCall
	osResolveDownloadURL = func(apiKey, username, password string, fileID int) (string, error) {
		calls = append(calls, osResolveCall{apiKey, username, password, fileID})
		// Not HTTPS, so the staging download rejects it before any
		// request is made. Reaching this at all proves the resolve step
		// ran and handed its answer on.
		return "http://cdn.invalid/sub.srt", nil
	}

	// Keep staging out of the real home directory.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	tmpDir, err := subtitle.NewTempDir()
	if err != nil {
		t.Fatalf("creating staging dir: %v", err)
	}
	t.Cleanup(tmpDir.Cleanup)

	// Two episodes of a season, each resolving one OpenSubtitles file.
	for i, fileID := range []int{11, 22} {
		sub := media.Subtitle{Language: "en", URL: "opensubtitles:" + strconv.Itoa(fileID)}
		_, err := resolveAndDownloadSub(tmpDir, sub, 1, i+1)
		if err == nil || !strings.Contains(err.Error(), "only HTTPS URLs are allowed") {
			t.Fatalf("episode %d: err = %v, want the staging download to reject the stub link", i+1, err)
		}
	}

	want := []osResolveCall{
		{"stub-api-key", "stub-user", "stub-pass", 11},
		{"stub-api-key", "stub-user", "stub-pass", 22},
	}
	if len(calls) != len(want) {
		t.Fatalf("resolve calls = %d, want %d", len(calls), len(want))
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("resolve call %d = %+v, want %+v", i, calls[i], want[i])
		}
	}
}
