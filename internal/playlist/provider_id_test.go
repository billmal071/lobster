package playlist

import (
	"testing"

	"lobster/internal/media"
)

// idRecordingProvider remembers the media ID it was asked about. The existing
// mockProvider ignores that argument entirely, so it cannot tell a session
// that calls its provider with the provider's own ID from one that calls it
// with the work's — which is exactly the distinction ProviderID exists to make.
type idRecordingProvider struct {
	mockProvider
	askedID string
}

func (p *idRecordingProvider) GetEpisodes(id string, seasonID string) ([]media.Episode, error) {
	p.askedID = id
	return p.mockProvider.GetEpisodes(id, seasonID)
}

func sessionForSeasonCrossing(p *idRecordingProvider, providerID string) *Session {
	p.mockProvider = mockProvider{episodes: map[string][]media.Episode{
		"s1": {{Number: 1, ID: "e1"}},
		"s2": {{Number: 1, ID: "e2"}},
	}}
	content := media.SearchResult{ID: "tv/1403", Title: "Test Show", Type: media.TV}
	seasons := []media.Season{{Number: 1, ID: "s1"}, {Number: 2, ID: "s2"}}
	// nil: this fixture exercises the ProviderID split, not the chain.
	return NewWithProviderID(p, content, providerID, nil, seasons, p.episodes["s1"], 0, 0)
}

// Crossing a season boundary is the one place the session talks to its
// provider on its own, and it has to use the provider's key. When the episode
// list was recovered from the fallback chain the provider is that chain member
// and knows the show by a different ID; asking it about the work's ID returns
// "season not found" and continuation dies at the season boundary.
func TestNextCrossesSeasonsUsingTheProvidersOwnID(t *testing.T) {
	p := &idRecordingProvider{}
	s := sessionForSeasonCrossing(p, "fallback-77")

	if _, err := s.Next(); err != nil {
		t.Fatalf("Next across the season boundary: %v", err)
	}
	if p.askedID != "fallback-77" {
		t.Fatalf("GetEpisodes was asked about %q, want the provider's own ID %q", p.askedID, "fallback-77")
	}
}

// Previous crosses the same boundary and had the same open-coded Content.ID.
func TestPreviousCrossesSeasonsUsingTheProvidersOwnID(t *testing.T) {
	p := &idRecordingProvider{}
	s := sessionForSeasonCrossing(p, "fallback-77")
	s.SeasonIdx, s.Episodes, s.EpisodeIdx = 1, p.episodes["s2"], 0

	if _, err := s.Previous(); err != nil {
		t.Fatalf("Previous across the season boundary: %v", err)
	}
	if p.askedID != "fallback-77" {
		t.Fatalf("GetEpisodes was asked about %q, want the provider's own ID %q", p.askedID, "fallback-77")
	}
}

// And with no ProviderID — every session built by New — the provider is still
// asked about the content's own ID, which is what it answered to before this
// field existed.
func TestSeasonCrossingFallsBackToTheContentID(t *testing.T) {
	p := &idRecordingProvider{}
	s := sessionForSeasonCrossing(p, "")

	if _, err := s.Next(); err != nil {
		t.Fatalf("Next across the season boundary: %v", err)
	}
	if p.askedID != "tv/1403" {
		t.Fatalf("GetEpisodes was asked about %q, want the content ID %q", p.askedID, "tv/1403")
	}
}
