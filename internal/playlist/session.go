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
	Provider provider.Provider
	Content  media.SearchResult
	// ProviderID is the key Provider answers to for this work, which is not
	// always Content.ID. When the primary can enumerate seasons but not
	// episodes, cmd recovers the list from a fallback provider and moves
	// playback there too (fallbackEpisodeList, cmd/episodes.go); that provider
	// has its own ID for the show.
	//
	// It is a separate field because Content.ID was being asked two different
	// questions at once. It is the provider-call key here and in
	// cmd/session.go's Watch, and it is *also* the history key — the checkpoint
	// writer, the --continue resume lookup and saveHistory all key on it. Once
	// the recovery rebound it, the same show, season and episode filed under
	// two history IDs depending on whether the primary's GetEpisodes happened
	// to work that run, and one transient 5xx made --continue restart from
	// zero.
	//
	// So Content.ID is the identity of the work and never moves; ProviderID
	// follows Provider. Use ProviderKey rather than reading this directly: a
	// Session built as a struct literal leaves it empty.
	ProviderID string

	// ChainPrimary is the provider the fallback chain must be built around,
	// which is not always Provider either. cmd builds the chain by excluding
	// its argument (fallbackProviders, cmd/fallback.go), so handing it a
	// Provider the episode-list recovery moved to would drop the one source
	// proven to have this work and add back the primary that could not even
	// list it.
	//
	// It is the same split ProviderID makes, one layer out: Provider is who
	// answers, ChainPrimary is who was configured. Use ChainBase rather than
	// reading this directly.
	ChainPrimary provider.Provider

	Seasons      []media.Season
	Episodes     []media.Episode // episodes for current season
	SeasonIdx    int
	EpisodeIdx   int
	LastPosition float64 // playback position from most recent play
	LastDuration float64 // total media duration from most recent play
	// LastPositionUnknown carries player.PlayResult.PositionUnknown from the
	// most recent play: the position tracker never observed a position, so
	// LastPosition is a default rather than a measurement and must not be
	// persisted over the episode's existing resume point.
	LastPositionUnknown bool
	// LastPositionUntracked carries player.PlayResult.PositionUntracked from
	// the most recent play: the player has no position tracking at all, so the
	// watch is real but LastPosition is meaningless. The episode is recorded,
	// keeping whatever position history already holds for it.
	LastPositionUntracked bool
}

// New creates a Session positioned at the given season and episode, whose
// provider answers to the content's own ID.
func New(p provider.Provider, content media.SearchResult, seasons []media.Season, episodes []media.Episode, seasonIdx, episodeIdx int) *Session {
	return NewWithProviderID(p, content, content.ID, seasons, episodes, seasonIdx, episodeIdx)
}

// NewWithProviderID is New for a session whose provider is not the one
// content.ID came from. The caller is cmd's episode-list recovery: when the
// primary cannot list a season, the list — and the playback that follows —
// move to a fallback provider, which knows the show by a different ID.
//
// Passing that ID here instead of overwriting content.ID is the whole point.
// content.ID is what history is keyed on (cmd/session.go), so it has to mean
// the same work on every run regardless of who answered.
func NewWithProviderID(p provider.Provider, content media.SearchResult, providerID string, seasons []media.Season, episodes []media.Episode, seasonIdx, episodeIdx int) *Session {
	return &Session{
		Provider:   p,
		Content:    content,
		ProviderID: providerID,
		Seasons:    seasons,
		Episodes:   episodes,
		SeasonIdx:  seasonIdx,
		EpisodeIdx: episodeIdx,
	}
}

// ChainBase is the provider to build the fallback chain around. It falls back
// to Provider, which is what every session did before the episode-list
// recovery could move Provider somewhere else.
func (s *Session) ChainBase() provider.Provider {
	if s.ChainPrimary != nil {
		return s.ChainPrimary
	}
	return s.Provider
}

// ProviderKey is the ID to call Provider with. It falls back to Content.ID so
// a Session built as a struct literal — the tests do — behaves as it did
// before ProviderID existed.
func (s *Session) ProviderKey() string {
	if s.ProviderID != "" {
		return s.ProviderID
	}
	return s.Content.ID
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
	episodes, err := s.Provider.GetEpisodes(s.ProviderKey(), nextSeason.ID)
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
	episodes, err := s.Provider.GetEpisodes(s.ProviderKey(), prevSeason.ID)
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
