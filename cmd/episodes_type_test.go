package cmd

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// seasonSpyProvider records every ID GetSeasons was asked about, so a test can
// assert on the call that should never have been made rather than only on the
// output it happened to produce.
type seasonSpyProvider struct {
	stubProvider

	mu       sync.Mutex
	askedIDs []string
}

func (s *seasonSpyProvider) GetSeasons(id string) ([]media.Season, error) {
	s.mu.Lock()
	s.askedIDs = append(s.askedIDs, id)
	s.mu.Unlock()
	return s.stubProvider.GetSeasons(id)
}

func (s *seasonSpyProvider) asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.askedIDs...)
}

// A TV ref must never be satisfied by a provider that only holds a same-titled
// film. resolver.Matches ignored media type, and resolver.Candidates falls
// back to the other type when nothing of the requested type is present
// (dedupeByType returns otherType when sameType is empty) — so the film was
// admitted and probeSeasons called GetSeasons on a movie ID. A provider that
// answers that with anything at all then had its "seasons" printed under the
// show's title, exit 0, with nothing for the caller to detect.
//
// "Spider-Man" is the honest input: the 2002 film and the 1994 animated series
// share a title exactly, so every title-based check in Matches passes and only
// the type distinguishes them.
func TestEpisodesRefusesAFallbackHoldingOnlyASameTitledMovie(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	// The primary cannot enumerate this ref — the foreign-ID case seasonSource
	// exists for — so the fallback chain is re-searched by title.
	primary := &stubProvider{seasonsErr: errors.New("unknown id")}
	withStubProvider(t, primary)

	// The only fallback holds the *film*, and would happily return seasons for
	// it if asked. Asking at all is the bug.
	film := &seasonSpyProvider{stubProvider: stubProvider{
		results: []media.SearchResult{
			{ID: "movie/557", Title: "Spider-Man", Year: "2002", Type: media.Movie},
		},
		seasons:  []media.Season{{ID: "s1", Number: 1}},
		episodes: []media.Episode{{ID: "e1", Number: 1, Title: "Not an episode of anything"}},
	}}
	prevFallbacks := agentFallbackProviders
	agentFallbackProviders = func(provider.Provider) []provider.Provider {
		return []provider.Provider{film}
	}
	t.Cleanup(func() { agentFallbackProviders = prevFallbacks })

	ref, err := encodeRef(playRef{ID: "tv/888", Title: "Spider-Man", Year: "1994", Type: "tv"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	withEpisodesFlags(t, ref, 1)

	runErr := episodesRun(episodesCmd, nil)

	if asked := film.asked(); len(asked) > 0 {
		t.Fatalf("GetSeasons was called on %v — a film's ID, for a TV ref", asked)
	}

	var ee *exitError
	if !errors.As(runErr, &ee) {
		t.Fatalf("episodesRun returned %T (%v), want a non-zero exit rather than a film's seasons", runErr, runErr)
	}
}

// The gate must not cost the fallback its purpose. This is the same shape as
// the test above with one field changed — the fallback's result is a TV series
// — and it has to keep working, because the season/episode fix that preceded
// this one made exactly this path unreachable.
func TestEpisodesStillAcceptsAFallbackHoldingTheSeries(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	primary := &stubProvider{seasonsErr: errors.New("unknown id")}
	withStubProvider(t, primary)

	series := &seasonSpyProvider{stubProvider: stubProvider{
		results: []media.SearchResult{
			{ID: "fb/spider-man-1994", Title: "Spider-Man", Year: "1994", Type: media.TV},
		},
		seasons:  []media.Season{{ID: "s1", Number: 1}},
		episodes: []media.Episode{{ID: "e1", Number: 1, Title: "Night of the Lizard"}},
	}}
	prevFallbacks := agentFallbackProviders
	agentFallbackProviders = func(provider.Provider) []provider.Provider {
		return []provider.Provider{series}
	}
	t.Cleanup(func() { agentFallbackProviders = prevFallbacks })

	ref, err := encodeRef(playRef{ID: "tv/888", Title: "Spider-Man", Year: "1994", Type: "tv"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	withEpisodesFlags(t, ref, 1)

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v (envelope: %s)", err, buf.String())
	}
	if asked := series.asked(); len(asked) != 1 || asked[0] != "fb/spider-man-1994" {
		t.Fatalf("GetSeasons asked about %v, want [fb/spider-man-1994]", asked)
	}
	if got := buf.String(); !strings.Contains(got, "Night of the Lizard") {
		t.Fatalf("envelope did not carry the fallback's episodes: %s", got)
	}
}
