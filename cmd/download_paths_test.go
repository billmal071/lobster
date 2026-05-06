package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
)

func TestBuildTVSeasonDownloadDir(t *testing.T) {
	baseDir := filepath.Join("C:", "Downloads")
	got := buildTVSeasonDownloadDir(baseDir, "Bojack Horseman", 1)
	if !strings.HasSuffix(got, filepath.Join("Bojack Horseman", "S01")) {
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

func TestResolveDownloadBaseDirFromEmpty(t *testing.T) {
	previousCfg := cfg
	t.Cleanup(func() {
		cfg = previousCfg
	})
	cfg = &config.Config{DownloadDir: "."}

	got, err := resolveDownloadBaseDir("")
	if err != nil {
		t.Fatalf("resolveDownloadBaseDir returned error: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("expected absolute path from empty arg, got %s", got)
	}
}

func TestResolveDownloadOutputDirForTV(t *testing.T) {
	baseDir := filepath.Join("C:", "Users", "Elitebook", "Videos", "lobster")
	selected := media.SearchResult{
		Title: "BoJack Horseman",
		Type:  media.TV,
	}
	got := resolveDownloadOutputDir(baseDir, selected, 5)
	if !strings.HasSuffix(got, filepath.Join("BoJack Horseman", "S05")) {
		t.Fatalf("unexpected TV output dir: %s", got)
	}
}
