// Package engine defines the download engine interface and implementations.
package engine

import "context"

// ProgressFunc is called periodically with download progress.
type ProgressFunc func(doneBytes int64, totalBytes int64)

// Engine downloads a stream URL to a local file with resume support.
type Engine interface {
	// Download fetches the stream to outputPath.
	// Cancel via ctx to pause or abort. progressFn may be nil.
	Download(ctx context.Context, streamURL, outputPath, referer string, progressFn ProgressFunc) error

	// Resume continues a previously interrupted download.
	Resume(ctx context.Context, streamURL, outputPath, referer string, progressFn ProgressFunc) error

	// Type returns the engine type identifier ("http" or "hls").
	Type() string
}
