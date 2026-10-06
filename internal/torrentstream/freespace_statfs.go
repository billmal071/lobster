//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package torrentstream

import "golang.org/x/sys/unix"

// availableBytes reports how many bytes an unprivileged process may still write
// to the filesystem holding path.
//
// Bavail, not Bfree: Bfree includes the blocks a filesystem reserves for root —
// 5% by default on ext4 — which this process cannot use. Counting them would
// report room on a volume that is already full as far as the user is concerned,
// which is the one direction this check must not be wrong in.
//
// The build tag is an explicit GOOS list rather than the repo's usual
// !windows/windows pair because unix.Statfs does not exist everywhere that is
// not Windows: solaris, aix and z/OS expose statvfs instead. Those fall through
// to freespace_unsupported.go, which reports that it cannot tell.
func availableBytes(path string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	// Bsize is int64 on Linux and uint32 on Darwin, and Bavail is unsigned on
	// both, so both operands are widened explicitly.
	return int64(st.Bavail) * int64(st.Bsize), nil
}
