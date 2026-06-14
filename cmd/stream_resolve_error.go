package cmd

import (
	"errors"
	"fmt"
	"strings"

	"lobster/internal/provider"
)

// providerAttempt records one failed stream resolution try.
type providerAttempt struct {
	Name string // e.g. MovieBox
	Role string // primary | fallback
	Err  error
}

// streamResolveError is returned when every provider fails for an episode.
type streamResolveError struct {
	Episode string
	Tries   []providerAttempt
}

func (e *streamResolveError) Error() string {
	return formatStreamResolveConcise(e.Episode, e.Tries)
}

func providerDisplayName(p provider.Provider) string {
	switch p.(type) {
	case *provider.MovieBox:
		return "MovieBox"
	case *provider.Soap2Day:
		return "Soap2Day"
	case *provider.Consumet:
		return "Consumet"
	case *provider.TBCPL:
		return "TBCPL"
	case *provider.VaPlayer:
		return "VaPlayer"
	case *provider.VidNest:
		return "VidNest"
	case *provider.FlixHQ:
		return "FlixHQ"
	case *provider.FlixHQWS:
		return "FlixHQ.ws"
	case *provider.KimCartoon:
		return "KimCartoon"
	default:
		return fmt.Sprintf("%T", p)
	}
}

func newStreamResolveError(episode string, tries []providerAttempt) error {
	if len(tries) == 0 {
		return errors.New("no stream providers available")
	}
	return &streamResolveError{Episode: episode, Tries: tries}
}

// formatStreamResolveConcise is the user-facing error. Per-provider detail lives in -x logs.
func formatStreamResolveConcise(episode string, tries []providerAttempt) string {
	parts := make([]string, len(tries))
	for i, t := range tries {
		parts[i] = fmt.Sprintf("%s: %s", t.Name, shortenResolveErr(t.Err))
	}
	summary := strings.Join(parts, "; ")

	label := episode
	if label == "" {
		label = "this title"
	}

	if cfg != nil && cfg.Debug {
		return fmt.Sprintf("could not resolve stream for %s (%s)", label, summary)
	}
	return fmt.Sprintf("could not resolve stream for %s (%s). Use -x for details", label, summary)
}

// shortenResolveErr pulls a short reason from a provider error for one-line summaries.
func shortenResolveErr(err error) string {
	if err == nil {
		return "failed"
	}
	msg := strings.TrimSpace(err.Error())
	lower := strings.ToLower(msg)

	switch {
	case strings.Contains(lower, "no embed id"):
		return "no source"
	case strings.Contains(lower, "empty source"):
		return "no source"
	case strings.Contains(lower, "no streams available"), strings.Contains(lower, "geo-restricted"):
		return "unavailable"
	case strings.Contains(lower, "status 407"):
		return "HTTP 407"
	case strings.Contains(lower, "status 403"):
		return "HTTP 403"
	case strings.Contains(lower, "status 404"):
		return "HTTP 404"
	case strings.Contains(lower, "status 522"):
		return "HTTP 522"
	case strings.Contains(lower, "status 503"):
		return "HTTP 503"
	case strings.Contains(lower, "no such host"), strings.Contains(lower, "lookup"):
		return "DNS error"
	case strings.Contains(lower, "timed out"), strings.Contains(lower, "timeout"):
		return "timeout"
	case strings.Contains(lower, "no matching result"):
		return "not found"
	case strings.Contains(lower, "all fallback servers failed"):
		return "no servers"
	case strings.Contains(lower, "search failed"):
		if i := strings.Index(lower, "status "); i >= 0 {
			rest := msg[i:]
			if j := strings.IndexByte(rest, ')'); j > 0 {
				return strings.TrimSpace(rest[:j])
			}
			return strings.TrimSpace(rest)
		}
		return "search failed"
	}

	if idx := strings.IndexAny(msg, ".\n"); idx > 0 && idx < 60 {
		return msg[:idx]
	}
	if len(msg) > 48 {
		return msg[:45] + "..."
	}
	return msg
}

// isPermanentProviderError reports errors that are unlikely to succeed on retry
// (dead domain, CDN hard-down). Transient failures stay retriable.
func isPermanentProviderError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	permanent := []string{
		"no such host",
		"lookup",
		"status 522",
		"522",
		"status 503",
		"connection refused",
		"host error",
	}
	for _, needle := range permanent {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// episodeLabel builds a short label like "Devil May Cry S01E08".
func episodeLabel(title string, season, episode int) string {
	if season > 0 && episode > 0 {
		return fmt.Sprintf("%s S%02dE%02d", title, season, episode)
	}
	return title
}
