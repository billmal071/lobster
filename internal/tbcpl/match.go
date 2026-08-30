package tbcpl

import (
	"net"
	"net/url"
	"strings"
)

// providerMatch pairs a host substring with the lobster provider name key.
// Order matters: more specific hosts first.
//
// This table lists ONLY providers that actually consume mirror overrides via
// provider.ResolveDomain (see cmd/provider.go / NewFlixHQ / NewKimCartoon).
// Other TBCPL catalog hosts (1shows/1flex/1tube -> tbcpl, soap2day, allanime)
// map to providers whose constructors ignore a domain override entirely
// (fixed API endpoints or a hardcoded default), so listing them here would
// silently discard the mirror mapping. Do not add an entry unless the
// corresponding provider constructor threads the override through
// ResolveDomain.
var providerMatches = []struct{ sub, name string }{
	{"flixhq.ws", "flixhqws"},
	{"flixhq", "flixhq"},
	{"kimcartoon", "kimcartoon"},
}

// ProviderFor maps a host to a lobster provider name key, if recognized.
// Matching is on whole hostname-label boundaries so that lookalike hosts such
// as "evil-flixhq.ws.example" or "notflixhq.example" are not classified as a
// supported provider.
func ProviderFor(host string) (string, bool) {
	h := strings.ToLower(host)
	for _, m := range providerMatches {
		if hostMatchesLabel(h, m.sub) {
			return m.name, true
		}
	}
	return "", false
}

// hostMatchesLabel reports whether sub appears in host as a contiguous run of
// whole dot-separated labels (bounded by a dot or a string end on each side).
// sub may itself contain dots (e.g. "flixhq.ws").
func hostMatchesLabel(host, sub string) bool {
	return host == sub ||
		strings.HasPrefix(host, sub+".") ||
		strings.HasSuffix(host, "."+sub) ||
		strings.Contains(host, "."+sub+".")
}

// hostOf returns the bare host (no scheme, no trailing slash) of a site URL,
// plus the bare hostname with any port stripped. The host keeps the port
// because it is used verbatim as the mirror endpoint; the hostname is what
// ProviderFor matches against, since "flixhq.ws:8443" would otherwise fall
// through the "flixhq.ws" label test and be misfiled under "flixhq".
func hostOf(rawURL string) (host, hostname string) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		// A schemeless entry ("flixhq.to/path") parses without error but with
		// an empty Host, so cut the path/query/fragment off by hand — mirror
		// consumers need a bare host, not a URL.
		trimmed := strings.Trim(rawURL, "/")
		if i := strings.IndexAny(trimmed, "/?#"); i >= 0 {
			trimmed = trimmed[:i]
		}
		host, _, err := net.SplitHostPort(trimmed)
		if err != nil {
			return trimmed, trimmed
		}
		return trimmed, host
	}
	return u.Host, u.Hostname()
}

// MirrorDomains groups site hosts by the lobster provider they map to.
func MirrorDomains(sites []Site) map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	for _, s := range sites {
		host, hostname := hostOf(s.URL)
		name, ok := ProviderFor(hostname)
		if !ok {
			continue
		}
		key := name + "|" + host
		if seen[key] {
			continue
		}
		seen[key] = true
		out[name] = append(out[name], host)
	}
	return out
}

// LivePlaylists returns m3u playlist URLs from the livetv category.
func (c *Catalog) LivePlaylists(includeUntrusted bool) []string {
	var out []string
	for _, s := range c.ByCategory("livetv") {
		if !s.Enabled {
			continue
		}
		if !includeUntrusted && s.Status != "trusted" {
			continue
		}
		if isPlaylistURL(s.URL) {
			out = append(out, s.URL)
		}
	}
	return out
}

func isPlaylistURL(rawURL string) bool {
	u := strings.ToLower(rawURL)
	return strings.HasSuffix(hostPath(u), ".m3u") ||
		strings.HasSuffix(hostPath(u), ".m3u8") ||
		strings.Contains(u, "get.php") ||
		strings.Contains(u, "type=m3u")
}

// hostPath returns the URL without its query string, for suffix checks.
func hostPath(rawURL string) string {
	if i := strings.IndexByte(rawURL, '?'); i >= 0 {
		return rawURL[:i]
	}
	return rawURL
}
