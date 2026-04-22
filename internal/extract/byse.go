package extract

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

// ByseExtractor extracts streams from weneverbeenfree.com (Byse Frontend)
// embed URLs. These are reached via vidcdn.co redirects from flixhq.ws.
type ByseExtractor struct {
	client *http.Client
}

// NewByse creates a new ByseExtractor.
func NewByse() *ByseExtractor {
	// Use a client that does NOT follow redirects automatically,
	// so we can handle vidcdn.co → weneverbeenfree.com ourselves.
	return &ByseExtractor{
		client: httputil.NewClient(),
	}
}

type bysePlaybackResponse struct {
	Playback bysePlayback `json:"playback"`
}

type bysePlayback struct {
	Algorithm string   `json:"algorithm"`
	IV        string   `json:"iv"`
	Payload   string   `json:"payload"`
	KeyParts  []string `json:"key_parts"`
}

type byseDecrypted struct {
	Sources []byseSource `json:"sources"`
	Tracks  []byseTrack  `json:"tracks"`
}

type byseSource struct {
	Quality    string `json:"quality"`
	Label      string `json:"label"`
	MimeType   string `json:"mime_type"`
	URL        string `json:"url"`
	BitrateKbps int   `json:"bitrate_kbps"`
	Height     int    `json:"height"`
}

type byseTrack struct {
	File    string `json:"file"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Default bool   `json:"default"`
}

// Extract resolves a vidcdn.co or weneverbeenfree.com embed URL into a stream.
func (b *ByseExtractor) Extract(embedURL string, preferredQuality string) (*media.Stream, error) {
	// Step 1: Resolve the video code.
	// embedURL may be a vidcdn.co URL that redirects to weneverbeenfree.com,
	// or directly a weneverbeenfree.com URL.
	code, err := b.resolveCode(embedURL)
	if err != nil {
		return nil, fmt.Errorf("resolving video code: %w", err)
	}

	// Step 2: Fetch encrypted playback data
	host := "weneverbeenfree.com"
	apiURL := fmt.Sprintf("https://%s/api/videos/%s/embed/playback", host, code)

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Referer", embedURL)

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching playback: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("playback API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading playback response: %w", err)
	}

	var pbResp bysePlaybackResponse
	if err := json.Unmarshal(body, &pbResp); err != nil {
		return nil, fmt.Errorf("parsing playback response: %w", err)
	}

	// Step 3: Decrypt
	decrypted, err := decryptBysePlayback(pbResp.Playback)
	if err != nil {
		return nil, fmt.Errorf("decrypting playback: %w", err)
	}

	if len(decrypted.Sources) == 0 {
		return nil, fmt.Errorf("no sources in decrypted playback")
	}

	// Step 4: Pick best quality
	streamURL := decrypted.Sources[0].URL
	targetHeight := 0
	fmt.Sscanf(preferredQuality, "%d", &targetHeight)

	if targetHeight > 0 {
		// Exact match first
		for _, s := range decrypted.Sources {
			if s.Height == targetHeight {
				streamURL = s.URL
				break
			}
		}
		// If no exact match, find closest not exceeding target
		if streamURL == decrypted.Sources[0].URL {
			best := &decrypted.Sources[0]
			for i := range decrypted.Sources {
				s := &decrypted.Sources[i]
				if s.Height <= targetHeight && s.Height > best.Height {
					best = s
				}
			}
			streamURL = best.URL
		}
	}

	// Step 5: Map subtitles
	var subtitles []media.Subtitle
	for _, t := range decrypted.Tracks {
		if t.Kind == "captions" && t.File != "" {
			subtitles = append(subtitles, media.Subtitle{
				Language: t.Label,
				Label:    t.Label,
				URL:      t.File,
			})
		}
	}

	return &media.Stream{
		URL:       streamURL,
		Subtitles: subtitles,
		Quality:   preferredQuality,
	}, nil
}

// resolveCode extracts the video code from an embed URL.
// Handles vidcdn.co redirects and direct weneverbeenfree.com URLs.
func (b *ByseExtractor) resolveCode(embedURL string) (string, error) {
	if strings.Contains(embedURL, "weneverbeenfree.com") {
		return extractCodeFromPath(embedURL), nil
	}

	// Follow the vidcdn.co redirect to get the final URL
	noRedirectClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequest(http.MethodGet, embedURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating redirect request: %w", err)
	}
	req.Header.Set("Referer", "https://flixhq.ws/")

	resp, err := noRedirectClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("following redirect: %w", err)
	}
	resp.Body.Close()

	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("no redirect location from %s", embedURL)
	}

	if !strings.Contains(loc, "weneverbeenfree.com") {
		return "", fmt.Errorf("redirect target %q is not weneverbeenfree.com", loc)
	}

	return extractCodeFromPath(loc), nil
}

// extractCodeFromPath extracts the video code from a URL path like /e/sc3pqgbox3m9
func extractCodeFromPath(u string) string {
	// Find /e/ in the URL and take everything after it
	if idx := strings.Index(u, "/e/"); idx != -1 {
		code := u[idx+3:]
		// Strip query params and trailing slashes
		if qIdx := strings.IndexAny(code, "?#"); qIdx != -1 {
			code = code[:qIdx]
		}
		return strings.TrimRight(code, "/")
	}
	// Fallback: last path segment
	parts := strings.Split(strings.TrimRight(u, "/"), "/")
	return parts[len(parts)-1]
}

// decryptBysePlayback decrypts the AES-256-GCM encrypted playback payload.
func decryptBysePlayback(pb bysePlayback) (*byseDecrypted, error) {
	if pb.Algorithm != "AES-256-GCM" {
		return nil, fmt.Errorf("unsupported algorithm: %s", pb.Algorithm)
	}

	// Decode key parts and concatenate
	var key []byte
	for _, part := range pb.KeyParts {
		decoded, err := base64URLDecode(part)
		if err != nil {
			return nil, fmt.Errorf("decoding key part: %w", err)
		}
		key = append(key, decoded...)
	}

	iv, err := base64URLDecode(pb.IV)
	if err != nil {
		return nil, fmt.Errorf("decoding IV: %w", err)
	}

	payload, err := base64URLDecode(pb.Payload)
	if err != nil {
		return nil, fmt.Errorf("decoding payload: %w", err)
	}

	// Decrypt AES-256-GCM
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, iv, payload, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypting: %w", err)
	}

	var result byseDecrypted
	if err := json.Unmarshal(plaintext, &result); err != nil {
		return nil, fmt.Errorf("parsing decrypted payload: %w", err)
	}

	return &result, nil
}

// base64URLDecode decodes a base64url-encoded string (with or without padding).
func base64URLDecode(s string) ([]byte, error) {
	// Add padding if needed
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}
