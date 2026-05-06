package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

func resolveDownloadBaseDir(downloadArg string) (string, error) {
	if strings.TrimSpace(downloadArg) != "" {
		return filepath.Abs(downloadArg)
	}
	return cfg.ExpandDownloadDir()
}

func buildTVSeasonDownloadDir(baseDir, showTitle string, seasonNumber int) string {
	showDir := httputil.SanitizeFilename(showTitle)
	seasonDir := fmt.Sprintf("S%02d", seasonNumber)
	return filepath.Join(baseDir, showDir, seasonDir)
}

func resolveDownloadOutputDir(baseDir string, selected media.SearchResult, season int) string {
	if selected.Type == media.TV && season > 0 {
		return buildTVSeasonDownloadDir(baseDir, selected.Title, season)
	}
	return baseDir
}
