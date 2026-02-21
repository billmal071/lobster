// Package decrypt handles communication with the decryption API
// to resolve embed URLs into playable stream URLs and subtitles.
package decrypt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

// Decryptor resolves embed URLs into playable streams.
type Decryptor struct {
	client *http.Client
	apiURL string
}

// New creates a new Decryptor.
func New() *Decryptor {
	return &Decryptor{
		client: httputil.NewClient(),
		apiURL: "https://api.consumet.org",
	}
}

// NewWithAPI creates a Decryptor with a custom API URL.
func NewWithAPI(apiURL string) *Decryptor {
	return &Decryptor{
		client: httputil.NewClient(),
		apiURL: strings.TrimRight(apiURL, "/"),
	}
}

// apiResponse represents the JSON response from the decryption API.
type apiResponse struct {
	Sources []struct {
		URL     string `json:"url"`
		Quality string `json:"quality"`
		IsM3U8  bool   `json:"isM3U8"`
	} `json:"sources"`
	Subtitles []struct {
		URL      string `json:"url"`
		Language string `json:"lang"`
		Label    string `json:"label"`
	} `json:"subtitles"`
}

// Decrypt resolves an embed URL into a playable stream.
func (d *Decryptor) Decrypt(embedURL string, preferredQuality string) (*media.Stream, error) {
	if err := httputil.ValidateURL(embedURL); err != nil {
		return nil, fmt.Errorf("invalid embed URL: %w", err)
	}

	// Extract the embed ID from the URL
	embedID := extractEmbedID(embedURL)
	if embedID == "" {
		return nil, fmt.Errorf("could not extract embed ID from %q", embedURL)
	}

	// Determine server type from URL
	serverType := "vidcloud"
	if strings.Contains(embedURL, "upcloud") {
		serverType = "upcloud"
	}

	apiEndpoint := fmt.Sprintf("%s/movies/flixhq/watch?episodeId=%s&mediaId=&server=%s",
		d.apiURL,
		httputil.EncodeQuery(embedID),
		httputil.EncodeQuery(serverType),
	)

	body, err := httputil.GetJSON(d.client, apiEndpoint)
	if err != nil {
		return nil, fmt.Errorf("decryption API request: %w", err)
	}

	var resp apiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing decryption response: %w", err)
	}

	if len(resp.Sources) == 0 {
		return nil, fmt.Errorf("no sources returned from decryption API")
	}

	// Select best matching quality
	streamURL := selectQuality(resp.Sources, preferredQuality)

	// Convert subtitles
	var subtitles []media.Subtitle
	for _, sub := range resp.Subtitles {
		if sub.URL == "" {
			continue
		}
		subtitles = append(subtitles, media.Subtitle{
			Language: sub.Language,
			Label:    sub.Label,
			URL:      sub.URL,
		})
	}

	return &media.Stream{
		URL:       streamURL,
		Subtitles: subtitles,
		Quality:   preferredQuality,
	}, nil
}

// selectQuality picks the best source matching the preferred quality.
func selectQuality(sources []struct {
	URL     string `json:"url"`
	Quality string `json:"quality"`
	IsM3U8  bool   `json:"isM3U8"`
}, preferred string) string {
	// First try exact match
	for _, s := range sources {
		if strings.Contains(s.Quality, preferred) {
			return s.URL
		}
	}

	// Fall back to "auto" quality (adaptive)
	for _, s := range sources {
		if strings.EqualFold(s.Quality, "auto") {
			return s.URL
		}
	}

	// Fall back to first available
	return sources[0].URL
}

// extractEmbedID extracts the ID portion from an embed URL.
func extractEmbedID(embedURL string) string {
	// Typical format: https://domain/embed-4/AbCdEf?k=1
	parts := strings.Split(embedURL, "/")
	if len(parts) == 0 {
		return ""
	}

	last := parts[len(parts)-1]
	// Remove query parameters
	if idx := strings.Index(last, "?"); idx != -1 {
		last = last[:idx]
	}

	return last
}
