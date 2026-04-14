package tui

import (
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
