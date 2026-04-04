// Package playlist manages episode navigation state for continuous playback.
package playlist

import (
	"fmt"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// Session tracks the current position within a TV show's episodes,
// supporting navigation across episodes and seasons.
type Session struct {
	Provider     provider.Provider
	Content      media.SearchResult
	Seasons      []media.Season
	Episodes     []media.Episode // episodes for current season
	SeasonIdx    int
	EpisodeIdx   int
	LastPosition float64 // playback position from most recent play
}

// New creates a Session positioned at the given season and episode.
func New(p provider.Provider, content media.SearchResult, seasons []media.Season, episodes []media.Episode, seasonIdx, episodeIdx int) *Session {
	return &Session{
		Provider:   p,
		Content:    content,
		Seasons:    seasons,
		Episodes:   episodes,
		SeasonIdx:  seasonIdx,
		EpisodeIdx: episodeIdx,
	}
}

// Current returns the currently selected episode.
func (s *Session) Current() media.Episode {
	return s.Episodes[s.EpisodeIdx]
}

// CurrentSeason returns the currently selected season.
func (s *Session) CurrentSeason() media.Season {
	return s.Seasons[s.SeasonIdx]
}

// Title returns a formatted title like "Show S01E03".
func (s *Session) Title() string {
	ep := s.Current()
	sn := s.CurrentSeason()
	return fmt.Sprintf("%s S%02dE%02d", s.Content.Title, sn.Number, ep.Number)
}

// HasNext returns true if there is a next episode in this season or a next season.
func (s *Session) HasNext() bool {
	if s.EpisodeIdx < len(s.Episodes)-1 {
		return true
	}
	return s.SeasonIdx < len(s.Seasons)-1
}

// HasPrevious returns true if there is a previous episode in this season or a previous season.
func (s *Session) HasPrevious() bool {
	if s.EpisodeIdx > 0 {
		return true
	}
	return s.SeasonIdx > 0
}

// Next advances to the next episode. If at the end of a season, loads the
// next season's episodes from the provider. Returns an error if already at
// the last episode of the last season.
func (s *Session) Next() (media.Episode, error) {
	if s.EpisodeIdx < len(s.Episodes)-1 {
		s.EpisodeIdx++
		return s.Current(), nil
	}

	if s.SeasonIdx >= len(s.Seasons)-1 {
		return media.Episode{}, fmt.Errorf("no next episode: end of series")
	}

	// Cross to next season
	s.SeasonIdx++
	nextSeason := s.Seasons[s.SeasonIdx]
	episodes, err := s.Provider.GetEpisodes(s.Content.ID, nextSeason.ID)
	if err != nil {
		s.SeasonIdx-- // rollback
		return media.Episode{}, fmt.Errorf("loading season %d episodes: %w", nextSeason.Number, err)
	}
	if len(episodes) == 0 {
		s.SeasonIdx--
		return media.Episode{}, fmt.Errorf("season %d has no episodes", nextSeason.Number)
	}

	s.Episodes = episodes
	s.EpisodeIdx = 0
	return s.Current(), nil
}

// Previous moves to the previous episode. If at the start of a season, loads
// the previous season's episodes and positions at the last episode.
func (s *Session) Previous() (media.Episode, error) {
	if s.EpisodeIdx > 0 {
		s.EpisodeIdx--
		return s.Current(), nil
	}

	if s.SeasonIdx <= 0 {
		return media.Episode{}, fmt.Errorf("no previous episode: start of series")
	}

	// Cross to previous season
	s.SeasonIdx--
	prevSeason := s.Seasons[s.SeasonIdx]
	episodes, err := s.Provider.GetEpisodes(s.Content.ID, prevSeason.ID)
	if err != nil {
		s.SeasonIdx++ // rollback
		return media.Episode{}, fmt.Errorf("loading season %d episodes: %w", prevSeason.Number, err)
	}
	if len(episodes) == 0 {
		s.SeasonIdx++
		return media.Episode{}, fmt.Errorf("season %d has no episodes", prevSeason.Number)
	}

	s.Episodes = episodes
	s.EpisodeIdx = len(s.Episodes) - 1
	return s.Current(), nil
}

// SetEpisodes replaces the episode list (e.g., when user picks from episode list).
func (s *Session) SetEpisodes(episodes []media.Episode, seasonIdx, episodeIdx int) {
	s.Episodes = episodes
	s.SeasonIdx = seasonIdx
	s.EpisodeIdx = episodeIdx
}
