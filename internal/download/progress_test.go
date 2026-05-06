package download

import (
	"bytes"
	"strings"
	"testing"
)

func TestFFmpegProgressUpdateFromLine(t *testing.T) {
	p := &ffmpegProgress{}

	updated, done := p.updateFromLine("out_time_ms=12000000")
	if !updated || done {
		t.Fatalf("expected out_time update without completion")
	}
	if p.outTimeMS != 12000000 {
		t.Fatalf("unexpected outTimeMS: %d", p.outTimeMS)
	}

	updated, done = p.updateFromLine("total_size=1048576")
	if !updated || done {
		t.Fatalf("expected size update without completion")
	}
	if p.totalSize != 1048576 {
		t.Fatalf("unexpected totalSize: %d", p.totalSize)
	}

	updated, done = p.updateFromLine("speed=1.5x")
	if !updated || done {
		t.Fatalf("expected speed update without completion")
	}
	if p.speed != "1.5x" {
		t.Fatalf("unexpected speed: %s", p.speed)
	}

	updated, done = p.updateFromLine("progress=end")
	if updated || !done {
		t.Fatalf("expected completion marker")
	}
}

func TestRenderLineIncludesFriendlyParts(t *testing.T) {
	p := &ffmpegProgress{
		outTimeMS: 65_000_000,
		totalSize: 5 * 1024 * 1024,
		speed:     "2.0x",
	}
	got := p.renderLine()
	wantParts := []string{"Downloading", "time 00:01:05", "size 5.0 MiB", "speed 2.0x"}
	for _, part := range wantParts {
		if !strings.Contains(got, part) {
			t.Fatalf("render output %q missing %q", got, part)
		}
	}
}

func TestTailLimitedBufferKeepsOnlyNewestBytes(t *testing.T) {
	var buf bytes.Buffer
	w := &tailLimitedBuffer{
		buf:   &buf,
		limit: 10,
	}
	_, _ = w.Write([]byte("abcdef"))
	_, _ = w.Write([]byte("ghijklmnop"))
	if got, want := buf.String(), "ghijklmnop"; got != want {
		t.Fatalf("tail buffer = %q, want %q", got, want)
	}
}
