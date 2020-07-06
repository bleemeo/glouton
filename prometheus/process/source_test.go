package process

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/ncabatoff/process-exporter/proc"
)

func Test_reflection(t *testing.T) {
	source := Processes{
		HostRootPath:    "/",
		DefaultValidity: 10 * time.Second,
	}

	procs := source.AllProcs()
	for procs.Next() {
		testProc(t, procs)
	}

	_, err := source.Processes(context.Background(), 0)
	if err != nil {
		t.Error(err)
	}

	procs = source.AllProcs()
	for procs.Next() {
		testProc(t, procs)
	}
}

func testProc(t *testing.T, procs proc.Iter) {
	internalProc := procs.(*iter)
	current := internalProc.procValue

	if current.procErr != nil {
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

	cmdline, err := getCmdline(current.proc)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}

		t.Error(err)
	}

	static, err := procs.GetStatic()
	if err != nil && os.IsNotExist(err) {
		return
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
