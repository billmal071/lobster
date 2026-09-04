package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/provider"
)

// find must complete without ever prompting. This is the whole point of the
// command: an agent that hits fzf hangs until it is killed.
func TestFindEmitsResultsWithoutPrompting(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	prevCfg := cfg
	cfg = &config.Config{Base: "flixhq.ws"}
	t.Cleanup(func() { cfg = prevCfg })

	prevProv := agentProvider
	agentProvider = func() provider.Provider { return &stubProvider{} }
	t.Cleanup(func() { agentProvider = prevProv })

	prevSearch := agentSearch
	agentSearch = func(primary provider.Provider, fallbacks []provider.Provider, query string) ([]media.SearchResult, error) {
		return []media.SearchResult{
			{ID: "movie/watch-the-matrix-19724", Title: "The Matrix", Year: "1999", Type: media.Movie},
			{ID: "movie/watch-the-matrix-reloaded-19725", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie},
		}, nil
	}
	t.Cleanup(func() { agentSearch = prevSearch })

	if err := findRun(findCmd, []string{"the matrix"}); err != nil {
		t.Fatalf("findRun: %v", err)
	}

	var got struct {
		Schema  int `json:"schema"`
		Results []struct {
			Idx   int    `json:"idx"`
			Ref   string `json:"ref"`
			Title string `json:"title"`
			Year  string `json:"year"`
			Type  string `json:"type"`
		} `json:"results"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, buf.String())
	}
	if got.Schema != 1 {
		t.Fatalf("schema = %d, want 1", got.Schema)
	}
	if len(got.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(got.Results))
	}
	if got.Results[0].Title != "The Matrix" || got.Results[0].Year != "1999" {
		t.Fatalf("first result = %+v", got.Results[0])
	}
	if got.Results[0].Type != "movie" {
		t.Fatalf("type = %q, want movie", got.Results[0].Type)
	}

	// The ref must decode back to the same selection, including the base.
	ref, err := decodeRef(got.Results[0].Ref)
	if err != nil {
		t.Fatalf("emitted ref does not decode: %v", err)
	}
	if ref.ID != "movie/watch-the-matrix-19724" || ref.Title != "The Matrix" {
		t.Fatalf("ref = %+v", ref)
	}
	if ref.Base != "flixhq.ws" {
		t.Fatalf("ref.Base = %q, want flixhq.ws", ref.Base)
	}
}

// No results is a distinct, recoverable outcome and must not be conflated with
// "every provider is down": exit 2 tells the agent to suggest a spelling, exit
// 3 tells it to run `lobster doctor`.
//
// The earlier version of this test stubbed agentSearch to return (nil, nil) —
// a state gatherSearchResults never produces, since it turns an empty merge
// into an error. It therefore exercised a branch findRun could not reach in
// production and passed while the real binary exited 3 on every typo. Each
// case below is a shape the real gatherSearchResults can actually return;
// TestGatherSearchResultsErrorShapes pins that claim against the real one.
func TestFindMapsSearchOutcomesToExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		results  []media.SearchResult
		err      error
		findType string
		wantCode int
		wantErr  string
	}{
		{
			name:     "nothing matched",
			err:      fmt.Errorf("%w for %q", errNoResults, "zzzznotathing"),
			wantCode: exitNoResults,
			wantErr:  "no_results",
		},
		{
			name:     "nothing matched, wrapped again by a caller",
			err:      fmt.Errorf("search failed: %w", fmt.Errorf("%w for %q", errNoResults, "zzzznotathing")),
			wantCode: exitNoResults,
			wantErr:  "no_results",
		},
		{
			name:     "every provider unreachable",
			err:      fmt.Errorf("%w (searching %q)", errProvidersFailed, "zzzznotathing"),
			wantCode: exitProvidersFailed,
			wantErr:  "providers_failed",
		},
		{
			name:     "an unclassified error is still a provider failure",
			err:      errors.New("dial tcp: connection refused"),
			wantCode: exitProvidersFailed,
			wantErr:  "providers_failed",
		},
		{
			// The one way findRun still reaches len(results) == 0: a search
			// that succeeded, filtered to nothing by --type. Driven through
			// the real filter with a real non-empty result set — stubbing
			// (nil, nil) here would repeat the mistake this test was written
			// to correct, since gatherSearchResults cannot return that.
			name: "a successful search that --type filters to empty",
			results: []media.SearchResult{
				{ID: "movie/a", Title: "A Film", Type: media.Movie},
			},
			findType: "tv",
			wantCode: exitNoResults,
			wantErr:  "no_results",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostileEnv(t)
			buf := captureAgentOut(t)

			prevCfg := cfg
			cfg = &config.Config{Base: "flixhq.ws"}
			t.Cleanup(func() { cfg = prevCfg })

			prevProv := agentProvider
			agentProvider = func() provider.Provider { return &stubProvider{} }
			t.Cleanup(func() { agentProvider = prevProv })

			prevSearch := agentSearch
			agentSearch = func(provider.Provider, []provider.Provider, string) ([]media.SearchResult, error) {
				return tt.results, tt.err
			}
			t.Cleanup(func() { agentSearch = prevSearch })

			prevType := flagFindType
			flagFindType = tt.findType
			t.Cleanup(func() { flagFindType = prevType })

			err := findRun(findCmd, []string{"zzzznotathing"})
			var ee *exitError
			if !errors.As(err, &ee) {
				t.Fatalf("findRun returned %T (%v), want *exitError", err, err)
			}
			if ee.code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d (envelope: %s)", ee.code, tt.wantCode, buf.String())
			}
			if !strings.Contains(buf.String(), `"code": "`+tt.wantErr+`"`) {
				t.Fatalf("envelope = %s, want code %q", buf.String(), tt.wantErr)
			}
		})
	}
}

// The stub above is only honest if the real gatherSearchResults really does
// return these two sentinels, and really does tell the two states apart. This
// exercises the real function against stub providers.
func TestGatherSearchResultsErrorShapes(t *testing.T) {
	boom := errors.New("dial tcp: connection refused")
	// How a real provider reports an empty catalog hit: an *error* wrapping
	// provider.ErrNoResults, not an empty slice. Observed from the live
	// binary — FlixHQWS, VaPlayer, TBCPL, VidNest, Soap2Day, AniPub and
	// KimCartoon all answer a nonsense query this way.
	empty := fmt.Errorf("%w for %q", provider.ErrNoResults, "zzzznotathing")

	tests := []struct {
		name      string
		primary   *stubProvider
		fallbacks []provider.Provider
		wantIs    error
	}{
		{
			name:    "every provider answered, none had the title",
			primary: &stubProvider{},
			fallbacks: []provider.Provider{
				&stubProvider{}, &stubProvider{},
			},
			wantIs: errNoResults,
		},
		{
			name:      "primary answered emptily with no fallbacks at all",
			primary:   &stubProvider{},
			fallbacks: nil,
			wantIs:    errNoResults,
		},
		{
			name:    "primary failed but a fallback answered emptily",
			primary: &stubProvider{searchErr: boom},
			fallbacks: []provider.Provider{
				&stubProvider{searchErr: boom}, &stubProvider{},
			},
			wantIs: errNoResults,
		},
		{
			// The live shape: a typo, every provider up. This is the case the
			// binary got wrong — it exited 3 and told the agent to run doctor.
			name:    "every provider reports an empty catalog by erroring",
			primary: &stubProvider{searchErr: empty},
			fallbacks: []provider.Provider{
				&stubProvider{searchErr: empty}, &stubProvider{searchErr: empty},
			},
			wantIs: errNoResults,
		},
		{
			name:      "one provider is genuinely down, the rest report empty",
			primary:   &stubProvider{searchErr: boom},
			fallbacks: []provider.Provider{&stubProvider{searchErr: empty}},
			wantIs:    errNoResults,
		},
		{
			name:      "every provider failed",
			primary:   &stubProvider{searchErr: boom},
			fallbacks: []provider.Provider{&stubProvider{searchErr: boom}},
			wantIs:    errProvidersFailed,
		},
		{
			name:      "primary failed and there is nothing to fall back to",
			primary:   &stubProvider{searchErr: boom},
			fallbacks: nil,
			wantIs:    errProvidersFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostileEnv(t)

			results, err := gatherSearchResults(tt.primary, tt.fallbacks, "zzzznotathing")
			if results != nil {
				t.Fatalf("results = %v, want nil", results)
			}
			if !errors.Is(err, tt.wantIs) {
				t.Fatalf("gatherSearchResults error = %v, want errors.Is(..., %v)", err, tt.wantIs)
			}
			// The query must survive the wrap: the interactive path prints
			// this error verbatim.
			if !strings.Contains(err.Error(), `"zzzznotathing"`) {
				t.Fatalf("error %q does not name the query", err)
			}
		})
	}
}

// A search that does find something must not be dressed up as either failure.
func TestGatherSearchResultsSucceeds(t *testing.T) {
	hostileEnv(t)

	primary := &stubProvider{results: []media.SearchResult{
		{ID: "movie/1", Title: "The Matrix", Year: "1999", Type: media.Movie},
	}}
	results, err := gatherSearchResults(primary, nil, "the matrix")
	if err != nil {
		t.Fatalf("gatherSearchResults: %v", err)
	}
	if len(results) != 1 || results[0].Title != "The Matrix" {
		t.Fatalf("results = %+v, want the one stub row", results)
	}
}

func TestFindLimitTruncates(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	prevCfg := cfg
	cfg = &config.Config{Base: "flixhq.ws"}
	t.Cleanup(func() { cfg = prevCfg })

	prevProv := agentProvider
	agentProvider = func() provider.Provider { return &stubProvider{} }
	t.Cleanup(func() { agentProvider = prevProv })

	prevSearch := agentSearch
	agentSearch = func(provider.Provider, []provider.Provider, string) ([]media.SearchResult, error) {
		return []media.SearchResult{
			{ID: "a", Title: "A", Type: media.Movie},
			{ID: "b", Title: "B", Type: media.Movie},
			{ID: "c", Title: "C", Type: media.Movie},
		}, nil
	}
	t.Cleanup(func() { agentSearch = prevSearch })

	prevLimit := flagFindLimit
	flagFindLimit = 2
	t.Cleanup(func() { flagFindLimit = prevLimit })

	if err := findRun(findCmd, []string{"x"}); err != nil {
		t.Fatalf("findRun: %v", err)
	}
	var got struct {
		Results []struct{} `json:"results"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if len(got.Results) != 2 {
		t.Fatalf("got %d results, want 2 (limit)", len(got.Results))
	}
}

// withFindStub installs a fixed two-result search (one film, one series) and
// the --type flag for the duration of the test.
func withFindStub(t *testing.T, typ string) {
	t.Helper()
	hostileEnv(t)

	prevCfg := cfg
	cfg = &config.Config{Base: "flixhq.ws"}
	t.Cleanup(func() { cfg = prevCfg })

	prevProv := agentProvider
	agentProvider = func() provider.Provider { return &stubProvider{} }
	t.Cleanup(func() { agentProvider = prevProv })

	prevSearch := agentSearch
	agentSearch = func(provider.Provider, []provider.Provider, string) ([]media.SearchResult, error) {
		return []media.SearchResult{
			{ID: "movie/a", Title: "A Film", Type: media.Movie},
			{ID: "tv/b", Title: "A Series", Type: media.TV},
		}, nil
	}
	t.Cleanup(func() { agentSearch = prevSearch })

	prevType := flagFindType
	flagFindType = typ
	t.Cleanup(func() { flagFindType = prevType })
}

// --type was compared to the literal "tv" and everything else fell through to
// "movie", so `--type series` or `--type show` silently filtered to films and
// reported success — the agent has no way to notice. Name the mistake.
func TestFindRejectsUnknownType(t *testing.T) {
	withFindStub(t, "series")
	buf := captureAgentOut(t)

	err := findRun(findCmd, []string{"x"})
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("findRun returned %T (%v), want *exitError", err, err)
	}
	if ee.code != exitUsage {
		t.Fatalf("exit code = %d, want %d", ee.code, exitUsage)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, buf.String())
	}
	if _, ok := got["results"]; ok {
		t.Fatalf("an unrecognised --type still produced results: %q", buf.String())
	}
}

// "TV" and "Movie" are what a human (or an agent echoing the user) naturally
// writes; case is not a mistake worth failing on.
func TestFindTypeIsCaseInsensitive(t *testing.T) {
	for _, typ := range []string{"TV", "Tv", "tv"} {
		t.Run(typ, func(t *testing.T) {
			withFindStub(t, typ)
			buf := captureAgentOut(t)

			if err := findRun(findCmd, []string{"x"}); err != nil {
				t.Fatalf("findRun: %v", err)
			}
			var got struct {
				Results []struct {
					Title string `json:"title"`
					Type  string `json:"type"`
				} `json:"results"`
			}
			if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
				t.Fatalf("bad JSON: %v (%q)", err, buf.String())
			}
			if len(got.Results) != 1 || got.Results[0].Type != "tv" {
				t.Fatalf("results = %+v, want only the series", got.Results)
			}
		})
	}
}

// The movie side of the same filter must keep working, including mixed case.
func TestFindTypeMovieFilters(t *testing.T) {
	withFindStub(t, "Movie")
	buf := captureAgentOut(t)

	if err := findRun(findCmd, []string{"x"}); err != nil {
		t.Fatalf("findRun: %v", err)
	}
	var got struct {
		Results []struct {
			Type string `json:"type"`
		} `json:"results"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, buf.String())
	}
	if len(got.Results) != 1 || got.Results[0].Type != "movie" {
		t.Fatalf("results = %+v, want only the film", got.Results)
	}
}
