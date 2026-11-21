package playlist

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// mockProvider implements provider.Provider for testing.
type mockProvider struct {
	episodes map[string][]media.Episode // keyed by seasonID
	errs     map[string]error
}

func (m *mockProvider) Search(query string) ([]media.SearchResult, error) {
	return nil, nil
}
func (m *mockProvider) GetDetails(id string) (*media.ContentDetail, error) {
	return nil, nil
}
func (m *mockProvider) GetSeasons(id string) ([]media.Season, error) { return nil, nil }
func (m *mockProvider) GetEpisodes(id string, seasonID string) ([]media.Episode, error) {
	if err, bad := m.errs[seasonID]; bad {
		return m.episodes[seasonID], err
	}
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

func TestNextAcrossSeasonEpisodeListErrors(t *testing.T) {
	partial := fmt.Errorf("probe budget: %w", provider.ErrIncompleteEpisodeList)
	cases := []struct {
		name    string
		eps     []media.Episode
		err     error
		wantErr bool
	}{
		{"measured prefix with incomplete is usable", []media.Episode{{Number: 1, ID: "n1"}}, partial, false},
		{"incomplete with no episodes fails", nil, partial, true},
		{"incomplete list starting after episode 1 fails", []media.Episode{{Number: 2, ID: "n2"}, {Number: 3, ID: "n3"}}, partial, true},
		{"incomplete list numbered from zero fails", []media.Episode{{Number: 0, ID: "n0"}, {Number: 1, ID: "n1"}}, partial, true},
		{"unrelated error with episodes fails", []media.Episode{{Number: 1, ID: "n1"}}, errors.New("boom"), true},
		{"empty next season fails", nil, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mp := &mockProvider{
				episodes: map[string][]media.Episode{"s2": c.eps},
				errs:     map[string]error{},
			}
			if c.err != nil {
				mp.errs["s2"] = c.err
			}
			seasons := []media.Season{{Number: 1, ID: "s1"}, {Number: 2, ID: "s2"}}
			s := New(mp, media.SearchResult{ID: "x", Title: "X", Type: media.TV}, seasons,
				[]media.Episode{{Number: 1, ID: "e1"}}, 0, 0)
			ep, err := s.Next()
			if c.wantErr {
				if err == nil {
					t.Fatal("Next returned nil error")
				}
				if s.SeasonIdx != 0 || s.EpisodeIdx != 0 {
					t.Fatalf("no rollback: season %d episode %d", s.SeasonIdx, s.EpisodeIdx)
				}
				return
			}
			if err != nil || ep.ID != "n1" || s.SeasonIdx != 1 {
				t.Fatalf("Next = %v, %v (season %d), want n1 in season 2", ep, err, s.SeasonIdx)
			}
		})
	}
}

func TestNextReportsMeasuredPrefixOnSeasonCrossing(t *testing.T) {
	partial := fmt.Errorf("probe budget: %w", provider.ErrIncompleteEpisodeList)
	build := func(err error) (*Session, *[]error) {
		mp := &mockProvider{
			episodes: map[string][]media.Episode{"s2": {{Number: 1, ID: "n1"}}},
			errs:     map[string]error{},
		}
		if err != nil {
			mp.errs["s2"] = err
		}
		seasons := []media.Season{{Number: 1, ID: "s1"}, {Number: 2, ID: "s2"}}
		s := New(mp, media.SearchResult{ID: "x", Title: "X", Type: media.TV}, seasons,
			[]media.Episode{{Number: 1, ID: "e1"}}, 0, 0)
		var got []error
		s.OnPartialSeason = func(season media.Season, err error) {
			if season.Number != 2 {
				t.Errorf("reported season %d, want 2", season.Number)
			}
			got = append(got, err)
		}
		return s, &got
	}

	s, got := build(partial)
	if _, err := s.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(*got) != 1 || !errors.Is((*got)[0], provider.ErrIncompleteEpisodeList) {
		t.Fatalf("OnPartialSeason calls = %v, want one with the incomplete error", *got)
	}

	s, got = build(nil)
	if _, err := s.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(*got) != 0 {
		t.Fatalf("OnPartialSeason fired for a complete season: %v", *got)
	}
}

func TestNextRejectsAPartialSeasonThatDoesNotStartAtEpisodeOne(t *testing.T) {
	partial := fmt.Errorf("eztv page failed: %w", provider.ErrIncompleteEpisodeList)
	mp := &mockProvider{
		episodes: map[string][]media.Episode{"s2": {{Number: 2, ID: "n2"}, {Number: 3, ID: "n3"}}},
		errs:     map[string]error{"s2": partial},
	}
	seasons := []media.Season{{Number: 1, ID: "s1"}, {Number: 2, ID: "s2"}}
	current := []media.Episode{{Number: 1, ID: "e1"}}
	s := New(mp, media.SearchResult{ID: "x", Title: "X", Type: media.TV}, seasons, current, 0, 0)
	reported := 0
	s.OnPartialSeason = func(media.Season, error) { reported++ }

	ep, err := s.Next()
	if err == nil {
		t.Fatalf("Next = %v, want an error: season 2 would start at episode 2 and skip episode 1", ep)
	}
	if !errors.Is(err, provider.ErrIncompleteEpisodeList) {
		t.Errorf("err = %v, want it to wrap ErrIncompleteEpisodeList", err)
	}
	if !strings.Contains(err.Error(), "season 2") || !strings.Contains(err.Error(), "episode 2") {
		t.Errorf("err = %v, want it to name season 2 and the episode 2 the list starts at", err)
	}
	if s.SeasonIdx != 0 || s.EpisodeIdx != 0 || s.Current().ID != "e1" {
		t.Errorf("position = season idx %d episode idx %d (%s), want it left on e1", s.SeasonIdx, s.EpisodeIdx, s.Current().ID)
	}
	if s.EpisodesPartial {
		t.Error("EpisodesPartial set by a season the session did not enter")
	}
	if reported != 0 {
		t.Errorf("OnPartialSeason fired %d times for a season the session did not enter", reported)
	}
	if !s.HasNext() {
		t.Error("HasNext false after the rejection, want the complete season 1 still followed by season 2")
	}
}

func TestNextKeepsACompleteSeasonThatDoesNotStartAtEpisodeOne(t *testing.T) {
	mp := &mockProvider{episodes: map[string][]media.Episode{"s2": {{Number: 2, ID: "n2"}, {Number: 3, ID: "n3"}}}}
	seasons := []media.Season{{Number: 1, ID: "s1"}, {Number: 2, ID: "s2"}}
	s := New(mp, media.SearchResult{ID: "x", Title: "X", Type: media.TV}, seasons,
		[]media.Episode{{Number: 1, ID: "e1"}}, 0, 0)
	ep, err := s.Next()
	if err != nil || ep.ID != "n2" || s.SeasonIdx != 1 || s.EpisodesPartial {
		t.Fatalf("Next = %v, %v (season idx %d, partial %v), want n2 in a complete season 2", ep, err, s.SeasonIdx, s.EpisodesPartial)
	}
}

func TestNextStopsAtEndOfMeasuredPrefix(t *testing.T) {
	partial := fmt.Errorf("probe budget: %w", provider.ErrIncompleteEpisodeList)
	build := func(err error) *Session {
		mp := &mockProvider{
			episodes: map[string][]media.Episode{
				"s2": {{Number: 1, ID: "n1"}, {Number: 2, ID: "n2"}},
				"s3": {{Number: 1, ID: "m1"}},
			},
			errs: map[string]error{},
		}
		if err != nil {
			mp.errs["s2"] = err
		}
		seasons := []media.Season{{Number: 1, ID: "s1"}, {Number: 2, ID: "s2"}, {Number: 3, ID: "s3"}}
		return New(mp, media.SearchResult{ID: "x", Title: "X", Type: media.TV}, seasons,
			[]media.Episode{{Number: 1, ID: "e1"}}, 0, 0)
	}

	s := build(partial)
	if _, err := s.Next(); err != nil {
		t.Fatalf("crossing into the prefix: %v", err)
	}
	if _, err := s.Next(); err != nil {
		t.Fatalf("inside the prefix: %v", err)
	}
	_, err := s.Next()
	if err == nil || !strings.Contains(err.Error(), "season 2") || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("Next at the end of a measured prefix = %v, want an error naming season 2 as incomplete", err)
	}
	if s.SeasonIdx != 1 || s.EpisodeIdx != 1 {
		t.Fatalf("position moved to season idx %d episode idx %d, want it left on the last measured episode", s.SeasonIdx, s.EpisodeIdx)
	}

	s = build(nil)
	s.Next()
	s.Next()
	ep, err := s.Next()
	if err != nil || ep.ID != "m1" || s.SeasonIdx != 2 {
		t.Fatalf("Next at the end of a complete season = %v, %v (season idx %d), want m1 in season 3", ep, err, s.SeasonIdx)
	}
}

func TestSessionBuiltOnAMeasuredPrefixStopsAtItsEnd(t *testing.T) {
	mp := &mockProvider{episodes: map[string][]media.Episode{"s2": {{Number: 1, ID: "n1"}}}}
	seasons := []media.Season{{Number: 1, ID: "s1"}, {Number: 2, ID: "s2"}}
	s := New(mp, media.SearchResult{ID: "x", Title: "X", Type: media.TV}, seasons,
		[]media.Episode{{Number: 1, ID: "e1"}}, 0, 0)
	s.EpisodesPartial = true
	if _, err := s.Next(); err == nil {
		t.Fatal("Next advanced past the end of a list the session was built on as a measured prefix")
	}
}

func TestHasNextIsFalseAtEndOfMeasuredPrefix(t *testing.T) {
	mp := &mockProvider{}
	seasons := []media.Season{{Number: 1, ID: "s1"}, {Number: 2, ID: "s2"}, {Number: 3, ID: "s3"}}
	eps := []media.Episode{{Number: 1, ID: "n1"}, {Number: 2, ID: "n2"}}
	s := New(mp, media.SearchResult{ID: "x", Title: "X", Type: media.TV}, seasons, eps, 1, 0)
	s.EpisodesPartial = true
	if !s.HasNext() {
		t.Fatal("HasNext false inside the measured prefix")
	}
	s.EpisodeIdx = 1
	if s.HasNext() {
		t.Fatal("HasNext true at the end of a measured prefix with a later season, so the menu would offer a Next that cannot be taken")
	}
	s.EpisodesPartial = false
	if !s.HasNext() {
		t.Fatal("HasNext false at the end of a complete season with a later season")
	}
}

func TestPreviousIntoACompleteSeasonDropsThePrefixFlag(t *testing.T) {
	mp := &mockProvider{episodes: map[string][]media.Episode{
		"s1": {{Number: 1, ID: "a1"}, {Number: 2, ID: "a2"}},
		"s2": {{Number: 1, ID: "b1"}, {Number: 2, ID: "b2"}},
	}}
	seasons := []media.Season{{Number: 1, ID: "s1"}, {Number: 2, ID: "s2"}}
	s := New(mp, media.SearchResult{ID: "x", Title: "X", Type: media.TV}, seasons,
		[]media.Episode{{Number: 1, ID: "b1"}}, 1, 0)
	s.EpisodesPartial = true

	if _, err := s.Previous(); err != nil {
		t.Fatalf("Previous: %v", err)
	}
	if s.EpisodesPartial {
		t.Fatal("the prefix flag of season 2 stayed set after moving into season 1")
	}
	if s.SeasonIdx != 0 || s.EpisodeIdx != 1 {
		t.Fatalf("Previous left season idx %d episode idx %d, want the last episode of season 1", s.SeasonIdx, s.EpisodeIdx)
	}
	ep, err := s.Next()
	if err != nil {
		t.Fatalf("Next at the end of season 1: %v", err)
	}
	if ep.ID != "b1" || s.SeasonIdx != 1 {
		t.Fatalf("Next = %v in season idx %d, want b1 in season idx 1", ep, s.SeasonIdx)
	}
}
