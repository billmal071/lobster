package resolver

import (
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
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

// embedRecorder is a Provider that is not a StreamProvider (no Watch), so resolveWithProvider takes the embed path; it records whether GetServers was reached.
type embedRecorder struct {
	asked bool
}

func (e *embedRecorder) Search(string) ([]media.SearchResult, error) {
	return []media.SearchResult{{ID: "tv/1", Title: "X", Type: media.TV}}, nil
}
func (e *embedRecorder) GetDetails(string) (*media.ContentDetail, error)     { return nil, nil }
func (e *embedRecorder) GetSeasons(string) ([]media.Season, error)           { return nil, nil }
func (e *embedRecorder) GetEpisodes(string, string) ([]media.Episode, error) { return nil, nil }
func (e *embedRecorder) GetEmbedURL(string) (string, error)                  { return "", nil }
func (e *embedRecorder) Trending(media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}
func (e *embedRecorder) Recent(media.MediaType) ([]media.SearchResult, error) { return nil, nil }
func (e *embedRecorder) GetServers(string, string) ([]media.Server, error) {
	e.asked = true
	return nil, nil
}

// TestEmbedProviderTVWithoutSeasonEpisodeIsRefused checks that an embed provider is not asked for servers on a TV title with no season or episode.
func TestEmbedProviderTVWithoutSeasonEpisodeIsRefused(t *testing.T) {
	e := &embedRecorder{}
	if _, isStream := any(e).(provider.StreamProvider); isStream {
		t.Fatal("embedRecorder must not be a StreamProvider")
	}
	_, stage, err := resolveWithProvider(e, Request{Title: "X", MediaType: media.TV}, func(string, ...any) {})
	if err == nil || e.asked || stage != "match" {
		t.Fatalf("stage=%q err=%v asked=%v; want match-stage refusal before GetServers", stage, err, e.asked)
	}
}
