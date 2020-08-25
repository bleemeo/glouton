// Copyright 2015-2019 Bleemeo
//
// bleemeo.com an infrastructure monitoring solution in the Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package facts

import (
	"context"
	"fmt"
	"glouton/logger"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

//nolint:gochecknoglobals
var (
	modNt = windows.NewLazySystemDLL("ntdll.dll")

	procNtQueryInformationProcess = modNt.NewProc("NtQueryInformationProcess")
	procNtQuerySystemInformation  = modNt.NewProc("NtQuerySystemInformation")
)

const (
	SystemProcessInformation      = 5
	ProcessCommandLineInformation = 60

	StatusInfoLengthMismatch = 0xC0000004
	StatusBufferTooSmall     = 0xC0000023
	StatusBufferOverflow     = 0x80000005
)

//nolint:maligned
type SystemProcessInformationStruct struct {
	NextEntryOffset              uint32
	NumberOfThreads              uint32
	WorkingSetPrivateSize        int64
	HardFaultCount               uint32
	NumberOfThreadsHighWatermark uint32
	CycleTime                    uint64
	CreateTime                   int64
	UserTime                     int64
	KernelTime                   int64
	ImageName                    UnicodeString
	BasePriority                 int32
	UniqueProcessID              uintptr
	InheritedFromUniqueProcessID uintptr
	HandleCount                  uint32
	SessionID                    uint32
	PageDirectoryBase            uintptr
	PeakVirtualSize              uintptr
	VirtualSize                  uintptr
	PageFaultCount               uint32
	PeakWorkingSetSize           uintptr
	WorkingSetSize               uintptr
	QuotaPeakPagedPoolUsage      uintptr
	QuotaPagedPoolUsage          uintptr
	QuotaPeakNonPagedPoolUsage   uintptr
	QuotaNonPagedPoolUsage       uintptr
	PagefileUsage                uintptr
	PeakPagefileUsage            uintptr
	PrivatePageCount             uintptr
	ReadOperationCount           int64
	WriteOperationCount          int64
	OtherOperationCount          int64
	ReadTransferCount            int64
	WriteTransferCount           int64
	OtherTransferCount           int64
}

// windows uses the amount of 100ns increments since Jan 1, 1601 instead of unix time.
// windowsTimeToTime converts a windows timestamp to a proper time object.
func windowsTimeToTime(t int64) time.Time {
	// set the starting point to the unix epoch.
	t -= 116444736000000000
	return time.Unix(t/10000000, (t%10000000)*100)
}

func (z psutilLister) Processes(ctx context.Context, maxAge time.Duration) (processes []Process, err error) {
	// In order to retrieve process information on windows, given the fact that LocalService has limited privileges,
	// we prefer to iterate over processes via the NtQuerySystemInformation syscall
	buf, err := getInformation(procNtQuerySystemInformation, SystemProcessInformation)
	if err != nil {
		logger.V(1).Printf("facts/process: NtQuerySystemInformation failed: %v", err)
		return nil, nil
	}

	// length/0x100 is the maximum theoretical number of processes that could be contained in a buffer of size 'bufLen'
	// on AMD64 (it is probably less in practice, due to thread information being mixed in the result). This should
	// reduce reallocations.
	processes = make([]Process, 0, len(buf)/0x100)

	for {
		process := (*SystemProcessInformationStruct)(unsafe.Pointer(&buf[0]))

		if p, ok := parseProcessData(process); ok {
			processes = append(processes, p)
		}

		offset := process.NextEntryOffset
		if offset == 0 {
			return processes, nil
		}

		buf = buf[offset:]
	}
}

func getInformation(syscall *windows.LazyProc, kind int) ([]byte, error) {
	var bufLen uint32

	ret, _, err := syscall.Call(
		uintptr(kind),
		uintptr(0),
		uintptr(0),
		uintptr(unsafe.Pointer(&bufLen)),
	)

	if bufLen == 0 {
		return nil, fmt.Errorf("getSystemInformation: empty buffer requested")
	}

	if ret >= 0x80000000 && ret != StatusInfoLengthMismatch && ret != StatusBufferTooSmall && ret != StatusBufferOverflow {
		logger.V(1).Println("a", err)
		return nil, err
	}

	buf := make([]byte, bufLen)
	r, _, err := syscall.Call(
		uintptr(kind),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(bufLen),
		uintptr(unsafe.Pointer(&bufLen)),
	)
	// the return value isn't a success type or an informational type (according to https://docs.microsoft.com/en-us/windows-hardware/drivers/kernel/using-ntstatus-values)
	if r >= 0x80000000 {
		logger.V(1).Println("b", r, err)
		return nil, err
	}

	return buf, nil
}

// convertUTF16ToString comes from gopsutil: https://github.com/shirou/gopsutil/blob/7e94bb8bcde053b6d6c98bda5145e9742c913c39/process/process_windows.go#L998-L1009
// Note that I have switched the left shift out of the uint16() cast, as it probably doesn't work otherwise.
func convertUTF16ToString(src []byte) string {
	srcLen := len(src) / 2

	codePoints := make([]uint16, srcLen)

	srcIdx := 0

	for i := 0; i < srcLen; i++ {
		codePoints[i] = uint16(src[srcIdx]) | uint16(src[srcIdx+1])<<8
		srcIdx += 2
	}

	return syscall.UTF16ToString(codePoints)
}

func retrieveCmdLine(pid uint32) (cmdline string, err error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}

	buf, err := getInformation(procNtQueryInformationProcess, ProcessCommandLineInformation)
	if err != nil {
		return "", fmt.Errorf("cannot retrieve the command line informations for the process %d, system call 'NtQueryInformationProcess' failed: %v", pid, err)
	}

	_ = syscall.CloseHandle(syscall.Handle(h))

	// the offset is probably 8 bytes instead of 16 on i686, to be confirmed
	return convertUTF16ToString(buf[16:]), nil
}

func retrieveUsername(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}

	var token windows.Token

	err = windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token)
	if err != nil {
		return "", fmt.Errorf("cannot retrieve the token for the process %d: %v", pid, err)
	}

	userToken, err := token.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("cannot retrieve the user token for the process %d: %v", pid, err)
	}

	res, _, _, err := userToken.User.Sid.LookupAccount("")

	_ = windows.CloseHandle(windows.Handle(token))
	_ = windows.CloseHandle(h)

	return res, err
}

func parseProcessData(process *SystemProcessInformationStruct) (res Process, ok bool) {
	if process.UniqueProcessID == 0 {
		// PID 0 on Windows is "System Idle Process", and we cannot retrieve information on this special process.
		return res, false
	}

	res = Process{
		PID: int(process.UniqueProcessID),
	}

	res.CreateTime = windowsTimeToTime(process.CreateTime)
	res.CreateTimestamp = res.CreateTime.Unix()

	if user, err := retrieveUsername(uint32(process.UniqueProcessID)); err == nil {
		res.Username = user
	}

	// this is the best approximation of the parent process we can get cheaply
	res.PPID = int(process.InheritedFromUniqueProcessID)

	res.MemoryRSS = uint64(process.WorkingSetSize) / 1024

	// increments of 100ns -> convert to seconds
	res.CPUTime = (float64(process.UserTime) + float64(process.KernelTime)) / 10000000.

	imageName := (*uint16)(process.ImageName.Buffer)
	if imageName != nil {
		res.Executable = windows.UTF16PtrToString(imageName)
	}

	exec := strings.Split(res.Executable, `\`)
	if len(exec) != 0 {
		res.Name = exec[len(exec)-1]
	}

	var err error

	res.CmdLine, err = retrieveCmdLine(uint32(process.UniqueProcessID))
	if err != nil || len(res.CmdLine) == 0 {
		res.CmdLine = res.Name
	}

	// TODO: split properly the arguments
	res.CmdLineList = []string{res.CmdLine}

	// the process status is not simple to derive on windows, and not currently supported by gopsutil
	res.Status = "?"

	res.NumThreads = int(process.NumberOfThreads)

	return res, true
}
