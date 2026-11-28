package tui

import (
	"path/filepath"
	"testing"

	"lobster/internal/config"
	"lobster/internal/dlmanager"
	"lobster/internal/dlmanager/store"
	"lobster/internal/media"
)

func testManager(t *testing.T) *dlmanager.Manager {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "dl.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return dlmanager.New(s, nil, nil, 1, 0)
}

func queuedRows(t *testing.T, mgr *dlmanager.Manager) []store.Download {
	t.Helper()
	list, err := mgr.Store().ListDownloads()
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	return list
}

func yearlessCatalogueRow(mt media.MediaType) media.SearchResult {
	return media.SearchResult{ID: "tv/37854", Title: "Some Show", Type: mt, FromCatalogue: true, Unresolved: true}
}

func TestQueuedCatalogueMovieKeepsItsCatalogueFlag(t *testing.T) {
	mgr := testManager(t)
	cfg := config.Default()
	cfg.DownloadDir = t.TempDir()
	item := yearlessCatalogueRow(media.Movie)
	m := AppModel{currentItem: &item, dlManager: mgr, config: cfg}

	m.queueCurrentDownload()()

	if rows := queuedRows(t, mgr); len(rows) != 1 || !rows[0].FromCatalogue {
		t.Fatalf("queued %+v; want one row marked FromCatalogue", rows)
	}
}

func TestQueuedCatalogueEpisodesKeepTheirCatalogueFlag(t *testing.T) {
	mgr := testManager(t)
	d := newDownloadDialog()
	d.item = yearlessCatalogueRow(media.TV)
	d.manager = mgr
	d.outputDir = t.TempDir()
	d.seasons = []media.Season{{ID: "s1", Number: 1}}

	d.queueEpisodes([]media.Episode{{ID: "e1", Number: 1}, {ID: "e2", Number: 2}})()

	rows := queuedRows(t, mgr)
	if len(rows) != 2 {
		t.Fatalf("queued %d rows, want 2", len(rows))
	}
	for _, r := range rows {
		if !r.FromCatalogue {
			t.Fatalf("queued %+v; want FromCatalogue", r)
		}
	}
}
