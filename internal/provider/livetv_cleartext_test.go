package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// captureWarnings redirects liveTVWarn into a slice for the duration of a
// test, restoring it afterwards.
func captureWarnings(t *testing.T) *[]string {
	t.Helper()
	var got []string
	prev := liveTVWarn
	liveTVWarn = func(format string, args ...any) {
		got = append(got, strings.TrimSpace(fmt.Sprintf(format, args...)))
	}
	t.Cleanup(func() { liveTVWarn = prev })
	return &got
}

func TestCarriesCredentials(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"http://host.example/get.php?username=alice&password=a1", true},
		// Userinfo without a password: carriesCredentials keys on
		// u.User != nil, so this covers the same branch as a
		// username-and-password userinfo would, without planting a
		// password-shaped string in the tree for secret scanners to flag.
		{"http://alice@host.example/list.m3u", true},
		{"http://host.example/list.m3u?token=abc", true},
		// The OAuth-shaped pair. "token" alone does not match these: the
		// lookup is on the whole key, not a substring, so access_token and
		// refresh_token each have to be listed.
		{"http://host.example/list.m3u?access_token=abc", true},
		{"http://host.example/list.m3u?refresh_token=abc", true},
		{"http://host.example/get.php?USERNAME=alice", true}, // key match is case-insensitive
		{"http://host.example/get.php?type=m3u_plus&output=m3u8", false},
		{"http://host.example/list.m3u", false},
	}
	for _, c := range cases {
		u := mustParse(t, c.raw)
		if got := carriesCredentials(u); got != c.want {
			t.Errorf("carriesCredentials(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

// A credentialed http source is fetched, not refused — the deliberate choice
// — but it must say so, and the warning must not repeat the credential it is
// warning about.
func TestFetchWarnsOnCleartextCredentialsWithoutRepeatingThem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("#EXTM3U\n#EXTINF:-1,Alpha\nhttp://example.invalid/1.m3u8\n"))
	}))
	t.Cleanup(srv.Close)

	warnings := captureWarnings(t)
	src := srv.URL + "/get.php?username=alice&password=hunter2"

	p := NewLiveTV([]string{src})
	if err := p.LoadContext(context.Background()); err != nil {
		t.Fatalf("LoadContext: %v", err)
	}
	// Warned, and still loaded: the source is not rejected.
	if len(p.AllChannels()) != 1 {
		t.Fatalf("got %d channels, want 1 — a credentialed http source must still load", len(p.AllChannels()))
	}
	if len(*warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", *warnings)
	}
	if strings.Contains((*warnings)[0], "hunter2") || strings.Contains((*warnings)[0], "alice") {
		t.Fatalf("the warning repeats the credential it is warning about: %q", (*warnings)[0])
	}
}

// Only once per source, even though the TLS-1.2 fallback re-runs the GET.
func TestFetchWarnsOncePerSource(t *testing.T) {
	warnings := captureWarnings(t)
	// An unreachable host makes the primary attempt fail and the fallback
	// client run a second GET for the same URL.
	p := NewLiveTV([]string{"http://127.0.0.1:1/get.php?username=alice&password=a1"})
	_ = p.LoadContext(context.Background())

	if len(*warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one despite the TLS fallback retry", *warnings)
	}
}

// A plain http source with no credentials is nobody's problem.
func TestFetchDoesNotWarnWithoutCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("#EXTM3U\n#EXTINF:-1,Alpha\nhttp://example.invalid/1.m3u8\n"))
	}))
	t.Cleanup(srv.Close)

	warnings := captureWarnings(t)
	p := NewLiveTV([]string{srv.URL + "/list.m3u?type=m3u_plus"})
	if err := p.LoadContext(context.Background()); err != nil {
		t.Fatalf("LoadContext: %v", err)
	}
	if len(*warnings) != 0 {
		t.Fatalf("warnings = %v, want none", *warnings)
	}
}

// The redirect hook warns on an https->http downgrade that carries
// credentials, and keeps following (warn, don't reject).
func TestCheckRedirectWarnsOnDowngrade(t *testing.T) {
	warnings := captureWarnings(t)

	req, err := http.NewRequest(http.MethodGet, "http://host.example/get.php?username=alice&password=a1", nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	prev, err := http.NewRequest(http.MethodGet, "https://host.example/get.php?username=alice&password=a1", nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}

	if err := liveTVCheckRedirect(req, []*http.Request{prev}); err != nil {
		t.Fatalf("liveTVCheckRedirect must keep following, got %v", err)
	}
	if len(*warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", *warnings)
	}
	if strings.Contains((*warnings)[0], "a1") {
		t.Fatalf("the warning repeats the credential: %q", (*warnings)[0])
	}
}

// The 10-hop cap that setting CheckRedirect would otherwise remove.
func TestCheckRedirectStillCapsRedirectChains(t *testing.T) {
	captureWarnings(t)
	req, err := http.NewRequest(http.MethodGet, "https://host.example/a", nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	via := make([]*http.Request, 10)
	for i := range via {
		via[i] = req
	}
	if err := liveTVCheckRedirect(req, via); err == nil {
		t.Fatal("liveTVCheckRedirect must stop after 10 redirects")
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parsing %q: %v", raw, err)
	}
	return u
}
