//go:build integration

package cmd

import (
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// TestFallbackSearch verifies that VaPlayer can search and return results.
// Requires network access: go test -tags=integration -run TestFallbackSearch ./cmd/...
func TestFallbackSearch(t *testing.T) {
	p := provider.NewVaPlayer()
	results, err := p.Search("dark knight")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result for 'dark knight'")
	}

	found := false
	for _, r := range results {
		t.Logf("result: %s (ID: %s, type: %s)", r.Title, r.ID, r.Type)
		if r.Type == media.Movie {
			found = true
		}
	}
	if !found {
		t.Fatal("expected at least one movie result")
	}
}

// TestFallbackWatch resolves a stream for The Dark Knight (TMDB 155).
// Requires network access: go test -tags=integration -run TestFallbackWatch ./cmd/...
func TestFallbackWatch(t *testing.T) {
	p := provider.NewVaPlayer()

	stream, err := p.Watch("155", "", "Default", "1080")
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}
	if stream.URL == "" {
		t.Fatal("expected non-empty stream URL")
	}
	t.Logf("stream URL: %s (quality: %s)", stream.URL, stream.Quality)
}
