package provider

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// knownDomains maps provider names to their known alternative domains.
// The first domain in each list is the preferred one.
var knownDomains = map[string][]string{
	"kimcartoon": {"kimcartoon.com.co", "kimcartoon.com.rs", "kimcartoon.li"},
	"flixhq":     {"flixhq.to"},
	"flixhqws":   {"flixhq.ws"},
}

// checkDomainHealth sends a HEAD request to https://<domain>/ and returns
// true if the server responds with a non-error status within 5 seconds.
func checkDomainHealth(domain string) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Head("https://" + domain + "/")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
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

	// Build candidate list: config overrides first, then built-in known domains.
	// Override keys are matched case-insensitively.
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
	candidates = append(candidates, knownDomains[providerName]...)

	for _, domain := range candidates {
		if domain == configured {
			continue // already tried
		}
		if checkDomainHealth(domain) {
			fmt.Fprintf(os.Stderr, "[failover] switching %s to %s\n", providerName, domain)
			return domain
		}
	}

	fmt.Fprintf(os.Stderr, "[failover] no healthy domain found for %s, using %s anyway\n", providerName, configured)
	return configured
}
