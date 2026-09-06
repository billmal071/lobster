package cmd

import (
	"testing"

	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/playlist"
)

// stubStreamProvider satisfies provider.StreamProvider without any network:
// Watch returns a canned stream, so resolveStream takes the Primary path and
// playCurrentEpisode never touches the fallback chain.
type stubStreamProvider struct{ stream *media.Stream }

func (p *stubStreamProvider) Search(string) ([]media.SearchResult, error)      { return nil, nil }
func (p *stubStreamProvider) GetDetails(string) (*media.ContentDetail, error)  { return nil, nil }
func (p *stubStreamProvider) GetSeasons(string) ([]media.Season, error)        { return nil, nil }
func (p *stubStreamProvider) GetEpisodes(string, string) ([]media.Episode, error) {
	return nil, nil
}
func (p *stubStreamProvider) GetServers(string, string) ([]media.Server, error) { return nil, nil }
func (p *stubStreamProvider) GetEmbedURL(string) (string, error)                { return "", nil }
func (p *stubStreamProvider) Trending(media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}
func (p *stubStreamProvider) Recent(media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}
func (p *stubStreamProvider) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	return p.stream, nil
}

func sessionForTest(prov *stubStreamProvider) *playlist.Session {
	return playlist.New(
		prov,
		media.SearchResult{ID: "tv/s", Title: "S", Type: media.TV},
		[]media.Season{{Number: 1, ID: "s1"}},
		[]media.Episode{{Number: 3, ID: "ep3"}},
		0, 0,
	)
}

// The session (playlist) path has its own play loop, so it needs the same
// mid-playback checkpointing as playStream: a hard shutdown during an episode
// must find the position already in history, while the session's own
// exit-time state still carries the (more precise) final position.
func TestPlayCurrentEpisodeCheckpointsPositionMidPlayback(t *testing.T) {
	stub := &stubCheckpointPlayer{
		stubPlayerImpl: stubPlayerImpl{result: player.PlayResult{Position: 1234, Duration: 5400}},
		fire:           true,
		mid:            [2]float64{600, 5400},
	}
	playStreamHarness(t, stub)

	prov := &stubStreamProvider{stream: &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}}
	sess := sessionForTest(prov)

	if err := playCurrentEpisode(sess); err != nil {
		t.Fatalf("playCurrentEpisode: %v", err)
	}

	// The state a hard shutdown mid-episode would have left behind.
	var found bool
	for _, e := range stub.midEntries {
		if e.ID == "tv/s" && e.Season == 1 && e.Episode == 3 {
			found = true
			if e.Position != 600 || e.Duration != 5400 {
				t.Fatalf("mid-playback history = pos %g dur %g, want 600/5400 from the checkpoint", e.Position, e.Duration)
			}
			if e.Title != "S" || e.Type != media.TV {
				t.Fatalf("mid-playback entry metadata = %+v, want title S type tv", e)
			}
		}
	}
	if !found {
		t.Fatalf("no history entry existed during session playback; a hard shutdown would have lost the position (mid entries: %+v)", stub.midEntries)
	}

	// The session still records the final position for its own exit-time save.
	if sess.LastPosition != 1234 || sess.LastDuration != 5400 {
		t.Fatalf("session final state = pos %g dur %g, want 1234/5400", sess.LastPosition, sess.LastDuration)
	}
}

// With history disabled the session path must not install a checkpoint
// callback either.
func TestPlayCurrentEpisodeNoCheckpointWhenHistoryDisabled(t *testing.T) {
	stub := &stubCheckpointPlayer{
		stubPlayerImpl: stubPlayerImpl{result: player.PlayResult{Position: 99, Duration: 100}},
	}
	playStreamHarness(t, stub)
	cfg.History = false

	prov := &stubStreamProvider{stream: &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}}
	if err := playCurrentEpisode(sessionForTest(prov)); err != nil {
		t.Fatalf("playCurrentEpisode: %v", err)
	}
	if stub.fn != nil {
		t.Fatal("checkpoint callback installed with cfg.History disabled")
	}
}
