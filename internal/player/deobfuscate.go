package player

import (
	"lobster/internal/hlsproxy"
	"lobster/internal/media"
)

// wrapDeobfuscated routes a fake-PNG-obfuscated HLS stream through a local
// proxy so the player receives clean MPEG-TS. For any other stream it is a
// no-op. The returned cleanup func shuts the proxy down and must be deferred
// by the caller; it is always safe to call.
func wrapDeobfuscated(stream *media.Stream) (*media.Stream, func(), error) {
	if stream == nil || !stream.Deobfuscate {
		return stream, func() {}, nil
	}
	p, err := hlsproxy.New(stream.Referer, stream.UserAgent)
	if err != nil {
		return stream, func() {}, err
	}
	clone := *stream
	clone.URL = p.PlaylistURL(stream.URL)
	return &clone, func() { p.Close() }, nil
}
