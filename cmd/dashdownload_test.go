package cmd

import (
	"strings"
	"testing"

	"lobster/internal/media"
)

// The download queue refuses a DASH manifest instead of saving it.
//
// This is not a style preference. streamToResult classifies by substring, and
// a .mpd URL contains neither ".m3u8" nor "hls", so it is labelled "http" and
// handed to the plain-file engine — which downloads the manifest itself: an
// 8 kB XML document written under a video filename, and a download the manager
// reports as complete. Nothing downstream can tell, and the user finds out
// when the file will not play.
//
// The fixture is the real thing a provider returns: AnimeOnsen's manifest URL.
func TestStreamToResultCheckedRefusesADASHManifestRatherThanSavingTheXML(t *testing.T) {
	s := &media.Stream{URL: "https://cdn.animeonsen.xyz/video/mp4-dash/cvYyOlmbfFWvJYWG/1/manifest.mpd"}

	// First, the reason: unchecked, this is exactly the silent corruption.
	if got := streamToResult(s); got.StreamType != "http" {
		t.Fatalf("streamToResult(.mpd).StreamType = %q, want \"http\"; the premise of the refusal is that the classifier reads a manifest as a plain file", got.StreamType)
	}

	res, err := streamToResultChecked(s)
	if err == nil {
		t.Fatalf("streamToResultChecked(.mpd) = %+v with nil error; the queue would save the manifest XML under a video filename and call it complete", res)
	}
	for _, want := range []string{"DASH", "-d"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q; a refusal has to say what happened and what does work instead", err, want)
		}
	}
}

// Everything that is not a DASH manifest still goes through.
//
// The guarantee-violating input is the third row: a URL whose *query string*
// carries ".mpd" without the resource being a manifest. A substring test over
// the whole URL would refuse it, turning a download that would have worked
// into an error — the inverse of the bug above, and just as invisible.
func TestStreamToResultCheckedOnlyRefusesURLsWhosePathIsAManifest(t *testing.T) {
	for _, tc := range []struct {
		url    string
		refuse bool
	}{
		{"https://cdn.example.test/a/1/manifest.mpd", true},
		{"https://cdn.example.test/a/1/MANIFEST.MPD", true},
		{"https://cdn.example.test/a/1/manifest.mpd?token=abc", true},
		{"https://cdn.example.test/a/1/manifest.mpd#frag", true},
		{"https://cdn.example.test/video.mp4?referrer=/x/manifest.mpd", false},
		{"https://cdn.example.test/video.mp4?name=show.mpd.mp4", false},
		{"https://cdn.example.test/hls/index.m3u8", false},
		{"https://cdn.example.test/video.mp4", false},
	} {
		_, err := streamToResultChecked(&media.Stream{URL: tc.url})
		refused := err != nil
		if refused != tc.refuse {
			t.Errorf("streamToResultChecked(%q) refused = %v, want %v", tc.url, refused, tc.refuse)
		}
	}
}
