// Copyright 2015-2023 Bleemeo
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

//go:build windows

package facts

import (
	"context"
	"errors"
	"fmt"
	"glouton/logger"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	errCannotRetrieveInfo      = errors.New("cannot retrieve the command line information for the process")
	errCannotRetrieveToken     = errors.New("cannot retrieve the token for the process")
	errCannotRetrieveUserToken = errors.New("cannot retrieve the user token for the process")
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

	initialBufferSize = 0x4000
)

// windows uses the amount of 100ns increments since Jan 1, 1601 instead of unix time.
// windowsTimeToTime converts a windows timestamp to a proper time object.
func windowsTimeToTime(t int64) time.Time {
	// set the starting point to the unix epoch.
	t -= 116444736000000000

	return time.Unix(t/10000000, (t%10000000)*100)
}

func (z PsutilLister) Processes(context.Context, time.Duration) ([]Process, error) {
	// In order to retrieve process information on windows, given the fact that LocalService has limited privileges,
	// we prefer to iterate over processes via the NtQuerySystemInformation syscall
	var (
		bufLen uint32
		ret    uintptr
		err    error
	)

	bufLen = initialBufferSize
	buf := make([]byte, bufLen)

	for {
		ret, _, err := procNtQuerySystemInformation.Call(
			uintptr(SystemProcessInformation),
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(bufLen),
			uintptr(unsafe.Pointer(&bufLen)),
		)

		if ret >= 0x80000000 && ret != StatusInfoLengthMismatch && ret != StatusBufferTooSmall {
			logger.V(1).Printf("facts/process: NtQuerySystemInformation failed (error code %d): %v", ret, err)

			return nil, nil
		}

		if bufLen == 0 {
			logger.V(1).Printf("facts/process: NtQuerySystemInformation failed: empty buffer requested")

			return nil, nil
		}

		if ret == StatusInfoLengthMismatch || ret == StatusBufferTooSmall {
			buf = make([]byte, bufLen)
		} else {
			break
		}
	}

	if ret >= 0x80000000 {
		logger.V(1).Printf("facts/process: NtQuerySystemInformation failed (error code %d): %v", ret, err)

		return nil, nil
	}

	// We use the maximum theoretical number of processes that could be contained in a buffer of size 'bufLen'
	// to reduce reallocations.
	processes := make([]Process, 0, uintptr(len(buf))/unsafe.Sizeof(SystemProcessInformationStruct{}))

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

func retrieveCmdLine(pid uint32) (cmdline string, err error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}

	var bufLen uint32

	ret, _, err := procNtQueryInformationProcess.Call(
		uintptr(h),
		uintptr(ProcessCommandLineInformation),
		uintptr(0),
		uintptr(0),
		uintptr(unsafe.Pointer(&bufLen)),
	)

	if ret >= 0x80000000 && ret != StatusInfoLengthMismatch && ret != StatusBufferTooSmall && ret != StatusBufferOverflow {
		return "", fmt.Errorf("%w %d, system call 'NtQueryInformationProcess' failed: %v", errCannotRetrieveInfo, pid, err)
	}

	if bufLen == 0 {
		// This errors represents a windows specific error realated to the NtQueryInformationProcess
		return "", errors.New("NtQueryInformationProcess: empty buffer requested") //nolint:goerr113
	}

	buf := make([]byte, bufLen)
	ret, _, err = procNtQueryInformationProcess.Call(
		uintptr(h),
		uintptr(ProcessCommandLineInformation),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(bufLen),
		uintptr(unsafe.Pointer(&bufLen)),
	)
	// the return value isn't a success type or an informational type (according to https://docs.microsoft.com/en-us/windows-hardware/drivers/kernel/using-ntstatus-values)
	if ret >= 0x80000000 {
		return "", fmt.Errorf("%w %d, system call 'NtQueryInformationProcess' failed: %v", errCannotRetrieveInfo, pid, err)
	}

	_ = syscall.CloseHandle(syscall.Handle(h))

	str := *(*UnicodeString)(unsafe.Pointer(&buf[0]))

	return windows.UTF16PtrToString((*uint16)(str.Buffer)), nil
}

func retrieveUsername(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}

	var token windows.Token

	err = windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token)
	if err != nil {
		return "", fmt.Errorf("%w %d: %v", errCannotRetrieveToken, pid, err)
	}

	userToken, err := token.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("%w %d: %v", errCannotRetrieveUserToken, pid, err)
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

	// Split the input arguments.
	// We do so argument by argument, cutting on `"`, `'` and ` ` boundaries
	ptr, err := windows.UTF16PtrFromString(res.CmdLine)
	if err == nil {
		var argc int32

		argv, err := windows.CommandLineToArgv(ptr, &argc)
		if err == nil {
			var i int32

			for i = range argc {
				res.CmdLineList = append(res.CmdLineList, windows.UTF16PtrToString((*uint16)(unsafe.Pointer(&argv[i][0]))))
			}
		} else {
			res.CmdLineList = []string{res.CmdLine}
		}
	} else {
		res.CmdLineList = []string{res.CmdLine}
	}

	// the process status is not simple to derive on windows, and not currently supported by gopsutil
	res.Status = "?"

	res.NumThreads = int(process.NumberOfThreads)

	return res, true
}
