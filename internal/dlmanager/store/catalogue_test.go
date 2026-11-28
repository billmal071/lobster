package store

import "testing"

func TestDownloadFromCatalogueRoundTripsToTheWorker(t *testing.T) {
	s := openTestStore(t)
	d := sampleDownload()
	d.FromCatalogue = true
	id, err := s.InsertDownload(d)
	if err != nil {
		t.Fatalf("InsertDownload: %v", err)
	}

	got, err := s.GetDownload(id)
	if err != nil || !got.FromCatalogue {
		t.Fatalf("GetDownload = %+v, %v; want FromCatalogue", got, err)
	}
	list, err := s.ListDownloads()
	if err != nil || len(list) != 1 || !list[0].FromCatalogue {
		t.Fatalf("ListDownloads = %+v, %v; want FromCatalogue", list, err)
	}
	next, err := s.NextQueued()
	if err != nil || next == nil || !next.FromCatalogue {
		t.Fatalf("NextQueued = %+v, %v; want FromCatalogue", next, err)
	}
	claimed, err := s.ClaimNextQueued()
	if err != nil || claimed == nil || !claimed.FromCatalogue {
		t.Fatalf("ClaimNextQueued = %+v, %v; want FromCatalogue", claimed, err)
	}
}

func TestRowFromThePreviousSchemaIsNotACatalogueRow(t *testing.T) {
	s, err := Open(oldSchemaDB(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	list, err := s.ListDownloads()
	if err != nil || len(list) != 1 || list[0].FromCatalogue {
		t.Fatalf("ListDownloads = %+v, %v; want the old row with FromCatalogue false", list, err)
	}
}
