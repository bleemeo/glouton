//go:build darwin || freebsd || netbsd

package containerd

import (
	"fmt"
	"syscall"
	"time"
)

func getStartTime(pid int) (time.Time, error) {
	var stat syscall.Stat_t

	err := syscall.Stat(fmt.Sprintf("/proc/%d", pid), &stat)
	if err != nil {
		return time.Time{}, err
	}

	// Some 32-bit platforms have these fields as an int32, so we need to enforce it as 64.
	return time.Unix(int64(stat.Ctimespec.Sec), int64(stat.Ctimespec.Nsec)), nil //nolint:unconvert
}
