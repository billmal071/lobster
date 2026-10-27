package provider

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

const oneBallMatchesURL = "https://1ball.pk/api/matches"

type oneBallMatch struct {
	Slug    string          `json:"slug"`
	Sport   string          `json:"sport"`
	Title   string          `json:"title"`
	Streams []oneBallStream `json:"streams"`
}

type oneBallStream struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// parseOneBallMatches converts a 1ball JSON match feed into browser-backed
// Live TV channels, rejecting malformed slugs and unsupported embed hosts.
func parseOneBallMatches(data []byte) ([]Channel, error) {
	var matches []oneBallMatch
	if err := json.Unmarshal(data, &matches); err != nil {
		return nil, err
	}
	var channels []Channel
	for _, match := range matches {
		if !validOneBallSlug(match.Slug) || strings.TrimSpace(match.Title) == "" {
			continue
		}
		for i, source := range match.Streams {
			embed, err := url.Parse(strings.TrimSpace(source.URL))
			if err != nil || !validOneBallEmbedURL(embed) {
				continue
			}
			label := strings.TrimSpace(source.Label)
			if label == "" {
				label = fmt.Sprintf("Stream %d", i+1)
			}
			name := strings.TrimSpace(match.Title) + " — " + label
			watchURL := "https://1ball.pk/watch/" + match.Slug
			channels = append(channels, Channel{
				ID:         match.Slug + fmt.Sprintf("-%d", i+1),
				Name:       name,
				Categories: []string{oneBallSportName(match.Sport)},
				URL:        watchURL,
				EmbedURL:   embed.String(),
			})
			break // The match page's built-in iframe uses its first stream.
		}
	}
	return channels, nil
}

// validOneBallSlug accepts only path-safe lowercase fixture slugs.
func validOneBallSlug(s string) bool {
	if s == "" || len(s) > 180 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// validOneBallEmbedURL restricts embeds to HTTPS pages on embed.st.
func validOneBallEmbedURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Hostname() != "embed.st" {
		return false
	}
	return strings.HasPrefix(u.Path, "/embed-low/")
}

// oneBallSportName maps the feed's sport codes to display categories.
func oneBallSportName(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "foot", "football":
		return "Football"
	case "nfl":
		return "NFL"
	case "nba":
		return "NBA"
	default:
		if s = strings.TrimSpace(s); s != "" {
			return strings.ToUpper(s[:1]) + s[1:]
		}
		return "Sports"
	}
}
