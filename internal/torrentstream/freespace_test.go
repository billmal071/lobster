package torrentstream

import (
	"errors"
	"strings"
	"testing"
)

const gib = int64(1) << 30

// stubDiskFree replaces the free-space syscall for the duration of a test, so a
// full volume can be simulated without filling one, and records the path it was
// asked about.
func stubDiskFree(t *testing.T, avail int64, err error) *string {
	t.Helper()
	var asked string
	prev := diskFree
	diskFree = func(p string) (int64, error) {
		asked = p
		return avail, err
	}
	t.Cleanup(func() { diskFree = prev })
	return &asked
}

// seasonPack is the shape that makes a total-size check wrong: one wanted
// episode alongside extras nobody will fetch. Serve sets every file but the
// chosen one to PiecePriorityNone, so only the chosen one's length is ever
// written to disk.
func seasonPack() []fileInfo {
	return []fileInfo{
		{path: "Pack/Sample/sample.mkv", length: 100 << 20},
		{path: "Pack/S01E01.mkv", length: 2 * gib},
		{path: "Pack/S01E02.mkv", length: 30 * gib},
		{path: "Pack/S01E03.mkv", length: 30 * gib},
	}
}

// The length that matters is the selected file's. A check against the torrent's
// total would refuse a 2 GiB episode out of a 62 GiB pack on a volume with 4 GiB
// free — a torrent that plays through to the end.
func TestPlanServeMeasuresTheChosenFileNotTheWholeTorrent(t *testing.T) {
	// pickVideo takes the largest non-sample video, so make that the small one.
	infos := []fileInfo{
		{path: "Pack/Sample/sample.mkv", length: 100 << 20},
		{path: "Pack/S01E01.mkv", length: 2 * gib},
		{path: "Pack/extras.nfo", length: 30 * gib},
		{path: "Pack/extras.srt", length: 30 * gib},
	}
	s := &Server{data: runDir{path: "/run/dir"}}
	stubDiskFree(t, 4*gib, nil)

	idx, v, err := s.planServe(infos)
	if err != nil {
		t.Fatalf("planServe error = %v", err)
	}
	if idx != 1 {
		t.Fatalf("planServe idx = %d, want 1 (the episode)", idx)
	}
	if v.msg != "" || v.refuse {
		t.Errorf("planServe complained about a 2.0 GiB file with 4.0 GiB free: refuse=%v msg=%q",
			v.refuse, v.msg)
	}
}

// Streaming writes only what is watched, so a film that cannot fit in full still
// plays — for over an hour in this case. Refusing it would break a torrent that
// works; saying nothing would let the volume fill mid-playback.
func TestPlanServeWarnsButStillStreamsAFilmThatDoesNotFit(t *testing.T) {
	infos := []fileInfo{{path: "Film/film.mkv", length: 40 * gib}}
	s := &Server{data: runDir{path: "/run/dir"}}
	stubDiskFree(t, 20*gib, nil)

	_, v, err := s.planServe(infos)
	if err != nil {
		t.Fatalf("planServe error = %v", err)
	}
	if v.refuse {
		t.Fatalf("planServe refused a stream that would have played: %q", v.msg)
	}
	if v.msg == "" {
		t.Fatal("planServe said nothing about a 40 GiB film on a volume with 20 GiB free")
	}
	for _, want := range []string{"40.0 GiB", "20.0 GiB", "torrent_dir"} {
		if !strings.Contains(v.msg, want) {
			t.Errorf("warning %q does not mention %q", v.msg, want)
		}
	}
}

// A download has no useful partial outcome: internal/download runs ffmpeg to
// produce one output file, so a transfer that provably cannot complete is an
// hour of bandwidth spent on nothing.
func TestPlanServeRefusesADownloadThatCannotFit(t *testing.T) {
	infos := []fileInfo{{path: "Film/film.mkv", length: 40 * gib}}
	s := &Server{data: runDir{path: "/run/dir"}, wholeFile: true}
	stubDiskFree(t, 20*gib, nil)

	_, v, err := s.planServe(infos)
	if err != nil {
		t.Fatalf("planServe error = %v", err)
	}
	if !v.refuse {
		t.Fatalf("planServe allowed a download that cannot fit: refuse=false msg=%q", v.msg)
	}
	if !strings.Contains(v.msg, "whole file") {
		t.Errorf("refusal %q does not say why a download is different", v.msg)
	}
}

// The directory statted has to be the one the pieces land in. $HOME is not it:
// a configured torrent_dir may be any volume, and the MkdirTemp fallback is
// wherever os.TempDir() points.
func TestPlanServeStatsTheRunDirectory(t *testing.T) {
	infos := []fileInfo{{path: "Film/film.mkv", length: 1 << 20}}
	s := &Server{data: runDir{path: "/mnt/other-volume/torrent-abc"}}
	asked := stubDiskFree(t, 100*gib, nil)

	if _, _, err := s.planServe(infos); err != nil {
		t.Fatalf("planServe error = %v", err)
	}
	if *asked != "/mnt/other-volume/torrent-abc" {
		t.Errorf("free space was read for %q, want the run data directory %q",
			*asked, "/mnt/other-volume/torrent-abc")
	}
}

// When the free figure is unknown the check must say nothing. A guess either way
// is worse: a wrong refusal blocks a working torrent, and a wrong reassurance is
// the surprise this check exists to replace.
func TestPlanServeSaysNothingWhenFreeSpaceIsUnknown(t *testing.T) {
	infos := []fileInfo{{path: "Film/film.mkv", length: 40 * gib}}
	s := &Server{data: runDir{path: "/run/dir"}, wholeFile: true}
	stubDiskFree(t, 0, errors.New("statfs: operation not supported"))

	_, v, err := s.planServe(infos)
	if err != nil {
		t.Fatalf("planServe error = %v; a failed stat must not fail the stream", err)
	}
	if v.refuse || v.msg != "" {
		t.Errorf("planServe spoke without knowing the free figure: refuse=%v msg=%q", v.refuse, v.msg)
	}
}

// The margin is part of the guarantee, not decoration: exactly the file's length
// is not enough room, because the volume has to stay writable for everything
// else on it while a multi-gigabyte transfer runs.
func TestSpaceAdviceKeepsHeadroomAboveTheFileLength(t *testing.T) {
	const length = 10 * gib
	cases := []struct {
		name      string
		avail     int64
		wantQuiet bool
	}{
		{"exactly the file length is not enough", length, false},
		{"one byte short of the headroom", length + spaceHeadroom - 1, false},
		{"the headroom exactly", length + spaceHeadroom, true},
		{"plenty", length + 4*spaceHeadroom, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := spaceAdvice("film.mkv", length, c.avail, false)
			if quiet := v.msg == ""; quiet != c.wantQuiet {
				t.Errorf("spaceAdvice(len=%d, avail=%d) quiet = %v, want %v (msg %q)",
					length, c.avail, quiet, c.wantQuiet, v.msg)
			}
		})
	}
}

// An unknown length is not a zero-sized file. Torrent metadata is the only
// source for it, and a check that treated "do not know" as "fits" or "does not
// fit" would be inventing one.
func TestSpaceAdviceSaysNothingAboutAnUnknownLength(t *testing.T) {
	for _, length := range []int64{0, -1} {
		if v := spaceAdvice("film.mkv", length, 0, true); v.msg != "" || v.refuse {
			t.Errorf("spaceAdvice(length=%d) = %+v, want silence", length, v)
		}
	}
}

// The stub above proves the decision; only the real syscall proves the number.
// This is the one test that would catch a build-tagged file that compiles and
// returns nonsense.
func TestAvailableBytesReadsTheRealFilesystem(t *testing.T) {
	dir := t.TempDir()
	got, err := availableBytes(dir)
	if err != nil {
		t.Fatalf("availableBytes(%q) error = %v", dir, err)
	}
	if got <= 0 {
		t.Errorf("availableBytes(%q) = %d, want a positive byte count", dir, got)
	}
}

// A path that does not exist must be an error, not a zero that reads as "full".
func TestAvailableBytesFailsOnAMissingPath(t *testing.T) {
	if _, err := availableBytes(t.TempDir() + "/nope/nothing-here"); err == nil {
		t.Error("availableBytes on a missing path returned nil error")
	}
}
