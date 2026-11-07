package resolver

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lobster/internal/media"
)

const validateTimeout = 4 * time.Second

// validateStream does a cheap reachability check on the resolved URL: a 1-byte
// Range GET replaying the stream's Referer and User-Agent (a browser one when
// the stream names none). It is deliberately a playlist-level check
// (status < 400), not a segment-level playability guarantee. Returns nil if
// reachable.
func validateStream(client *http.Client, s *media.Stream) error {
	if s == nil || s.URL == "" {
		return fmt.Errorf("empty stream")
	}
	// A magnet cannot be fetched: its reachability is only known once the
	// swarm answers, which the torrent server bounds with its own timeout.
	// What can be checked is that it carries an info hash at all.
	if isMagnet(s.URL) {
		if !magnetHasInfoHash(s.URL) {
			return fmt.Errorf("magnet has no usable info hash")
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), validateTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return err
	}
	ua := "Mozilla/5.0"
	if s.UserAgent != "" {
		ua = s.UserAgent
	}
	req.Header.Set("User-Agent", ua)
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

// isMagnet mirrors torrentstream.IsMagnet without pulling the torrent library
// into the resolver.
func isMagnet(u string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(u)), "magnet:")
}

// magnetHasInfoHash reports whether a magnet URI names a BitTorrent info hash
// (an xt=urn:btih: of 40 hex or 32 base32 characters) the torrent server can
// act on. A magnet naming several different hashes is refused: the torrent
// client rejects it.
func magnetHasInfoHash(u string) bool {
	_, query, found := strings.Cut(strings.TrimSpace(u), "?")
	if !found {
		return false
	}
	// ParseQuery still returns what it could parse; a stray ';' or '%' in
	// dn must not discard a magnet whose xt is fine.
	values, _ := url.ParseQuery(query)
	valid := map[string]bool{}
	for _, xt := range values["xt"] {
		hash, ok := strings.CutPrefix(strings.ToLower(xt), "urn:btih:")
		if !ok {
			continue
		}
		switch len(hash) {
		case 40:
			if strings.Trim(hash, "0123456789abcdef") == "" {
				valid[hash] = true
			}
		case 32:
			if strings.Trim(hash, "abcdefghijklmnopqrstuvwxyz234567") == "" {
				valid[hash] = true
			}
		}
	}
	return len(valid) == 1
}
