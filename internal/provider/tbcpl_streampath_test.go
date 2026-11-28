package provider

import "testing"

// A legacy ref's TMDB-shaped ID reaches Watch with no search or season listing
// behind it, so the stream path must come from the IDs alone.
func TestTBCPLStreamPathNeedsNoCachedCatalog(t *testing.T) {
	got, err := tbcplStreamPath("tv/219", "219:2:15")
	if err != nil || got != "tv/219/2/15" {
		t.Fatalf("tbcplStreamPath = %q, %v; want tv/219/2/15", got, err)
	}
	if !MintsTMDBShapedIDs(NewTBCPL("tbcpl")) {
		t.Fatal("TBCPL must declare TMDB-shaped IDs")
	}
}
