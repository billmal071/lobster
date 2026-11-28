package cmd

import (
	"encoding/json"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

func findRefs(t *testing.T, rows []media.SearchResult) []playRef {
	t.Helper()
	_, envelope := runFindForWarnings(t, "tbcpl", provider.NewTBCPL("tbcpl"), rows)
	var results []struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(envelope["results"], &results); err != nil {
		t.Fatalf("results = %s, err %v", envelope["results"], err)
	}
	refs := make([]playRef, 0, len(results))
	for _, r := range results {
		decoded, err := decodeRef(r.Ref)
		if err != nil {
			t.Fatalf("decodeRef: %v", err)
		}
		refs = append(refs, decoded)
	}
	return refs
}

func TestFindRecordsThatARowsBaseIssuedItsID(t *testing.T) {
	refs := findRefs(t, []media.SearchResult{
		{ID: "tv/219", Title: "Kyle XY", Year: "2006", Type: media.TV, Provider: "tbcpl"},
		{ID: "x-9", Title: "Kyle XY", Year: "2006", Type: media.TV, Provider: ""},
	})
	if len(refs) != 2 {
		t.Fatalf("got %d refs, want 2", len(refs))
	}
	if !refs[0].IDFromBase || refs[0].Base != "tbcpl" {
		t.Errorf("row from tbcpl: ref = %+v, want base tbcpl with id_from_base set", refs[0])
	}
	if refs[1].IDFromBase {
		t.Errorf("row with no base token: ref = %+v, want id_from_base unset, since no base names its producer", refs[1])
	}
}

func TestRefForRecordsThatTheBaseIssuedTheID(t *testing.T) {
	hostileEnv(t)
	r, err := decodeRef(refFor(provider.NewTBCPL("tbcpl"), media.SearchResult{ID: "tv/219", Title: "Kyle XY", Type: media.TV, Provider: "tbcpl"}))
	if err != nil {
		t.Fatalf("decodeRef: %v", err)
	}
	if !r.IDFromBase {
		t.Errorf("ref = %+v, want id_from_base set", r)
	}
}

func TestSearchResultNamesAnIssuerOnlyWhenTheRefRecordsOne(t *testing.T) {
	for _, tc := range []struct {
		ref  playRef
		want string
	}{
		{playRef{ID: "tv/1", Title: "T", Type: "tv", Base: "tbcpl", IDFromBase: true}, "tbcpl"},
		{playRef{ID: "tv/1", Title: "T", Type: "tv", Base: "tbcpl"}, ""},
		{playRef{ID: "tv/1", Title: "T", Type: "tv", IDFromBase: true}, ""},
	} {
		got, err := tc.ref.searchResult()
		if err != nil {
			t.Fatalf("searchResult(%+v): %v", tc.ref, err)
		}
		if got.Provider != tc.want {
			t.Errorf("searchResult(%+v).Provider = %q, want %q", tc.ref, got.Provider, tc.want)
		}
	}
}

func TestOnlyTMDBShapedPrimariesCreditALegacyRefsBase(t *testing.T) {
	legacy := playRef{ID: "tv/219", Title: "Kyle XY", Type: "tv", Base: "tbcpl"}
	if got := legacy.legacyIssuer(provider.NewTBCPL("tbcpl")); got != "tbcpl" {
		t.Errorf("TBCPL primary: legacyIssuer = %q, want tbcpl", got)
	}
	if got := legacy.legacyIssuer(provider.NewFlixHQWS("flixhq.ws")); got != "" {
		t.Errorf("FlixHQWS primary: legacyIssuer = %q, want none", got)
	}
}
