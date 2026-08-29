package player

import "testing"

// The audio-language preference has to reach the player: a Hindi-dubbed release
// with tagged tracks otherwise plays whichever track the muxer happened to put
// first, which is what "all 4 audio tracks are Hindi" looks like from the couch.
func TestAudioLangArgsPrefersEnglishSpellings(t *testing.T) {
	got := audioLangArgs("english")
	// mpv matches on the track's language tag, and releases spell it eng, en or
	// english depending on the muxer — all three have to be listed, in order.
	want := "--alang=eng,en,english"
	if len(got) != 1 || got[0] != want {
		t.Errorf("audioLangArgs(english) = %v, want [%s]", got, want)
	}
}

func TestAudioLangArgsEmptyPreferenceAddsNothing(t *testing.T) {
	if got := audioLangArgs(""); len(got) != 0 {
		t.Errorf("audioLangArgs(\"\") = %v, want no args", got)
	}
}

func TestAudioLangArgsUnknownLanguagePassedThrough(t *testing.T) {
	got := audioLangArgs("japanese")
	want := "--alang=jpn,ja,japanese"
	if len(got) != 1 || got[0] != want {
		t.Errorf("audioLangArgs(japanese) = %v, want [%s]", got, want)
	}
}
