// Package subtitle handles subtitle filtering and staging-directory management.
// Staging directories carry a random suffix rather than a predictable name, and
// live where a sandboxed player can read them (see staging.go).
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

// seasonEpisode is a season/episode pair some text states.
type seasonEpisode struct {
	Season  int
	Episode int
}

// seasonEpisodesIn returns every season/episode pair lowercase text states.
// Patterns: s01e02, s1e2, 1x02. Both numbers are read out of the text rather
// than one of them being assumed, so text naming a season other than the one
// asked for is recognised as a statement about a different season instead of
// as no statement at all -- which is what let a zip entry named s02e05 answer
// a season 1 episode 5 request.
//
// Every occurrence of each pattern is collected, not just the first: a file
// covering two episodes states both (s01e01.s01e02), and stopping at the first
// marker left the second episode unstated as far as callers could tell.
//
// Both patterns are bounded, because release names are full of digit-x-digit
// and s-digit runs that mean resolutions and codecs rather than episodes. The
// s form takes at most three digits on either side; the x form takes at most
// two, and both of its runs must end at a non-digit. That is what keeps
// 1080x720 from reading as season 1080 episode 720, and aac5.1x264 from
// reading as season 1 episode 264.
func seasonEpisodesIn(text string) []seasonEpisode {
	var pairs []seasonEpisode
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case 's':
			season, next, ok := digitsAt(text, i+1, 3)
			if !ok || next >= len(text) || text[next] != 'e' {
				continue
			}
			episode, _, ok := digitsAt(text, next+1, 3)
			if !ok {
				continue
			}
			pairs = append(pairs, seasonEpisode{Season: season, Episode: episode})
		case 'x':
			season, ok := digitsBefore(text, i, 2)
			if !ok {
				continue
			}
			episode, _, ok := digitsAt(text, i+1, 2)
			if !ok {
				continue
			}
			pairs = append(pairs, seasonEpisode{Season: season, Episode: episode})
		}
	}
	return pairs
}

// digitsAt reads the run of digits starting at i, reporting the value and the
// index after it. A run that is empty, longer than max, or reads as zero is
// rejected rather than truncated, so a bounded pattern cannot quietly match
// the leading digits of a longer number.
func digitsAt(text string, i, max int) (val, next int, ok bool) {
	n := 0
	for ; i < len(text) && text[i] >= '0' && text[i] <= '9'; i++ {
		val = val*10 + int(text[i]-'0')
		n++
	}
	if n == 0 || n > max || val == 0 {
		return 0, i, false
	}
	return val, i, true
}

// digitsBefore is the same read running backwards from i, which is exclusive.
func digitsBefore(text string, i, max int) (val int, ok bool) {
	start := i
	for start > 0 && text[start-1] >= '0' && text[start-1] <= '9' {
		start--
	}
	if n := i - start; n == 0 || n > max {
		return 0, false
	}
	for j := start; j < i; j++ {
		val = val*10 + int(text[j]-'0')
	}
	if val == 0 {
		return 0, false
	}
	return val, true
}

// statesRequest reports whether a stated pair is the one being asked for. A
// request carrying no season matches on the episode alone, since the episode
// is the whole of what it stated.
func statesRequest(se seasonEpisode, season, episode int) bool {
	if season > 0 && se.Season != season {
		return false
	}
	return se.Episode == episode
}

// isWrongEpisode checks if text states a season/episode pair and none of the
// pairs it states is the requested one. Returns false (keep) if no marker is
// found.
//
// A stated pair other than the requested one is only disqualifying when the
// requested one is absent: a file covering s01e01 and s01e02 is the right file
// for either of them, and rejecting it for naming the neighbour as well is how
// a double episode ends up with no subtitles at all. A pair naming a different
// season disqualifies on the same terms -- stating the wrong season is a
// statement, not silence.
func isWrongEpisode(text string, season, episode int) bool {
	wrong := false
	for _, se := range seasonEpisodesIn(text) {
		if statesRequest(se, season, episode) {
			return false
		}
		wrong = true
	}
	return wrong
}

// namesEpisode is the other half of isWrongEpisode, and the two are not
// opposites: both are false for text carrying no marker at all. Telling "this
// is the episode" apart from "this says nothing" is what lets the right file be
// picked out of a season pack rather than the first one that fails to object.
func namesEpisode(text string, season, episode int) bool {
	for _, se := range seasonEpisodesIn(text) {
		if statesRequest(se, season, episode) {
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

// TempDir manages the throwaway directory downloaded subtitles are staged in.
type TempDir struct {
	path string

	// stopAlive ends the goroutine that keeps refreshing the directory's
	// liveness marker, so a session outlasting staleAfter is not swept by a
	// second lobster starting in the meantime. A run killed without reaching it
	// stops refreshing by itself, which is what keeps abandoned directories
	// collectable at all — see userdir.Keepalive.
	stopAlive func()
}

// NewTempDir creates a randomized staging directory for subtitle files in a
// location sandboxed players can actually read.
func NewTempDir() (*TempDir, error) {
	dir, stopAlive, err := newStagingDir()
	if err != nil {
		return nil, fmt.Errorf("creating subtitle staging dir: %w", err)
	}
	return &TempDir{path: dir, stopAlive: stopAlive}, nil
}

// Path returns the directory subtitle files are written into.
func (t *TempDir) Path() string { return t.path }

// Cleanup removes the staging directory and all contents. It must be called:
// unlike /tmp, the staging directory lives under $HOME and nothing else
// reclaims it.
func (t *TempDir) Cleanup() {
	removeStagingDir(t.path, t.stopAlive)
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
