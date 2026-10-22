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

// TestSubDLPackOfUnnamedFilesIsRefused covers the other way a pack can state
// no episodes: files numbered in a shape nothing recognises (01.srt, 02.srt).
// The unnamed fallback kept whichever came last, so asking for episode 3 was
// answered with some other episode's lines under a plausible-looking name.
// Several candidates and no named match is a guess, not an answer.
func TestSubDLPackOfUnnamedFilesIsRefused(t *testing.T) {
	url := zipOf(t, "01.srt", "02.srt", "03.srt")
	_, body, err := extract(t, url, 1, 3)
	if err == nil {
		t.Fatalf("DownloadAndExtract returned %q for S01E03 from a pack of three unnamed entries; want an error -- picking one of them is a guess", body)
	}
}

// TestSubDLSeasonPackExtractsADoubleEpisodeFile guards the other half of the
// marker scan: a file covering two episodes states both, and it is the right
// file for either of them.
func TestSubDLSeasonPackExtractsADoubleEpisodeFile(t *testing.T) {
	url := zipOf(t,
		"Breaking.Bad.S01E01.S01E02.srt",
		"Breaking.Bad.S01E03.srt",
	)
	_, body, err := extract(t, url, 1, 2)
	if err != nil {
		t.Fatalf("DownloadAndExtract: %v", err)
	}
	if !strings.Contains(body, "S01E01.S01E02") {
		t.Errorf("extracted %q for S01E02; want the S01E01.S01E02 entry -- a file stating two episodes is right for both", body)
	}
}

// TestSubDLSingleReleaseZipInTwoFormatsStillExtracts guards the refusal above
// from overreaching: one release shipped as .srt and .ass is a single
// candidate in two formats, which is what preferSRT is for, not two rival
// episodes.
func TestSubDLSingleReleaseZipInTwoFormatsStillExtracts(t *testing.T) {
	url := zipOf(t, "breaking.bad.pilot.web-dl.ass", "breaking.bad.pilot.web-dl.srt")
	path, body, err := extract(t, url, 1, 1)
	if err != nil {
		t.Fatalf("DownloadAndExtract: %v", err)
	}
	if !strings.HasSuffix(path, ".srt") || !strings.Contains(body, "pilot") {
		t.Errorf("extracted %q (%q); want the .srt of the zip's only release", path, body)
	}
}

// TestSubDLFilmZipWithSeveralFilesStillExtracts guards it from the other
// direction: with no episode asked for there is no episode to name, so every
// entry is unnamed and refusing would break films outright.
func TestSubDLFilmZipWithSeveralFilesStillExtracts(t *testing.T) {
	url := zipOf(t, "dune.part.two.forced.ass", "dune.part.two.full.srt")
	_, body, err := extract(t, url, 0, 0)
	if err != nil {
		t.Fatalf("DownloadAndExtract: %v", err)
	}
	if !strings.Contains(body, "dune") {
		t.Errorf("extracted %q; want one of the film zip's entries", body)
	}
}

// TestSubDLZipWithMacOSResourceForkStillExtracts covers an archive zipped on
// macOS: alongside the real subtitle sits __MACOSX/._<name>.srt, an
// AppleDouble resource fork carrying the same name and extension. It passed
// the extension filter and named no episode, so it counted as a second
// unnamed release and the refusal rejected an archive holding exactly one
// usable subtitle.
func TestSubDLZipWithMacOSResourceForkStillExtracts(t *testing.T) {
	url := zipOf(t,
		"__MACOSX/._breaking.bad.pilot.web-dl.srt",
		"breaking.bad.pilot.web-dl.srt",
	)
	path, body, err := extract(t, url, 1, 1)
	if err != nil {
		t.Fatalf("DownloadAndExtract: %v -- the zip holds one subtitle plus macOS packaging debris", err)
	}
	if strings.Contains(path, "._") || !strings.Contains(body, "body of breaking.bad.pilot.web-dl.srt") {
		t.Errorf("extracted %q (%q); want the real subtitle, not the __MACOSX resource fork", path, body)
	}
}

// TestSubDLDoubleEpisodeDirectoryDoesNotRenameItsFiles is the directory-marker
// case: a pack whose folder states both episodes it covers gave every file
// inside it both markers, so asking for S01E02 could be answered with the
// S01E01 file. A file's own name is the authority on which episode it holds.
func TestSubDLDoubleEpisodeDirectoryDoesNotRenameItsFiles(t *testing.T) {
	url := zipOf(t,
		"Show.S01E01.S01E02/S01E01.srt",
		"Show.S01E01.S01E02/S01E02.srt",
	)
	_, body, err := extract(t, url, 1, 2)
	if err != nil {
		t.Fatalf("DownloadAndExtract: %v", err)
	}
	if !strings.HasSuffix(body, "/S01E02.srt") {
		t.Errorf("extracted %q for S01E02; want the S01E02.srt entry -- the directory naming both episodes must not make the S01E01 file look like episode 2", body)
	}
}

// TestSubDLSingleReleaseInANamedDirectoryStillExtracts guards the fallback the
// test above narrows: when a file's own name states no episode, the directory
// around it is all there is to go on, and a single release unpacked into a
// release-named folder is the common shape of that.
func TestSubDLSingleReleaseInANamedDirectoryStillExtracts(t *testing.T) {
	url := zipOf(t, "Breaking.Bad.S01E03.1080p.BluRay/subtitles.srt")
	_, body, err := extract(t, url, 1, 3)
	if err != nil {
		t.Fatalf("DownloadAndExtract: %v -- the directory names the episode and the file names none", err)
	}
	if !strings.HasSuffix(body, "/subtitles.srt") {
		t.Errorf("extracted %q; want the release directory's only subtitle", body)
	}
}

// TestSubDLKeepsAFullSeasonPackThatNamesAnEpisode guards the pack path against
// SubDL's own episode field. A pack covers the whole season, so whatever its
// episode field happens to say is not a statement about what the archive
// holds -- and filtering on it would drop the pack for every episode but one.
// Precautionary: every full_season entry seen live on 2026-10-22 (Breaking Bad
// S1, Game of Thrones S1) carried episode: null, so this shape did not occur
// in practice; the filter would simply be wrong if it ever did.
func TestSubDLKeepsAFullSeasonPackThatNamesAnEpisode(t *testing.T) {
	stub := &subdlStub{
		results: []subdlResult{{SDId: 1234, Type: "tv", Name: "Breaking Bad", Year: 2008}},
		subtitles: []subdlSubtitle{{
			ReleaseName: "Breaking.Bad.S01.COMPLETE.1080p.BluRay",
			Language:    "English",
			URL:         "/subtitle/breaking-bad-s01.zip",
			Season:      1,
			Episode:     1,
			FullSeason:  true,
		}},
	}
	stub.serve(t)

	subs, err := NewSubDL("stub-key").Search("Breaking Bad", "en", 1, 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("Search returned %d subtitles for S01E05 when SubDL offered a full_season pack tagged episode=1; want 1 -- a pack holds every episode, so its own episode field must not filter it", len(subs))
	}
}
