// +build linux

package process

import (
	"context"
	"glouton/facts"
	"os"
	"reflect"
	"runtime"
	"strings"
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
		t.Error(err)
	}

	if !foundMyself {
		t.Errorf("My PID (=%d) is not in processes list", myPID)
	}
}

func testProcessses(t *testing.T, source facts.ProcessLister, maxAge time.Duration) {
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

//nolint: gocyclo
func testProc(t *testing.T, procs proc.Iter) {
	internalProc := procs.(*iter)
	current := internalProc.procValue

	if os.IsNotExist(current.procErr) {
		return
	}

	if current.procErr != nil {
		// skip overflow errors if we are in 32bits mode (we assume we are on a 64bits system).
		// We do this because enumerating 64bits process when running in 32bits will fail,
		// as the memory space of theses processes will overflow the capacity of uint,
		// and uint is the type the procfs uses to represent the VM size of processes.
		if runtime.GOARCH == "386" && strings.Contains(current.procErr.Error(), "integer overflow") {
			return
		}

		t.Error(current.procErr)
	}

	if current.proc == nil {
		t.Fatalf("current.proc = nil, want non-nil")
	}

	stat, err := getStat(current.proc)
	if err != nil {
		t.Error(err)
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
		if os.IsNotExist(err) {
			return
		}

		t.Error(err)
	}

	if !reflect.DeepEqual(cmdline, static.Cmdline) {
		t.Errorf("cmdline = %v, want %v", cmdline, static.Cmdline)
	}

	if current.procStat.Comm != static.Name {
		t.Errorf("current.procStat.Comm = %v, want %v", current.procStat.Comm, static.Name)
	}

	_, _, err = procs.GetMetrics()
	if err != nil && !os.IsNotExist(err) {
		t.Error(err)
	}
}
