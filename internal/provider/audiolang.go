package provider

import (
	"sort"

	"lobster/internal/lang"
)

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
		return lang.Matches(out[i].Lang, pref) && !lang.Matches(out[j].Lang, pref)
	})
	return out
}
