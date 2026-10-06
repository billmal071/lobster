//go:build !windows && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package torrentstream

import "errors"

// availableBytes cannot answer on a platform with neither statfs nor
// GetDiskFreeSpaceEx wired up here. Returning an error rather than a made-up
// number is what keeps the caller silent: spaceAdvice is never consulted when
// the free figure is unknown, so an unsupported platform loses the warning
// instead of gaining a wrong one.
func availableBytes(string) (int64, error) {
	return 0, errors.New("free space is not reported on this platform")
}
