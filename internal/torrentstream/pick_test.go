package torrentstream

import "testing"

// YTS torrents ship the film alongside subtitles, a sample and a README. Serving
// the first file, or merely the largest without checking the extension, is how a
// player ends up opening a .srt or a 30-second sample.
func TestPickVideoIgnoresNonVideoAndSamples(t *testing.T) {
	files := []fileInfo{
		{path: "The.Movie.2012/README.txt", length: 900},
		{path: "The.Movie.2012/Subs/eng.srt", length: 90_000},
		{path: "The.Movie.2012/sample.mp4", length: 30_000_000},
		{path: "The.Movie.2012/The.Movie.2012.1080p.mp4", length: 2_000_000_000},
	}
	got, err := pickVideo(files)
	if err != nil {
		t.Fatal(err)
	}
	if got != 3 {
		t.Errorf("picked %d (%s), want the feature", got, files[got].path)
	}
}

// A sample can be the only video present; better to play it than to refuse.
func TestPickVideoFallsBackToSampleWhenItIsAllThereIs(t *testing.T) {
	files := []fileInfo{
		{path: "x/readme.nfo", length: 100},
		{path: "x/sample.mkv", length: 20_000_000},
	}
	got, err := pickVideo(files)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Errorf("picked %d, want the sample", got)
	}
}

func TestPickVideoErrorsWhenNoVideoPresent(t *testing.T) {
	if _, err := pickVideo([]fileInfo{{path: "a/readme.txt", length: 10}}); err == nil {
		t.Error("want an error when the torrent holds no video file")
	}
}

func TestPickVideoRecognisesCommonContainers(t *testing.T) {
	for _, ext := range []string{".mp4", ".mkv", ".avi", ".mov", ".m4v", ".webm"} {
		files := []fileInfo{{path: "a/readme.txt", length: 10}, {path: "a/film" + ext, length: 1000}}
		if got, err := pickVideo(files); err != nil || got != 1 {
			t.Errorf("%s not recognised as video (got %d, err %v)", ext, got, err)
		}
	}
}
