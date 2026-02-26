package httputil

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// validIDPattern matches alphanumeric IDs with hyphens and slashes (provider content IDs).
	validIDPattern = regexp.MustCompile(`^[a-zA-Z0-9/_-]+$`)

	// numericIDPattern matches purely numeric IDs.
	numericIDPattern = regexp.MustCompile(`^[0-9]+$`)
)

// ValidateURL checks that a URL is well-formed and uses HTTPS.
func ValidateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("malformed URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("only HTTPS URLs are allowed, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("URL has no host")
	}
	return nil
}

// ValidateID checks that a provider content ID contains only safe characters.
func ValidateID(id string) error {
	if id == "" {
		return fmt.Errorf("ID cannot be empty")
	}
	if len(id) > 256 {
		return fmt.Errorf("ID too long: %d characters", len(id))
	}
	if !validIDPattern.MatchString(id) {
		return fmt.Errorf("ID contains invalid characters: %q", id)
	}
	if strings.Contains(id, "..") {
		return fmt.Errorf("ID contains path traversal: %q", id)
	}
	return nil
}

// ValidateNumericID checks that an ID is purely numeric.
func ValidateNumericID(id string) error {
	if id == "" {
		return fmt.Errorf("numeric ID cannot be empty")
	}
	if !numericIDPattern.MatchString(id) {
		return fmt.Errorf("expected numeric ID, got %q", id)
	}
	return nil
}

// SanitizeFilename removes path traversal and dangerous characters from a filename.
// Returns just the base name, stripped of any directory components.
func SanitizeFilename(name string) string {
	// Take only the base name to strip directory components
	name = filepath.Base(name)

	// Replace characters that are problematic on various OSes
	replacer := strings.NewReplacer(
		"..", "_",
		"/", "_",
		"\\", "_",
		"\x00", "",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	name = replacer.Replace(name)

	if name == "" || name == "." || name == ".." {
		return "untitled"
	}

	return name
}

// SafeDownloadPath resolves and validates a download path ensuring it stays within the target directory.
func SafeDownloadPath(dir, filename string) (string, error) {
	sanitized := SanitizeFilename(filename)

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving directory: %w", err)
	}

	full := filepath.Join(absDir, sanitized)

	// Resolve symlinks and verify containment
	resolved, err := filepath.Abs(full)
	if err != nil {
		return "", fmt.Errorf("resolving path: %w", err)
	}

	if !strings.HasPrefix(resolved, absDir+string(filepath.Separator)) && resolved != absDir {
		return "", fmt.Errorf("path traversal detected: %q escapes %q", resolved, absDir)
	}

	return resolved, nil
}

// EncodeQuery encodes a search query for inclusion in FlixHQ search URLs.
// FlixHQ expects hyphen-separated words in the path (e.g., /search/star-wars).
func EncodeQuery(query string) string {
	words := strings.Fields(query)
	return url.PathEscape(strings.Join(words, "-"))
}

// BuildURL constructs a URL from base and path components, encoding each path segment.
func BuildURL(base string, pathSegments ...string) string {
	u := strings.TrimRight(base, "/")
	for _, seg := range pathSegments {
		u += "/" + url.PathEscape(seg)
	}
	return u
}
