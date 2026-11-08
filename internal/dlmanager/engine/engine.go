// Package engine defines the download engine interface and implementations.
package engine

import (
	"context"
	"net/http"
)

// ProgressFunc is called periodically with download progress.
type ProgressFunc func(doneBytes int64, totalBytes int64)

// Headers are the request headers a stream's CDN requires; zero values mean
// the engine's own defaults.
type Headers struct {
	Referer   string
	UserAgent string
}

// Engine downloads a stream URL to a local file with resume support.
type Engine interface {
	// Download fetches the stream to outputPath.
	// Cancel via ctx to pause or abort. progressFn may be nil.
	Download(ctx context.Context, streamURL, outputPath string, hdr Headers, progressFn ProgressFunc) error

	// Resume continues a previously interrupted download.
	Resume(ctx context.Context, streamURL, outputPath string, hdr Headers, progressFn ProgressFunc) error

	// Type returns the engine type identifier ("http" or "hls").
	Type() string
}

const defaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0"

func (h Headers) apply(req *http.Request) {
	ua := h.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	req.Header.Set("User-Agent", ua)
	if h.Referer != "" {
		req.Header.Set("Referer", h.Referer)
	}
}
