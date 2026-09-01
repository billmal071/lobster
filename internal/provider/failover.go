package provider

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// knownDomains maps provider names to their known alternative domains.
// The first domain in each list is the preferred one.
var knownDomains = map[string][]string{
	"kimcartoon": {"kimcartoon.com.co", "kimcartoon.com.rs", "kimcartoon.li"},
	// flixhq.to's origin cluster has been down since ~Aug 2026 (Cloudflare 522).
	// sflix.to and myflixerz.to run the identical engine (same markup and /ajax/
	// endpoints), so the FlixHQ scraper works on them unchanged; they are listed
	// as revival candidates for whenever any of the family's origins return.
	"flixhq":   {"flixhq.to", "flixhq.click", "flixhq.pe", "flixhq.bz", "sflix.to", "myflixerz.to"},
	"flixhqws": {"flixhq.ws"},
}

// healthURLFor maps a domain to the URL probed for health. A package var so
// tests can point probes at httptest servers instead of the live network.
var healthURLFor = func(domain string) string { return "https://" + domain + "/" }

// probeTimeout bounds a single health probe. Package var for tests.
var probeTimeout = 3 * time.Second

// checkDomainHealth sends a HEAD request to the domain's health URL and
// returns true if the server responds with a non-5xx status in time.
func checkDomainHealth(domain string) bool {
	// Explicit dialer with Happy Eyeballs (FallbackDelay > 0): a broken IPv6
	// route must not make a live domain look dead — the dial races v6 and v4
	// and takes whichever connects. Go enables this by default; the explicit
	// dialer pins the behavior so a future custom transport can't lose it.
	client := &http.Client{
		Timeout: probeTimeout,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:       probeTimeout,
				FallbackDelay: 100 * time.Millisecond,
			}).DialContext,
		},
	}
	defer client.CloseIdleConnections()
	resp, err := client.Head(healthURLFor(domain))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

// candidateDomains returns the preference-ordered candidate list for a
// provider: config overrides first (case-insensitive key match), then the
// built-in known domains.
func candidateDomains(providerName string, overrides map[string][]string) []string {
	var candidates []string
	if overrides != nil {
		lowerName := strings.ToLower(providerName)
		for key, domains := range overrides {
			if strings.ToLower(key) == lowerName {
				candidates = append(candidates, domains...)
				break
			}
		}
	}
	return append(candidates, knownDomains[providerName]...)
}

// FirstHealthyDomain probes every candidate domain for a provider in parallel
// and returns the first healthy one in preference order, or "" if none
// respond. Wall-clock cost is roughly one probeTimeout regardless of how many
// candidates are dead, which is what makes gating a usually-dead provider
// affordable.
func FirstHealthyDomain(providerName string, overrides map[string][]string) string {
	candidates := candidateDomains(providerName, overrides)
	if len(candidates) == 0 {
		return ""
	}
	healthy := make([]bool, len(candidates))
	var wg sync.WaitGroup
	for i, d := range candidates {
		wg.Add(1)
		go func(i int, d string) {
			defer wg.Done()
			healthy[i] = checkDomainHealth(d)
		}(i, d)
	}
	wg.Wait()
	for i, ok := range healthy {
		if ok {
			return candidates[i]
		}
	}
	return ""
}

var (
	domainCacheMu sync.Mutex
	domainCache   = map[string]string{}
)

// FirstHealthyDomainCached memoizes FirstHealthyDomain per provider name for
// the process lifetime. A miss ("" — nothing healthy) is cached too: a dead
// provider costs one parallel probe per session, not one per search.
//
// Invariant: the cache key is providerName only, not overrides. Callers must
// pass stable overrides for a given provider name within a process; calling
// with differing overrides for the same name will share whatever result was
// cached on the first call.
func FirstHealthyDomainCached(providerName string, overrides map[string][]string) string {
	domainCacheMu.Lock()
	if d, ok := domainCache[providerName]; ok {
		domainCacheMu.Unlock()
		return d
	}
	domainCacheMu.Unlock()

	d := FirstHealthyDomain(providerName, overrides)

	domainCacheMu.Lock()
	domainCache[providerName] = d
	domainCacheMu.Unlock()
	return d
}

// ResetDomainCache clears the memoized probe results. Test helper.
func ResetDomainCache() {
	domainCacheMu.Lock()
	domainCache = map[string]string{}
	domainCacheMu.Unlock()
}

// ResolveDomain picks a working domain for a provider. It checks the
// configured domain first, then tries overrides from the config, and
// finally falls back to the built-in known domains list.
//
// If the configured domain responds, it is returned immediately without
// checking alternatives. Returns the first healthy domain found, or the
// original domain if none respond (so the caller can still try and show
// a meaningful error).
func ResolveDomain(configured string, providerName string, overrides map[string][]string) string {
	if checkDomainHealth(configured) {
		return configured
	}
	fmt.Fprintf(os.Stderr, "[failover] %s (%s) is unreachable, trying alternatives...\n", configured, providerName)

	// Delegate the candidate scan to FirstHealthyDomain so the alternatives
	// are probed in parallel (~one probeTimeout) instead of sequentially,
	// which matters once a provider has several known mirrors.
	if alt := FirstHealthyDomain(providerName, overrides); alt != "" && alt != configured {
		fmt.Fprintf(os.Stderr, "[failover] switching %s to %s\n", providerName, alt)
		return alt
	}

	fmt.Fprintf(os.Stderr, "[failover] no healthy domain found for %s, using %s anyway\n", providerName, configured)
	return configured
}
