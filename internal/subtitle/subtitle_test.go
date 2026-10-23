package subtitle

import (
	"testing"

	"lobster/internal/media"
)

func TestFilter(t *testing.T) {
	subs := []media.Subtitle{
		{Language: "English", Label: "English"},
		{Language: "English", Label: "English - SDH"},
		{Language: "Spanish", Label: "Spanish"},
		{Language: "French", Label: "French"},
	}

	tests := []struct {
		lang     string
		expected int
	}{
		{"english", 2},
		{"spanish", 1},
		{"french", 1},
		{"german", 0},
		{"", 4},
	}

	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			got := Filter(subs, tt.lang)
			if len(got) != tt.expected {
				t.Errorf("Filter(%q) returned %d subs, want %d", tt.lang, len(got), tt.expected)
			}
		})
	}
}

func TestBestMatch(t *testing.T) {
	subs := []media.Subtitle{
		{Language: "English", Label: "English - SDH", URL: "https://example.com/sdh.vtt"},
		{Language: "English", Label: "English", URL: "https://example.com/en.vtt"},
		{Language: "Spanish", Label: "Spanish", URL: "https://example.com/es.vtt"},
	}

	// Should prefer non-SDH English
	best := BestMatch(subs, "english")
	if best == nil {
		t.Fatal("BestMatch returned nil for english")
	}
	if best.Label != "English" {
		t.Errorf("BestMatch preferred %q, want 'English' (non-SDH)", best.Label)
	}

	// Spanish
	best = BestMatch(subs, "spanish")
	if best == nil {
		t.Fatal("BestMatch returned nil for spanish")
	}
	if best.Language != "Spanish" {
		t.Errorf("got language %q, want Spanish", best.Language)
	}

	// No match
	best = BestMatch(subs, "japanese")
	if best != nil {
		t.Error("BestMatch should return nil for unmatched language")
	}
}

func TestTempDir(t *testing.T) {
	tmpDir, err := NewTempDir()
	if err != nil {
		t.Fatalf("NewTempDir() error: %v", err)
	}
	defer tmpDir.Cleanup()

	if tmpDir.path == "" {
		t.Error("temp dir path is empty")
	}
}

// TestNamesEpisodeSeesEveryStatedEpisode covers a filename stating more than
// one episode: the scan stopped at the first marker, so the requested episode
// went unseen whenever it was stated second.
func TestNamesEpisodeSeesEveryStatedEpisode(t *testing.T) {
	const name = "show.s01e01.s01e02.srt"
	if !namesEpisode(name, 1, 2) {
		t.Errorf("namesEpisode(%q, 1, 2) = false; want true -- the name states episode 2 as its second marker", name)
	}
	if !namesEpisode(name, 1, 1) {
		t.Errorf("namesEpisode(%q, 1, 1) = false; want true", name)
	}
	if namesEpisode(name, 1, 3) {
		t.Errorf("namesEpisode(%q, 1, 3) = true; want false", name)
	}
}

// TestIsWrongEpisodeKeepsADoubleEpisodeFile is the companion: a file covering
// episodes 1 and 2 is not the wrong file for either, so a stated number other
// than the requested one only disqualifies it when the requested one is
// absent.
func TestIsWrongEpisodeKeepsADoubleEpisodeFile(t *testing.T) {
	const name = "show.s01e01.s01e02.srt"
	if isWrongEpisode(name, 1, 2) {
		t.Errorf("isWrongEpisode(%q, 1, 2) = true; want false -- the name states episode 2", name)
	}
	if !isWrongEpisode(name, 1, 3) {
		t.Errorf("isWrongEpisode(%q, 1, 3) = false; want true -- it states episodes, none of them 3", name)
	}
}

// TestSeasonEpisodesInIgnoresReleaseMetadata is the guard on reading both
// numbers out of the text instead of assuming the season. Release names are
// dense with digit-x-digit and s-digit runs that mean resolutions, codecs and
// channel layouts, and a loose scan turns those into episode markers -- which
// would reject the very full-season packs the earlier fixes went to keep.
// The first two strings are real SubDL release names.
func TestSeasonEpisodesInIgnoresReleaseMetadata(t *testing.T) {
	names := []string{
		"breaking.bad.s01.2160p.webrip.dts-hd.ma5.1.x264-trolluhd",
		"the office (us) s01-s09 (1080p mixed x265 hevc 10bit aac 5.1 silence qxr",
		"show.1080x720.aac2.0.srt",
		"some.release.aac5.1x264.srt",
		"season 1 complete 720x480 dvdrip.srt",
	}
	for _, name := range names {
		if got := seasonEpisodesIn(name); len(got) != 0 {
			t.Errorf("seasonEpisodesIn(%q) = %v; want none -- codecs and resolutions are not episode markers", name, got)
		}
		if isWrongEpisode(name, 1, 5) {
			t.Errorf("isWrongEpisode(%q, 1, 5) = true; want false -- it states no episode at all", name)
		}
		if namesEpisode(name, 1, 5) {
			t.Errorf("namesEpisode(%q, 1, 5) = true; want false", name)
		}
	}
}

// TestSeasonEpisodesInReadsBothNumbers covers the shapes that must still be
// recognised, including the x form the bounds above narrow.
func TestSeasonEpisodesInReadsBothNumbers(t *testing.T) {
	tests := []struct {
		text string
		want []seasonEpisode
	}{
		{"show.s01e02.srt", []seasonEpisode{{Season: 1, Episode: 2}}},
		{"show.s1e2.srt", []seasonEpisode{{Season: 1, Episode: 2}}},
		{"show - 1x02.srt", []seasonEpisode{{Season: 1, Episode: 2}}},
		{"show.s02e05.srt", []seasonEpisode{{Season: 2, Episode: 5}}},
		{"show.s01e01.s01e02.srt", []seasonEpisode{{Season: 1, Episode: 1}, {Season: 1, Episode: 2}}},
		{"show.s01.complete.srt", nil},
	}
	for _, tt := range tests {
		got := seasonEpisodesIn(tt.text)
		if len(got) != len(tt.want) {
			t.Errorf("seasonEpisodesIn(%q) = %v; want %v", tt.text, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("seasonEpisodesIn(%q)[%d] = %v; want %v", tt.text, i, got[i], tt.want[i])
			}
		}
	}
}

// TestIsWrongEpisodeRejectsAnotherSeason is the finding itself at unit level:
// detection was built from the requested season's patterns, so text naming a
// different season matched nothing and read as text naming no episode -- which
// is the one verdict that gets a file served rather than skipped.
func TestIsWrongEpisodeRejectsAnotherSeason(t *testing.T) {
	const name = "show.s02e05.srt"
	if !isWrongEpisode(name, 1, 5) {
		t.Errorf("isWrongEpisode(%q, 1, 5) = false; want true -- it states season 2", name)
	}
	if namesEpisode(name, 1, 5) {
		t.Errorf("namesEpisode(%q, 1, 5) = true; want false", name)
	}
	if isWrongEpisode(name, 2, 5) {
		t.Errorf("isWrongEpisode(%q, 2, 5) = true; want false", name)
	}
	if !namesEpisode(name, 2, 5) {
		t.Errorf("namesEpisode(%q, 2, 5) = false; want true", name)
	}
}
