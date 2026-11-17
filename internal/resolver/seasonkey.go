package resolver

import (
	"net/url"
	"regexp"
	"strconv"

	"lobster/internal/media"
)

var (
	trailingOrdinalSeason = regexp.MustCompile(`^(.+) (\d{1,2})(?:st|nd|rd|th) season$`)
	trailingSeasonWord    = regexp.MustCompile(`^(.+) season (one|two|three|four|five|six|seven|eight|nine|ten)$`)
	trailingRoman         = regexp.MustCompile(`^(.+) (ii|iii|iv|v|vi|vii|viii|ix|x)$`)
)

var romanSeasons = map[string]int{"ii": 2, "iii": 3, "iv": 4, "v": 5, "vi": 6, "vii": 7, "viii": 8, "ix": 9, "x": 10}

// titleKey is normalize with a trailing season designator spelled one way, so
// "Show 2nd Season", "Show Season 2", "Show Season Two" and "Show II" agree.
// Part and cour designators are left alone: they are not seasons.
func titleKey(s string) string { return canonSeason(normalize(s)) }

func canonSeason(key string) string {
	if m := trailingOrdinalSeason.FindStringSubmatch(key); m != nil {
		return m[1] + " season " + m[2]
	}
	if m := trailingSeasonWord.FindStringSubmatch(key); m != nil {
		return m[1] + " season " + strconv.Itoa(seasonWords[m[2]])
	}
	if m := trailingRoman.FindStringSubmatch(key); m != nil {
		return m[1] + " season " + strconv.Itoa(romanSeasons[m[2]])
	}
	return key
}

// isNyaaSequelOf reports whether c is the Nyaa series req names by its plain
// title and season: Nyaa lists a sequel as "<Show> Season N" under an ID that
// carries the show's own title and s=N.
func isNyaaSequelOf(c media.SearchResult, req Request) bool {
	id := nyaaSeasonIDRe.FindStringSubmatch(c.ID)
	if id == nil || req.Season < 2 {
		return false
	}
	base, err := url.QueryUnescape(id[1])
	if err != nil {
		return false
	}
	n, _ := strconv.Atoi(id[2])
	return n == req.Season && normalize(base) == normalize(req.Title)
}
