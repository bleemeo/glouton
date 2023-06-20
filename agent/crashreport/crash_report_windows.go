//go:build windows

package crashreport

import "golang.org/x/sys/windows"

func redirectOSSpecificStderrToFile(stderrFileFd uintptr) error {
	return windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(stderrFileFd))
}
