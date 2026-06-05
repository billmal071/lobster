// Package subtitle handles subtitle filtering and secure temp file management.
// Uses os.MkdirTemp with random suffixes instead of predictable /tmp/lobster/ paths.
package subtitle

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

// FilterByEpisode removes subtitles that are clearly for a different episode
// based on filename patterns like S01E03. Keeps subs with no episode marker.
func FilterByEpisode(subtitles []media.Subtitle, season, episode int) []media.Subtitle {
	if season == 0 && episode == 0 {
		return subtitles
	}

	var filtered []media.Subtitle
	for _, sub := range subtitles {
		// Check URL and label for episode markers
		text := strings.ToLower(sub.URL + " " + sub.Label)
		if isWrongEpisode(text, season, episode) {
			continue
		}
		filtered = append(filtered, sub)
	}
	return filtered
}

// isWrongEpisode checks if text contains an episode marker for a different episode.
// Returns false (keep) if no marker is found.
func isWrongEpisode(text string, season, episode int) bool {
	// Match patterns: s01e02, S01E02, 1x02
	patterns := []string{
		fmt.Sprintf("s%02de", season),
		fmt.Sprintf("s%de", season),
		fmt.Sprintf("%dx", season),
	}
	for _, prefix := range patterns {
		idx := strings.Index(text, prefix)
		if idx < 0 {
			continue
		}
		// Extract episode number after the prefix
		rest := text[idx+len(prefix):]
		epNum := 0
		for _, c := range rest {
			if c >= '0' && c <= '9' {
				epNum = epNum*10 + int(c-'0')
			} else {
				break
			}
		}
		if epNum > 0 && epNum != episode {
			return true
		}
	}
	return false
}

// Filter returns subtitles matching the preferred language (case-insensitive).
func Filter(subtitles []media.Subtitle, language string) []media.Subtitle {
	if language == "" {
		return subtitles
	}

	lang := strings.ToLower(language)
	var matched []media.Subtitle

	for _, sub := range subtitles {
		if strings.Contains(strings.ToLower(sub.Language), lang) ||
			strings.Contains(strings.ToLower(sub.Label), lang) {
			matched = append(matched, sub)
		}
	}

	return matched
}

// BestMatch returns the best matching subtitle for the given language.
// Prefers exact language match, then partial match, then SDH variants.
func BestMatch(subtitles []media.Subtitle, language string) *media.Subtitle {
	filtered := Filter(subtitles, language)
	if len(filtered) == 0 {
		return nil
	}

	lang := strings.ToLower(language)

	// Prefer non-SDH exact match
	for _, sub := range filtered {
		label := strings.ToLower(sub.Label)
		if strings.Contains(label, lang) && !strings.Contains(label, "sdh") {
			return &sub
		}
	}

	// Fall back to first match
	return &filtered[0]
}

// TempDir manages a secure temporary directory for subtitle files.
type TempDir struct {
	path string
}

// NewTempDir creates a randomized temporary directory for subtitle files.
func NewTempDir() (*TempDir, error) {
	dir, err := os.MkdirTemp("", "lobster-subs-*")
	if err != nil {
		return nil, fmt.Errorf("creating subtitle temp dir: %w", err)
	}
	return &TempDir{path: dir}, nil
}

// Cleanup removes the temporary directory and all contents.
func (t *TempDir) Cleanup() {
	if t.path != "" {
		os.RemoveAll(t.path)
	}
}

// Download fetches a subtitle file to the temp directory and returns the local path.
func (t *TempDir) Download(sub media.Subtitle) (string, error) {
	if err := httputil.ValidateURL(sub.URL); err != nil {
		return "", fmt.Errorf("invalid subtitle URL: %w", err)
	}

	// Determine filename from URL
	filename := "subtitle.vtt"
	if parts := strings.Split(sub.URL, "/"); len(parts) > 0 {
		last := parts[len(parts)-1]
		if idx := strings.Index(last, "?"); idx != -1 {
			last = last[:idx]
		}
		if last != "" {
			filename = httputil.SanitizeFilename(last)
		}
	}

	localPath := filepath.Join(t.path, filename)

	client := httputil.NewClient()
	resp, err := client.Get(sub.URL)
	if err != nil {
		return "", fmt.Errorf("downloading subtitle: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("subtitle download returned status %d", resp.StatusCode)
	}

	f, err := os.Create(localPath)
	if err != nil {
		return "", fmt.Errorf("creating subtitle file: %w", err)
	}
	defer f.Close()

	// Limit subtitle file size to 10MB
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 10*1024*1024)); err != nil {
		return "", fmt.Errorf("writing subtitle file: %w", err)
	}

	return localPath, nil
}
