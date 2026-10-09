package config

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.Player != "mpv" {
		t.Errorf("default player = %q, want mpv", cfg.Player)
	}
	if cfg.Quality != "1080" {
		t.Errorf("default quality = %q, want 1080", cfg.Quality)
	}
	if cfg.Provider != "Default" {
		t.Errorf("default provider = %q, want Default", cfg.Provider)
	}
	if !cfg.History {
		t.Error("default history should be true")
	}
	if cfg.Base != BaseAuto {
		t.Fatalf("Base=%q want %q", cfg.Base, BaseAuto)
	}
	if cfg.AnimeDub {
		t.Error("default AnimeDub should be false (sub)")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{"valid defaults", func(c *Config) {}, false},
		{"invalid player", func(c *Config) { c.Player = "notepad" }, true},
		{"invalid provider", func(c *Config) { c.Provider = "BadServer" }, true},
		{"invalid quality", func(c *Config) { c.Quality = "4k" }, true},
		{"empty base", func(c *Config) { c.Base = "" }, true},
		{"valid vlc", func(c *Config) { c.Player = "vlc" }, false},
		{"valid upcloud", func(c *Config) { c.Provider = "UpCloud" }, false},
		{"valid 720", func(c *Config) { c.Quality = "720" }, false},
		{"max_concurrent_downloads 0", func(c *Config) { c.MaxConcurrentDownloads = 0 }, true},
		{"max_concurrent_downloads 6", func(c *Config) { c.MaxConcurrentDownloads = 6 }, true},
		{"max_concurrent_downloads 1", func(c *Config) { c.MaxConcurrentDownloads = 1 }, false},
		{"max_concurrent_downloads 5", func(c *Config) { c.MaxConcurrentDownloads = 5 }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.modify(cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadFromTOML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := `
base = "moviebox"
player = "vlc"
provider = "Default"
quality = "720"
history = false
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Override config dir for testing (platform-specific)
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", tmpDir)
	} else {
		t.Setenv("XDG_CONFIG_HOME", tmpDir)
	}

	// Create the lobster subdir and move the config
	lobsterDir := filepath.Join(tmpDir, "lobster")
	os.MkdirAll(lobsterDir, 0755)
	os.Rename(configPath, filepath.Join(lobsterDir, "config.toml"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Base != "moviebox" {
		t.Errorf("base = %q, want moviebox", cfg.Base)
	}
	if cfg.Player != "vlc" {
		t.Errorf("player = %q, want vlc", cfg.Player)
	}
	if cfg.Provider != "Default" {
		t.Errorf("provider = %q, want Default", cfg.Provider)
	}
	if cfg.Quality != "720" {
		t.Errorf("quality = %q, want 720", cfg.Quality)
	}
	if cfg.History {
		t.Error("history should be false")
	}
}

func TestLoadMissingFile(t *testing.T) {
	tmp := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", tmp)
	} else {
		t.Setenv("XDG_CONFIG_HOME", tmp)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() should not error on missing file: %v", err)
	}
	if cfg.Player != "mpv" {
		t.Errorf("missing file should return defaults, got player = %q", cfg.Player)
	}
}

func TestLoadAPIURL(t *testing.T) {
	tmpDir := t.TempDir()
	lobsterDir := filepath.Join(tmpDir, "lobster")
	if err := os.MkdirAll(lobsterDir, 0755); err != nil {
		t.Fatal(err)
	}

	content := `
base = "flixhq.to"
player = "mpv"
provider = "Vidcloud"
quality = "1080"
api_url = "https://my-consumet.example.com"
`
	if err := os.WriteFile(filepath.Join(lobsterDir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", tmpDir)
	} else {
		t.Setenv("XDG_CONFIG_HOME", tmpDir)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.APIURL != "https://my-consumet.example.com" {
		t.Errorf("APIURL = %q, want %q", cfg.APIURL, "https://my-consumet.example.com")
	}
}

func TestExpandDownloadDir(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := Default()
	cfg.DownloadDir = tmpDir

	dir, err := cfg.ExpandDownloadDir()
	if err != nil {
		t.Fatalf("ExpandDownloadDir() error: %v", err)
	}
	want, _ := filepath.Abs(tmpDir)
	if dir != want {
		t.Errorf("got %q, want %q", dir, want)
	}
}

func TestHealthPath(t *testing.T) {
	p, err := HealthPath()
	if err != nil {
		t.Fatal(err)
	}
	// dataDir is platform-specific (XDG on Unix, AppData on Windows), so assert
	// the stable contract: a file named health.json under a "lobster" dir.
	if filepath.Base(p) != "health.json" || filepath.Base(filepath.Dir(p)) != "lobster" {
		t.Fatalf("HealthPath=%q want .../lobster/health.json", p)
	}
}

func TestLiveTVDefaultsOn(t *testing.T) {
	if !Default().LiveTV.IPTVOrg {
		t.Fatal("LiveTV.IPTVOrg should default to true")
	}
}

func TestLiveTVSources(t *testing.T) {
	c := LiveTVConfig{
		IPTVOrg:   true,
		Playlists: []string{"https://x/p.m3u", "/home/u/local.m3u"},
		Xtream:    XtreamConfig{Server: "h:8080", Username: "u s", Password: "p&p"},
	}
	got := c.Sources()
	if len(got) != 4 {
		t.Fatalf("want 4 sources, got %d: %v", len(got), got)
	}
	if got[0] != "https://iptv-org.github.io/iptv/index.category.m3u" {
		t.Fatalf("iptv-org first, got %q", got[0])
	}
	if got[1] != "https://x/p.m3u" || got[2] != "/home/u/local.m3u" {
		t.Fatalf("playlists out of order: %v", got)
	}
	if got[3] != "http://h:8080/get.php?username=u+s&password=p%26p&type=m3u_plus&output=m3u8" {
		t.Fatalf("xtream url wrong: %q", got[3])
	}
}

func TestLiveTVSourcesOmitsIPTVOrgWhenOff(t *testing.T) {
	c := LiveTVConfig{IPTVOrg: false}
	if len(c.Sources()) != 0 {
		t.Fatalf("want no sources, got %v", c.Sources())
	}
}

func TestLiveTVSourcesHTTPSXtream(t *testing.T) {
	c := LiveTVConfig{Xtream: XtreamConfig{Server: "https://h:8443", Username: "u", Password: "p"}}
	got := c.Sources()
	if len(got) != 1 || got[0] != "https://h:8443/get.php?username=u&password=p&type=m3u_plus&output=m3u8" {
		t.Fatalf("https xtream url wrong: %v", got)
	}
}

// "best" must be an accepted quality: numeric values cap at their height, so
// without it there is no way to ask for whatever the source actually offers.
func TestValidateAcceptsBestQuality(t *testing.T) {
	for _, q := range []string{"best", "BEST", " best "} {
		c := Default()
		c.Quality = q
		if err := c.Validate(); err != nil {
			t.Errorf("Quality=%q rejected: %v", q, err)
		}
	}
	// Validation must also normalize: the stored value is handed straight to
	// the extractors, where strconv.Atoi(" 720 ") fails and a padded value
	// silently degrades quality selection.
	for _, tc := range []struct{ in, want string }{
		{" best ", "best"},
		{"BEST", "best"},
		{" 720 ", "720"},
	} {
		c := Default()
		c.Quality = tc.in
		if err := c.Validate(); err != nil {
			t.Fatalf("Quality=%q rejected: %v", tc.in, err)
		}
		if c.Quality != tc.want {
			t.Errorf("Quality=%q not normalized: got %q, want %q", tc.in, c.Quality, tc.want)
		}
	}

	for _, q := range []string{"9999", "max", "highest"} {
		c := Default()
		c.Quality = q
		if err := c.Validate(); err == nil {
			t.Errorf("Quality=%q should be rejected: one documented spelling only", q)
		}
	}
}

func TestTBCPLDefaults(t *testing.T) {
	c := Default()
	if !c.TBCPLFeed {
		t.Errorf("TBCPLFeed default = false, want true")
	}
	if c.TBCPLRegion != "" {
		t.Errorf("TBCPLRegion default = %q, want empty", c.TBCPLRegion)
	}
	if c.TBCPLIncludeUntrusted {
		t.Errorf("TBCPLIncludeUntrusted default = true, want false")
	}
}

func TestTBCPLCachePath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p, err := TBCPLCachePath()
	if err != nil {
		t.Fatalf("TBCPLCachePath: %v", err)
	}
	if filepath.Base(p) != "tbcpl-cache.json" {
		t.Errorf("cache path base = %q, want tbcpl-cache.json", filepath.Base(p))
	}
}

// A "~/..." playlist path must expand like download_dir does, rather than
// reaching os.ReadFile verbatim and failing with "no such file or directory".
func TestLiveTVSourcesExpandsTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	c := LiveTVConfig{
		IPTVOrg: false,
		Playlists: []string{
			"~/playlists/mine.m3u",
			"https://example.com/keep.m3u8", // URLs must pass through untouched
			"/already/absolute.m3u",
			"~notauser/literal.m3u", // only "~/" is a home reference
		},
	}
	got := c.Sources()
	want := []string{
		filepath.Join(home, "playlists/mine.m3u"),
		"https://example.com/keep.m3u8",
		"/already/absolute.m3u",
		"~notauser/literal.m3u",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Sources() = %v, want %v", got, want)
	}
}

// The `~\` form is a home reference on Windows only. On Unix a backslash is an
// ordinary filename character, so `~\playlists\mine.m3u` names a real relative
// path and expanding it silently loads a different file than the user asked
// for. expandTilde's own doc comment says "on Windows"; this pins the code to
// that promise.
func TestExpandTildeBackslashIsWindowsOnly(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	const in = `~\playlists\mine.m3u`
	got := expandTilde(in)

	if runtime.GOOS == "windows" {
		want := filepath.Join(home, `playlists\mine.m3u`)
		if got != want {
			t.Fatalf("expandTilde(%q) = %q, want %q (backslash is a home reference on Windows)", in, got, want)
		}
		return
	}
	if got != in {
		t.Fatalf("expandTilde(%q) = %q, want it unchanged: on %s a backslash is part of the filename, not a separator", in, got, runtime.GOOS)
	}
}

// Base is compared in three separate places in cmd — case-insensitively in
// mayStreamTorrent, exactly in baseIsAuto, and by substring in newProvider —
// and it arrives from two inputs that neither trim nor case-fold it: a
// config.toml `base =` line and the --base flag. Validate is the one routine
// that runs after both have landed (Load, then again in applyConfig after the
// flag overrides), so it is where the value has to be made canonical. Without
// this, `base = "AUTO"` was auto for one reader and an unrecognised base for
// the others.
//
// A third input, a ref's stamped base, never reaches Validate at all; it
// canonicalises through the same NormalizeBase in applyRefBase, and is
// asserted on the ref path in cmd/lateref_storage_test.go.
func TestValidateNormalizesBase(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"AUTO", BaseAuto},
		{"  auto  ", BaseAuto},
		{"YTS", "yts"},
		{"FlixHQ.WS", "flixhq.ws"},
		{"soap2day", "soap2day"},
	} {
		c := Default()
		c.Base = tc.in
		if err := c.Validate(); err != nil {
			t.Fatalf("Validate() with base %q = %v, want nil", tc.in, err)
		}
		if c.Base != tc.want {
			t.Errorf("Validate() left base %q as %q, want %q", tc.in, c.Base, tc.want)
		}
	}
}

// A base of nothing but whitespace is an empty base once trimmed, and has to
// be rejected as one rather than reaching newProvider as a value that matches
// no branch and falls through to MovieBox.
func TestValidateRejectsAWhitespaceOnlyBase(t *testing.T) {
	c := Default()
	c.Base = "   "
	if err := c.Validate(); err == nil {
		t.Fatalf("Validate() with a whitespace-only base = nil, want an error")
	}
}

// Sources prepends a scheme to a scheme-less [live_tv.xtream].server, so a
// bare IPv6 address there is a host we assemble into a URL rather than a URL
// the user wrote. That assembly has to produce a legal URL: Go 1.26 made
// url.Parse reject an unbracketed IPv6 authority (GODEBUG urlstrictcolons),
// and the lenient parse it replaced split on the last colon, so
// "2001:db8::1" used to come out as host "2001:db8:" port 1 and dial nothing
// that exists. Bracketing is the fix for both.
func TestLiveTVSourcesBracketsBareIPv6XtreamServer(t *testing.T) {
	cases := []struct {
		server string
		want   string
	}{
		// IPv6 literal, no port — one reading only, because the text
		// before the last colon ("2001:db8:") is not an address.
		{"2001:db8::1", "http://[2001:db8::1]/get.php?username=u&password=p&type=m3u_plus&output=m3u8"},
		{"fe80::1", "http://[fe80::1]/get.php?username=u&password=p&type=m3u_plus&output=m3u8"},
		{"::1", "http://[::1]/get.php?username=u&password=p&type=m3u_plus&output=m3u8"},
		// IPv6 literal with a port — one reading only, because the whole
		// string is nine groups and so not an address.
		{"2001:db8:1:2:3:4:5:6:8080", "http://[2001:db8:1:2:3:4:5:6]:8080/get.php?username=u&password=p&type=m3u_plus&output=m3u8"},
		// Already bracketed: must not be bracketed twice.
		{"[::1]:8080", "http://[::1]:8080/get.php?username=u&password=p&type=m3u_plus&output=m3u8"},
		{"[::1]", "http://[::1]/get.php?username=u&password=p&type=m3u_plus&output=m3u8"},
		{"[2001:db8::1]:8080", "http://[2001:db8::1]:8080/get.php?username=u&password=p&type=m3u_plus&output=m3u8"},
		// Hostnames and IPv4 are untouched.
		{"tv.example.com:8080", "http://tv.example.com:8080/get.php?username=u&password=p&type=m3u_plus&output=m3u8"},
		{"tv.example.com", "http://tv.example.com/get.php?username=u&password=p&type=m3u_plus&output=m3u8"},
		{"1.2.3.4:8080", "http://1.2.3.4:8080/get.php?username=u&password=p&type=m3u_plus&output=m3u8"},
	}
	for _, tc := range cases {
		c := LiveTVConfig{Xtream: XtreamConfig{Server: tc.server, Username: "u", Password: "p"}}
		got := c.Sources()
		if len(got) != 1 {
			t.Fatalf("server %q: want 1 source, got %v", tc.server, got)
		}
		if got[0] != tc.want {
			t.Errorf("server %q: Sources()[0] = %q, want %q", tc.server, got[0], tc.want)
		}
		// The guarantee that actually matters: the assembled URL parses.
		if _, err := url.Parse(got[0]); err != nil {
			t.Errorf("server %q: assembled URL does not parse: %v", tc.server, err)
		}
	}
}

// "::1:8080" is genuinely ambiguous: it is a valid IPv6 address on its own
// *and* a valid address-plus-port split. Guessing either way would dial a host
// the user did not write, so Sources leaves it alone and url.Parse rejects it
// — a failed source naming the host beats a silent connection elsewhere.
func TestLiveTVSourcesLeavesAmbiguousIPv6AuthorityAlone(t *testing.T) {
	// "2001:db8:1:2:3:4:5:6:99999" is a different kind of refusal: the
	// trailing group is not a port at all, so there is no host:port reading
	// and no address reading either. Bracketing the first eight groups would
	// invent a port url.Parse happens to accept because it only checks that
	// the characters are digits.
	for _, server := range []string{"::1:8080", "fe80::1:80", "2001:db8::1:443", "2001:db8:1:2:3:4:5:6:99999"} {
		c := LiveTVConfig{Xtream: XtreamConfig{Server: server, Username: "u", Password: "p"}}
		got := c.Sources()
		if len(got) != 1 {
			t.Fatalf("server %q: want 1 source, got %v", server, got)
		}
		if want := "http://" + server + "/get.php?username=u&password=p&type=m3u_plus&output=m3u8"; got[0] != want {
			t.Errorf("server %q: Sources()[0] = %q, want it passed through as %q", server, got[0], want)
		}
		if _, err := url.Parse(got[0]); err == nil {
			t.Errorf("server %q: url.Parse accepted the ambiguous authority; the error is the point", server)
		}
	}
}

// End to end over the path that broke: Sources() -> http.NewRequestWithContext
// -> a real connection. The loopback address is written in full-form so the
// authority is unambiguous; "::1:<port>" is the ambiguous shape covered above.
func TestLiveTVSourcesXtreamURLReachesABareIPv6Server(t *testing.T) {
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback on this host: %v", err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "#EXTM3U\n")
	}))
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)

	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort(%q) = %v", ln.Addr(), err)
	}
	c := LiveTVConfig{Xtream: XtreamConfig{Server: "0:0:0:0:0:0:0:1:" + port, Username: "u", Password: "p"}}
	src := c.Sources()[0]

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, src, nil)
	if err != nil {
		t.Fatalf("building a request for %q failed: %v", src, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %q failed: %v", src, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the body failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "#EXTM3U\n" {
		t.Fatalf("got %d %q, want 200 %q", resp.StatusCode, body, "#EXTM3U\n")
	}
}
