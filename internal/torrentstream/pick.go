package torrentstream

import (
	"fmt"
	"path"
	"strings"
)

// fileInfo is the subset of a torrent file this package needs to choose one.
type fileInfo struct {
	path   string
	length int64
}

// videoExt are the containers a player can open.
var videoExt = map[string]bool{
	".mp4": true, ".mkv": true, ".avi": true, ".mov": true,
	".m4v": true, ".webm": true, ".mpg": true, ".mpeg": true, ".wmv": true,
}

// pickVideo returns the index of the file to play: the largest video, ignoring
// sample clips unless one is all the torrent contains. Releases routinely ship
// subtitles, an .nfo and a sample alongside the feature, and serving the first
// file — or the largest without checking the extension — plays the wrong thing.
func pickVideo(files []fileInfo) (int, error) {
	best, bestSample := -1, -1
	for i, f := range files {
		if !videoExt[strings.ToLower(path.Ext(f.path))] {
			continue
		}
		if isSample(f.path) {
			if bestSample < 0 || f.length > files[bestSample].length {
				bestSample = i
			}
			continue
		}
		if best < 0 || f.length > files[best].length {
			best = i
		}
	}
	if best >= 0 {
		return best, nil
	}
	if bestSample >= 0 {
		return bestSample, nil
	}
	return 0, fmt.Errorf("torrent contains no video file")
}

// isSample matches the conventional sample clip, on the path segment rather than
// the whole path so a directory named e.g. "Sample Cinema" does not hide a film.
func isSample(p string) bool {
	base := strings.ToLower(path.Base(p))
	return strings.HasPrefix(base, "sample") || strings.Contains(base, "-sample")
}
