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
//
// The error it returns never carries rawURL in full, but it can quote a short
// fragment of it, and that fragment is sometimes a credential. net/url finds
// the authority by cutting rawURL at the first '/', '?' or '#', so a password
// containing one of those characters raw falls outside the userinfo and is read
// as a port: url.Parse("http://u:pa?ss@host") fails with `invalid port ":pa"
// after host`. CauseWithoutURL keeps the whole URL out of the message (see
// urlerr.go) but cannot keep that fragment out, and dropping the cause as well
// would reduce every rejection to a bare "malformed URL", which is the one
// thing a validator exists to explain.
//
// So ValidateURL does not promise credential safety, and as a library function
// it cannot: it has no way to know whether the string it was handed is a
// secret. A caller that validates credential-bearing URLs has to redact the
// error itself and decide whether to show the cause at all, as
// provider.LiveTV.httpGet does when its source cannot be taken apart safely.
func ValidateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		// Never %w the *url.Error itself: url.Error.Error prints the URL it
		// was handed in full, and rawURL may carry credentials (an Xtream
		// playlist URL, a tokenised download link). The cause it leaves
		// behind can still quote a fragment of rawURL; see the doc comment.
		return fmt.Errorf("malformed URL: %w", CauseWithoutURL(err))
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
