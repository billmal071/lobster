package resolver

import (
	"testing"

	"lobster/internal/media"
)

// TestTVWithoutSeasonEpisodeDoesNotAskForMoviePath checks that a TV request with season and episode both 0 is never resolved as a movie: a StreamProvider is handed an empty episode ID only for movies.
func TestTVWithoutSeasonEpisodeDoesNotAskForMoviePath(t *testing.T) {
	r := &episodeIDRecorder{fakeSP: &fakeSP{
		searchResults: []media.SearchResult{{ID: "tv/31910", Title: "Naruto", Type: media.TV}},
		stream:        &media.Stream{URL: "https://cdn/x.m3u8"},
	}}
	_, _, err := resolveWithProvider(r, Request{Title: "Naruto", MediaType: media.TV, Quality: "1080"}, func(string, ...any) {})
	t.Logf("season=0 episode=0 -> episodeID=%q err=%v", r.got, err)
	if r.got == "" && err == nil {
		t.Fatalf("TV request with season/episode 0 reached Watch with empty episode ID (movie path)")
	}
	r.got = ""
	_, _, _ = resolveWithProvider(r, Request{Title: "Naruto", MediaType: media.TV, Season: 1, Episode: 1, Quality: "1080"}, func(string, ...any) {})
	t.Logf("season=1 episode=1 -> episodeID=%q", r.got)
}
