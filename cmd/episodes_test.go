package cmd

import (
	"encoding/json"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// An agent cannot guess episode numbers, so it needs a listing — and that
// listing must not prompt.
func TestEpisodesListsWithoutPrompting(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	prevProv := agentProvider
	agentProvider = func() provider.Provider {
		return &stubProvider{
			seasons:  []media.Season{{ID: "s1", Number: 1}, {ID: "s2", Number: 2}},
			episodes: []media.Episode{{ID: "e1", Number: 1, Title: "Pilot"}},
		}
	}
	t.Cleanup(func() { agentProvider = prevProv })

	ref, err := encodeRef(playRef{ID: "tv/show-1", Title: "Some Show", Type: "tv"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef := flagRef
	flagRef = ref
	t.Cleanup(func() { flagRef = prevRef })

	prevSeason := flagSeason
	flagSeason = 1
	t.Cleanup(func() { flagSeason = prevSeason })

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}

	var got struct {
		Schema   int `json:"schema"`
		Episodes []struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, buf.String())
	}
	if got.Schema != 1 {
		t.Fatalf("schema = %d, want 1", got.Schema)
	}
	if len(got.Episodes) != 1 || got.Episodes[0].Number != 1 || got.Episodes[0].Title != "Pilot" {
		t.Fatalf("episodes = %+v", got.Episodes)
	}
}

// Asking for episodes of a film is a caller mistake worth naming clearly.
func TestEpisodesRejectsMovieRef(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	ref, err := encodeRef(playRef{ID: "movie/x", Title: "A Film", Type: "movie"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef := flagRef
	flagRef = ref
	t.Cleanup(func() { flagRef = prevRef })

	if err := episodesRun(episodesCmd, nil); err == nil {
		t.Fatal("episodesRun accepted a movie ref, want an error")
	}
}
