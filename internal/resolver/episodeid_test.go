package resolver

import "testing"

// TestNumberedEpisodeID checks the "<id>:<season>:<episode>" form a StreamProvider is asked by number with, across the ID shapes that reach it.
func TestNumberedEpisodeID(t *testing.T) {
	for _, c := range []struct {
		id              string
		season, episode int
		want            string
	}{
		{"tv/1396", 2, 5, "1396:2:5"},
		{"movie/603", 1, 1, "603:1:1"},
		{"1396", 2, 5, "1396:2:5"},
		{"nyaa/Show?s=2", 2, 3, "nyaa/Show?s=2:2:3"},
		{"eztv/0903747", 2, 10, "eztv/0903747:2:10"},
		{"1337x/The+Expanse", 2, 5, "1337x/The+Expanse:2:5"},
		{"tv/", 1, 1, "tv/:1:1"},
		{"", 1, 1, ":1:1"},
		{"tv/1396", 0, 0, "1396:0:0"},
		{"tv/1396", -1, -2, "1396:-1:-2"},
	} {
		if got := NumberedEpisodeID(c.id, c.season, c.episode); got != c.want {
			t.Errorf("NumberedEpisodeID(%q, %d, %d) = %q, want %q", c.id, c.season, c.episode, got, c.want)
		}
	}
}
