package player

import (
	"lobster/internal/media"
)

// mpvHeaderArgs builds the mpv args for whatever of Referer and User-Agent a
// source requires.
//
// These two args are enough on their own, and they reach more than the first
// request. Measured against a header-logging localhost server with mpv 0.37.0:
// with only --referrer and --user-agent, both headers arrived on the HLS
// playlist and on all four of its segment requests, and on a DASH manifest, its
// init segment and all four media segments. So the extra
// --demuxer-lavf-o=headers= value this used to add carried nothing new.
//
// It was also fatal. --demuxer-lavf-o is a key-value *list* option: mpv splits
// the value on commas and demands a "key=value" in every piece. A real browser
// User-Agent contains ", like Gecko", so mpv died with
//
//	Expected '=' and a value.
//	Error parsing option demuxer-lavf-o (option parameter could not be parsed)
//
// and exit status 1 before opening the URL at all — the logging server recorded
// zero requests. That broke every AnimeOnsen playback, AnimeOnsen being the only
// provider that sets Stream.UserAgent, and any live-TV channel whose
// #EXTVLCOPT:http-user-agent is a browser UA.
//
// The comma is the entire cause, and it is not specific to DASH: the same flag
// with a comma-free value parses fine, trailing CRLF and all, and the failure
// happens in option parsing before mpv has chosen a demuxer.
//
// The Referer goes through --referrer rather than --http-header-fields for the
// same reason. --http-header-fields is a plain string list, also split on
// commas, and it fails silently instead of loudly: measured,
// "--http-header-fields=Referer: https://host/a,b/" delivered "Referer:
// https://host/a" and dropped the rest. A live-TV Referer comes from
// #EXTVLCOPT:http-referrer in a playlist the user supplied
// (internal/provider/m3u.go), so lobster does not control its contents.
// --referrer is a plain string option and delivered that same value verbatim.
//
// To re-measure any of this, run mpv by hand against a logging server, or
// against the live AnimeOnsen CDN, which 403s without an exact Referer:
//
//	mpv --no-config --vo=null --ao=null --frames=3 \
//	  --referrer=https://www.animeonsen.xyz/ \
//	  --user-agent='Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36' \
//	  'https://cdn.animeonsen.xyz/video/mp4-dash/cvYyOlmbfFWvJYWG/1/manifest.mpd'
func mpvHeaderArgs(s *media.Stream) []string {
	var args []string
	if s.Referer != "" {
		args = append(args, "--referrer="+s.Referer)
	}
	if s.UserAgent != "" {
		args = append(args, "--user-agent="+s.UserAgent)
	}
	return args
}

// vlcHeaderArgs builds VLC args for Referer/User-Agent.
func vlcHeaderArgs(s *media.Stream) []string {
	var args []string
	if s.Referer != "" {
		args = append(args, "--http-referrer", s.Referer)
	}
	if s.UserAgent != "" {
		args = append(args, "--http-user-agent", s.UserAgent)
	}
	return args
}

// genericHeaderArgs builds the header args for iina and celluloid, which take
// mpv's command line. --referrer and --user-agent are both plain mpv core
// options, so there is nothing to vary here and nothing to keep in sync by hand.
func genericHeaderArgs(s *media.Stream) []string { return mpvHeaderArgs(s) }
