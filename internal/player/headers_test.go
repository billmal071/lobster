package player

import (
	"fmt"
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

func TestMPVHeaderArgsCarryRefererAndUA(t *testing.T) {
	args := mpvHeaderArgs(&media.Stream{Referer: "https://ref/", UserAgent: "UA/1.0"})
	if !has(args, "--referrer=https://ref/") {
		t.Fatalf("missing referer: %v", args)
	}
	if !has(args, "--user-agent=UA/1.0") {
		t.Fatalf("missing user-agent: %v", args)
	}
	// A Referer must NOT silently disable TLS verification.
	if has(args, "--tls-verify=no") {
		t.Fatalf("Referer should not disable TLS verification: %v", args)
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

// --- a model of mpv's own option parsing ------------------------------------
//
// Asserting that an arg appears in the list is not enough: the bug this guards
// against was an arg that was emitted exactly as intended and that mpv itself
// then refused. mpv splits a *list* option's value on commas. For a key-value
// list such as --demuxer-lavf-o it demands "key=value" in every piece and
// aborts the whole run when one lacks it; for a plain string list such as
// --http-header-fields it keeps the first piece and silently discards the rest.
// Neither is visible in the arg list — only in what mpv does with it.
//
// mpvOptionModel below reproduces those rules. It is a model, so its fidelity
// was checked against the real binary (mpv 0.37.0) rather than assumed. Five
// predictions, five matches:
//
//	--referrer=https://host/a,b/                        -> Referer: https://host/a,b/
//	--http-header-fields=Referer: https://host/a,b/     -> Referer: https://host/a
//	--user-agent=<browser UA containing ", like Gecko"> -> delivered verbatim
//	--demuxer-lavf-o=headers=<value containing a comma> -> exit 1, nothing opened
//	--demuxer-lavf-o=headers=<comma-free value + CRLF>  -> accepted
//
// The first three were read off a header-logging localhost server on the HLS
// playlist, on every HLS segment, on a DASH manifest, and on its init and media
// segments. See the live command in internal/player/headers.go's comment.

// mpvOptionModel returns the HTTP request headers mpv would end up sending for
// args, or an error if mpv would reject one of them and never open the stream.
// It fails on an option it does not model rather than ignoring it, so a new
// header arg cannot slip past this test by being unrecognised.
func mpvOptionModel(args []string) (map[string]string, error) {
	hdrs := map[string]string{}
	for _, a := range args {
		name, value, ok := strings.Cut(a, "=")
		if !ok {
			return nil, fmt.Errorf("mpv would reject %q: not an --option=value", a)
		}
		switch name {
		case "--referrer": // plain string: arrives verbatim
			hdrs["Referer"] = value
		case "--user-agent": // plain string: arrives verbatim
			hdrs["User-Agent"] = value
		case "--http-header-fields": // string *list*: split on commas
			for _, field := range strings.Split(value, ",") {
				k, v, ok := strings.Cut(field, ":")
				if !ok {
					continue // mpv drops a piece that is not a header line
				}
				hdrs[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		case "--demuxer-lavf-o", "--stream-lavf-o": // key-value *list*
			for _, pair := range strings.Split(value, ",") {
				if !strings.Contains(pair, "=") {
					return nil, fmt.Errorf("mpv would abort: Expected '=' and a value. "+
						"Error parsing option %s (option parameter could not be parsed), on %q",
						strings.TrimPrefix(name, "--"), pair)
				}
			}
			// Modelled only far enough to catch the fatal case; nothing in this
			// package is expected to reach for these at all.
		default:
			return nil, fmt.Errorf("unmodelled option %q: extend mpvOptionModel and "+
				"verify the new rule against the real mpv before trusting this test", name)
		}
	}
	return hdrs, nil
}

// Every value the builders handle comes from outside lobster, so each one must
// reach the server exactly as the source asked for it — and mpv must still
// start. The two shapes below are the real ones: AnimeOnsen's User-Agent
// (internal/provider/animeonsen.go) is a browser UA containing ", like Gecko",
// and a live-TV Referer comes from #EXTVLCOPT:http-referrer in a playlist the
// user supplied (internal/provider/m3u.go), so it may contain anything.
func TestMPVHeaderArgsDeliverTheHeadersVerbatim(t *testing.T) {
	cases := []struct {
		name   string
		stream *media.Stream
	}{
		{"browser user-agent with a comma", &media.Stream{
			Referer:   "https://www.animeonsen.xyz/",
			UserAgent: "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		}},
		{"referer with a comma", &media.Stream{Referer: "https://host/a,b/c"}},
		{"plain values", &media.Stream{Referer: "https://ref/", UserAgent: "UA/1.0"}},
		{"user-agent only", &media.Stream{UserAgent: "UA/1.0"}},
	}
	builders := []struct {
		label string
		build func(*media.Stream) []string
	}{{"mpvHeaderArgs", mpvHeaderArgs}, {"genericHeaderArgs", genericHeaderArgs}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, b := range builders {
				args := b.build(tc.stream)
				hdrs, err := mpvOptionModel(args)
				if err != nil {
					t.Errorf("%s(%v) = %v, which mpv will not accept: %v; "+
						"the player exits before it opens the stream, so playback fails outright",
						b.label, tc.stream, args, err)
					continue
				}
				if got := hdrs["Referer"]; got != tc.stream.Referer {
					t.Errorf("%s: mpv would send Referer %q, source asked for %q (args %v)",
						b.label, got, tc.stream.Referer, args)
				}
				if got := hdrs["User-Agent"]; got != tc.stream.UserAgent {
					t.Errorf("%s: mpv would send User-Agent %q, source asked for %q (args %v)",
						b.label, got, tc.stream.UserAgent, args)
				}
			}
		})
	}
}

// The demuxer/stream lavf options add nothing: measured against a
// header-logging server, --referrer and --user-agent alone put both headers on
// every HLS segment and on every DASH init and media segment. They are also the
// one way to make mpv refuse to start at all. So nothing here may set one, and
// this pins that rather than leaving it to the comment.
func TestMPVHeaderArgsSetNoLavfDemuxerOptions(t *testing.T) {
	s := &media.Stream{Referer: "https://ref/", UserAgent: "UA/1.0"}
	for _, b := range []struct {
		label string
		build func(*media.Stream) []string
	}{{"mpvHeaderArgs", mpvHeaderArgs}, {"genericHeaderArgs", genericHeaderArgs}} {
		for _, a := range b.build(s) {
			if strings.HasPrefix(a, "--demuxer-lavf-o") || strings.HasPrefix(a, "--stream-lavf-o") {
				t.Errorf("%s emitted %q; the network options already carry both headers to "+
					"every segment request, and this one aborts mpv outright on any "+
					"comma-bearing value", b.label, a)
			}
		}
	}
}
