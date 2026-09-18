package cmd

import (
	"encoding/json"
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/provider"
)

type findWarning struct {
	Code    string `json:"code"`
	Base    string `json:"base"`
	Message string `json:"message"`
}

// runFindForWarnings runs findRun over stubbed results and returns the decoded
// envelope's warnings plus the raw payload keys.
func runFindForWarnings(t *testing.T, base string, primary provider.Provider, rows []media.SearchResult) ([]findWarning, map[string]json.RawMessage) {
	t.Helper()
	hostileEnv(t)
	buf := captureAgentOut(t)

	prevCfg := cfg
	cfg = &config.Config{Base: base}
	t.Cleanup(func() { cfg = prevCfg })

	prevProv := agentProvider
	agentProvider = func() provider.Provider { return primary }
	t.Cleanup(func() { agentProvider = prevProv })

	prevSearch := agentSearch
	agentSearch = func(provider.Provider, []provider.Provider, string) ([]media.SearchResult, error) {
		return rows, nil
	}
	t.Cleanup(func() { agentSearch = prevSearch })

	if err := findRun(findCmd, []string{"the matrix"}); err != nil {
		t.Fatalf("findRun: %v", err)
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &envelope); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, buf.String())
	}
	var warnings []findWarning
	if raw, ok := envelope["warnings"]; ok {
		if err := json.Unmarshal(raw, &warnings); err != nil {
			t.Fatalf("warnings is not an array of objects: %v (%s)", err, raw)
		}
	}
	return warnings, envelope
}

// The guarantee: when a base was asked for and nothing the caller received
// came from it, the JSON says so — without -x, and without a TTY.
//
// Broadening past the requested base was announced only by debugf and by a
// spinner that ui.spinnerVisible suppresses when stderr is not a terminal, so
// the one caller find exists for — an agent — saw no signal whatsoever. The
// input here is the shape that violates the guarantee: --base yts is asked
// for, and every row came from a scraper.
func TestFindWarnsWhenTheRequestedBaseProducedNothing(t *testing.T) {
	warnings, _ := runFindForWarnings(t, "yts", provider.NewYTS(), []media.SearchResult{
		{ID: "movie/603", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "soap2day"},
		{ID: "movie/604", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie, Provider: "vidnest"},
	})
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want 1: %+v", len(warnings), warnings)
	}
	if warnings[0].Code != "base_not_used" {
		t.Errorf("warning code = %q, want %q", warnings[0].Code, "base_not_used")
	}
	if warnings[0].Base != "yts" {
		t.Errorf("warning base = %q, want %q; the field has to name which base went unused", warnings[0].Base, "yts")
	}
	if warnings[0].Message == "" {
		t.Errorf("warning has no message")
	}
}

// The other half: silent when there is nothing to warn about. The key must be
// absent, not an empty array — a consumer written against schema 1 before this
// existed must see a byte-identical payload.
func TestFindStaysSilentWhenTheRequestedBaseAnswered(t *testing.T) {
	warnings, envelope := runFindForWarnings(t, "yts", provider.NewYTS(), []media.SearchResult{
		{ID: "3024", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "yts"},
		{ID: "movie/604", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie, Provider: "vidnest"},
	})
	if len(warnings) != 0 {
		t.Fatalf("got warnings %+v, want none: the requested base produced a row", warnings)
	}
	if _, ok := envelope["warnings"]; ok {
		t.Fatalf("the warnings key is present with nothing to warn about; it must be absent so an existing consumer sees an unchanged payload")
	}
}

// "auto" is the absence of a preference, not a request for a source, so
// broadening past it is the documented behaviour rather than a surprise. The
// default install must not emit a warning on every single search.
func TestFindDoesNotWarnUnderAuto(t *testing.T) {
	warnings, envelope := runFindForWarnings(t, config.BaseAuto, provider.NewSoap2Day(), []media.SearchResult{
		{ID: "movie/604", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "vidnest"},
	})
	if len(warnings) != 0 {
		t.Fatalf("got warnings %+v under base=auto, want none", warnings)
	}
	if _, ok := envelope["warnings"]; ok {
		t.Fatalf("the warnings key is present under base=auto")
	}
}

// The warning is about what the caller received, so --limit counts. Truncating
// away the only row the requested base produced leaves a response in which
// nothing came from that base, and saying otherwise would be a claim about
// rows the caller cannot see.
func TestFindWarnsWhenLimitTruncatesAwayTheBasesOnlyRow(t *testing.T) {
	prevLimit := flagFindLimit
	flagFindLimit = 1
	t.Cleanup(func() { flagFindLimit = prevLimit })

	warnings, _ := runFindForWarnings(t, "yts", provider.NewYTS(), []media.SearchResult{
		{ID: "movie/603", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "soap2day"},
		{ID: "3024", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie, Provider: "yts"},
	})
	if len(warnings) != 1 || warnings[0].Code != "base_not_used" {
		t.Fatalf("got warnings %+v, want one base_not_used: --limit cut the only yts row, so nothing the caller received came from yts", warnings)
	}
}
