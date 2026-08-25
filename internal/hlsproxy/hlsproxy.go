// Package hlsproxy runs a localhost HTTP proxy that fronts an obfuscated HLS
// stream for a media player. It injects the CDN's required Referer/User-Agent
// on every hop and de-obfuscates segments that are wrapped in a fake-PNG
// header (as megaplay/ibyteimg serves them), which mpv/ffmpeg cannot demux
// directly. Playlists are rewritten so every child URI is fetched back
// through the proxy.
package hlsproxy

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"lobster/internal/httputil"
)

// Proxy is a running localhost HLS de-obfuscating proxy.
type Proxy struct {
	referer   string
	userAgent string
	base      string // http://127.0.0.1:PORT/p
	srv       *http.Server
	ln        net.Listener
	client    *http.Client
}

var pngSig = []byte("\x89PNG\r\n\x1a\n")

// uriAttrRe matches a URI="..." attribute in playlist tag lines
// (EXT-X-KEY, EXT-X-MEDIA, EXT-X-I-FRAME-STREAM-INF, EXT-X-MAP).
var uriAttrRe = regexp.MustCompile(`URI="([^"]*)"`)

// New starts a proxy listening on a random loopback port. Referer and
// userAgent (either may be empty) are sent on every upstream request.
func New(referer, userAgent string) (*Proxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &Proxy{
		referer:   referer,
		userAgent: userAgent,
		base:      fmt.Sprintf("http://%s/p", ln.Addr().String()),
		ln:        ln,
		client:    httputil.NewClient(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/p", p.handle)
	p.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go p.srv.Serve(ln)
	return p, nil
}

// PlaylistURL returns the loopback URL a player should open to reach upstream
// through this proxy.
func (p *Proxy) PlaylistURL(upstream string) string {
	return p.base + "?u=" + base64.RawURLEncoding.EncodeToString([]byte(upstream))
}

// Close shuts the proxy down.
func (p *Proxy) Close() error {
	if p.srv == nil {
		return nil
	}
	return p.srv.Close()
}

func (p *Proxy) handle(w http.ResponseWriter, r *http.Request) {
	raw, err := base64.RawURLEncoding.DecodeString(r.URL.Query().Get("u"))
	if err != nil {
		http.Error(w, "bad upstream", http.StatusBadRequest)
		return
	}
	upstream := string(raw)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream, nil)
	if err != nil {
		http.Error(w, "bad upstream", http.StatusBadRequest)
		return
	}
	if p.referer != "" {
		req.Header.Set("Referer", p.referer)
	}
	if p.userAgent != "" {
		req.Header.Set("User-Agent", p.userAgent)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		http.Error(w, "upstream fetch failed", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		http.Error(w, "upstream read failed", http.StatusBadGateway)
		return
	}

	// Serve through ServeContent so a player can seek: it handles Range,
	// 206/Content-Range and 416 for us. The range must be applied to the bytes
	// the client actually sees — stripPNG removes a wrapper and so shifts every
	// offset, which is why the client's Range is never forwarded upstream.
	payload := stripPNG(body)
	contentType := "video/mp2t"
	if isPlaylist(body) {
		payload = p.rewritePlaylist(body, upstream)
		contentType = "application/vnd.apple.mpegurl"
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(payload))
}

// isPlaylist reports whether body is an m3u8 playlist.
func isPlaylist(body []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(body), []byte("#EXTM3U"))
}

// stripPNG removes a fake-PNG wrapper (…IEND<crc><payload>) so the real
// MPEG-TS payload underneath is served. Clean payloads pass through untouched.
func stripPNG(body []byte) []byte {
	if !bytes.HasPrefix(body, pngSig) {
		return body
	}
	if i := bytes.Index(body, []byte("IEND")); i >= 0 && i+8 <= len(body) {
		return body[i+8:]
	}
	return body
}

// rewritePlaylist resolves every child URI relative to the playlist's own URL
// and points it back at the proxy, so segments and sub-playlists are fetched
// (and de-obfuscated) through this proxy too.
func (p *Proxy) rewritePlaylist(body []byte, playlistURL string) []byte {
	base, err := url.Parse(playlistURL)
	if err != nil {
		return body
	}
	resolve := func(ref string) string {
		u, err := url.Parse(strings.TrimSpace(ref))
		if err != nil {
			return ref
		}
		return p.PlaylistURL(base.ResolveReference(u).String())
	}

	var out strings.Builder
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			out.WriteString(line)
		case strings.HasPrefix(trimmed, "#"):
			// Rewrite any URI="..." attribute (keys, media, maps).
			out.WriteString(uriAttrRe.ReplaceAllStringFunc(line, func(m string) string {
				inner := uriAttrRe.FindStringSubmatch(m)[1]
				return `URI="` + resolve(inner) + `"`
			}))
		default:
			out.WriteString(resolve(line))
		}
		out.WriteByte('\n')
	}
	return []byte(out.String())
}
