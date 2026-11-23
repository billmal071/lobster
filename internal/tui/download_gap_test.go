package tui

import (
	"fmt"
	"strings"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

type gappedListProvider struct{ provider.Provider }

func (gappedListProvider) GetEpisodes(string, string) ([]media.Episode, error) {
	return []media.Episode{{ID: "2x2", Number: 2}, {ID: "2x3", Number: 3}},
		fmt.Errorf("%w: %w", provider.ErrIncompleteEpisodeList, provider.ErrNotCarried)
}

func TestFetchEpisodesKeepsAListWithGaps(t *testing.T) {
	prev := EpisodeListFallback
	EpisodeListFallback = nil
	t.Cleanup(func() { EpisodeListFallback = prev })

	d := &downloadDialog{
		item:     media.SearchResult{ID: "eztv/1", Type: media.TV},
		provider: gappedListProvider{},
		seasons:  []media.Season{{ID: "2", Number: 2}},
	}
	msg, ok := d.fetchEpisodes(0)().(dlEpisodesMsg)
	if !ok {
		t.Fatalf("fetchEpisodes returned %T, want dlEpisodesMsg", msg)
	}
	if msg.err != nil || len(msg.episodes) != 2 {
		t.Fatalf("err = %v, episodes = %d; want the 2 listed episodes and no error", msg.err, len(msg.episodes))
	}
}

func TestFetchEpisodesFlagsAListWithGapsAsPartial(t *testing.T) {
	prev := EpisodeListFallback
	EpisodeListFallback = nil
	t.Cleanup(func() { EpisodeListFallback = prev })

	d := &downloadDialog{
		item:     media.SearchResult{ID: "eztv/1", Type: media.TV},
		provider: gappedListProvider{},
		seasons:  []media.Season{{ID: "2", Number: 2}},
	}
	msg := d.fetchEpisodes(0)().(dlEpisodesMsg)
	if !msg.partial {
		t.Fatal("a list kept despite gaps was passed on as if it were complete")
	}
}

func TestRangeViewDisclosesAPartialList(t *testing.T) {
	d := newDownloadDialog()
	d.active = true
	d.Update(dlEpisodesMsg{episodes: []media.Episode{{ID: "2x2", Number: 2}, {ID: "2x5", Number: 5}}, partial: true})
	d.step = dlStepRangeInput

	view := d.View(120, 40)
	if strings.Contains(view, "Available: 1-5") {
		t.Errorf("view presents a contiguous 1-5 range for a list with gaps:\n%s", view)
	}
	if !strings.Contains(view, "Partial list") || !strings.Contains(view, "2, 5") {
		t.Errorf("view does not say the list is partial and which episodes have standalone releases:\n%s", view)
	}
}

func TestModePickerDisclosesAPartialList(t *testing.T) {
	d := newDownloadDialog()
	d.active = true
	d.seasons = []media.Season{{ID: "2", Number: 2}}
	d.Update(dlEpisodesMsg{episodes: []media.Episode{{ID: "2x2", Number: 2}, {ID: "2x5", Number: 5}}, partial: true})
	if d.step != dlStepPickMode {
		t.Fatalf("step = %v, want the mode picker", d.step)
	}
	view := d.View(120, 40)
	if !strings.Contains(view, "Partial list") || !strings.Contains(view, "2, 5") {
		t.Errorf("mode picker does not say the list is partial before \"Download all episodes\":\n%s", view)
	}
}

func TestModePickerOfACompleteListSaysNothingAboutGaps(t *testing.T) {
	d := newDownloadDialog()
	d.active = true
	d.seasons = []media.Season{{ID: "1", Number: 1}}
	d.Update(dlEpisodesMsg{episodes: []media.Episode{{ID: "1x1", Number: 1}, {ID: "1x2", Number: 2}}})
	if view := d.View(120, 40); strings.Contains(view, "Partial list") {
		t.Errorf("mode picker flags a complete list as partial:\n%s", view)
	}
}

func TestRangeViewOfACompleteListKeepsTheContiguousRange(t *testing.T) {
	d := newDownloadDialog()
	d.active = true
	d.Update(dlEpisodesMsg{episodes: []media.Episode{{ID: "1x1", Number: 1}, {ID: "1x2", Number: 2}}})
	d.step = dlStepRangeInput
	if view := d.View(120, 40); !strings.Contains(view, "Available: 1-2") {
		t.Errorf("view = %s, want the contiguous range for a complete list", view)
	}
}

func TestFetchEpisodesCarriesAFallbackListsGapStatus(t *testing.T) {
	cases := []struct {
		name    string
		partial bool
	}{
		{"a fallback list with gaps stays partial", true},
		{"a complete fallback list is not partial", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prev := EpisodeListFallback
			EpisodeListFallback = func(provider.Provider, media.SearchResult, int) ([]media.Episode, bool, error) {
				return []media.Episode{{ID: "2x2", Number: 2}, {ID: "2x5", Number: 5}}, c.partial, nil
			}
			t.Cleanup(func() { EpisodeListFallback = prev })

			d := &downloadDialog{
				item:     media.SearchResult{ID: "tv/1", Type: media.TV},
				provider: listlessProvider{},
				seasons:  []media.Season{{ID: "2", Number: 2}},
			}
			msg := d.fetchEpisodes(0)().(dlEpisodesMsg)
			if msg.partial != c.partial {
				t.Fatalf("partial = %v, want %v", msg.partial, c.partial)
			}
		})
	}
}

func TestParseEpisodeRangeSelectsListedEpisodesByNumber(t *testing.T) {
	listed := []media.Episode{{ID: "2x2", Number: 2}, {ID: "2x5", Number: 5}}
	got, err := parseEpisodeRange("2-5", listed)
	if err != nil || len(got) != 2 || got[0].ID != "2x2" || got[1].ID != "2x5" {
		t.Fatalf("2-5 over episodes 2 and 5 = %v, %v; want exactly those two", got, err)
	}
	got, err = parseEpisodeRange("5", listed)
	if err != nil || len(got) != 1 || got[0].ID != "2x5" {
		t.Fatalf("5 = %v, %v; want episode 5 and not the first listed", got, err)
	}
}

type gappedSeasonsProvider struct{ provider.Provider }

func (gappedSeasonsProvider) GetSeasons(string) ([]media.Season, error) {
	return []media.Season{{ID: "2", Number: 2}, {ID: "4", Number: 4}},
		fmt.Errorf("%w: %w", provider.ErrIncompleteEpisodeList, provider.ErrNotCarried)
}

func TestStartKeepsSeasonsListedBesideACatalogueGap(t *testing.T) {
	d := newDownloadDialog()
	cmd := d.start(media.SearchResult{ID: "eztv/1", Type: media.TV}, gappedSeasonsProvider{}, nil, "")
	d.Update(cmd())
	if d.err != nil || len(d.seasons) != 2 {
		t.Fatalf("err = %v, seasons = %d; want both listed seasons offered and no error", d.err, len(d.seasons))
	}
}
