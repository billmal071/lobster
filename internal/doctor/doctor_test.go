package doctor

import (
	"errors"
	"strings"
	"testing"

	"lobster/internal/media"
)

// fakeProvider stands in for a real one. Each stage can be made to fail so the
// diagnosis can be checked against a known cause.
type fakeProvider struct {
	name      string
	results   []media.SearchResult
	searchErr error
	servers   []media.Server
	serverErr error
	embed     string
	embedErr  error
	stream    *media.Stream
	watchErr  error
	isStream  bool
}

func (f *fakeProvider) Search(string) ([]media.SearchResult, error) { return f.results, f.searchErr }
func (f *fakeProvider) GetDetails(string) (*media.ContentDetail, error) { return nil, nil }
func (f *fakeProvider) GetSeasons(string) ([]media.Season, error)       { return nil, nil }
func (f *fakeProvider) GetEpisodes(string, string) ([]media.Episode, error) {
	return nil, nil
}
func (f *fakeProvider) GetServers(string, string) ([]media.Server, error) {
	return f.servers, f.serverErr
}
func (f *fakeProvider) GetEmbedURL(string) (string, error) { return f.embed, f.embedErr }
func (f *fakeProvider) Trending(media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}
func (f *fakeProvider) Recent(media.MediaType) ([]media.SearchResult, error) { return nil, nil }

type fakeStreamProvider struct{ fakeProvider }

func (f *fakeStreamProvider) Watch(string, string, string, string) (*media.Stream, error) {
	return f.stream, f.watchErr
}

func oneResult() []media.SearchResult {
	return []media.SearchResult{{ID: "movie/1", Title: "Foo", Type: media.Movie, Year: "2012"}}
}

// The whole point is naming the stage that broke. "Provider is down" is what
// the user already knows; "search works, embeds fail" is what makes a repair
// findable.
func TestCheckReportsSearchFailure(t *testing.T) {
	got := Check("Fake", &fakeProvider{searchErr: errors.New("status 522")}, "foo")
	if got.OK {
		t.Fatal("want failure")
	}
	if got.Stage != StageSearch {
		t.Errorf("stage = %q, want %q", got.Stage, StageSearch)
	}
	if !strings.Contains(got.Detail, "522") {
		t.Errorf("detail should carry the cause, got %q", got.Detail)
	}
}

// A search that succeeds but returns nothing is a different failure from one
// that errors: the endpoint is alive and the parse or the query is wrong.
func TestCheckDistinguishesEmptySearchFromSearchError(t *testing.T) {
	got := Check("Fake", &fakeProvider{results: nil}, "foo")
	if got.OK {
		t.Fatal("want failure")
	}
	if got.Stage != StageSearch {
		t.Errorf("stage = %q, want %q", got.Stage, StageSearch)
	}
	if !strings.Contains(strings.ToLower(got.Detail), "no results") {
		t.Errorf("detail should say the search was empty, got %q", got.Detail)
	}
}

func TestCheckReportsServerStageFailure(t *testing.T) {
	got := Check("Fake", &fakeProvider{results: oneResult(), serverErr: errors.New("404")}, "foo")
	if got.Stage != StageServers {
		t.Errorf("stage = %q, want %q", got.Stage, StageServers)
	}
}

func TestCheckReportsEmbedStageFailure(t *testing.T) {
	p := &fakeProvider{
		results:  oneResult(),
		servers:  []media.Server{{Name: "S1", ID: "1"}},
		embedErr: errors.New("no embed id"),
	}
	got := Check("Fake", p, "foo")
	if got.Stage != StageEmbed {
		t.Errorf("stage = %q, want %q", got.Stage, StageEmbed)
	}
}

// A StreamProvider skips the embed hop entirely, so its failures must be
// attributed to watch rather than to a stage it never runs.
func TestCheckStreamProviderReportsWatchFailure(t *testing.T) {
	p := &fakeStreamProvider{fakeProvider{results: oneResult(), isStream: true}}
	p.watchErr = errors.New("all candidates failed")
	got := Check("Fake", p, "foo")
	if got.Stage != StageWatch {
		t.Errorf("stage = %q, want %q", got.Stage, StageWatch)
	}
}

func TestCheckHealthyStreamProvider(t *testing.T) {
	p := &fakeStreamProvider{fakeProvider{results: oneResult()}}
	p.stream = &media.Stream{URL: "https://cdn/x.m3u8"}
	got := Check("Fake", p, "foo")
	if !got.OK {
		t.Fatalf("want OK, got stage=%q detail=%q", got.Stage, got.Detail)
	}
	if got.Stage != StageOK {
		t.Errorf("stage = %q, want %q", got.Stage, StageOK)
	}
}

// A magnet is a legitimate result: YTS never returns an http URL.
func TestCheckAcceptsMagnetAsHealthy(t *testing.T) {
	p := &fakeStreamProvider{fakeProvider{results: oneResult()}}
	p.stream = &media.Stream{URL: "magnet:?xt=urn:btih:abc"}
	if got := Check("Fake", p, "foo"); !got.OK {
		t.Errorf("magnet should count as a resolved stream, got %q/%q", got.Stage, got.Detail)
	}
}

// An embed provider that reaches a URL has done all it can here; extraction is
// a separate subsystem and probing it would make doctor a playback test.
func TestCheckEmbedProviderOKAtEmbedURL(t *testing.T) {
	p := &fakeProvider{
		results: oneResult(),
		servers: []media.Server{{Name: "S1", ID: "1"}},
		embed:   "https://host/embed/x",
	}
	got := Check("Fake", p, "foo")
	if !got.OK {
		t.Errorf("want OK, got stage=%q detail=%q", got.Stage, got.Detail)
	}
}

func TestCheckRecordsLatency(t *testing.T) {
	p := &fakeStreamProvider{fakeProvider{results: oneResult()}}
	p.stream = &media.Stream{URL: "https://cdn/x.m3u8"}
	if got := Check("Fake", p, "foo"); got.Latency < 0 {
		t.Error("latency must be recorded")
	}
}

// Anime providers index a different catalogue, so probing them with a live-
// action title reports "broken" for a provider that is fine. Each entry can
// carry its own probe query.
func TestCheckAllUsesPerProviderQuery(t *testing.T) {
	var gotDefault, gotOverride string
	def := &recordingProvider{seen: &gotDefault}
	ovr := &recordingProvider{seen: &gotOverride}

	CheckAll([]Named{
		{Name: "Default", Provider: def},
		{Name: "Anime", Provider: ovr, Query: "Naruto"},
	}, "The Matrix")

	if gotDefault != "The Matrix" {
		t.Errorf("default query = %q, want the shared one", gotDefault)
	}
	if gotOverride != "Naruto" {
		t.Errorf("override query = %q, want Naruto", gotOverride)
	}
}

type recordingProvider struct {
	fakeProvider
	seen *string
}

func (r *recordingProvider) Search(q string) ([]media.SearchResult, error) {
	*r.seen = q
	return oneResult(), nil
}
