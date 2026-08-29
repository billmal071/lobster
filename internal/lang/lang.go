// Package lang is the single table of audio-language spellings.
//
// It exists because there were two: the player built --alang from one and the
// provider ordered per-dub sources from another, and they drifted. Asking for
// russian set mpv's preference but left the provider's source order untouched,
// so the wrong dub was still selected first. A preference that is honoured in
// one half of the pipeline and ignored in the other is worse than one that is
// unsupported everywhere, because nothing reports it.
package lang

import "strings"

// aliases maps a language name to its ISO 639-2 and 639-1 codes. Sources are
// inconsistent about which they use — the same English track is tagged "eng",
// "en" or "english" depending on who packed it — so all three are offered.
// aliases maps a language name to its ISO codes, 639-2 first then 639-1.
// Sources are inconsistent about which they use — the same English track is
// tagged "eng", "en" or "english" depending on who packed it — so all are
// offered. Several languages have both a bibliographic and a terminological
// 639-2 code (fre/fra, ger/deu, chi/zho, dut/nld) and both appear in the wild,
// so both are listed rather than picking one.
var aliases = map[string][]string{
	"english":    {"eng", "en"},
	"spanish":    {"spa", "es"},
	"french":     {"fre", "fra", "fr"},
	"german":     {"ger", "deu", "de"},
	"italian":    {"ita", "it"},
	"portuguese": {"por", "pt"},
	"russian":    {"rus", "ru"},
	"japanese":   {"jpn", "ja"},
	"korean":     {"kor", "ko"},
	"chinese":    {"chi", "zho", "zh"},
	"arabic":     {"ara", "ar"},
	"turkish":    {"tur", "tr"},
	"hindi":      {"hin", "hi"},
	"tamil":      {"tam", "ta"},
	"telugu":     {"tel", "te"},
	"dutch":      {"dut", "nld", "nl"},
	"polish":     {"pol", "pl"},
}

// Supported returns every language name the table knows.
func Supported() []string {
	out := make([]string, 0, len(aliases))
	for name := range aliases {
		out = append(out, name)
	}
	return out
}

// Aliases returns the spellings to prefer for a language, most specific first,
// so a player given the list tries the ISO codes before the English word. An
// unknown name is returned alone rather than dropped: a degraded match beats a
// silent no-op.
func Aliases(pref string) []string {
	pref = normalize(pref)
	if pref == "" {
		return nil
	}
	if codes, ok := aliases[pref]; ok {
		return append(append([]string{}, codes...), pref)
	}
	return []string{pref}
}

// Matches reports whether a source's language tag names pref. The tag is
// compared on its primary subtag, so "en-US" counts as English.
func Matches(tag, pref string) bool {
	tag = normalize(tag)
	if tag == "" {
		return false
	}
	if i := strings.IndexAny(tag, "-_"); i > 0 {
		tag = tag[:i]
	}
	for _, a := range Aliases(pref) {
		if tag == a {
			return true
		}
	}
	return false
}

func normalize(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
