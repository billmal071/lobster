package provider

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
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

var vidrockWatchBudget = 24 * time.Second

var vidrockAPITimeout = 8 * time.Second

var vidrockProbeTimeout = 6 * time.Second

var (
	vidrockResolutionRe = regexp.MustCompile(`RESOLUTION=\d+x(\d+)`)
	vidrockBandwidthRe  = regexp.MustCompile(`(?:^|[:,])BANDWIDTH=(\d+)`)
	vidrockPathHeightRe = regexp.MustCompile(`/(\d{3,4})p?/`)
	vidrockAudioAttrRe  = regexp.MustCompile(`(?:^|[:,])AUDIO="([^"]*)"`)
	vidrockGroupIDRe    = regexp.MustCompile(`GROUP-ID="([^"]*)"`)
)

// Vidrock resolves streams from vidrock.to's unofficial JSON API by TMDB id.
// It is a StreamProvider: the API has no search, listing or detail endpoints,
// so Search borrows TMDB's and Watch takes the ids that search produced.
type Vidrock struct {
	base       string
	client     *http.Client
	allowLocal bool
	lookup     func(ctx context.Context, host string) ([]netip.Addr, error)
	dial       func(ctx context.Context, network, addr string) (net.Conn, error)
	proxy      func(*http.Request) (*url.URL, error)
	tlsRoots   *x509.CertPool

	mu        sync.RWMutex
	audioLang string
	logf      func(string, ...any)
}

func NewVidrock() *Vidrock {
	return &Vidrock{
		base:      vidrockBase,
		client:    &http.Client{Timeout: 10 * time.Second},
		audioLang: "english",
	}
}

func (v *Vidrock) SetAudioLanguage(lang string) {
	v.mu.Lock()
	v.audioLang = lang
	v.mu.Unlock()
}

func (v *Vidrock) SetLogger(fn func(string, ...any)) {
	v.mu.Lock()
	v.logf = fn
	v.mu.Unlock()
}

func (v *Vidrock) log(format string, args ...any) {
	v.mu.RLock()
	fn := v.logf
	v.mu.RUnlock()
	if fn != nil {
		fn(format, args...)
	}
}

func (v *Vidrock) Search(query string) ([]media.SearchResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), vidrockWatchBudget)
	defer cancel()
	body, err := tmdbSearchMulti(ctx, tmdbBaseURL, query, func(rawURL string) ([]byte, error) {
		return v.get(ctx, rawURL, strings.TrimRight(tmdbBaseURL, "/")+"/", "application/json, text/html, */*", 10<<20)
	})
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
	return nil, fmt.Errorf("vidrock: season listing unavailable; the API exposes no index (%w)", ErrNoEpisodeIndex)
}

// GetEpisodes is unavailable for the same reason. Both wrap ErrNoEpisodeIndex,
// so a season lookup that reaches Vidrock in the fallback chain reads its
// answer as "lists nothing", not as a lookup that failed.
func (v *Vidrock) GetEpisodes(id string, seasonID string) ([]media.Episode, error) {
	return nil, fmt.Errorf("vidrock: %w; the API exposes no episode index", ErrNoEpisodeIndex)
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
// episodeID ("<tmdb>:<season>:<episode>") to the best server whose playlist
// answers: preferred audio language first, then resolution against quality.
func (v *Vidrock) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	path, err := tbcplStreamPath(mediaID, episodeID)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), vidrockWatchBudget)
	defer cancel()

	body, err := v.getAPI(ctx, strings.TrimRight(v.base, "/")+"/api/"+path)
	if err != nil {
		return nil, fmt.Errorf("vidrock: %w", err)
	}
	var servers map[string]vidrockEntry
	if err := json.Unmarshal(body, &servers); err != nil {
		return nil, fmt.Errorf("vidrock: parsing response: %w", err)
	}

	candidates, lastErr := v.candidates(servers)
	probes, probeErr := v.probeAll(ctx, candidates, vidrockQualityCap(quality))
	if probeErr != nil {
		lastErr = probeErr
	}
	if len(probes) > 0 {
		best := v.rank(probes, quality)
		v.log("vidrock: using server %s (%s) at %dp", best.c.name, best.c.language, best.height)
		return &media.Stream{URL: best.streamURL(quality), Quality: quality, UserAgent: vidrockUA}, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("%w: %v", errVidrockNoStream, lastErr)
	}
	return nil, errVidrockNoStream
}

type vidrockVariant struct {
	height, bandwidth int
	url               string
	separateAudio     bool
}

type vidrockProbe struct {
	c        vidrockCandidate
	variants []vidrockVariant
	height   int
	maxH     int
	bw       int
	pick     *vidrockVariant
}

func (p vidrockProbe) streamURL(quality string) string {
	if vidrockQualityCap(quality) > 0 && p.pick != nil && p.pick.height < p.maxH && !p.pick.separateAudio {
		return p.pick.url
	}
	return p.c.url
}

func vidrockQualityCap(quality string) int {
	n, err := strconv.Atoi(strings.TrimSpace(quality))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func vidrockTier(height, qcap int) int {
	switch {
	case height <= 0:
		return 2
	case qcap > 0 && height > qcap:
		return 1
	}
	return 0
}

func vidrockBetter(ah, abw, bh, bbw, qcap int) bool {
	at, bt := vidrockTier(ah, qcap), vidrockTier(bh, qcap)
	if at != bt {
		return at < bt
	}
	if ah != bh {
		if at == 1 {
			return ah < bh
		}
		return ah > bh
	}
	return abw > bbw
}

func (v *Vidrock) probeAll(ctx context.Context, cands []vidrockCandidate, qcap int) ([]vidrockProbe, error) {
	type result struct {
		p   vidrockProbe
		err error
	}
	results := make([]result, len(cands))
	var wg sync.WaitGroup
	for i, c := range cands {
		wg.Add(1)
		go func(i int, c vidrockCandidate) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, vidrockProbeTimeout)
			defer cancel()
			p, err := v.probe(pctx, c, qcap)
			results[i] = result{p, err}
		}(i, c)
	}
	wg.Wait()

	var out []vidrockProbe
	var lastErr error
	for _, r := range results {
		if r.err != nil {
			lastErr = r.err
			continue
		}
		out = append(out, r.p)
	}
	return out, lastErr
}

func (v *Vidrock) rank(probes []vidrockProbe, quality string) vidrockProbe {
	qcap := vidrockQualityCap(quality)
	v.mu.RLock()
	pref := v.audioLang
	v.mu.RUnlock()
	sort.SliceStable(probes, func(i, j int) bool {
		a, b := probes[i], probes[j]
		if pref != "" {
			if ma, mb := lang.Matches(a.c.language, pref), lang.Matches(b.c.language, pref); ma != mb {
				return ma
			}
		}
		return vidrockBetter(a.height, a.bw, b.height, b.bw, qcap)
	})
	for _, p := range probes {
		v.log("vidrock: server %s (%s, %s) offers %dp", p.c.name, p.c.language, vidrockKind(p.c), p.height)
	}
	return probes[0]
}

func vidrockKind(c vidrockCandidate) string {
	if c.hls {
		return "hls"
	}
	return "other"
}

func parseVidrockPlaylist(body string, base *url.URL) []vidrockVariant {
	lines := strings.Split(body, "\n")
	splitAudio := map[string]bool{}
	for _, line := range lines {
		if strings.HasPrefix(line, "#EXT-X-MEDIA") && strings.Contains(line, "TYPE=AUDIO") && strings.Contains(line, "URI=") {
			if m := vidrockGroupIDRe.FindStringSubmatch(line); m != nil {
				splitAudio[m[1]] = true
			}
		}
	}
	var out []vidrockVariant
	for i, line := range lines {
		if !strings.HasPrefix(line, "#EXT-X-STREAM-INF") {
			continue
		}
		var v vidrockVariant
		if m := vidrockResolutionRe.FindStringSubmatch(line); m != nil {
			v.height, _ = strconv.Atoi(m[1])
		}
		if m := vidrockBandwidthRe.FindStringSubmatch(line); m != nil {
			v.bandwidth, _ = strconv.Atoi(m[1])
		}
		if m := vidrockAudioAttrRe.FindStringSubmatch(line); m != nil {
			v.separateAudio = splitAudio[m[1]]
		}
		for _, next := range lines[i+1:] {
			next = strings.TrimSpace(next)
			if next == "" || strings.HasPrefix(next, "#") {
				continue
			}
			ref, err := url.Parse(next)
			if err == nil {
				v.url = base.ResolveReference(ref).String()
				out = append(out, v)
			}
			break
		}
	}
	return out
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

var vidrockCGNAT = netip.MustParsePrefix("100.64.0.0/10")

func vidrockBlockedAddr(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsMulticast() ||
		a.IsUnspecified() || vidrockCGNAT.Contains(a)
}

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
	if strings.Contains(host, "%") {
		return fmt.Errorf("refusing scoped stream host %q", host)
	}
	if a, err := netip.ParseAddr(host); err == nil && vidrockBlockedAddr(a) {
		return fmt.Errorf("refusing local stream host %q", host)
	}
	return nil
}

func (v *Vidrock) dialPublic(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	lookup := v.lookup
	if lookup == nil {
		lookup = func(ctx context.Context, h string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", h)
		}
	}
	addrs, err := lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	dial := v.dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	}
	var lastErr error = fmt.Errorf("no address for %q", host)
	for _, a := range addrs {
		if a.Zone() != "" || vidrockBlockedAddr(a) {
			lastErr = fmt.Errorf("refusing local stream host %q (%s)", host, a)
			continue
		}
		conn, err := dial(ctx, network, net.JoinHostPort(a.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (v *Vidrock) guardedTransport() http.RoundTripper {
	proxy := v.proxy
	if proxy == nil {
		proxy = http.ProxyFromEnvironment
	}
	direct := http.DefaultTransport.(*http.Transport).Clone()
	direct.Proxy = nil
	direct.DialContext = v.dialPublic
	proxied := http.DefaultTransport.(*http.Transport).Clone()
	proxied.Proxy = proxy
	if v.tlsRoots != nil {
		direct.TLSClientConfig = &tls.Config{RootCAs: v.tlsRoots}
		proxied.TLSClientConfig = &tls.Config{RootCAs: v.tlsRoots}
	}
	return vidrockRouter{direct: direct, proxied: proxied, proxy: proxy}
}

type vidrockRouter struct {
	direct, proxied *http.Transport
	proxy           func(*http.Request) (*url.URL, error)
}

func (r vidrockRouter) RoundTrip(req *http.Request) (*http.Response, error) {
	if u, err := r.proxy(req); err == nil && u != nil {
		return r.proxied.RoundTrip(req)
	}
	return r.direct.RoundTrip(req)
}

func (v *Vidrock) httpClient() *http.Client {
	client := *v.client
	if !v.allowLocal && client.Transport == nil {
		client.Transport = v.guardedTransport()
	}
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return v.checkURL(r.URL.String())
	}
	return &client
}

func (v *Vidrock) fetchPlaylist(ctx context.Context, name, target string) (string, *url.URL, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", nil, "", err
	}
	req.Header.Set("User-Agent", vidrockUA)
	resp, err := v.httpClient().Do(req)
	if err != nil {
		return "", nil, "", fmt.Errorf("server %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, "", fmt.Errorf("server %s: playlist status %d", name, resp.StatusCode)
	}
	head, err := io.ReadAll(io.LimitReader(resp.Body, 16384))
	if err != nil {
		return "", nil, "", fmt.Errorf("server %s: %w", name, err)
	}
	return strings.TrimPrefix(string(head), utf8BOM), resp.Request.URL, resp.Header.Get("Content-Type"), nil
}

func looksLikeErrorPage(contentType, body string) bool {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "html") || strings.Contains(ct, "json") || strings.Contains(ct, "xml") || strings.HasPrefix(ct, "text/") {
		return true
	}
	b := strings.TrimSpace(body)
	return strings.HasPrefix(b, "<") || strings.HasPrefix(b, "{") || strings.HasPrefix(b, "[")
}

func isHLSPlaylist(body string) bool {
	return strings.HasPrefix(strings.TrimSpace(body), "#EXTM3U")
}

func (v *Vidrock) probe(ctx context.Context, c vidrockCandidate, qcap int) (vidrockProbe, error) {
	p := vidrockProbe{c: c}
	text, final, ctype, err := v.fetchPlaylist(ctx, c.name, c.url)
	if err != nil {
		return p, err
	}
	if c.hls && !isHLSPlaylist(text) {
		return p, fmt.Errorf("server %s: not an HLS playlist", c.name)
	}
	if !c.hls {
		if looksLikeErrorPage(ctype, text) {
			return p, fmt.Errorf("server %s: answered with a web page or error body, not video", c.name)
		}
		return p, nil
	}

	for _, vr := range parseVidrockPlaylist(text, final) {
		if v.checkURL(vr.url) == nil {
			p.variants = append(p.variants, vr)
		}
	}
	for len(p.variants) > 0 {
		p.pick, p.maxH = nil, 0
		for i := range p.variants {
			vr := p.variants[i]
			if vr.height > p.maxH {
				p.maxH = vr.height
			}
			if p.pick == nil || vidrockBetter(vr.height, vr.bandwidth, p.pick.height, p.pick.bandwidth, qcap) {
				p.pick = &p.variants[i]
			}
		}
		if qcap <= 0 || p.pick.height >= p.maxH {
			break
		}
		if vtext, _, _, err := v.fetchPlaylist(ctx, c.name, p.pick.url); err == nil && isHLSPlaylist(vtext) {
			break
		}
		v.log("vidrock: server %s variant %dp is not a playable playlist; trying the next", c.name, p.pick.height)
		idx := p.pick
		for i := range p.variants {
			if &p.variants[i] == idx {
				p.variants = append(p.variants[:i], p.variants[i+1:]...)
				break
			}
		}
		p.pick, p.maxH = nil, 0
	}
	if p.pick != nil {
		p.height, p.bw = p.pick.height, p.pick.bandwidth
		return p, nil
	}
	if m := vidrockPathHeightRe.FindStringSubmatch(final.Path); m != nil {
		p.height, _ = strconv.Atoi(m[1])
		p.maxH = p.height
	}
	return p, nil
}

func (v *Vidrock) getAPI(ctx context.Context, apiURL string) ([]byte, error) {
	var body []byte
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		actx, cancel := context.WithTimeout(ctx, vidrockAPITimeout)
		body, err = v.get(actx, apiURL, strings.TrimRight(v.base, "/")+"/", "application/json, */*", vidrockMaxBody)
		cancel()
		if err == nil || ctx.Err() != nil || !vidrockTransient(err) {
			break
		}
		v.log("vidrock: API request failed (%v); retrying once", err)
	}
	return body, err
}

func vidrockTransient(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout() ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) ||
		errors.Is(err, syscall.ECONNRESET)
}

func (v *Vidrock) get(ctx context.Context, rawURL, referer, accept string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", vidrockUA)
	req.Header.Set("Accept", accept)
	req.Header.Set("Referer", referer)
	resp, err := v.httpClient().Do(req)
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
