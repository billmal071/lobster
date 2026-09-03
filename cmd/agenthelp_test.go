package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"lobster/internal/media"
)

// hostileEnv makes any attempt to prompt the user fail the test rather than
// hang it. Injecting ui.Select alone is not enough: ui.Input execs fzf
// directly, ui.SelectWithTimeout reads os.Stdin raw before reaching Select,
// and tui.StartApp is Bubble Tea rather than fzf. Closing stdin and shimming
// fzf on PATH catches all of those, including any added later.
func hostileEnv(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shim relies on a POSIX shell; the guarantee is covered on unix CI")
	}

	dir := t.TempDir()
	shim := filepath.Join(dir, "fzf")
	script := "#!/bin/sh\necho 'fzf was invoked by a non-interactive command' >&2\nexit 97\n"
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fzf shim: %v", err)
	}
	t.Setenv("PATH", dir)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	w.Close() // reads return EOF immediately instead of blocking
	prev := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = prev
		r.Close()
	})
}

// stubProvider is a provider.Provider that answers from fixed data and never
// touches the network.
type stubProvider struct {
	results   []media.SearchResult
	seasons   []media.Season
	episodes  []media.Episode
	searchErr error
}

func (s *stubProvider) Search(string) ([]media.SearchResult, error) {
	return s.results, s.searchErr
}
func (s *stubProvider) GetDetails(string) (*media.ContentDetail, error) {
	return &media.ContentDetail{}, nil
}
func (s *stubProvider) GetSeasons(string) ([]media.Season, error) { return s.seasons, nil }
func (s *stubProvider) GetEpisodes(string, string) ([]media.Episode, error) {
	return s.episodes, nil
}
func (s *stubProvider) GetServers(string, string) ([]media.Server, error) {
	return []media.Server{{ID: "srv1", Name: "stub"}}, nil
}
func (s *stubProvider) GetEmbedURL(string) (string, error) { return "https://example.invalid/e", nil }
func (s *stubProvider) Trending(media.MediaType) ([]media.SearchResult, error) {
	return s.results, nil
}
func (s *stubProvider) Recent(media.MediaType) ([]media.SearchResult, error) {
	return s.results, nil
}
