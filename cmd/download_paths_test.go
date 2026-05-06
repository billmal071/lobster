package cmd

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildTVSeasonDownloadDir(t *testing.T) {
	baseDir := filepath.Join("C:", "Downloads")
	got := buildTVSeasonDownloadDir(baseDir, "Bojack Horseman", 1)
	if !strings.HasSuffix(got, filepath.Join("Bojack Horseman", "S1")) {
		t.Fatalf("unexpected season dir: %s", got)
	}
}

func TestResolveDownloadBaseDirFromFlag(t *testing.T) {
	got, err := resolveDownloadBaseDir(".")
	if err != nil {
		t.Fatalf("resolveDownloadBaseDir returned error: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("expected absolute path, got %s", got)
	}
}
