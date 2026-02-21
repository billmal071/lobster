package extract

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

const (
	megacloudKeysURL = "https://raw.githubusercontent.com/yogesh-hacker/MegacloudKeys/refs/heads/main/keys.json"
)

// MegaCloudExtractor extracts streams from MegaCloud/VidCloud embed URLs.
type MegaCloudExtractor struct {
	client *http.Client

	// Cached megacloud keys
	keysMu sync.Mutex
	keys   map[string]string
}

// NewMegaCloud creates a new MegaCloudExtractor.
func NewMegaCloud() *MegaCloudExtractor {
	return &MegaCloudExtractor{
		client: httputil.NewClient(),
	}
}

// sourcesResponse represents the JSON from the getSources endpoint.
type sourcesResponse struct {
	Sources   json.RawMessage `json:"sources"`
	Tracks    []track         `json:"tracks"`
	Encrypted bool            `json:"encrypted"`
}

type track struct {
	File    string `json:"file"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Default bool   `json:"default"`
}

type source struct {
	File string `json:"file"`
	Type string `json:"type"`
}

// Extract resolves an embed URL into a playable stream.
func (m *MegaCloudExtractor) Extract(embedURL string, preferredQuality string) (*media.Stream, error) {
	if err := httputil.ValidateURL(embedURL); err != nil {
		return nil, fmt.Errorf("invalid embed URL: %w", err)
	}

	// Parse the embed URL to extract domain, embed prefix, and source ID
	domain, embedPrefix, sourceID, err := parseEmbedURL(embedURL)
	if err != nil {
		return nil, fmt.Errorf("parsing embed URL: %w", err)
	}

	// Step 1: Fetch embed page HTML to get the client key
	embedPageURL := fmt.Sprintf("https://%s/%s/v3/e-1/%s?z=", domain, embedPrefix, sourceID)
	embedHTML, err := m.fetchHTML(embedPageURL, "https://flixhq.to/")
	if err != nil {
		return nil, fmt.Errorf("fetching embed page: %w", err)
	}

	// Step 2: Extract client key from HTML
	clientKey, err := extractClientKey(embedHTML)
	if err != nil {
		return nil, fmt.Errorf("extracting client key: %w", err)
	}

	// Step 3: Call getSources endpoint
	getSourcesURL := fmt.Sprintf("https://%s/%s/v3/e-1/getSources?id=%s&_k=%s",
		domain, embedPrefix, url.QueryEscape(sourceID), url.QueryEscape(clientKey))

	body, err := m.fetchJSON(getSourcesURL, embedURL)
	if err != nil {
		return nil, fmt.Errorf("fetching sources: %w", err)
	}

	// Step 4: Parse response
	var resp sourcesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing sources response: %w", err)
	}

	// Step 5: Decrypt sources if encrypted
	var sources []source
	if resp.Encrypted {
		// Sources is a JSON string (encrypted)
		var encryptedSrc string
		if err := json.Unmarshal(resp.Sources, &encryptedSrc); err != nil {
			return nil, fmt.Errorf("parsing encrypted sources: %w", err)
		}

		// Fetch megacloud key
		megaKey, err := m.getMegacloudKey()
		if err != nil {
			return nil, fmt.Errorf("fetching megacloud key: %w", err)
		}

		decrypted := decryptSrc2(encryptedSrc, clientKey, megaKey)
		if decrypted == "" {
			return nil, fmt.Errorf("decryption returned empty result")
		}

		if err := json.Unmarshal([]byte(decrypted), &sources); err != nil {
			return nil, fmt.Errorf("parsing decrypted sources: %w", err)
		}
	} else {
		// Sources is already a JSON array
		if err := json.Unmarshal(resp.Sources, &sources); err != nil {
			return nil, fmt.Errorf("parsing plaintext sources: %w", err)
		}
	}

	if len(sources) == 0 {
		return nil, fmt.Errorf("no sources found")
	}

	// Step 6: Select best source URL
	streamURL := sources[0].File
	for _, s := range sources {
		if strings.Contains(s.File, preferredQuality) {
			streamURL = s.File
			break
		}
	}

	// Step 7: Map subtitle tracks
	var subtitles []media.Subtitle
	for _, t := range resp.Tracks {
		if t.Kind != "captions" || t.File == "" {
			continue
		}
		subtitles = append(subtitles, media.Subtitle{
			Language: t.Label,
			Label:    t.Label,
			URL:      t.File,
		})
	}

	return &media.Stream{
		URL:       streamURL,
		Subtitles: subtitles,
		Quality:   preferredQuality,
	}, nil
}

// parseEmbedURL extracts domain, embed prefix, and source ID from an embed URL.
// Example: https://streameeeeee.site/embed-1/v3/e-1/AbCdEf?z= -> ("streameeeeee.site", "embed-1", "AbCdEf")
func parseEmbedURL(embedURL string) (domain, embedPrefix, sourceID string, err error) {
	u, err := url.Parse(embedURL)
	if err != nil {
		return "", "", "", fmt.Errorf("parsing URL: %w", err)
	}

	domain = u.Host

	// Path format: /embed-N/... or /embed-N/v3/e-1/{sourceId}
	path := strings.TrimPrefix(u.Path, "/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 {
		return "", "", "", fmt.Errorf("empty URL path")
	}

	// Extract embed prefix (e.g., "embed-1", "embed-2")
	embedPrefix = parts[0]
	if !regexp.MustCompile(`^embed-\d+$`).MatchString(embedPrefix) {
		// Fallback: use "embed-2" as default
		embedPrefix = "embed-2"
	}

	// Source ID is the last path segment (before query params)
	sourceID = parts[len(parts)-1]
	if sourceID == "" && len(parts) > 1 {
		sourceID = parts[len(parts)-2]
	}

	if sourceID == "" {
		return "", "", "", fmt.Errorf("could not extract source ID from %q", embedURL)
	}

	return domain, embedPrefix, sourceID, nil
}

// fetchHTML fetches a page and returns its HTML body.
func (m *MegaCloudExtractor) fetchHTML(pageURL, referer string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, pageURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	return string(body), nil
}

// fetchJSON fetches a JSON endpoint and returns the raw body.
func (m *MegaCloudExtractor) fetchJSON(apiURL, referer string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	return body, nil
}

// getMegacloudKey fetches and caches the megacloud decryption key.
func (m *MegaCloudExtractor) getMegacloudKey() (string, error) {
	m.keysMu.Lock()
	defer m.keysMu.Unlock()

	if m.keys != nil {
		if key, ok := m.keys["mega"]; ok {
			return key, nil
		}
	}

	body, err := httputil.GetJSON(m.client, megacloudKeysURL)
	if err != nil {
		return "", fmt.Errorf("fetching megacloud keys: %w", err)
	}

	var keys map[string]string
	if err := json.Unmarshal(body, &keys); err != nil {
		return "", fmt.Errorf("parsing megacloud keys: %w", err)
	}

	m.keys = keys

	key, ok := keys["mega"]
	if !ok {
		return "", fmt.Errorf("mega key not found in keys response")
	}

	return key, nil
}
