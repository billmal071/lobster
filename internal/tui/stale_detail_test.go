package tui

import (
	"testing"

	"lobster/internal/media"
)

type detailStub struct {
	stubProvider
	detail *media.ContentDetail
	err    error
}

func (d detailStub) GetDetails(string) (*media.ContentDetail, error) { return d.detail, d.err }

func seriesTabAfterItsListingFailed(t *testing.T) AppModel {
	t.Helper()
	m := browseModel(tabMovies, stubProvider{})
	m.switchTab(tabSeries)
	return apply(t, m, fetchErrMsg{err: assertErr("series boom"), tab: tabSeries, gen: m.tabReqGen})
}

func anotherTitleSelectedWithAnError(t *testing.T) AppModel {
	t.Helper()
	m := browseModel(tabMovies, stubProvider{})
	m.currentItem = &media.SearchResult{ID: "movie/2", Title: "Other"}
	m.err = assertErr("the current selection's error")
	return m
}

func TestDetailFromAnEarlierSelectionKeepsTheCurrentError(t *testing.T) {
	cases := map[string]func(*testing.T) AppModel{
		"no selection after the tab's listing failed": seriesTabAfterItsListingFailed,
		"another title is selected":                   anotherTitleSelectedWithAnError,
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			m := setup(t)
			want := m.err

			m = apply(t, m, detailFetchedMsg{id: "movie/1", detail: &media.ContentDetail{Description: "late"}})

			if m.err != want {
				t.Errorf("err = %v, want %v; a late Movies detail must not clear it", m.err, want)
			}
			if m.currentDetail != nil {
				t.Errorf("detail = %+v, a late Movies detail must not be shown", m.currentDetail)
			}
		})
	}
}

func TestDetailForTheCurrentSelectionIsShownAndClearsItsError(t *testing.T) {
	m := browseModel(tabMovies, stubProvider{})
	m.currentItem = &media.SearchResult{ID: "movie/1", Title: "Current"}
	m.err = assertErr("an earlier detail failure")
	detail := &media.ContentDetail{Description: "fresh"}

	m = apply(t, m, detailFetchedMsg{id: "movie/1", detail: detail})

	if m.currentDetail != detail || m.err != nil {
		t.Fatalf("detail = %+v, err = %v; want the current selection's detail and no error", m.currentDetail, m.err)
	}
}

func TestDetailFailureFromAnEarlierSelectionIsIgnored(t *testing.T) {
	late := fetchDetailCmd(detailStub{err: assertErr("movies detail boom")}, "movie/1")()

	t.Run("no selection on the open tab", func(t *testing.T) {
		m := browseModel(tabMovies, stubProvider{})
		m.switchTab(tabSeries)

		m = apply(t, m, late)

		if m.err != nil {
			t.Fatalf("err = %v, a late Movies detail failure must not show on Series", m.err)
		}
	})
	t.Run("another title is selected", func(t *testing.T) {
		m := anotherTitleSelectedWithAnError(t)
		want := m.err

		m = apply(t, m, late)

		if m.err != want {
			t.Fatalf("err = %v, want %v; a late detail failure must not replace it", m.err, want)
		}
	})
}

func TestDetailFailureForTheCurrentSelectionIsShown(t *testing.T) {
	m := browseModel(tabMovies, stubProvider{})
	m.currentItem = &media.SearchResult{ID: "movie/1", Title: "Current"}

	m = apply(t, m, fetchDetailCmd(detailStub{err: assertErr("detail boom")}, "movie/1")())

	if m.err == nil || m.err.Error() != "detail boom" {
		t.Fatalf("err = %v, want the current selection's detail failure", m.err)
	}
}

func TestFetchedDetailIsStampedWithItsTitle(t *testing.T) {
	detail := &media.ContentDetail{Description: "d"}

	got, ok := fetchDetailCmd(detailStub{detail: detail}, "movie/1")().(detailFetchedMsg)

	if !ok || got.id != "movie/1" || got.detail != detail {
		t.Fatalf("msg = %#v, want the detail stamped movie/1", got)
	}
}
