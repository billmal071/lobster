package tbcpl

import (
	"net/url"
	"strings"
)

// providerMatch pairs a host substring with the lobster provider name key.
// Order matters: more specific hosts first.
var providerMatches = []struct{ sub, name string }{
	{"flixhq.ws", "flixhqws"},
	{"flixhq", "flixhq"},
	{"1shows", "tbcpl"},
	{"1flex", "tbcpl"},
	{"1tube", "tbcpl"},
	{"soap2day", "soap2day"},
	{"kimcartoon", "kimcartoon"},
	{"allanime", "allanime"},
}

// ProviderFor maps a host to a lobster provider name key, if recognized.
func ProviderFor(host string) (string, bool) {
	h := strings.ToLower(host)
	for _, m := range providerMatches {
		if strings.Contains(h, m.sub) {
			return m.name, true
		}
	}
	return "", false
}

// hostOf returns the bare host (no scheme, no trailing slash) of a site URL.
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return strings.Trim(rawURL, "/")
	}
	return u.Host
}

// MirrorDomains groups site hosts by the lobster provider they map to.
func MirrorDomains(sites []Site) map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	for _, s := range sites {
		host := hostOf(s.URL)
		name, ok := ProviderFor(host)
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
