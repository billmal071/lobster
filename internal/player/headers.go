package player

import (
	"strings"

	"lobster/internal/media"
)

// mpvHeaderArgs builds mpv args for Referer/User-Agent. The HLS demuxer headers
// MUST be combined into a single --demuxer-lavf-o=headers= value: mpv treats
// "headers" as one key, so a second --demuxer-lavf-o=headers= would overwrite
// the first and drop a header on segment requests.
func mpvHeaderArgs(s *media.Stream) []string {
	if s.Referer == "" && s.UserAgent == "" {
		return nil
	}
	var args []string
	var hdr strings.Builder
	if s.Referer != "" {
		args = append(args, "--http-header-fields=Referer: "+s.Referer)
		hdr.WriteString("Referer: " + s.Referer + "\r\n")
		args = append(args, "--tls-verify=no")
	}
	if s.UserAgent != "" {
		args = append(args, "--user-agent="+s.UserAgent)
		hdr.WriteString("User-Agent: " + s.UserAgent + "\r\n")
	}
	args = append(args, "--demuxer-lavf-o=headers="+hdr.String())
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

// genericHeaderArgs builds best-effort mpv-style header args for iina/celluloid.
func genericHeaderArgs(s *media.Stream) []string {
	var args []string
	if s.Referer != "" {
		args = append(args, "--http-header-fields=Referer: "+s.Referer)
	}
	if s.UserAgent != "" {
		args = append(args, "--user-agent="+s.UserAgent)
	}
	return args
}
