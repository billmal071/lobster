package provider

import (
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
