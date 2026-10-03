package tui

import (
	"testing"

	"lobster/internal/config"
)

// The TUI's own call site carries the adult-anime setting.
//
// There are two call sites for a setter-based flag — this one and
// cmd.newProvider — and a forgotten call in either is a documented setting
// that silently does nothing in half the program. The CLI half is pinned by
// cmd.TestNewProviderCarriesTheAdultAnimeSettingIntoAllAnime.
func TestNewTUIAllAnimeCarriesTheAdultAnimeSetting(t *testing.T) {
	for _, want := range []bool{false, true} {
		c := config.Default()
		c.AllowAdultAnime = want
		if got := newTUIAllAnime(c).AllowAdult(); got != want {
			t.Fatalf("allow_adult_anime = %v, but the TUI's AllAnime has AllowAdult() = %v", want, got)
		}
	}
}
