package provider

import "testing"

func TestParseM3UBasic(t *testing.T) {
	data := []byte(`#EXTM3U x-tvg-url="https://guide"
#EXTINF:-1 tvg-id="ASpor.tr@SD" tvg-logo="https://i/logo.png" group-title="Sports",A Spor (1080p)
https://cdn/aspor.m3u8
`)
	chs := ParseM3U(data)
	if len(chs) != 1 {
		t.Fatalf("want 1 channel, got %d", len(chs))
	}
	c := chs[0]
	if c.ID != "ASpor.tr@SD" || c.Name != "A Spor (1080p)" || c.Logo != "https://i/logo.png" {
		t.Fatalf("bad parse: %+v", c)
	}
	if len(c.Categories) != 1 || c.Categories[0] != "Sports" {
		t.Fatalf("bad categories: %+v", c.Categories)
	}
	if c.URL != "https://cdn/aspor.m3u8" {
		t.Fatalf("bad url: %q", c.URL)
	}
}

func TestParseM3UMultiCategory(t *testing.T) {
	data := []byte("#EXTINF:-1 group-title=\"News;Sports\",X\nhttp://u\n")
	c := ParseM3U(data)[0]
	if len(c.Categories) != 2 || c.Categories[0] != "News" || c.Categories[1] != "Sports" {
		t.Fatalf("multi-category split wrong: %+v", c.Categories)
	}
}

func TestParseM3UUndefinedAndEmptyCategory(t *testing.T) {
	for _, gt := range []string{`group-title="Undefined"`, `group-title=""`, ``} {
		data := []byte("#EXTINF:-1 " + gt + ",X\nhttp://u\n")
		c := ParseM3U(data)[0]
		if len(c.Categories) != 1 || c.Categories[0] != "Uncategorized" {
			t.Fatalf("gt %q -> %+v, want [Uncategorized]", gt, c.Categories)
		}
	}
}

func TestParseM3UCommasInAttrAndName(t *testing.T) {
	data := []byte(`#EXTINF:-1 group-title="A, B" tvg-logo="http://i/a,b.png",Name, With Comma
http://u
`)
	c := ParseM3U(data)[0]
	if c.Name != "Name, With Comma" {
		t.Fatalf("name split wrong: %q", c.Name)
	}
	if len(c.Categories) != 1 || c.Categories[0] != "A, B" {
		t.Fatalf("attr-comma split wrong: %+v", c.Categories)
	}
}

func TestParseM3UEmptyTvgIDFallsBackToSlug(t *testing.T) {
	data := []byte("#EXTINF:-1 tvg-id=\"\" group-title=\"Sports\",Hello World\nhttp://u\n")
	c := ParseM3U(data)[0]
	if c.ID != "hello-world" {
		t.Fatalf("slug fallback wrong: %q", c.ID)
	}
}

func TestParseM3UHeaders(t *testing.T) {
	data := []byte(`#EXTINF:-1 group-title="Sports",X
#EXTVLCOPT:http-user-agent=Mozilla/5.0 (X11) key=val
#EXTVLCOPT:http-referrer=https://ref/
http://u
`)
	c := ParseM3U(data)[0]
	if c.UserAgent != "Mozilla/5.0 (X11) key=val" {
		t.Fatalf("UA split-on-first-= wrong: %q", c.UserAgent)
	}
	if c.Referer != "https://ref/" {
		t.Fatalf("referrer wrong: %q", c.Referer)
	}
}

func TestParseM3USkipsBadLinesAndNoURL(t *testing.T) {
	// First EXTINF has no URL (immediately followed by another EXTINF) -> dropped.
	// Blank lines and unknown # directives are skipped.
	data := []byte(`#EXTINF:-1 group-title="A",NoUrl

#EXTGRP:ignored
#EXTINF:-1 group-title="B",HasUrl
http://u
`)
	chs := ParseM3U(data)
	if len(chs) != 1 || chs[0].Name != "HasUrl" {
		t.Fatalf("want only HasUrl, got %+v", chs)
	}
}

func TestParseM3UCRLF(t *testing.T) {
	data := []byte("#EXTINF:-1 group-title=\"A\",X\r\nhttp://u\r\n")
	c := ParseM3U(data)
	if len(c) != 1 || c[0].URL != "http://u" {
		t.Fatalf("CRLF handling wrong: %+v", c)
	}
}
