//go:build windows

package torrentstream

import (
	"math"

	"golang.org/x/sys/windows"
)

// availableBytes reports how many bytes this process may still write to the
// volume holding path.
//
// freeBytesAvailableToCaller is the first out-parameter rather than
// totalNumberOfFreeBytes deliberately: it accounts for a per-user disk quota, so
// on a quota'd volume it is the number that decides whether the download fits.
func availableBytes(path string) (int64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var freeToCaller, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeToCaller, &total, &totalFree); err != nil {
		return 0, err
	}
	if freeToCaller > math.MaxInt64 {
		return math.MaxInt64, nil
	}
	return int64(freeToCaller), nil
}
