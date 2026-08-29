package provider

import "testing"

// Vidzee lists one source per audio dub and does not order them predictably.
// Taking the first is how a Hindi dub ends up playing for an English film on
// every one of the four servers.
func TestPreferEnglishSourcePicksEnglishNotFirst(t *testing.T) {
	srcs := []tbcplVidzeeSource{
		{Lang: "hindi", Link: "hi"},
		{Lang: "tamil", Link: "ta"},
		{Lang: "english", Link: "en"},
		{Lang: "telugu", Link: "te"},
	}
	got := preferAudioLang(srcs, "english")
	if len(got) == 0 || got[0].Link != "en" {
		t.Fatalf("english source not first: %+v", got)
	}
	// The rest must survive as fallbacks — an English source that fails to
	// decrypt should not leave the caller with nothing to try.
	if len(got) != len(srcs) {
		t.Errorf("dropped candidates: got %d, want %d", len(got), len(srcs))
	}
}

func TestPreferAudioLangAcceptsCodeSpellings(t *testing.T) {
	for _, tag := range []string{"eng", "en", "English", "en-US"} {
		srcs := []tbcplVidzeeSource{{Lang: "hindi", Link: "hi"}, {Lang: tag, Link: "en"}}
		if got := preferAudioLang(srcs, "english"); got[0].Link != "en" {
			t.Errorf("tag %q not matched as english", tag)
		}
	}
}

// No tagged match must not reorder anything: the provider's own order is the
// only signal left, and a stable list keeps behaviour predictable.
func TestPreferAudioLangNoMatchKeepsOrder(t *testing.T) {
	srcs := []tbcplVidzeeSource{{Lang: "hindi", Link: "hi"}, {Lang: "tamil", Link: "ta"}}
	got := preferAudioLang(srcs, "english")
	if got[0].Link != "hi" || got[1].Link != "ta" {
		t.Errorf("order changed with no match: %+v", got)
	}
}

func TestPreferAudioLangUntaggedSourcesUnchanged(t *testing.T) {
	srcs := []tbcplVidzeeSource{{Link: "a"}, {Link: "b"}}
	if got := preferAudioLang(srcs, "english"); got[0].Link != "a" {
		t.Errorf("untagged order changed: %+v", got)
	}
}
