package cmd

import (
	"encoding/json"
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/provider"
)

type findWarning struct {
	Code            string `json:"code"`
	Base            string `json:"base"`
	Message         string `json:"message"`
	ResultsFromBase int    `json:"results_from_base"`
	ResultsTotal    int    `json:"results_total"`
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

// The other half: silent when there is nothing to warn about, which is when
// every emitted row came from the requested base. The key must be absent, not
// an empty array — a consumer written against schema 1 before this existed
// must see a byte-identical payload.
//
// A single yts row alongside a foreign one is *not* this case and must warn;
// that is TestFindWarnsWhenOnlySomeResultsCameFromTheRequestedBase.
func TestFindStaysSilentWhenEveryResultCameFromTheRequestedBase(t *testing.T) {
	warnings, envelope := runFindForWarnings(t, "yts", provider.NewYTS(), []media.SearchResult{
		{ID: "3024", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "yts"},
		{ID: "3025", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie, Provider: "yts"},
	})
	if len(warnings) != 0 {
		t.Fatalf("got warnings %+v, want none: every result came from the requested base", warnings)
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

// The common case, and the one the all-or-nothing test above cannot reach:
// the base was asked for, it answered, and most of what came back is somebody
// else's. `--base yts "breaking bad"` against live providers returns 21 rows
// of which 2 are YTS's; saying nothing there tells the caller their source
// supplied the list, which is false for 19 of the 21 rows.
//
// The input is the shape that violates the guarantee: a majority of foreign
// rows *with* the requested base present, which the base_not_used test can
// never produce.
func TestFindWarnsWhenOnlySomeResultsCameFromTheRequestedBase(t *testing.T) {
	warnings, _ := runFindForWarnings(t, "yts", provider.NewYTS(), []media.SearchResult{
		{ID: "3024", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "yts"},
		{ID: "movie/603", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie, Provider: "soap2day"},
		{ID: "movie/604", Title: "The Matrix Revolutions", Year: "2003", Type: media.Movie, Provider: "vidnest"},
	})
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want 1: %+v", len(warnings), warnings)
	}
	if warnings[0].Code != "base_partially_used" {
		t.Errorf("warning code = %q, want %q; a caller has to be able to tell a base that answered partially from one that answered nothing", warnings[0].Code, "base_partially_used")
	}
	if warnings[0].Base != "yts" {
		t.Errorf("warning base = %q, want %q", warnings[0].Base, "yts")
	}
	if warnings[0].ResultsFromBase != 1 || warnings[0].ResultsTotal != 3 {
		t.Errorf("counts = %d of %d, want 1 of 3", warnings[0].ResultsFromBase, warnings[0].ResultsTotal)
	}
}

// The two cases must stay machine-distinguishable, and the counts must be
// carried by both so a consumer parses one shape. "yts answered nothing" and
// "yts answered 1 of 3" call for different next steps: the first means try
// another source, the second means the rows are there but mostly foreign.
func TestFindDistinguishesATotallyUnusedBaseFromAPartiallyUsedOne(t *testing.T) {
	none, _ := runFindForWarnings(t, "yts", provider.NewYTS(), []media.SearchResult{
		{ID: "movie/603", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "soap2day"},
		{ID: "movie/604", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie, Provider: "vidnest"},
	})
	some, _ := runFindForWarnings(t, "yts", provider.NewYTS(), []media.SearchResult{
		{ID: "3024", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "yts"},
		{ID: "movie/604", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie, Provider: "vidnest"},
	})
	if len(none) != 1 || len(some) != 1 {
		t.Fatalf("want one warning each, got %+v and %+v", none, some)
	}
	if none[0].Code == some[0].Code {
		t.Errorf("both cases emit code %q; nothing in the payload separates a base that produced nothing from one that produced some", none[0].Code)
	}
	if none[0].ResultsFromBase != 0 || none[0].ResultsTotal != 2 {
		t.Errorf("unused-base counts = %d of %d, want 0 of 2; both codes must carry the same fields", none[0].ResultsFromBase, none[0].ResultsTotal)
	}
	if some[0].ResultsFromBase != 1 || some[0].ResultsTotal != 2 {
		t.Errorf("partial counts = %d of %d, want 1 of 2", some[0].ResultsFromBase, some[0].ResultsTotal)
	}
}

// Broadening is the normal behaviour when no source was requested, so the
// partial case must stay silent under auto too — otherwise the default
// install warns on essentially every search, which is noise, not honesty.
func TestFindDoesNotWarnOnPartialBroadeningUnderAuto(t *testing.T) {
	warnings, envelope := runFindForWarnings(t, config.BaseAuto, provider.NewSoap2Day(), []media.SearchResult{
		{ID: "movie/603", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "soap2day"},
		{ID: "movie/604", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie, Provider: "vidnest"},
	})
	if len(warnings) != 0 {
		t.Fatalf("got warnings %+v under base=auto with mixed provenance, want none", warnings)
	}
	if _, ok := envelope["warnings"]; ok {
		t.Fatalf("the warnings key is present under base=auto")
	}
}

// --limit and --type decide what the caller actually received, so the counts
// have to describe the emitted rows. Here the base produced two of three rows
// but the caller sees one of one, all of it from the base: there is nothing to
// warn about, and a count computed over the pre-limit slice would both warn
// and report a total the caller cannot see.
func TestFindCountsOnlyTheRowsTheCallerReceived(t *testing.T) {
	prevLimit := flagFindLimit
	flagFindLimit = 1
	t.Cleanup(func() { flagFindLimit = prevLimit })

	warnings, envelope := runFindForWarnings(t, "yts", provider.NewYTS(), []media.SearchResult{
		{ID: "3024", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "yts"},
		{ID: "movie/604", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie, Provider: "vidnest"},
		{ID: "3025", Title: "The Matrix Revolutions", Year: "2003", Type: media.Movie, Provider: "yts"},
	})
	if len(warnings) != 0 {
		t.Fatalf("got warnings %+v, want none: --limit left one row and it came from yts", warnings)
	}
	if _, ok := envelope["warnings"]; ok {
		t.Fatalf("the warnings key is present when every emitted row came from the requested base")
	}
}

// A base token that selects nothing at all.
//
// newProvider has no unknown-base arm: `--base sopa2day` falls through to
// MovieBox, so primaryBase becomes "moviebox" and every MovieBox row matches
// it. Counting those rows as the requested base's would make the typo look
// like a source that answered in full, and the caller would be told nothing —
// while every ref in the same response honestly says "moviebox". The input
// here is exactly that shape: the configured token names no provider, and the
// rows all come from the fall-through.
func TestFindWarnsWhenTheBaseNamesNoProviderAtAll(t *testing.T) {
	warnings, _ := runFindForWarnings(t, "sopa2day", provider.NewMovieBox(), []media.SearchResult{
		{ID: "1", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "moviebox"},
		{ID: "2", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie, Provider: "moviebox"},
	})
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want 1: %+v; --base sopa2day selected no provider, so nothing the caller received came from the base they asked for", len(warnings), warnings)
	}
	if warnings[0].Code != "base_unknown" {
		t.Errorf("warning code = %q, want %q; a base that names nothing is a different next step (fix the spelling) from a base that answered nothing (try another source)", warnings[0].Code, "base_unknown")
	}
	if warnings[0].Base != "sopa2day" {
		t.Errorf("warning base = %q, want %q", warnings[0].Base, "sopa2day")
	}
	if warnings[0].ResultsFromBase != 0 {
		t.Errorf("results_from_base = %d, want 0; no row can come from a base that selects no provider", warnings[0].ResultsFromBase)
	}
	if warnings[0].ResultsTotal != 2 {
		t.Errorf("results_total = %d, want 2", warnings[0].ResultsTotal)
	}
	if warnings[0].Message == "" {
		t.Errorf("warning has no message")
	}
}

// The alias that a naive "does the configured spelling contain the primary's
// token" test gets wrong. "1shows.org" is a real TBCPL base — newProvider
// selects TBCPL from it — yet it does not contain "tbcpl". TBCPL answering in
// full is a clean result and must stay silent.
func TestFindDoesNotCallALegitimateAliasAnUnknownBase(t *testing.T) {
	warnings, envelope := runFindForWarnings(t, "1shows.org", provider.NewTBCPL("1shows.org"), []media.SearchResult{
		{ID: "1", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "tbcpl"},
		{ID: "2", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie, Provider: "tbcpl"},
	})
	if _, ok := envelope["warnings"]; ok {
		t.Fatalf("got warnings %+v, want none: 1shows.org is a TBCPL base and TBCPL produced every row", warnings)
	}
}

// The fall-through provider named on purpose. "moviebox" selects MovieBox
// through the default arm rather than by falling through it, so it is a base
// that was used, not one that named nothing.
func TestFindTreatsAnExplicitMovieBoxBaseAsNamed(t *testing.T) {
	warnings, envelope := runFindForWarnings(t, "moviebox", provider.NewMovieBox(), []media.SearchResult{
		{ID: "1", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "moviebox"},
	})
	if _, ok := envelope["warnings"]; ok {
		t.Fatalf("got warnings %+v, want none: --base moviebox asked for MovieBox and MovieBox produced every row", warnings)
	}
}

// The substring trap. "notmoviebox" names no provider — newProvider has no
// moviebox arm at all, so it reaches MovieBox through the same default branch
// "sopa2day" does — yet it *contains* "moviebox". A containment test therefore
// reads it as an explicit MovieBox base, which costs twice over: the caller is
// told nothing about a token that selected nothing, and every ref in the
// response is stamped "notmoviebox", naming a source that does not exist.
//
// Only the fall-through token itself may be matched exactly; containment is
// what makes "notmoviebox" indistinguishable from "moviebox".
func TestFindDoesNotMistakeASubstringOfTheFallThroughForThatBase(t *testing.T) {
	warnings, envelope := runFindForWarnings(t, "notmoviebox", provider.NewMovieBox(), []media.SearchResult{
		{ID: "1", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "moviebox"},
		{ID: "2", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie, Provider: "moviebox"},
	})
	if len(warnings) != 1 || warnings[0].Code != "base_unknown" {
		t.Fatalf("got warnings %+v, want one base_unknown; --base notmoviebox selected no provider, it merely contains the fall-through's token", warnings)
	}
	if warnings[0].Base != "notmoviebox" {
		t.Errorf("warning base = %q, want %q", warnings[0].Base, "notmoviebox")
	}

	var results []struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(envelope["results"], &results); err != nil {
		t.Fatalf("results is not an array of objects: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	for i, r := range results {
		decoded, err := decodeRef(r.Ref)
		if err != nil {
			t.Fatalf("decodeRef(results[%d]): %v", i, err)
		}
		if decoded.Base != "moviebox" {
			t.Errorf("ref for row %d has base %q, want %q; MovieBox produced the row and \"notmoviebox\" names nothing, so stamping it would send a replay looking for a source that does not exist", i, decoded.Base, "moviebox")
		}
	}
}
