package provider

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"lobster/internal/lang"
	"lobster/internal/media"
)

const (
	vidrockBase = "https://vidrock.to"
	// vidrockKeyHex is the AES-256-GCM key vidrock.to's own page decrypts every
	// server URL with; it is the string constant TQ in the site's public
	// /assets/index-*.js bundle. If streams start failing to decrypt, read the
	// constant from the live bundle again.
	vidrockKeyHex  = "7f3e9c2a8b5d1f4e6a9c3b7d2e5f8a1c4b6d9e2f5a8c1b4d7e9f2a5c8b1d4e7f"
	vidrockMaxBody = 1 << 20
	vidrockUA      = "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0"
	utf8BOM        = "\xef\xbb\xbf"
)

var errVidrockNoStream = errors.New("vidrock has no stream for this title")

var vidrockWatchBudget = 20 * time.Second

// Vidrock resolves streams from vidrock.to's unofficial JSON API by TMDB id.
// It is a StreamProvider: the API has no search, listing or detail endpoints,
// so Search borrows TMDB's and Watch takes the ids that search produced.
type Vidrock struct {
	base   string
	client *http.Client
	allowLocal bool

	mu        sync.RWMutex
	audioLang string
}

// NewVidrock creates a new Vidrock provider.
func NewVidrock() *Vidrock {
	return &Vidrock{
		base:      vidrockBase,
		client:    &http.Client{Timeout: 10 * time.Second},
		audioLang: "english",
	}
}

// SetAudioLanguage sets the preferred audio language used to order servers.
// Empty keeps the API's own order.
func (v *Vidrock) SetAudioLanguage(lang string) {
	v.mu.Lock()
	v.audioLang = lang
	v.mu.Unlock()
}

// Search uses TMDB's keyless multi-search, whose ids the Vidrock API takes.
func (v *Vidrock) Search(query string) ([]media.SearchResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), vidrockWatchBudget)
	defer cancel()
	body, err := v.get(ctx, tmdbMultiSearchURL(tmdbBaseURL, query), strings.TrimRight(tmdbBaseURL, "/")+"/", "application/json, text/html, */*", 10<<20)
	if err != nil {
		return nil, fmt.Errorf("vidrock search: %w", err)
	}
	results, err := parseTMDBSearchResults(body, tmdbBaseURL)
	if err != nil {
		return nil, fmt.Errorf("vidrock search: %w", err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("%w for %q", ErrNoResults, query)
	}
	return results, nil
}

func (v *Vidrock) GetDetails(id string) (*media.ContentDetail, error) {
	return &media.ContentDetail{}, nil
}

// GetSeasons is unavailable: the API answers any season and episode number
// with an empty server list and exposes no index, so a list would be invented.
// The resolver reaches Vidrock through Watch with a built episode ID, and a
// primary that cannot enumerate hands listing to the fallback chain.
func (v *Vidrock) GetSeasons(id string) ([]media.Season, error) {
	return nil, errors.New("vidrock: season listing unavailable; the API exposes no episode index")
}

func (v *Vidrock) GetEpisodes(id string, seasonID string) ([]media.Episode, error) {
	return nil, errors.New("vidrock: episode listing unavailable; the API exposes no episode index")
}

func (v *Vidrock) GetServers(id string, episodeID string) ([]media.Server, error) {
	return nil, errors.New("use Watch for Vidrock")
}

func (v *Vidrock) GetEmbedURL(serverID string) (string, error) {
	return "", errors.New("use Watch for Vidrock")
}

func (v *Vidrock) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	return nil, errors.New("vidrock: no trending list")
}

func (v *Vidrock) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	return nil, errors.New("vidrock: no recent list")
}

type vidrockEntry struct {
	URL      string `json:"url"`
	Language string `json:"language"`
	Type     string `json:"type"`
}

type vidrockCandidate struct {
	name, url, language string
	hls                 bool
}

// Watch resolves mediaID ("tv/<tmdb>" or "movie/<tmdb>") and, for a series,
// episodeID ("<tmdb>:<season>:<episode>") to the first server whose playlist
// answers. Servers in the preferred audio language come first, then HLS ones.
func (v *Vidrock) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	path, err := tbcplStreamPath(mediaID, episodeID)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), vidrockWatchBudget)
	defer cancel()

	body, err := v.get(ctx, strings.TrimRight(v.base, "/")+"/api/"+path, strings.TrimRight(v.base, "/")+"/", "application/json, */*", vidrockMaxBody)
	if err != nil {
		return nil, fmt.Errorf("vidrock: %w", err)
	}
	var servers map[string]vidrockEntry
	if err := json.Unmarshal(body, &servers); err != nil {
		return nil, fmt.Errorf("vidrock: parsing response: %w", err)
	}

	candidates, lastErr := v.candidates(servers)
	for _, c := range candidates {
		if ctx.Err() != nil {
			break
		}
		if err := v.probe(ctx, c); err != nil {
			lastErr = err
			continue
		}
		return &media.Stream{URL: c.url, Quality: quality}, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("%w: %v", errVidrockNoStream, lastErr)
	}
	return nil, errVidrockNoStream
}

func (v *Vidrock) candidates(servers map[string]vidrockEntry) ([]vidrockCandidate, error) {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	var lastErr error
	seen := map[string]bool{}
	var out []vidrockCandidate
	for _, name := range names {
		e := servers[name]
		if strings.TrimSpace(e.URL) == "" {
			continue
		}
		plain, err := vidrockDecrypt(e.URL)
		if err != nil {
			lastErr = fmt.Errorf("server %s: %w", name, err)
			continue
		}
		if err := v.checkURL(plain); err != nil {
			lastErr = fmt.Errorf("server %s: %w", name, err)
			continue
		}
		if seen[plain] {
			continue
		}
		seen[plain] = true
		out = append(out, vidrockCandidate{name: name, url: plain, language: e.Language, hls: strings.EqualFold(e.Type, "hls")})
	}

	v.mu.RLock()
	pref := v.audioLang
	v.mu.RUnlock()
	sort.SliceStable(out, func(i, j int) bool {
		if pref != "" {
			if mi, mj := lang.Matches(out[i].language, pref), lang.Matches(out[j].language, pref); mi != mj {
				return mi
			}
		}
		return out[i].hls && !out[j].hls
	})
	return out, lastErr
}

// vidrockDecrypt opens a server URL: base64url of a 12-byte IV followed by
// AES-256-GCM ciphertext and its 16-byte tag.
func vidrockDecrypt(s string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(s), "="))
	if err != nil {
		return "", fmt.Errorf("decoding: %w", err)
	}
	const ivLen, tagLen = 12, 16
	if len(raw) < ivLen+tagLen {
		return "", errors.New("ciphertext too short")
	}
	key, err := hex.DecodeString(vidrockKeyHex)
	if err != nil {
		return "", fmt.Errorf("key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("key: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, raw[:ivLen], raw[ivLen:], nil)
	if err != nil {
		return "", errors.New("decrypt failed")
	}
	return string(plain), nil
}

// checkURL accepts only an https URL with a host and no credentials, and
// unless allowLocal is set, no loopback, private or link-local address.
func (v *Vidrock) checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("refusing stream URL %q", raw)
	}
	if v.allowLocal {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("refusing local stream host %q", host)
	}
	if ip := net.ParseIP(host); ip != nil &&
		(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
		return fmt.Errorf("refusing local stream host %q", host)
	}
	return nil
}

// probe fetches the start of the candidate with no Referer, and for HLS
// requires the body to be a playlist. Redirects must stay on vetted https URLs.
func (v *Vidrock) probe(ctx context.Context, c vidrockCandidate) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", vidrockUA)
	client := *v.client
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return v.checkURL(r.URL.String())
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("server %s: %w", c.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("server %s: playlist status %d", c.name, resp.StatusCode)
	}
	head, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("server %s: %w", c.name, err)
	}
	if c.hls && !strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(string(head), utf8BOM)), "#EXTM3U") {
		return fmt.Errorf("server %s: not an HLS playlist", c.name)
	}
	return nil
}

func (v *Vidrock) get(ctx context.Context, rawURL, referer, accept string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", vidrockUA)
	req.Header.Set("Accept", accept)
	req.Header.Set("Referer", referer)
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d for %s", resp.StatusCode, rawURL)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("response too large (over %d bytes)", max)
	}
	return body, nil
}
