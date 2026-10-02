package provider

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// recordingDoer captures the GraphQL request bodies AllAnime sends, so a test
// can assert on the variables rather than on what the canned answer happens to
// contain.
type recordingDoer struct {
	body string

	mu     sync.Mutex
	bodies []string
}

func (d *recordingDoer) Do(r *http.Request) (*http.Response, error) {
	b := ""
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		b = string(raw)
	}
	d.mu.Lock()
	d.bodies = append(d.bodies, b)
	d.mu.Unlock()
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(d.body))}, nil
}

const allanimeKamuiEdges = `{"data":{"shows":{"edges":[
  {"_id":"6a4c603bca026efe66e51ae8","name":"Ushiro no Shoumen Kamui-san","englishName":"KAMUI: He's Behind You","availableEpisodes":{"sub":12,"dub":3}}
]}}}`

// The adult filter follows the setting, in both of Search's queries.
//
// The second query is the subtitle-stripped retry, and it is the one worth
// pinning: a title like "KAMUI: He's Behind You" finds nothing under its full
// name and is reached only through the base-title retry, so a retry that kept
// the filter on would leave exactly the titles this setting exists for still
// invisible.
func TestAllAnimeSearchSendsTheConfiguredAdultFilterOnEveryQuery(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[allow], func(t *testing.T) {
			// An empty first answer forces the base-title retry, so both
			// queries are observed in one run.
			d := &recordingDoer{body: `{"data":{"shows":{"edges":[]}}}`}
			a := NewAllAnime(false)
			a.client = d
			a.SetAllowAdult(allow)
			if got := a.AllowAdult(); got != allow {
				t.Fatalf("AllowAdult() = %v after SetAllowAdult(%v)", got, allow)
			}
			_, _ = a.Search("KAMUI: He's Behind You")

			d.mu.Lock()
			bodies := append([]string(nil), d.bodies...)
			d.mu.Unlock()
			if len(bodies) != 2 {
				t.Fatalf("AllAnime sent %d queries, want 2 (the full title and the base-title retry); the fixture is not reaching the retry", len(bodies))
			}
			want := `"allowAdult":false`
			if allow {
				want = `"allowAdult":true`
			}
			for i, b := range bodies {
				if !strings.Contains(b, want) {
					t.Errorf("query %d does not carry %s; variables were %s", i, want, b)
				}
			}
			// allowUnknown is a separate filter and does nothing for this —
			// all four combinations were probed and only allowAdult changed
			// the answer — so it must not be flipped along with it.
			for i, b := range bodies {
				if !strings.Contains(b, `"allowUnknown":false`) {
					t.Errorf("query %d changed allowUnknown; it is a different filter and does nothing here: %s", i, b)
				}
			}
		})
	}
}

// Off is the default. The filter leaks adult titles into suggestive ordinary
// searches ("love" gained two hentai entries over a 40-row cap), so a user who
// has not asked for it must not get it.
func TestAllAnimeAdultFilterIsOffUnlessAsked(t *testing.T) {
	if NewAllAnime(false).AllowAdult() {
		t.Fatal("a freshly constructed AllAnime lifts the adult filter; it must be opt-in")
	}
	d := &recordingDoer{body: allanimeKamuiEdges}
	a := NewAllAnime(false)
	a.client = d
	if _, err := a.Search("KAMUI: He's Behind You"); err != nil {
		t.Fatalf("Search: %v", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, b := range d.bodies {
		if !strings.Contains(b, `"allowAdult":false`) {
			t.Errorf("query %d lifted the adult filter without being asked: %s", i, b)
		}
	}
}
