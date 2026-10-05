package torrentstream

import "fmt"

// spaceHeadroom is how much of the volume the free-space check insists on
// leaving unused, over and above the file being fetched.
//
// It is flat rather than a fraction of the file because of what it protects: not
// the download, but everything else writing to the same volume while a
// multi-gigabyte transfer runs for an hour — the desktop session, a browser
// cache, a package manager, lobster's own subtitle staging. What those need does
// not grow with the size of the film, so scaling the margin with it would be the
// wrong shape.
//
// It is explicitly not slack for the filesystem's own reserve. availableBytes
// already reports only what an unprivileged process may use — statfs Bavail
// excludes ext4's root reservation and GetDiskFreeSpaceEx honours a per-user
// quota — so adding a percentage for that would count it twice.
const spaceHeadroom = 1 << 30

// diskFree reports the bytes available on the filesystem holding a path. A
// package var so a test can simulate a full volume instead of filling one.
var diskFree = availableBytes

// spaceVerdict is what the free-space check concluded. The zero value means
// there is nothing to say — either the file fits or the check could not tell.
type spaceVerdict struct {
	// msg is the message for the user, empty when there is none.
	msg string
	// refuse says the stream must not start at all.
	refuse bool
}

// spaceAdvice decides what to say about a file of length bytes landing in a
// volume with avail bytes free.
//
// wholeFile distinguishes the two callers, and they want opposite answers.
// Streaming writes only the part that is actually watched, so a film that cannot
// fit in full still plays — for over an hour, in the 40 GB-film-20 GB-free case —
// and refusing it would break something that works. A download has no such
// partial outcome: internal/download runs ffmpeg to produce one output file, so a
// transfer that provably cannot complete is an hour of bandwidth spent on
// nothing, and is refused up front.
//
// length <= 0 means the length is not known, which is silence rather than a
// guess.
func spaceAdvice(name string, length, avail int64, wholeFile bool) spaceVerdict {
	if length <= 0 {
		return spaceVerdict{}
	}
	if avail >= length+spaceHeadroom {
		return spaceVerdict{}
	}
	if wholeFile {
		return spaceVerdict{
			refuse: true,
			msg: fmt.Sprintf(
				"%s is %s and the torrent directory's volume has %s free, which is not enough to hold it "+
					"(%s on top of the file is kept free for everything else on the volume).\n"+
					"A download needs the whole file, so this would fail part-way through. "+
					"Free some space, or point torrent_dir at a volume with more room — or play it instead of downloading it, "+
					"which only writes the part you watch.",
				name, formatSize(length), formatSize(avail), formatSize(spaceHeadroom)),
		}
	}
	return spaceVerdict{
		msg: fmt.Sprintf(
			"%s is %s and the torrent directory's volume has %s free, so it will not fit in full. "+
				"Streaming only writes what you actually watch, so playback will start — but it will stall "+
				"if you get far enough in to fill the volume. Free some space, or point torrent_dir at a volume with more room.",
			name, formatSize(length), formatSize(avail)),
	}
}

// formatSize renders a byte count the way a user reads a film's size.
func formatSize(b int64) string {
	const (
		kib = 1024
		mib = kib * 1024
		gib = mib * 1024
	)
	switch {
	case b >= gib:
		return fmt.Sprintf("%.1f GiB", float64(b)/float64(gib))
	case b >= mib:
		return fmt.Sprintf("%.0f MiB", float64(b)/float64(mib))
	case b >= kib:
		return fmt.Sprintf("%.0f KiB", float64(b)/float64(kib))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
