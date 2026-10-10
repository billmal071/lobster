package subtitle

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// osServer is a stand-in for api.opensubtitles.com that tells anonymous
// requests apart from logged-in ones.
//
// Credentials here are obviously fake and are never sent anywhere: the whole
// point of the stub is that no request leaves the process.
type osServer struct {
	mu sync.Mutex

	// logins counts POST /login calls, and tokens is what each one hands
	// back, in order. A login past the end of tokens gets the last entry.
	logins int
	tokens []string

	// loginStatus is the HTTP status /login answers with.
	loginStatus int

	// acceptBearer, when non-empty, is the only bearer /download honours;
	// anything else gets a 401. Empty means /download does not care, which
	// is how the anonymous tier is modelled.
	acceptBearer string

	// downloadAuth records the Authorization header of every POST /download,
	// so a test can assert that the first attempt was the logged-in one.
	downloadAuth []string
}

func newOSServer(t *testing.T, s *osServer) {
	t.Helper()
	if s.loginStatus == 0 {
		s.loginStatus = http.StatusOK
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/login":
			s.logins++
			body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
			var creds struct{ Username, Password string }
			if err := json.Unmarshal(body, &creds); err != nil {
				t.Errorf("/login body %q is not JSON: %v", body, err)
			}
			if creds.Username == "" || creds.Password == "" {
				t.Errorf("/login called without credentials: %q", body)
			}
			if s.loginStatus != http.StatusOK {
				w.WriteHeader(s.loginStatus)
				_, _ = w.Write([]byte(`{"message":"nope"}`))
				return
			}
			tok := s.tokens[min(s.logins, len(s.tokens))-1]
			_, _ = w.Write([]byte(`{"token":"` + tok + `","status":200}`))
		case r.URL.Path == "/download":
			s.downloadAuth = append(s.downloadAuth, r.Header.Get("Authorization"))
			if s.acceptBearer != "" && r.Header.Get("Authorization") != "Bearer "+s.acceptBearer {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"token expired"}`))
				return
			}
			_, _ = w.Write([]byte(`{"link":"https://cdn.invalid/sub.srt"}`))
		case r.URL.Path == "/subtitles":
			s.downloadAuth = append(s.downloadAuth, "SEARCH:"+r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"total_count":1,"data":[{"attributes":{"language":"en","feature_details":{"feature_type":"Movie"},"files":[{"file_id":7,"file_name":"a.srt"}]}}]}`))
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	saved := openSubtitlesAPI
	openSubtitlesAPI = srv.URL
	t.Cleanup(func() { openSubtitlesAPI = saved })
}

func (s *osServer) loginCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins
}

func (s *osServer) auths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.downloadAuth...)
}

// The whole point of the login: without a bearer token OpenSubtitles meters
// the API key at 5 downloads per 24h instead of 20, and a 12-episode season
// dies around episode 6 with a bare "API returned status 403". A download must
// therefore go out authenticated whenever credentials exist.
func TestOpenSubtitlesDownloadLogsInAndSendsBearer(t *testing.T) {
	srv := &osServer{tokens: []string{"token-1"}, acceptBearer: "token-1"}
	newOSServer(t, srv)

	link, err := newOpenSubtitlesWithLogin("stub-key", "stub-user", "stub-pass").ResolveDownloadURL(7)
	if err != nil {
		t.Fatalf("ResolveDownloadURL: %v", err)
	}
	if link != "https://cdn.invalid/sub.srt" {
		t.Fatalf("link = %q", link)
	}
	if srv.loginCount() != 1 {
		t.Fatalf("logins = %d, want exactly 1", srv.loginCount())
	}
	if got := srv.auths(); len(got) != 1 || got[0] != "Bearer token-1" {
		t.Fatalf("download Authorization headers = %v, want one \"Bearer token-1\"", got)
	}
}

// One login per client, not one per episode. A season downloads a subtitle per
// episode through the same client, and a round trip each time would be a
// round trip nobody needs on an interactive playback path.
func TestOpenSubtitlesLogsInOncePerClient(t *testing.T) {
	srv := &osServer{tokens: []string{"token-1"}, acceptBearer: "token-1"}
	newOSServer(t, srv)

	c := newOpenSubtitlesWithLogin("stub-key", "stub-user", "stub-pass")
	for i := range 3 {
		if _, err := c.ResolveDownloadURL(i + 1); err != nil {
			t.Fatalf("download %d: %v", i, err)
		}
	}
	if srv.loginCount() != 1 {
		t.Fatalf("logins = %d for 3 downloads, want 1", srv.loginCount())
	}
}

// Searching is free and unmetered, so a search must not pay for a login.
// `find` and `episodes` are bounded agent-facing commands and never download
// anything; charging them a round trip for a quota they do not spend is the
// kind of cost this project rejects.
func TestOpenSubtitlesSearchDoesNotLogIn(t *testing.T) {
	srv := &osServer{tokens: []string{"token-1"}}
	newOSServer(t, srv)

	subs, err := newOpenSubtitlesWithLogin("stub-key", "stub-user", "stub-pass").
		Search("Some Film", "en", 0, 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("subs = %d, want 1", len(subs))
	}
	if srv.loginCount() != 0 {
		t.Fatalf("logins = %d, want 0: searching spends no quota", srv.loginCount())
	}
	if got := srv.auths(); len(got) != 1 || got[0] != "SEARCH:" {
		t.Fatalf("search Authorization = %v, want unauthenticated", got)
	}
}

// No credentials is the default and must stay the anonymous tier exactly as it
// was: 5 downloads a day still works, and it must not become an error or a
// wasted request to /login.
func TestOpenSubtitlesWithoutCredentialsStaysAnonymous(t *testing.T) {
	for _, tc := range []struct{ name, user, pass string }{
		{"neither", "", ""},
		{"username only", "stub-user", ""},
		{"password only", "", "stub-pass"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &osServer{tokens: []string{"token-1"}}
			newOSServer(t, srv)

			link, err := newOpenSubtitlesWithLogin("stub-key", tc.user, tc.pass).ResolveDownloadURL(7)
			if err != nil {
				t.Fatalf("ResolveDownloadURL: %v", err)
			}
			if link == "" {
				t.Fatal("no link")
			}
			if srv.loginCount() != 0 {
				t.Fatalf("logins = %d, want 0", srv.loginCount())
			}
			if got := srv.auths(); len(got) != 1 || got[0] != "" {
				t.Fatalf("download Authorization = %v, want unauthenticated", got)
			}
		})
	}
}

// Losing 20/day down to 5/day is much better than losing subtitles, so a
// login that fails has to fall through to the anonymous request rather than
// failing the download.
func TestOpenSubtitlesLoginFailureDegradesToAnonymous(t *testing.T) {
	srv := &osServer{loginStatus: http.StatusUnauthorized}
	newOSServer(t, srv)

	c := newOpenSubtitlesWithLogin("stub-key", "stub-user", "stub-pass")
	link, err := c.ResolveDownloadURL(7)
	if err != nil {
		t.Fatalf("a failed login must not fail the download: %v", err)
	}
	if link != "https://cdn.invalid/sub.srt" {
		t.Fatalf("link = %q", link)
	}
	if got := srv.auths(); len(got) != 1 || got[0] != "" {
		t.Fatalf("download Authorization = %v, want the anonymous request", got)
	}
	// And it must not keep paying for the same failure on every episode.
	if _, err := c.ResolveDownloadURL(8); err != nil {
		t.Fatalf("second download: %v", err)
	}
	if srv.loginCount() != 1 {
		t.Fatalf("logins = %d after two downloads with dead credentials, want 1", srv.loginCount())
	}
}

// The token expires (OpenSubtitles documents roughly a day). A long-running
// session has to notice the rejection and log in again — once — instead of
// reporting the expiry as a download failure.
func TestOpenSubtitlesRelogsInWhenTokenIsRejected(t *testing.T) {
	srv := &osServer{tokens: []string{"stale-token", "fresh-token"}, acceptBearer: "fresh-token"}
	newOSServer(t, srv)

	c := newOpenSubtitlesWithLogin("stub-key", "stub-user", "stub-pass")
	link, err := c.ResolveDownloadURL(7)
	if err != nil {
		t.Fatalf("ResolveDownloadURL: %v", err)
	}
	if link != "https://cdn.invalid/sub.srt" {
		t.Fatalf("link = %q", link)
	}
	if srv.loginCount() != 2 {
		t.Fatalf("logins = %d, want 2 (stale token, then a fresh one)", srv.loginCount())
	}
	want := []string{"Bearer stale-token", "Bearer fresh-token"}
	got := srv.auths()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("download Authorization headers = %v, want %v", got, want)
	}
	// The fresh token is kept: the next episode must not log in a third time.
	if _, err := c.ResolveDownloadURL(8); err != nil {
		t.Fatalf("second download: %v", err)
	}
	if srv.loginCount() != 2 {
		t.Fatalf("logins = %d after the retry, want the fresh token reused", srv.loginCount())
	}
}

// A 401 that survives a fresh login is a real failure and must be reported as
// one, not retried forever.
func TestOpenSubtitlesStopsAfterOneRelogin(t *testing.T) {
	srv := &osServer{tokens: []string{"a", "b", "c"}, acceptBearer: "never-issued"}
	newOSServer(t, srv)

	c := newOpenSubtitlesWithLogin("stub-key", "stub-user", "stub-pass")
	if _, err := c.ResolveDownloadURL(7); err == nil {
		t.Fatal("ResolveDownloadURL = nil error, want the 401 reported")
	} else if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error = %v, want it to name status 401", err)
	}
	if srv.loginCount() > 2 {
		t.Fatalf("logins = %d, want at most 2", srv.loginCount())
	}
	if n := len(srv.auths()); n > 2 {
		t.Fatalf("download attempts = %d, want at most 2", n)
	}
}
