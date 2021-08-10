// +build linux

package process

import (
	"context"
	"glouton/facts"
	"os"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ncabatoff/process-exporter/proc"
)

func Test_reflection(t *testing.T) {
	source := &Processes{
		HostRootPath:    "/",
		DefaultValidity: 10 * time.Second,
	}

	testAllProcs(t, source)
	testProcessses(t, source, 0)
	testAllProcs(t, source)

	for _, maxAge := range []time.Duration{0, time.Hour} {
		maxAge := maxAge

		for _, testName := range []string{"AllProcs", "Processses", "mixed"} {
			testName := testName

			for n := 0; n < 5; n++ {
				n := n

				source := &Processes{
					HostRootPath:    "/",
					DefaultValidity: 10 * time.Second,
				}

				t.Run(testName, func(t *testing.T) {
					t.Parallel()

					switch {
					case testName == "AllProcs" || (testName == "mixed" && n%2 == 0):
						testAllProcs(t, source)
					default:
						testProcessses(t, source, maxAge)
					}
				})
			}
		}
	}
}

func testAllProcs(t *testing.T, source proc.Source) {
	t.Helper()

	myPID := os.Getpid()
	foundMyself := false

	procs := source.AllProcs()
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

func testProcessses(t *testing.T, source facts.ProcessLister, maxAge time.Duration) {
	t.Helper()

	myPID := os.Getpid()
	foundMyself := false

	procs, err := source.Processes(context.Background(), maxAge)
	if err != nil {
		t.Error(err)
	}

	for _, p := range procs {
		if p.PID == myPID {
			foundMyself = true
		}
	}

	if !foundMyself {
		t.Errorf("My PID (=%d) is not in processes list", myPID)
	}
}

//nolint:gocyclo,cyclop
func testProc(t *testing.T, procs proc.Iter) {
	t.Helper()

	internalProc, _ := procs.(*iter)
	current := internalProc.procValue

	if current.procErr != nil {
		// [ESRCH] No process or process group can be found corresponding to that specified by pid.
		// This error can happen when the processcurrent exist while opening it, but not anymore when you want to read it.
		if os.IsNotExist(current.procErr) || strings.Contains(current.procErr.Error(), syscall.ESRCH.Error()) {
			return
		}

		// skip overflow errors if we are in 32bits mode (we assume we are on a 64bits system).
		// We do this because enumerating 64bits process when running in 32bits will fail,
		// as the memory space of theses processes will overflow the capacity of uint,
		// and uint is the type the procfs uses to represent the VM size of processes.
		if runtime.GOARCH == "386" && strings.Contains(current.procErr.Error(), "integer overflow") {
			return
		}

		t.Errorf("An error occurred for the current process: %w", current.procErr)
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
	if err != nil && os.IsNotExist(err) {
		return
	}

	cmdline, err := current.getCmdline()
	if err != nil {
		if os.IsNotExist(err) || strings.Contains(current.procErr.Error(), syscall.ESRCH.Error()) {
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
	if err != nil && !os.IsNotExist(err) {
		t.Errorf("An error occurred wile trying to get Metrics: %v", err)
	}
}
