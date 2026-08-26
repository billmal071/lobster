package extract

import "strings"

// IsBestQuality reports whether a quality preference means "the highest the
// source offers" rather than a specific height. The numeric preferences cap at
// their value, so without this there is no way to ask for 1440p or 2160p.
func IsBestQuality(q string) bool {
	return strings.ToLower(strings.TrimSpace(q)) == "best"
}

// pickSourceByQualityString returns the first source whose URL contains the
// quality string, or "" when none does. A non-numeric preference such as
// "best" is never matched as literal text — "best-of-times.mp4" is a title,
// not a resolution.
func pickSourceByQualityString(urls []string, preferredQuality string) string {
	if IsBestQuality(preferredQuality) {
		return ""
	}
	for _, u := range urls {
		if strings.Contains(u, preferredQuality) {
			return u
		}
	}
	return ""
}
