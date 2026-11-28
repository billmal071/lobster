package cmd

import (
	"errors"
	"testing"

	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/provider"
)

type unlistedOwner struct{ *recordingStreamProvider }

func (unlistedOwner) Search(string) ([]media.SearchResult, error) {
	return nil, errors.New("tmdb search: status 403")
}

func (unlistedOwner) GetSeasons(string) ([]media.Season, error) {
	return nil, errors.New("getting seasons: 1shows answered with an HTML page, not season data")
}

func newUnlistedOwner(t *testing.T) unlistedOwner {
	return unlistedOwner{&recordingStreamProvider{url: stubStreamServer(t)}}
}

func playOwnedRef(t *testing.T, primary provider.Provider, base string) error {
	t.Helper()
	return playRefAgainst(t, primary, playRef{ID: "tv/219", Title: "Kyle XY", Year: "2006", Type: "tv", Base: base, IDFromBase: true})
}

func playRefAgainst(t *testing.T, primary provider.Provider, r playRef) error {
	t.Helper()
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
	captureAgentOut(t)
	withPlayerAvailable(t)
	withBaseFlag(t)

	a, b := newDeadChain("Kyle XY")
	withFallbackChain(t, a, b)
	withStubProviderBase(t, primary, "owner")
	prevProv := agentProvider
	agentProvider = func() provider.Provider { return primary }
	t.Cleanup(func() { agentProvider = prevProv })

	ref, err := encodeRef(r)
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef, prevSeason, prevEpisode := flagRef, flagSeason, flagEpisode
	flagRef, flagSeason, flagEpisode = ref, 2, 15
	t.Cleanup(func() { flagRef, flagSeason, flagEpisode = prevRef, prevSeason, prevEpisode })

	return playRun(playCmd, nil)
}

func TestPlayAsksTheRefsOwnPrimaryForAnEpisodeItCannotList(t *testing.T) {
	primary := newUnlistedOwner(t)
	if err := playOwnedRef(t, primary, "owner"); err != nil {
		t.Fatalf("playRun: %v", err)
	}
	if got := primary.episodeAsked(); got != "219:2:15" {
		t.Fatalf("primary Watch episode = %q, want 219:2:15", got)
	}
}

type episodelessOwner struct{ *recordingStreamProvider }

func (episodelessOwner) Search(string) ([]media.SearchResult, error) {
	return nil, errors.New("tmdb search: status 403")
}

func (episodelessOwner) GetSeasons(string) ([]media.Season, error) {
	return []media.Season{{Number: 1, ID: "219:1"}, {Number: 2, ID: "219:2"}}, nil
}

func (episodelessOwner) GetEpisodes(string, string) ([]media.Episode, error) {
	return nil, errors.New("getting episodes: 1shows answered with an HTML page, not season data")
}

func TestPlayAsksTheRefsOwnPrimaryWhenOnlyItsEpisodeListFails(t *testing.T) {
	primary := episodelessOwner{&recordingStreamProvider{url: stubStreamServer(t)}}
	if err := playOwnedRef(t, primary, "owner"); err != nil {
		t.Fatalf("playRun: %v", err)
	}
	if got := primary.episodeAsked(); got != "219:2:15" {
		t.Fatalf("primary Watch episode = %q, want 219:2:15", got)
	}
}

func TestPlayDoesNotAskAPrimaryByAnIDItDoesNotOwn(t *testing.T) {
	primary := newUnlistedOwner(t)
	if err := playOwnedRef(t, primary, "elsewhere"); err == nil {
		t.Fatal("playRun succeeded although only the primary could stream and the ID is not its own")
	}
	if primary.watchCalled {
		t.Fatalf("primary Watch was asked for %q under an ID another source issued", primary.episodeAsked())
	}
}

func TestPlayDoesNotAskAPrimaryByALegacyRefsID(t *testing.T) {
	primary := newUnlistedOwner(t)
	err := playRefAgainst(t, primary, playRef{ID: "tv/219", Title: "Kyle XY", Year: "2006", Type: "tv", Base: "owner"})
	if err == nil {
		t.Fatal("playRun succeeded although only the primary could stream and the legacy ref does not say the ID is its own")
	}
	if primary.watchCalled {
		t.Fatalf("primary Watch was asked for %q under a legacy ref's ID", primary.episodeAsked())
	}
}

// tmdbOwner is a primary whose own IDs are TMDB-shaped, like TBCPL.
type tmdbOwner struct{ unlistedOwner }

func (tmdbOwner) MintsTMDBShapedIDs() bool { return true }

func newTMDBOwner(t *testing.T) tmdbOwner { return tmdbOwner{newUnlistedOwner(t)} }

func TestPlayAsksTheOwnerByALegacyRefsTMDBShapedID(t *testing.T) {
	primary := newTMDBOwner(t)
	err := playRefAgainst(t, primary, playRef{ID: "tv/219", Title: "Kyle XY", Year: "2006", Type: "tv", Base: "owner"})
	if err != nil {
		t.Fatalf("playRun: %v", err)
	}
	if got := primary.episodeAsked(); got != "219:2:15" {
		t.Fatalf("primary Watch episode = %q, want 219:2:15", got)
	}
}

func TestPlayDoesNotAskAnOwnerByALegacyIDOnlyAFallbackCouldHaveIssued(t *testing.T) {
	for name, r := range map[string]playRef{
		"slug id":         {ID: "tv/kyle-xy-219", Title: "Kyle XY", Type: "tv", Base: "owner"},
		"movie id for tv": {ID: "movie/219", Title: "Kyle XY", Type: "tv", Base: "owner"},
		"trailing path":   {ID: "tv/219/2/15", Title: "Kyle XY", Type: "tv", Base: "owner"},
		"other base":      {ID: "tv/219", Title: "Kyle XY", Type: "tv", Base: "elsewhere"},
		"no base":         {ID: "tv/219", Title: "Kyle XY", Type: "tv"},
	} {
		t.Run(name, func(t *testing.T) {
			primary := newTMDBOwner(t)
			if err := playRefAgainst(t, primary, r); err == nil {
				t.Fatal("playRun succeeded although only the primary could stream")
			}
			if primary.watchCalled {
				t.Fatalf("primary Watch was asked for %q", primary.episodeAsked())
			}
		})
	}
}
