package player

import (
	"strings"
	"testing"

	"lobster/internal/media"
)

func has(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestMPVHeaderArgsCombinesRefererAndUA(t *testing.T) {
	args := mpvHeaderArgs(&media.Stream{Referer: "https://ref/", UserAgent: "UA/1.0"})
	if !has(args, "--http-header-fields=Referer: https://ref/") {
		t.Fatalf("missing referer field: %v", args)
	}
	if !has(args, "--user-agent=UA/1.0") {
		t.Fatalf("missing user-agent: %v", args)
	}
	// Exactly one demuxer headers arg, carrying BOTH (Referer not clobbered).
	var hdr string
	n := 0
	for _, a := range args {
		if strings.HasPrefix(a, "--demuxer-lavf-o=headers=") {
			hdr = a
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want exactly 1 demuxer headers arg, got %d: %v", n, args)
	}
	if !strings.Contains(hdr, "Referer: https://ref/") || !strings.Contains(hdr, "User-Agent: UA/1.0") {
		t.Fatalf("combined headers missing one value: %q", hdr)
	}
}

func TestMPVHeaderArgsEmpty(t *testing.T) {
	if args := mpvHeaderArgs(&media.Stream{}); len(args) != 0 {
		t.Fatalf("want no args, got %v", args)
	}
}

func TestVLCHeaderArgs(t *testing.T) {
	args := vlcHeaderArgs(&media.Stream{Referer: "https://ref/", UserAgent: "UA/1.0"})
	if !has(args, "--http-referrer") || !has(args, "--http-user-agent") {
		t.Fatalf("vlc header flags missing: %v", args)
	}
}

func TestGenericHeaderArgs(t *testing.T) {
	args := genericHeaderArgs(&media.Stream{UserAgent: "UA/1.0"})
	if !has(args, "--user-agent=UA/1.0") {
		t.Fatalf("generic UA missing: %v", args)
	}
}
