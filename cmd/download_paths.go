package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"lobster/internal/httputil"
)

func resolveDownloadBaseDir(downloadArg string) (string, error) {
	if downloadArg == downloadFlagDefaultSentinel {
		return cfg.ExpandDownloadDir()
	}
	if strings.TrimSpace(downloadArg) != "" {
		return filepath.Abs(downloadArg)
	}
	return cfg.ExpandDownloadDir()
}

func buildTVSeasonDownloadDir(baseDir, showTitle string, seasonNumber int) string {
	showDir := httputil.SanitizeFilename(showTitle)
	seasonDir := fmt.Sprintf("S%d", seasonNumber)
	return filepath.Join(baseDir, showDir, seasonDir)
}
