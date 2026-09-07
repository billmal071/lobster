package provider

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// liveTVWarn is where cleartext-credential warnings go.
//
// Stderr, not stdout: the agent-facing commands put their JSON envelope on
// stdout, and a warning line mixed into it would break every caller that
// parses the output. A package var so tests can capture what was warned
// without reading the process's real stderr.
var liveTVWarn = func(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[livetv] "+format+"\n", args...)
}

// credentialParams are the query keys treated as carrying a credential.
//
// A conservative list rather than "any query string at all": a playlist URL
// legitimately carries parameters like type=m3u_plus and output=m3u8, and
// warning about those would train the reader to ignore the warning. The
// Xtream builder in internal/config emits username and password; the rest
// cover the shapes other providers use for the same thing.
var credentialParams = map[string]bool{
	"username": true, "user": true, "login": true,
	"password": true, "pass": true, "pwd": true,
	"token": true, "auth": true, "secret": true,
	"access_token": true, "refresh_token": true,
	"api_key": true, "apikey": true, "key": true,
}

// carriesCredentials reports whether u would put a credential on the wire:
// userinfo (http://user:pass@host/...) or a query parameter named like one.
func carriesCredentials(u *url.URL) bool {
	if u.User != nil {
		return true
	}
	for k := range u.Query() {
		if credentialParams[strings.ToLower(k)] {
			return true
		}
	}
	return false
}

// warnIfCleartextCredentials warns when src would send a credential over
// plain http.
//
// It warns rather than refusing. Rejecting would be the stronger guarantee,
// but an http(s)-agnostic Xtream URL is a perfectly ordinary thing to have
// in a working config — some providers publish no https endpoint at all —
// and turning those into a failed source would break setups that work today
// in order to fix a risk the user may already have accepted. The warning
// makes the exposure visible and leaves the choice with them.
//
// The source is redacted before printing: the whole point is that this URL
// carries a credential, so the warning must not repeat it.
//
// Called from fetch, which runs once per source, rather than from httpGet,
// which the TLS-1.2 fallback calls a second time for the same URL.
func warnIfCleartextCredentials(src string) {
	u, err := url.Parse(src)
	if err != nil || !strings.EqualFold(u.Scheme, "http") {
		return
	}
	if !carriesCredentials(u) {
		return
	}
	liveTVWarn("%s carries credentials over plain http; they travel in cleartext. Use an https:// playlist URL if your provider offers one.", redactURL(src))
}

// liveTVCheckRedirect warns when a redirect chain downgrades https to http,
// which would put a credentialed URL on the wire in the clear even though
// the configured source was https.
//
// It warns and continues, matching the treatment of a configured http
// source. The default 10-hop limit is reimplemented here because setting
// CheckRedirect at all replaces it.
func liveTVCheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	if strings.EqualFold(req.URL.Scheme, "http") && carriesCredentials(req.URL) {
		for _, v := range via {
			if strings.EqualFold(v.URL.Scheme, "https") {
				liveTVWarn("%s redirected from https to plain http with credentials attached; they travel in cleartext.", redactURL(req.URL.String()))
				break
			}
		}
	}
	return nil
}
