package extract

import (
	"fmt"
	"io"
	"net/http"
	"regexp"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

// VidWishExtractor extracts streams from player.vidwish.live pages.
// These pages contain JWPlayer setup with m3u8 URLs in the sources array.
type VidWishExtractor struct {
	client *http.Client
}

// NewVidWish creates a new VidWishExtractor.
func NewVidWish() *VidWishExtractor {
	return &VidWishExtractor{
		client: httputil.NewClient(),
	}
}

var vidwishSourceRe = regexp.MustCompile(`"file"\s*:\s*"(https?://[^"]+\.m3u8[^"]*)"`)

// Extract resolves a vidwish.live player URL into a stream.
func (v *VidWishExtractor) Extract(embedURL string, preferredQuality string) (*media.Stream, error) {
	req, err := http.NewRequest(http.MethodGet, embedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Referer", "https://vid.kimcartoon.com.co/")

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching player page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("player page returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading page: %w", err)
	}
	html := string(body)

	match := vidwishSourceRe.FindStringSubmatch(html)
	if len(match) < 2 {
		return nil, fmt.Errorf("no m3u8 URL found in player page")
	}

	return &media.Stream{
		URL:     match[1],
		Quality: preferredQuality,
	}, nil
}
