// Package history manages the watch history using TSV format,
// compatible with the original lobster.sh history format.
// Uses atomic writes (temp+rename) to prevent data corruption.
package history

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"lobster/internal/config"
	"lobster/internal/media"
)

// TSV columns: id, title, type, season, episode, position, duration
const numColumns = 7

// Load reads the history file and returns all entries.
func Load() ([]media.HistoryEntry, error) {
	path, err := config.HistoryPath()
	if err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening history: %w", err)
	}
	defer f.Close()

	var entries []media.HistoryEntry
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		entry, err := parseLine(line)
		if err != nil {
			continue // Skip malformed lines
		}
		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading history: %w", err)
	}

	return entries, nil
}

// Save writes or updates an entry in the history file.
// Uses atomic write (write to temp file, then rename) to prevent corruption.
func Save(entry media.HistoryEntry) error {
	path, err := config.HistoryPath()
	if err != nil {
		return err
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating history dir: %w", err)
	}

	// Load existing entries
	entries, _ := Load()

	// Update existing entry or append new one
	found := false
	for i, e := range entries {
		if e.ID == entry.ID && e.Season == entry.Season && e.Episode == entry.Episode {
			entries[i] = entry
			found = true
			break
		}
	}
	if !found {
		entries = append(entries, entry)
	}

	// Atomic write: temp file + rename
	tmpFile, err := os.CreateTemp(dir, "history-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Write all entries
	writer := bufio.NewWriter(tmpFile)
	for _, e := range entries {
		line := formatLine(e)
		if _, err := writer.WriteString(line + "\n"); err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("writing history: %w", err)
		}
	}

	if err := writer.Flush(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("flushing history: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming history file: %w", err)
	}

	return nil
}

// Remove deletes an entry from the history.
func Remove(id string, season, episode int) error {
	entries, err := Load()
	if err != nil {
		return err
	}

	var filtered []media.HistoryEntry
	for _, e := range entries {
		if !(e.ID == id && e.Season == season && e.Episode == episode) {
			filtered = append(filtered, e)
		}
	}

	path, err := config.HistoryPath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, "history-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	writer := bufio.NewWriter(tmpFile)
	for _, e := range filtered {
		line := formatLine(e)
		if _, err := writer.WriteString(line + "\n"); err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("writing history: %w", err)
		}
	}

	if err := writer.Flush(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("flushing history: %w", err)
	}

	tmpFile.Close()
	return os.Rename(tmpPath, path)
}

// FormatForDisplay creates display strings for fzf selection from history entries.
func FormatForDisplay(entries []media.HistoryEntry) []string {
	var items []string
	for _, e := range entries {
		var display string
		if e.Type == media.TV {
			display = fmt.Sprintf("%s S%02dE%02d", e.Title, e.Season, e.Episode)
		} else {
			display = e.Title
		}
		if e.Position > 0 {
			pct := 0.0
			if e.Duration > 0 {
				pct = (e.Position / e.Duration) * 100
			}
			display += fmt.Sprintf(" [%.0f%%]", pct)
		}
		items = append(items, display)
	}
	return items
}

// parseLine parses a TSV line into a HistoryEntry.
func parseLine(line string) (media.HistoryEntry, error) {
	fields := strings.Split(line, "\t")
	if len(fields) < numColumns {
		return media.HistoryEntry{}, fmt.Errorf("expected %d columns, got %d", numColumns, len(fields))
	}

	mediaType := media.Movie
	if fields[2] == "tv" {
		mediaType = media.TV
	}

	season, _ := strconv.Atoi(fields[3])
	episode, _ := strconv.Atoi(fields[4])
	position, _ := strconv.ParseFloat(fields[5], 64)
	duration, _ := strconv.ParseFloat(fields[6], 64)

	return media.HistoryEntry{
		ID:       fields[0],
		Title:    fields[1],
		Type:     mediaType,
		Season:   season,
		Episode:  episode,
		Position: position,
		Duration: duration,
	}, nil
}

// formatLine converts a HistoryEntry to a TSV line.
func formatLine(e media.HistoryEntry) string {
	return strings.Join([]string{
		e.ID,
		e.Title,
		e.Type.String(),
		strconv.Itoa(e.Season),
		strconv.Itoa(e.Episode),
		strconv.FormatFloat(e.Position, 'f', 0, 64),
		strconv.FormatFloat(e.Duration, 'f', 0, 64),
	}, "\t")
}
