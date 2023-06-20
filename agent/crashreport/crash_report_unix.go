//go:build !windows

package crashreport

import "golang.org/x/sys/unix"

func redirectOSSpecificStderrToFile(stderrFileFd uintptr) error {
	return unix.Dup2(int(stderrFileFd), unix.Stderr)
}
