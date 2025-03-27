// Copyright 2015-2025 Bleemeo
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

//go:build linux

package process

import (
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/bleemeo/glouton/facts"
	gloutonTypes "github.com/bleemeo/glouton/types"

	"github.com/ncabatoff/process-exporter/proc"
)

func Test_reflection(t *testing.T) {
	ps := &Processes{
		HostRootPath: "/",
	}

	procs, factory, err := ps.Processes(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	testAllProcs(t, factory())
	testProcesses(t, procs)
	testAllProcs(t, factory())

	for _, testName := range []string{"AllProcs", "Processes", "mixed"} {
		for n := range 5 {
			ps := &Processes{
				HostRootPath: "/",
			}

			t.Run(testName, func(t *testing.T) {
				t.Parallel()

				procs, factory, err := ps.Processes(t.Context())
				if err != nil {
					t.Fatal(err)
				}

				switch {
				case testName == "AllProcs" || (testName == "mixed" && n%2 == 0):
					testAllProcs(t, factory())
				default:
					testProcesses(t, procs)
				}
			})
		}
	}
}

func testAllProcs(t *testing.T, procsIntf gloutonTypes.ProcIter) {
	t.Helper()

	procs, ok := procsIntf.(proc.Iter)
	if !ok {
		t.Fatal("wrong interface for procsIntf")
	}

	myPID := os.Getpid()
	foundMyself := false

	for procs.Next() {
		testProc(t, procs)

		if procs.GetPid() == myPID {
			foundMyself = true
		}
	}

	if err := procs.Close(); err != nil {
		t.Errorf("An error occurred while trying to close the process: %v", err)
	}

	if !foundMyself {
		t.Errorf("My PID (=%d) is not in processes list", myPID)
	}
}

func testProcesses(t *testing.T, procs []facts.Process) {
	t.Helper()

	myPID := os.Getpid()
	foundMyself := false

	for _, p := range procs {
		if p.PID == myPID {
			foundMyself = true
		}
	}

	if !foundMyself {
		t.Errorf("My PID (=%d) is not in processes list", myPID)
	}
}

func testProc(t *testing.T, procs proc.Iter) {
	t.Helper()

	internalProc, _ := procs.(*iter)
	current := internalProc.procValue

	if current.procErr != nil {
		if isProcNotExist(current.procErr) {
			return
		}

		// skip overflow errors if we are in 32bits mode (we assume we are on a 64bits system).
		// We do this because enumerating 64bits process when running in 32bits will fail,
		// as the memory space of theses processes will overflow the capacity of uint,
		// and uint is the type the procfs uses to represent the VM size of processes.
		if runtime.GOARCH == "386" && strings.Contains(current.procErr.Error(), "integer overflow") {
			return
		}

		t.Errorf("An error occurred for the current process: %v", current.procErr)
	}

	if current.proc == nil {
		t.Fatalf("current.proc = nil, want non-nil")
	}

	stat, err := getStat(current.proc)
	if err != nil {
		t.Errorf("Error on get stat: %v", err)
	}

	if stat.PID != procs.GetPid() {
		t.Errorf("stat.PID = %d, want %d", stat.PID, os.Getpid())
	}

	if current.procStat.PID != procs.GetPid() {
		t.Errorf("current.procStat.PID = %d, want %d", current.procStat.PID, procs.GetPid())
	}

	static, err := procs.GetStatic()
	if err != nil && isProcNotExist(err) {
		return
	}

	cmdline, err := current.getCmdline()
	if err != nil {
		if isProcNotExist(err) {
			return
		}

		t.Errorf("An error occurred while trying to get te command line from proc cache: %v", err)
	}

	if !reflect.DeepEqual(cmdline, static.Cmdline) {
		t.Errorf("cmdline = %v, want %v", cmdline, static.Cmdline)
	}

	if current.procStat.Comm != static.Name {
		t.Errorf("current.procStat.Comm = %v, want %v", current.procStat.Comm, static.Name)
	}

	_, _, err = procs.GetMetrics()
	if err != nil && !isProcNotExist(err) {
		t.Errorf("An error occurred wile trying to get Metrics: %v", err)
	}
}
