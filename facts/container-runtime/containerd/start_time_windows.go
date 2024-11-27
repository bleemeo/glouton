//go:build windows

package containerd

import "time"

func getStartTime(_ int) (time.Time, error) {
	return time.Time{}, nil
}
