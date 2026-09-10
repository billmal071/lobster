package cmd

import (
	"testing"

	"lobster/internal/history"
	"lobster/internal/media"
	"lobster/internal/player"
)

// A watch's history identity is the *work*, not whichever provider happened to
// answer for it. The episode-list recovery breaks that if it rebinds the ID it
// hands the session, because that one field is asked two different questions:
// playlist.Session uses it as the provider-call key (internal/playlist/session.go)
// while cmd/session.go uses it as the history key (checkpoint, --continue
// resume and saveHistory). Rebinding it filed the same show, season and episode
// under two IDs depending on whether the primary's GetEpisodes happened to work
// that run, so one transient 5xx made --continue restart from zero.
//
// This is a property, not a shape: drive the same request twice — once with a
// primary that lists the season itself, once with a primary that cannot, so the
// chain's list (and the chain's own ID) is what the session is built on — and
// history must hold ONE row for it either way.
func TestHistoryIdentityIsStableWhicheverProviderListedTheEpisodes(t *testing.T) {
	hostileEnv(t)
	pl := &countingPlayer{stubPlayerImpl: stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}}}
	playStreamHarness(t, pl)
	// Nothing here may prompt: both runs name their episode.
	recordSelections(t, 0)

	sel := media.SearchResult{ID: "tv/1403", Title: "Some Show", Year: "2013", Type: media.TV}
	url := stubStreamServer(t)

	// Run 1 — the primary lists season 1 itself, so the session is built on
	// the primary's own list and ID.
	healthy := &stubProvider{
		seasons:          []media.Season{{ID: "s1", Number: 1}},
		episodesBySeason: map[string][]media.Episode{"s1": twentyTwoEpisodes()},
	}
	withFallbackChain(t, newListingChainProvider(url))
	if err := resolveAndPlay(healthy, sel, 1, 22); err != nil {
		t.Fatalf("resolveAndPlay with a healthy primary = %v", err)
	}

	// Run 2 — same show, same season, same episode, but this run the primary's
	// GetEpisodes fails and the chain (ID "tv/fallback-1") supplies the list.
	broken := &stubProvider{
		seasons:     []media.Season{{ID: "s1", Number: 1}},
		episodesErr: errProviderCannotList,
	}
	fb := newListingChainProvider(url)
	withFallbackChain(t, fb)
	if err := resolveAndPlay(broken, sel, 1, 22); err != nil {
		t.Fatalf("resolveAndPlay with a primary that cannot list = %v", err)
	}

	// The other half of the split, and the reason it cannot just be "never
	// rebind anything": the provider that supplied the list has to be called
	// with its own ID, or the recovery resolves nothing.
	if got := fb.mediaAsked(); got != "tv/fallback-1" {
		t.Fatalf("chain provider was asked about %q, want its own ID %q — the provider-call key must follow the provider", got, "tv/fallback-1")
	}

	entries, err := history.Load()
	if err != nil {
		t.Fatalf("history.Load: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("history holds %d rows for one show/season/episode watched twice: %+v — the history identity must not follow the provider that answered", len(entries), entries)
	}
	if entries[0].ID != sel.ID {
		t.Fatalf("history row ID = %q, want the work's own ID %q", entries[0].ID, sel.ID)
	}
	if entries[0].Season != 1 || entries[0].Episode != 22 {
		t.Fatalf("history row = S%dE%d, want S1E22", entries[0].Season, entries[0].Episode)
	}
}
