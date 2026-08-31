package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// vaplayer returns several stream URLs per title — different mirrors, and in
// practice different dubs. Only the first was ever used, so an English film
// served with a Hindi track first had no reachable alternative.
func TestVaPlayerGetServersExposesEveryStreamURL(t *testing.T) {
	srv := vaplayerStub(t, []string{"http://a/1.m3u8", "http://b/2.m3u8", "http://c/3.m3u8"})
	defer srv.Close()

	vp := NewVaPlayer()
	servers, err := vp.GetServers("movie/1930", "")
	if err != nil {
		t.Fatalf("GetServers: %v", err)
	}
	if len(servers) != 3 {
		t.Fatalf("got %d servers, want 3: %+v", len(servers), servers)
	}
	if servers[0].Name != "Source 1" || servers[2].Name != "Source 3" {
		t.Errorf("unexpected names: %+v", servers)
	}
}

func TestVaPlayerWatchHonoursSelectedSource(t *testing.T) {
	srv := vaplayerStub(t, []string{"http://a/1.m3u8", "http://b/2.m3u8", "http://c/3.m3u8"})
	defer srv.Close()

	vp := NewVaPlayer()
	for name, want := range map[string]string{
		"Source 1": "http://a/1.m3u8",
		"Source 2": "http://b/2.m3u8",
		"Source 3": "http://c/3.m3u8",
	} {
		st, err := vp.Watch("movie/1930", "", name, "1080")
		if err != nil {
			t.Fatalf("Watch(%s): %v", name, err)
		}
		if st.URL != want {
			t.Errorf("Watch(%s) = %s, want %s", name, st.URL, want)
		}
	}
}

// An unknown or legacy server name must still play rather than error — the
// resolver and history both pass names this provider never issued.
func TestVaPlayerWatchUnknownSourceFallsBackToFirst(t *testing.T) {
	srv := vaplayerStub(t, []string{"http://a/1.m3u8", "http://b/2.m3u8"})
	defer srv.Close()

	vp := NewVaPlayer()
	// "Source 2 legacy" must NOT select source 2: fmt.Sscanf stops after %d
	// and ignores the trailing text, so a name this provider never issued
	// would otherwise index stream_urls[1] instead of falling back.
	for _, name := range []string{"", "Default", "VaPlayer", "Source 99", "Source 2 legacy", "Source 2x", "Source 2."} {
		st, err := vp.Watch("movie/1930", "", name, "1080")
		if err != nil {
			t.Fatalf("Watch(%q): %v", name, err)
		}
		if st.URL != "http://a/1.m3u8" {
			t.Errorf("Watch(%q) = %s, want the first source", name, st.URL)
		}
	}
}

func vaplayerStub(t *testing.T, urls []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"status_code": "200",
			"data": map[string]any{
				"title":       "The Amazing Spider-Man",
				"stream_urls": urls,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	old := vaplayerBase
	vaplayerBase = srv.URL
	t.Cleanup(func() { vaplayerBase = old })
	return srv
}
