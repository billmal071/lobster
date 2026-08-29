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
