package provider

import (
	"strings"
	"testing"

	"lobster/internal/media"
)

// Compile-time proof the type satisfies the streaming interface.
var _ StreamProvider = (*AniPub)(nil)

func newTestAniPub(routes map[string]string) *AniPub {
	p := NewAniPub()
	p.client = fakeDoer{routes: routes} // fakeDoer is defined in allanime_test.go (same package)
	return p
}

func TestAniPubResolveByTitle(t *testing.T) {
	p := newTestAniPub(map[string]string{
		`/api/search/Death`:    `[{"Name":"Death Note","Id":38,"Image":"http://img/dn.jpg","finder":"death-note"}]`,
		`/v1/api/details/38`:   `{"local":{"ep":[{"link":"src=https://gogoanime.com.by/streaming.php?id=x&ep=1465&server=hd-1&type=dub"},{"link":"src=...&ep=1466&server=hd-1&type=dub"}]}}`,
		`/stream/s-2/1466/sub`: `<div data-id="55501"></div>`,
		`getSources?id=55501`:  `{"sources":{"file":"https://cdn/master.m3u8"},"tracks":[{"file":"https://cdn/en.vtt","label":"English","kind":"captions"}]}`,
	})
	st, err := p.ResolveByTitle("Death Note", 2, false) // episode 2, sub
	if err != nil {
		t.Fatal(err)
	}
	if st.URL != "https://cdn/master.m3u8" {
		t.Fatalf("stream URL = %q", st.URL)
	}
	if len(st.Subtitles) != 1 || st.Subtitles[0].URL != "https://cdn/en.vtt" {
		t.Fatalf("subtitles wrong: %+v", st.Subtitles)
	}
	if st.Referer != "https://megaplay.buzz/" {
		t.Fatalf("referer = %q", st.Referer)
	}
	if !st.Deobfuscate {
		t.Fatal("megaplay stream must be flagged for de-obfuscation")
	}
}

func TestAniPubVideoFormatAndNoMatch(t *testing.T) {
	// Cowboy Bebop uses the /video/{id} link shape (not ?ep=).
	p := newTestAniPub(map[string]string{
		`/api/search/Cowboy`:   `[{"Name":"Cowboy Bebop","Id":8270,"Image":"x"}]`,
		`/v1/api/details/8270`: `{"local":{"ep":[{"link":"src=https://anipub.xyz/video/850/sub"}]}}`,
		`/stream/s-2/850/sub`:  `<div data-id="41014"></div>`,
		`getSources?id=41014`:  `{"sources":{"file":"https://cdn/cb.m3u8"},"tracks":[]}`,
	})
	st, err := p.ResolveByTitle("Cowboy Bebop", 1, false)
	if err != nil || st == nil || st.URL != "https://cdn/cb.m3u8" {
		t.Fatalf("video-format resolve failed: %v / %v", err, st)
	}
	// found:false (object, not array) -> graceful no-results, not a parse error.
	p2 := newTestAniPub(map[string]string{`/api/search/zzz`: `{"found":false}`})
	if _, err := p2.Search("zzz"); err == nil {
		t.Fatal("expected no-results error for found:false")
	}
}

func TestAniPubSearch(t *testing.T) {
	p := newTestAniPub(map[string]string{
		`/api/search/naruto`: `[{"Name":"Naruto","Id":20,"Image":"http://i/n.jpg","finder":"naruto"}]`,
	})
	res, err := p.Search("naruto")
	if err != nil || len(res) != 1 || res[0].ID != "20" || res[0].Title != "Naruto" || res[0].Type != media.TV {
		t.Fatalf("search wrong: %v / %+v", err, res)
	}
}

// AniPub's 2026 details shape: the top-level "link" is episode 1 and "ep"
// holds episodes 2..N, all as /play/{malID}/{ep}/{audio} links that resolve
// through megaplay's /stream/mal/ route.
func TestAniPubPlayLinkFormat(t *testing.T) {
	routes := map[string]string{
		`/api/search/KAMUI`:       `[{"Name":"Ninja Kamui","Id":2267,"finder":"ninja-kamui"},{"Name":"KAMUI: He's Behind You","Id":8420,"finder":"kamui-he-s-behind-you"}]`,
		`/v1/api/details/8420`:    `{"local":{"_id":8420,"name":"Episode 1","link":"src=https://anipub.xyz/play/63468/1/sub","type":"iframe","ep":[{"link":"src=https://anipub.xyz/play/63468/2/sub"},{"link":"src=https://anipub.xyz/play/63468/3/sub"}]}}`,
		`/stream/mal/63468/1/sub`: `<div data-id="177688"></div>`,
		`/stream/mal/63468/3/sub`: `<div data-id="177690"></div>`,
		`getSources?id=177688`:    `{"sources":{"file":"https://cdn/kamui-ep1.m3u8"},"tracks":[]}`,
		`getSources?id=177690`:    `{"sources":{"file":"https://cdn/kamui-ep3.m3u8"},"tracks":[]}`,
	}

	// Episode 1 comes from the top-level link, which the old parser ignored.
	p := newTestAniPub(routes)
	st, err := p.ResolveByTitle("KAMUI: He's Behind You", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if st.URL != "https://cdn/kamui-ep1.m3u8" {
		t.Fatalf("ep1 URL = %q", st.URL)
	}

	// Episode 3 comes from the ep array, offset by the top-level link.
	st, err = p.ResolveByTitle("KAMUI: He's Behind You", 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if st.URL != "https://cdn/kamui-ep3.m3u8" {
		t.Fatalf("ep3 URL = %q", st.URL)
	}
}

// The fallback resolver reconstructs episode IDs as "showID:season:episode"
// (and passes "" for movies); Watch must accept those alongside native
// megaplay refs.
func TestAniPubWatchNumericEpisodeID(t *testing.T) {
	routes := map[string]string{
		`/v1/api/details/8420`:    `{"local":{"link":"src=https://anipub.xyz/play/63468/1/sub","ep":[{"link":"src=https://anipub.xyz/play/63468/2/sub"}]}}`,
		`/stream/mal/63468/2/sub`: `<div data-id="9"></div>`,
		`/stream/mal/63468/1/sub`: `<div data-id="8"></div>`,
		`getSources?id=9`:         `{"sources":{"file":"https://cdn/ep2.m3u8"},"tracks":[]}`,
		`getSources?id=8`:         `{"sources":{"file":"https://cdn/ep1.m3u8"},"tracks":[]}`,
	}

	t.Run("resolver-style show:season:episode", func(t *testing.T) {
		st, err := newTestAniPub(routes).Watch("8420", "8420:1:2", "Default", "1080")
		if err != nil {
			t.Fatal(err)
		}
		if st.URL != "https://cdn/ep2.m3u8" {
			t.Fatalf("URL = %q", st.URL)
		}
	})

	t.Run("empty episode ID resolves episode 1", func(t *testing.T) {
		st, err := newTestAniPub(routes).Watch("8420", "", "Default", "1080")
		if err != nil {
			t.Fatal(err)
		}
		if st.URL != "https://cdn/ep1.m3u8" {
			t.Fatalf("URL = %q", st.URL)
		}
	})

	t.Run("season beyond 1 is rejected", func(t *testing.T) {
		if _, err := newTestAniPub(routes).Watch("8420", "8420:2:1", "Default", "1080"); err == nil || !strings.Contains(err.Error(), "season") {
			t.Fatalf("want season rejection, got %v", err)
		}
	})

	t.Run("native megaplay ref still works", func(t *testing.T) {
		st, err := newTestAniPub(routes).Watch("8420", "mal:63468:2", "sub", "1080")
		if err != nil {
			t.Fatal(err)
		}
		if st.URL != "https://cdn/ep2.m3u8" {
			t.Fatalf("URL = %q", st.URL)
		}
	})
}

// AniPub returns a bare object (not a one-element array) when exactly one
// show matches, e.g. an exact full-title search.
func TestAniPubSearchSingleObjectResult(t *testing.T) {
	p := newTestAniPub(map[string]string{
		`/api/search/KAMUI`: `{"Name":"KAMUI: He's Behind You","Id":8420,"finder":"kamui-he-s-behind-you","Image":"https://cdn/img.jpg"}`,
	})
	res, err := p.Search("KAMUI: He's Behind You")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ID != "8420" || res[0].Title != "KAMUI: He's Behind You" {
		t.Fatalf("single-object result wrong: %+v", res)
	}
}
