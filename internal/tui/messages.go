package tui

import (
	"lobster/internal/dlmanager"
	"lobster/internal/media"
)

// errMsg indicates an error occurred during an async operation.
type errMsg struct{ err error }

func (e errMsg) Error() string { return e.err.Error() }

// resultsFetchedMsg is sent when a list of search/trending results is fetched.
type resultsFetchedMsg []media.SearchResult

// detailFetchedMsg is sent when detailed metadata for an item is fetched.
type detailFetchedMsg struct {
	id     string
	detail *media.ContentDetail
}

// seasonsFetchedMsg is sent when TV show seasons are fetched.
type seasonsFetchedMsg []media.Season

// episodesFetchedMsg is sent when TV show episodes are fetched.
type episodesFetchedMsg []media.Episode

// serverFlowFinishedMsg indicates the stream resolution process is complete and playback can begin
type serverFlowFinishedMsg struct {
	stream     *media.Stream
	subFile    string
	seasonIdx  int
	episodeIdx int
	// If it fails
	err error
}

// downloadQueuedMsg is sent when a download is added to the queue.
type downloadQueuedMsg struct {
	downloadID int
	title      string
}

// downloadBatchQueuedMsg is sent when multiple downloads are queued.
type downloadBatchQueuedMsg struct {
	count int
	title string
}

// downloadProgressMsg relays progress from the download manager.
type downloadProgressMsg dlmanager.ProgressUpdate

// downloadListUpdatedMsg signals the downloads list should refresh from store.
type downloadListUpdatedMsg struct{}

// liveItemsFetchedMsg carries one level of the Live TV drill-down (categories or
// channels) to the Update loop. level 0 = categories, level 1 = channels.
type liveItemsFetchedMsg struct {
	rows  []liveRow
	level int
	title string
	gen   int // request generation; stale/cross-tab responses are ignored
}

// posterFetchedMsg is sent when a poster image has been prepared.
// For char-art terminals, `poster` holds the rendered cell-art.
// For inline terminals, `inline` is true and `b64`/`imgW`/`imgH` hold the image.
type posterFetchedMsg struct {
	id     string
	poster string
	b64    string
	imgW   int
	imgH   int
	inline bool
}
