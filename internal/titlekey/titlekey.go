// Package titlekey reduces titles to comparison keys shared by every package that matches titles.
package titlekey

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Normalize reduces a title to a comparison key, so that two catalogs
// punctuating the same work differently still agree. It case-folds, strips
// accents (NFD then drop the combining marks, so "Pokémon" keys as
// "pokemon"), spells "&" as "and", drops apostrophes outright so "Marvel's"
// keys as "marvels", and collapses every other non-alphanumeric run —
// hyphens, colons, dots, brackets — to a single space.
//
// Dropping rather than spacing apostrophes is the one asymmetry: catalogs
// write "Marvels", never "Marvel s". Dots become spaces because
// "S.H.I.E.L.D." is written "S H I E L D" as often as it is elided.
//
// A key can legitimately be empty (a title of pure punctuation); callers must
// treat that as "no title" rather than as something to compare.
func Normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
			// A combining mark NFD split off an accented letter.
		case r == '\'' || r == '’':
			// Elided, not spaced.
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if pendingSpace && b.Len() > 0 {
				b.WriteByte(' ')
			}
			pendingSpace = false
			b.WriteRune(r)
		case r == '&':
			// Always a word of its own, so "Rick&Morty" keys like "Rick & Morty".
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString("and")
			pendingSpace = true
		default:
			pendingSpace = true
		}
	}
	return b.String()
}
