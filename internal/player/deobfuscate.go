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
	return hlsproxy.Wrap(stream)
}
