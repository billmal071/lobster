package resolver

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"lobster/internal/media"
)

const validateTimeout = 4 * time.Second

// validateStream does a cheap reachability check on the resolved URL: a 1-byte
// Range GET replaying the stream's Referer and a browser User-Agent. It is
// deliberately a playlist-level check (status < 400), not a segment-level
// playability guarantee. Returns nil if reachable.
func validateStream(client *http.Client, s *media.Stream) error {
	if s == nil || s.URL == "" {
		return fmt.Errorf("empty stream")
	}
	ctx, cancel := context.WithTimeout(context.Background(), validateTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Range", "bytes=0-0")
	if s.Referer != "" {
		req.Header.Set("Referer", s.Referer)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("validation status %d", resp.StatusCode)
	}
	return nil
}
