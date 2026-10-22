package subtitle

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// zipOf serves an in-memory zip of the named entries, each holding its own
// name as its body so a test can tell which one was extracted.
func zipOf(t *testing.T, names ...string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatalf("building zip entry %q: %v", n, err)
		}
		if _, err := fmt.Fprintf(w, "body of %s", n); err != nil {
			t.Fatalf("writing zip entry %q: %v", n, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
	body := buf.Bytes()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/pack.zip"
}

func extract(t *testing.T, zipURL string, season, episode int) (string, string, error) {
	t.Helper()
	tmp, err := NewTempDir()
	if err != nil {
		t.Fatalf("NewTempDir: %v", err)
	}
	t.Cleanup(tmp.Cleanup)

	path, err := NewSubDL("stub-key").DownloadAndExtract(zipURL, tmp, season, episode)
	if err != nil {
		return "", "", err
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("reading extracted subtitle: %v", readErr)
	}
	return path, string(data), nil
}

// TestSubDLKeepsFullSeasonPacksForAnEpisode is the Breaking Bad case: every
// English subtitle SubDL has for the series is a full-season pack, and Search
// discarded all of them the moment an episode was asked for -- while
// DownloadAndExtract has always picked the right episode out of the archive.
// The result was "no subtitles" for a title with ten of them.
func TestSubDLKeepsFullSeasonPacksForAnEpisode(t *testing.T) {
	var packs []subdlSubtitle
	for i := 1; i <= 10; i++ {
		packs = append(packs, subdlSubtitle{
			ReleaseName: fmt.Sprintf("Breaking.Bad.S01.COMPLETE.1080p.BluRay.group%d", i),
			Language:    "English",
			URL:         fmt.Sprintf("/subtitle/breaking-bad-s01-%d.zip", i),
			Season:      1,
			FullSeason:  true,
		})
	}
	stub := &subdlStub{
		results:   []subdlResult{{SDId: 1234, Type: "tv", Name: "Breaking Bad", Year: 2008}},
		subtitles: packs,
	}
	stub.serve(t)

	subs, err := NewSubDL("stub-key").Search("Breaking Bad", "en", 1, 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(subs) != len(packs) {
		t.Fatalf("Search returned %d subtitles for S01E01 when SubDL offered %d full-season packs; want %d -- the download path extracts the episode from the archive, so discarding packs at search time loses every result",
			len(subs), len(packs), len(packs))
	}
}

// TestSubDLSeasonPackExtractsTheRequestedEpisode is the guarantee the search
// change leans on: given a pack, the extraction picks the asked-for episode.
func TestSubDLSeasonPackExtractsTheRequestedEpisode(t *testing.T) {
	url := zipOf(t,
		"Breaking.Bad.S01E01.srt",
		"Breaking.Bad.S01E02.srt",
		"Breaking.Bad.S01E03.srt",
		"Breaking.Bad.S01E04.srt",
	)
	_, body, err := extract(t, url, 1, 3)
	if err != nil {
		t.Fatalf("DownloadAndExtract: %v", err)
	}
	if !strings.Contains(body, "S01E03") {
		t.Errorf("extracted %q from a four-episode pack asked for S01E03; want the S01E03 entry", body)
	}
}

// TestSubDLSeasonPackPrefersTheNamedEpisodeOverAnUnnamedFile covers the half
// the wrong-episode filter cannot: it only rejects a *stated* mismatch, so an
// entry stating no episode at all passes it, and the first such entry won --
// even with the requested episode sitting further down the archive.
func TestSubDLSeasonPackPrefersTheNamedEpisodeOverAnUnnamedFile(t *testing.T) {
	url := zipOf(t,
		"Breaking Bad - read me first.srt",
		"Breaking.Bad.S01E03.srt",
	)
	_, body, err := extract(t, url, 1, 3)
	if err != nil {
		t.Fatalf("DownloadAndExtract: %v", err)
	}
	if !strings.Contains(body, "S01E03") {
		t.Errorf("extracted %q; want the S01E03 entry -- an entry naming the requested episode beats one naming no episode", body)
	}
}

// TestSubDLSeasonPackWithoutTheEpisodeIsRefused keeps the search change from
// trading "no subtitles" for "the wrong episode's subtitles". An archive that
// names other episodes is a pack; if none of them is the requested one, there
// is nothing here to play, and falling back to an unnamed entry is a guess.
func TestSubDLSeasonPackWithoutTheEpisodeIsRefused(t *testing.T) {
	url := zipOf(t,
		"Breaking.Bad.S01E09.srt",
		"Breaking.Bad.S01E10.srt",
		"bonus feature commentary.srt",
	)
	_, body, err := extract(t, url, 1, 3)
	if err == nil {
		t.Fatalf("DownloadAndExtract returned %q for S01E03 from a pack holding only S01E09, S01E10 and an unnamed entry; want an error", body)
	}
}

// TestSubDLSingleReleaseZipStillExtractsItsOnlyFile is the guard against
// overcorrecting the refusal above: a single release's zip names no episode at
// all, and its one file is the right one.
func TestSubDLSingleReleaseZipStillExtractsItsOnlyFile(t *testing.T) {
	url := zipOf(t, "breaking.bad.pilot.web-dl.srt")
	_, body, err := extract(t, url, 1, 1)
	if err != nil {
		t.Fatalf("DownloadAndExtract: %v", err)
	}
	if !strings.Contains(body, "pilot") {
		t.Errorf("extracted %q; want the zip's only subtitle entry", body)
	}
}
