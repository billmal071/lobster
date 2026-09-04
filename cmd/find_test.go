package cmd

import (
	"encoding/json"
	"errors"
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
// "every provider is down".
func TestFindNoResultsExitsTwo(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	prevCfg := cfg
	cfg = &config.Config{Base: "flixhq.ws"}
	t.Cleanup(func() { cfg = prevCfg })

	prevProv := agentProvider
	agentProvider = func() provider.Provider { return &stubProvider{} }
	t.Cleanup(func() { agentProvider = prevProv })

	prevSearch := agentSearch
	agentSearch = func(provider.Provider, []provider.Provider, string) ([]media.SearchResult, error) {
		return nil, nil
	}
	t.Cleanup(func() { agentSearch = prevSearch })

	err := findRun(findCmd, []string{"zzzznotathing"})
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("findRun returned %T (%v), want *exitError", err, err)
	}
	if ee.code != exitNoResults {
		t.Fatalf("exit code = %d, want %d", ee.code, exitNoResults)
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
