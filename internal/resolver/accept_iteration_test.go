package resolver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

type perIDSP struct {
	*fakeSP
	streams map[string]*media.Stream
}

func (p *perIDSP) Watch(id, _, _, _ string) (*media.Stream, error) { return p.streams[id], nil }

// refuseDASH is an Accept hook that refuses the DASH manifest URL used by these tests.
func refuseDASH(s *media.Stream) error {
	if s.URL == "https://cdn/a.mpd" {
		return errors.New("dash")
	}
	return nil
}

// TestResolveWithProviderAcceptSkipsARefusedCandidate checks that a candidate whose stream Accept refuses is skipped in favour of the next candidate.
func TestResolveWithProviderAcceptSkipsARefusedCandidate(t *testing.T) {
	p := &perIDSP{
		fakeSP: &fakeSP{searchResults: []media.SearchResult{
			{ID: "movie/1", Title: "Foo", Type: media.Movie},
			{ID: "movie/2", Title: "Foo", Type: media.Movie},
		}},
		streams: map[string]*media.Stream{
			"movie/1": {URL: "https://cdn/a.mpd"},
			"movie/2": {URL: "https://cdn/b.m3u8"},
		},
	}
	got, _, err := resolveWithProvider(p, Request{Title: "Foo", MediaType: media.Movie, Accept: refuseDASH}, func(string, ...any) {})
	if err != nil || got == nil || got.URL != "https://cdn/b.m3u8" {
		t.Fatalf("got %v / %v, want the second candidate's HLS stream", got, err)
	}
}

type embedOnlySP struct{ provider.Provider }

func (embedOnlySP) Search(string) ([]media.SearchResult, error) {
	return []media.SearchResult{{ID: "movie/1", Title: "Foo", Type: media.Movie}}, nil
}
func (embedOnlySP) GetServers(string, string) ([]media.Server, error) {
	return []media.Server{{ID: "dash", Name: "dash"}, {ID: "hls", Name: "hls"}}, nil
}
func (embedOnlySP) GetEmbedURL(id string) (string, error) { return "https://embed/" + id, nil }

// TestResolveWithProviderAcceptSkipsARefusedServer checks that an embed server whose stream Accept refuses is skipped in favour of the next server.
func TestResolveWithProviderAcceptSkipsARefusedServer(t *testing.T) {
	prev := extractEmbed
	extractEmbed = func(embedURL, _, _ string) (*media.Stream, error) {
		if embedURL == "https://embed/dash" {
			return &media.Stream{URL: "https://cdn/a.mpd"}, nil
		}
		return &media.Stream{URL: "https://cdn/b.m3u8"}, nil
	}
	t.Cleanup(func() { extractEmbed = prev })

	got, _, err := resolveWithProvider(embedOnlySP{}, Request{Title: "Foo", MediaType: media.Movie, Accept: refuseDASH}, func(string, ...any) {})
	if err != nil || got == nil || got.URL != "https://cdn/b.m3u8" {
		t.Fatalf("got %v / %v, want the second server's HLS stream", got, err)
	}
}

// TestResolveWithProviderEveryStreamRefusedIsAnAcceptStage checks that a provider whose every stream is refused fails at the accept stage.
func TestResolveWithProviderEveryStreamRefusedIsAnAcceptStage(t *testing.T) {
	p := &perIDSP{
		fakeSP:  &fakeSP{searchResults: []media.SearchResult{{ID: "movie/1", Title: "Foo", Type: media.Movie}}},
		streams: map[string]*media.Stream{"movie/1": {URL: "https://cdn/a.mpd"}},
	}
	_, stage, err := resolveWithProvider(p, Request{Title: "Foo", MediaType: media.Movie, Accept: refuseDASH}, func(string, ...any) {})
	if err == nil || stage != StageAccept {
		t.Fatalf("stage=%q err=%v, want a failure at stage %q", stage, err, StageAccept)
	}
}

type perIDResultSP struct {
	*fakeSP
	streams map[string]*media.Stream
	errs    map[string]error
}

// Watch returns the stream and error configured for the candidate id.
func (p *perIDResultSP) Watch(id, _, _, _ string) (*media.Stream, error) {
	return p.streams[id], p.errs[id]
}

// twoWorksProvider returns a provider with a 1995 film and a second film, both titled Foo; the 1995 one streams a DASH manifest and the other, from secondYear, returns second and secondErr.
func twoWorksProvider(secondYear string, second *media.Stream, secondErr error) *perIDResultSP {
	return &perIDResultSP{
		fakeSP: &fakeSP{searchResults: []media.SearchResult{
			{ID: "movie/1", Title: "Foo", Year: "1995", Type: media.Movie},
			{ID: "movie/2", Title: "Foo", Year: secondYear, Type: media.Movie},
		}},
		streams: map[string]*media.Stream{"movie/1": {URL: "https://cdn/a.mpd"}, "movie/2": second},
		errs:    map[string]error{"movie/2": secondErr},
	}
}

// TestResolveWithProviderRefusedBestAndFailedNextIsStillAnAcceptStage checks that a refused best candidate followed by a failing one still ends at the accept stage with the refusal reason.
func TestResolveWithProviderRefusedBestAndFailedNextIsStillAnAcceptStage(t *testing.T) {
	p := twoWorksProvider("1996", nil, errors.New("status 404"))
	_, stage, err := resolveWithProvider(p, Request{Title: "Foo", Year: "1995", MediaType: media.Movie, Accept: refuseDASH}, func(string, ...any) {})
	if stage != StageAccept || err == nil || !strings.Contains(err.Error(), "dash") {
		t.Fatalf("stage=%q err=%v, want stage accept keeping the refusal reason", stage, err)
	}
}

// TestResolveRefusedBestAndFailedNextLeavesProviderHealthUnchanged checks that a refused best candidate followed by a failing one records nothing in the health store.
func TestResolveRefusedBestAndFailedNextLeavesProviderHealthUnchanged(t *testing.T) {
	h := NewHealthStore()
	r := New([]provider.Provider{twoWorksProvider("1996", nil, errors.New("status 404"))}, h, func(string, ...any) {})
	r.validate = false
	_, _, _ = r.Resolve(context.Background(), Request{Title: "Foo", Year: "1995", MediaType: media.Movie, Accept: refuseDASH})
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.records) != 0 {
		t.Fatalf("a refused best candidate plus a 404 changed provider health: %+v", h.records)
	}
}

// TestResolveWithProviderRefusalNeverFallsThroughToADifferentWork checks that after a refusal the resolver does not return a lower-ranked candidate that is a different work.
func TestResolveWithProviderRefusalNeverFallsThroughToADifferentWork(t *testing.T) {
	p := twoWorksProvider("2013", &media.Stream{URL: "https://cdn/other.m3u8"}, nil)
	got, stage, err := resolveWithProvider(p, Request{Title: "Foo", Year: "1995", MediaType: media.Movie, Accept: refuseDASH}, func(string, ...any) {})
	if got != nil || stage != StageAccept || err == nil {
		t.Fatalf("got %v stage=%q err=%v, want no stream: the 2013 film is not the 1995 one that was requested", got, stage, err)
	}
}

// TestResolveWithProviderWithoutAcceptStillFallsThroughToTheNextCandidate checks that a request with no Accept hook still falls through to the next candidate when the best one fails.
func TestResolveWithProviderWithoutAcceptStillFallsThroughToTheNextCandidate(t *testing.T) {
	p := twoWorksProvider("2013", &media.Stream{URL: "https://cdn/other.m3u8"}, nil)
	p.errs["movie/1"] = errors.New("status 404")
	got, _, err := resolveWithProvider(p, Request{Title: "Foo", Year: "1995", MediaType: media.Movie}, func(string, ...any) {})
	if err != nil || got == nil || got.URL != "https://cdn/other.m3u8" {
		t.Fatalf("got %v / %v, want the next candidate for a caller with no Accept", got, err)
	}
}

type emptyAfterSP struct {
	*fakeSP
	streams map[string]*media.Stream
}

// Watch returns the stream configured for the candidate id, which is nil for candidates without one.
func (p *emptyAfterSP) Watch(id, _, _, _ string) (*media.Stream, error) { return p.streams[id], nil }

// TestResolveWithProviderRefusalThenEmptyAnswerIsStillAnAcceptStage checks that a refusal followed by a candidate answering with no stream still ends at the accept stage with the refusal reason.
func TestResolveWithProviderRefusalThenEmptyAnswerIsStillAnAcceptStage(t *testing.T) {
	p := &emptyAfterSP{
		fakeSP: &fakeSP{searchResults: []media.SearchResult{
			{ID: "movie/1", Title: "Foo", Year: "1995", Type: media.Movie},
			{ID: "movie/2", Title: "Foo", Year: "1996", Type: media.Movie},
		}},
		streams: map[string]*media.Stream{"movie/1": {URL: "https://cdn/a.mpd"}},
	}
	got, stage, err := resolveWithProvider(p, Request{Title: "Foo", Year: "1995", MediaType: media.Movie, Accept: refuseDASH}, func(string, ...any) {})
	if got != nil || stage != StageAccept || err == nil || !strings.Contains(err.Error(), "dash") {
		t.Fatalf("got %v stage=%q err=%v, want stage accept keeping the refusal reason", got, stage, err)
	}
}

// TestResolveWithProviderRefusedServerThenEmptyServerIsStillAnAcceptStage checks that a refusal followed by an embed server returning no stream still ends at the accept stage with the refusal reason.
func TestResolveWithProviderRefusedServerThenEmptyServerIsStillAnAcceptStage(t *testing.T) {
	prev := extractEmbed
	extractEmbed = func(embedURL, _, _ string) (*media.Stream, error) {
		if embedURL == "https://embed/dash" {
			return &media.Stream{URL: "https://cdn/a.mpd"}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { extractEmbed = prev })

	got, stage, err := resolveWithProvider(embedOnlySP{}, Request{Title: "Foo", MediaType: media.Movie, Accept: refuseDASH}, func(string, ...any) {})
	if got != nil || stage != StageAccept || err == nil || !strings.Contains(err.Error(), "dash") {
		t.Fatalf("got %v stage=%q err=%v, want stage accept keeping the refusal reason", got, stage, err)
	}
}
