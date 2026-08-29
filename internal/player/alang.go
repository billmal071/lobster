package player

import "strings"

// audioLangCodes maps a language name to the spellings a muxer might have
// written into the track's language tag. Releases are inconsistent: the same
// English track is tagged "eng" (ISO 639-2), "en" (639-1) or "english"
// depending on who packed it, so all three have to be offered or the
// preference silently misses.
var audioLangCodes = map[string][2]string{
	"english":    {"eng", "en"},
	"spanish":    {"spa", "es"},
	"french":     {"fre", "fr"},
	"german":     {"ger", "de"},
	"italian":    {"ita", "it"},
	"portuguese": {"por", "pt"},
	"russian":    {"rus", "ru"},
	"japanese":   {"jpn", "ja"},
	"korean":     {"kor", "ko"},
	"chinese":    {"chi", "zh"},
	"arabic":     {"ara", "ar"},
	"turkish":    {"tur", "tr"},
	"hindi":      {"hin", "hi"},
	"tamil":      {"tam", "ta"},
	"telugu":     {"tel", "te"},
	"dutch":      {"dut", "nl"},
	"polish":     {"pol", "pl"},
}

// audioLangList returns the ordered language tags to prefer, most specific
// first. An unknown name is still passed through on its own, so an uncommon
// language is a degraded match rather than a silent no-op.
func audioLangList(pref string) []string {
	pref = strings.ToLower(strings.TrimSpace(pref))
	if pref == "" {
		return nil
	}
	if codes, ok := audioLangCodes[pref]; ok {
		return []string{codes[0], codes[1], pref}
	}
	return []string{pref}
}

// audioLangArgs returns the mpv argument selecting the preferred audio track.
// Without it mpv plays whichever track comes first, which on a multi-dub
// release is routinely not the original language.
func audioLangArgs(pref string) []string {
	list := audioLangList(pref)
	if len(list) == 0 {
		return nil
	}
	return []string{"--alang=" + strings.Join(list, ",")}
}

// vlcAudioLangArgs is the same preference in VLC's spelling.
func vlcAudioLangArgs(pref string) []string {
	list := audioLangList(pref)
	if len(list) == 0 {
		return nil
	}
	return []string{"--audio-language=" + strings.Join(list, ",")}
}
