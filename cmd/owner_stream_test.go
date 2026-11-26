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

	ref, err := encodeRef(playRef{ID: "tv/219", Title: "Kyle XY", Year: "2006", Type: "tv", Base: base})
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
