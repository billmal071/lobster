package playlist

import (
	"fmt"
	"testing"

	"lobster/internal/media"
)

// mockProvider implements provider.Provider for testing.
type mockProvider struct {
	episodes map[string][]media.Episode // keyed by seasonID
}

func (m *mockProvider) Search(query string) ([]media.SearchResult, error) {
	return nil, nil
}
func (m *mockProvider) GetDetails(id string) (*media.ContentDetail, error) {
	return nil, nil
}
func (m *mockProvider) GetSeasons(id string) ([]media.Season, error) { return nil, nil }
func (m *mockProvider) GetEpisodes(id string, seasonID string) ([]media.Episode, error) {
	eps, ok := m.episodes[seasonID]
	if !ok {
		return nil, fmt.Errorf("season %s not found", seasonID)
	}
	return eps, nil
}
func (m *mockProvider) GetServers(id string, episodeID string) ([]media.Server, error) {
	return nil, nil
}
func (m *mockProvider) GetEmbedURL(serverID string) (string, error) { return "", nil }
func (m *mockProvider) Trending(mt media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}
func (m *mockProvider) Recent(mt media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}

func newTestSession() *Session {
	mp := &mockProvider{
		episodes: map[string][]media.Episode{
			"s1": {
				{Number: 1, Title: "Pilot", ID: "e1"},
				{Number: 2, Title: "Second", ID: "e2"},
				{Number: 3, Title: "Third", ID: "e3"},
			},
			"s2": {
				{Number: 1, Title: "S2 Premiere", ID: "e4"},
				{Number: 2, Title: "S2 Second", ID: "e5"},
			},
			"s3": {
				{Number: 1, Title: "S3 Premiere", ID: "e6"},
			},
		},
	}

	seasons := []media.Season{
		{Number: 1, ID: "s1"},
		{Number: 2, ID: "s2"},
		{Number: 3, ID: "s3"},
	}

	episodes := []media.Episode{
		{Number: 1, Title: "Pilot", ID: "e1"},
		{Number: 2, Title: "Second", ID: "e2"},
		{Number: 3, Title: "Third", ID: "e3"},
	}

	content := media.SearchResult{
		ID:    "show-1",
		Title: "Test Show",
		Type:  media.TV,
	}

	return New(mp, content, seasons, episodes, 0, 0)
}

func TestNext(t *testing.T) {
	tests := []struct {
		name       string
		seasonIdx  int
		episodeIdx int
		wantEpNum  int
		wantSnNum  int
		wantErr    bool
	}{
		{
			name:       "next within season",
			seasonIdx:  0,
			episodeIdx: 0,
			wantEpNum:  2,
			wantSnNum:  1,
		},
		{
			name:       "next crosses to next season",
			seasonIdx:  0,
			episodeIdx: 2,
			wantEpNum:  1,
			wantSnNum:  2,
		},
		{
			name:       "next at end of series",
			seasonIdx:  2,
			episodeIdx: 0,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestSession()
			s.SeasonIdx = tt.seasonIdx
			s.EpisodeIdx = tt.episodeIdx

			// If crossing seasons, load the correct episodes for the starting season
			if tt.seasonIdx > 0 {
				seasonID := s.Seasons[tt.seasonIdx].ID
				eps, _ := s.Provider.GetEpisodes(s.Content.ID, seasonID)
				s.Episodes = eps
			}

			ep, err := s.Next()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ep.Number != tt.wantEpNum {
				t.Errorf("episode number = %d, want %d", ep.Number, tt.wantEpNum)
			}
			if s.CurrentSeason().Number != tt.wantSnNum {
				t.Errorf("season number = %d, want %d", s.CurrentSeason().Number, tt.wantSnNum)
			}
		})
	}
}

func TestPrevious(t *testing.T) {
	tests := []struct {
		name       string
		seasonIdx  int
		episodeIdx int
		wantEpNum  int
		wantSnNum  int
		wantErr    bool
	}{
		{
			name:       "previous within season",
			seasonIdx:  0,
			episodeIdx: 2,
			wantEpNum:  2,
			wantSnNum:  1,
		},
		{
			name:       "previous crosses to prior season last episode",
			seasonIdx:  1,
			episodeIdx: 0,
			wantEpNum:  3,
			wantSnNum:  1,
		},
		{
			name:       "previous at start of series",
			seasonIdx:  0,
			episodeIdx: 0,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestSession()
			s.SeasonIdx = tt.seasonIdx
			s.EpisodeIdx = tt.episodeIdx

			if tt.seasonIdx > 0 {
				seasonID := s.Seasons[tt.seasonIdx].ID
				eps, _ := s.Provider.GetEpisodes(s.Content.ID, seasonID)
				s.Episodes = eps
			}

			ep, err := s.Previous()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ep.Number != tt.wantEpNum {
				t.Errorf("episode number = %d, want %d", ep.Number, tt.wantEpNum)
			}
			if s.CurrentSeason().Number != tt.wantSnNum {
				t.Errorf("season number = %d, want %d", s.CurrentSeason().Number, tt.wantSnNum)
			}
		})
	}
}

func TestHasNextAndHasPrevious(t *testing.T) {
	tests := []struct {
		name       string
		seasonIdx  int
		episodeIdx int
		wantNext   bool
		wantPrev   bool
	}{
		{"middle of season", 0, 1, true, true},
		{"first ep first season", 0, 0, true, false},
		{"last ep last season", 2, 0, false, true},
		{"last ep mid season", 0, 2, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestSession()
			s.SeasonIdx = tt.seasonIdx
			s.EpisodeIdx = tt.episodeIdx

			if tt.seasonIdx > 0 {
				seasonID := s.Seasons[tt.seasonIdx].ID
				eps, _ := s.Provider.GetEpisodes(s.Content.ID, seasonID)
				s.Episodes = eps
			}

			if got := s.HasNext(); got != tt.wantNext {
				t.Errorf("HasNext() = %v, want %v", got, tt.wantNext)
			}
			if got := s.HasPrevious(); got != tt.wantPrev {
				t.Errorf("HasPrevious() = %v, want %v", got, tt.wantPrev)
			}
		})
	}
}

func TestTitle(t *testing.T) {
	s := newTestSession()
	want := "Test Show S01E01"
	if got := s.Title(); got != want {
		t.Errorf("Title() = %q, want %q", got, want)
	}
}
