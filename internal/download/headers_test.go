package download

import (
	"strings"
	"testing"

	"lobster/internal/media"
)

// Both headers reach ffmpeg, in one -headers value.
//
// The single-value part is the bug this guards. ffmpeg treats -headers as one
// option, so emitting it twice drops the first — and the header it would drop
// is whichever came first, silently, with the download failing on an access
// error that names neither.
func TestFFmpegHeaderArgsCarryBothHeadersInASingleOption(t *testing.T) {
	args := ffmpegHeaderArgs(&media.Stream{
		Referer:   "https://www.animeonsen.xyz/",
		UserAgent: "Mozilla/5.0 (X11; Linux x86_64)",
	})

	if n := countArg(args, "-headers"); n != 1 {
		t.Fatalf("ffmpegHeaderArgs produced %d -headers options (%q); a second one replaces the first and drops a header", n, args)
	}
	if len(args) != 2 {
		t.Fatalf("ffmpegHeaderArgs = %q, want exactly -headers and its value", args)
	}
	val := args[1]
	if !strings.Contains(val, "Referer: https://www.animeonsen.xyz/\r\n") {
		t.Errorf("-headers value %q is missing the Referer", val)
	}
	// The one that was absent before, and the one that matters most: without
	// it ffmpeg sends "Lavf/<version>", which cdn.animeonsen.xyz answers 403
	// to while serving every other value.
	if !strings.Contains(val, "User-Agent: Mozilla/5.0 (X11; Linux x86_64)\r\n") {
		t.Errorf("-headers value %q is missing the User-Agent; ffmpeg would send its Lavf default and be refused", val)
	}
}

// A stream needing only one of the two gets only that one, and a stream
// needing neither gets no -headers at all rather than an empty value.
func TestFFmpegHeaderArgsOmitsWhatTheStreamDoesNotAskFor(t *testing.T) {
	if got := ffmpegHeaderArgs(&media.Stream{}); got != nil {
		t.Errorf("ffmpegHeaderArgs(no headers) = %q, want nil: an empty -headers value is still an option ffmpeg parses", got)
	}
	got := ffmpegHeaderArgs(&media.Stream{Referer: "https://example.test/"})
	if len(got) != 2 || strings.Contains(got[1], "User-Agent") {
		t.Errorf("ffmpegHeaderArgs(referer only) = %q, want just the Referer", got)
	}
	got = ffmpegHeaderArgs(&media.Stream{UserAgent: "UA/1"})
	if len(got) != 2 || strings.Contains(got[1], "Referer") {
		t.Errorf("ffmpegHeaderArgs(ua only) = %q, want just the User-Agent", got)
	}
}

func countArg(args []string, want string) int {
	n := 0
	for _, a := range args {
		if a == want {
			n++
		}
	}
	return n
}

// The headers are actually passed to ffmpeg, not merely built correctly.
//
// ffmpegHeaderArgs being right says nothing about the command line if the call
// site drops it, and CI has no ffmpeg to run the real thing against — so the
// arg list is the thing asserted. The input is a stream that needs both
// headers and a resume offset, because the resume branch is what inserts
// options between the headers and the input and is where a reordering would
// show up.
func TestFFmpegDownloadArgsPassTheHeadersBeforeTheInput(t *testing.T) {
	stream := &media.Stream{
		URL:       "https://cdn.animeonsen.xyz/video/mp4-dash/abc/1/manifest.mpd",
		Referer:   "https://www.animeonsen.xyz/",
		UserAgent: "Mozilla/5.0 (X11; Linux x86_64)",
	}
	args := ffmpegDownloadArgs(stream, "Some Show - 01", "", "/tmp/out.mkv", 42)

	hdr := indexOfArg(args, "-headers")
	in := indexOfArg(args, "-i")
	if hdr < 0 {
		t.Fatalf("args %q carry no -headers; the stream's Referer and User-Agent never reach ffmpeg", args)
	}
	if countArg(args, "-headers") != 1 {
		t.Fatalf("args %q carry -headers more than once; the later one replaces the earlier and drops a header", args)
	}
	if in < 0 || hdr > in {
		t.Fatalf("args %q put -headers after -i; ffmpeg applies input options only before the input they belong to", args)
	}
	if !strings.Contains(args[hdr+1], "User-Agent: Mozilla/5.0 (X11; Linux x86_64)") {
		t.Fatalf("-headers value %q omits the User-Agent; ffmpeg would send Lavf/<version>, which cdn.animeonsen.xyz answers 403 to", args[hdr+1])
	}
	if !strings.Contains(args[hdr+1], "Referer: https://www.animeonsen.xyz/") {
		t.Fatalf("-headers value %q omits the Referer", args[hdr+1])
	}
	if args[len(args)-1] != "/tmp/out.mkv" {
		t.Errorf("args end with %q, want the output path", args[len(args)-1])
	}
}

func indexOfArg(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}
