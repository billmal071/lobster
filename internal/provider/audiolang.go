package provider

import (
	"sort"
	"strings"
)

// audioLangAliases are the spellings a source may use for a language tag.
// Vidzee and friends are inconsistent — "english", "eng", "en" and "en-US" all
// appear for the same dub.
var audioLangAliases = map[string][]string{
	"english":    {"english", "eng", "en"},
	"spanish":    {"spanish", "spa", "es"},
	"french":     {"french", "fre", "fra", "fr"},
	"german":     {"german", "ger", "deu", "de"},
	"italian":    {"italian", "ita", "it"},
	"portuguese": {"portuguese", "por", "pt"},
	"japanese":   {"japanese", "jpn", "ja"},
	"korean":     {"korean", "kor", "ko"},
	"hindi":      {"hindi", "hin", "hi"},
	"tamil":      {"tamil", "tam", "ta"},
	"telugu":     {"telugu", "tel", "te"},
}

// matchesAudioLang reports whether a source's language tag names pref. The tag
// is matched on its primary subtag so "en-US" counts as English.
func matchesAudioLang(tag, pref string) bool {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return false
	}
	if i := strings.IndexAny(tag, "-_"); i > 0 {
		tag = tag[:i]
	}
	pref = strings.ToLower(strings.TrimSpace(pref))
	aliases, ok := audioLangAliases[pref]
	if !ok {
		aliases = []string{pref}
	}
	for _, a := range aliases {
		if tag == a {
			return true
		}
	}
	return false
}

// preferAudioLang returns srcs reordered so sources tagged with the preferred
// language come first, keeping the provider's own order within each group.
// Nothing is dropped: a preferred source that fails to decrypt still falls
// through to the others.
func preferAudioLang(srcs []tbcplVidzeeSource, pref string) []tbcplVidzeeSource {
	if pref == "" || len(srcs) < 2 {
		return srcs
	}
	out := make([]tbcplVidzeeSource, len(srcs))
	copy(out, srcs)
	sort.SliceStable(out, func(i, j int) bool {
		return matchesAudioLang(out[i].Lang, pref) && !matchesAudioLang(out[j].Lang, pref)
	})
	return out
}
