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
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"

	"github.com/AstromechZA/etcpwdparse"
	"github.com/ncabatoff/process-exporter/proc"
	"github.com/prometheus/procfs"
	"github.com/shirou/gopsutil/v4/process"
)

var (
	errUnknownUser     = errors.New("user not found")
	errNullPointer     = errors.New("getProcCache return null-pointer")
	errChangedInternal = errors.New("process-exporter changed its internal")
)

// See https://github.com/prometheus/procfs/blob/master/proc_stat.go for details on userHZ.
const userHZ = 100

//go:linkname getCmdLinePrivateMethod github.com/ncabatoff/process-exporter/proc.(*proccache).getCmdLine
func getCmdLinePrivateMethod(unsafe.Pointer) ([]string, error)

//go:linkname getStatPrivateMethod github.com/ncabatoff/process-exporter/proc.(*proccache).getStat
func getStatPrivateMethod(unsafe.Pointer) (procfs.ProcStat, error)

// Processes allows to list processes and keep the last values in cache.
// It allows to query a list of processes for multiple usages without re-doing the list.
type Processes struct {
	HostRootPath string

	l         sync.Mutex
	source    *proc.FS
	exeCache  map[proc.ID]string
	userCache map[proc.ID]string
}

func expectedError(expected string) error {
	return fmt.Errorf("%w, expected a %s", errChangedInternal, expected)
}

// NewProcessLister creates a new ProcessLister using the specified parameters.
func NewProcessLister(hostRootPath string) facts.ProcessLister {
	return &Processes{
		HostRootPath: hostRootPath,
	}
}

func (c *Processes) getPwdlookup() func(uid int) (string, error) {
	hostRootPath := c.HostRootPath
	if hostRootPath == "" {
		hostRootPath = "/"
	}

	pwdCache := etcpwdparse.NewEtcPasswdCache(true)
	fileName := filepath.Join(hostRootPath, "etc/passwd")

	if err := pwdCache.LoadFromPath(fileName); err != nil {
		return func(uid int) (string, error) {
			u, err := user.LookupId(strconv.FormatInt(int64(uid), 10))
			if err != nil {
				return "", err
			}

			return u.Username, err
		}
	}

	return func(uid int) (string, error) {
		u, ok := pwdCache.LookupUserByUid(uid)
		if !ok {
			return "", errUnknownUser
		}

		return u.Username(), nil
	}
}

func isProcNotExist(err error) bool {
	if err == nil {
		return false
	}

	// [ESRCH] No process or process group can be found corresponding to that specified by pid.
	// This error can happen when the processcurrent exist while opening it, but not anymore when you want to read it.
	return os.IsNotExist(err) || strings.Contains(err.Error(), syscall.ESRCH.Error())
}

// Processes lists all the processes.
func (c *Processes) Processes(ctx context.Context) (processes []facts.Process, factory func() types.ProcIter, err error) {
	procs, err := c.getProcs()

	c.l.Lock()
	exeCache := c.exeCache
	userCache := c.userCache
	c.l.Unlock()

	factory = func() types.ProcIter {
		return &iter{
			err:  err,
			list: procs,
		}
	}

	if err != nil {
		return nil, factory, err
	}

	newExeCache := make(map[proc.ID]string, len(exeCache))
	newUserCache := make(map[proc.ID]string, len(userCache))
	pwdLookup := c.getPwdlookup()

	result := make([]facts.Process, 0, len(procs))

	var skippedProcesses error

	for _, p := range procs {
		if isProcNotExist(p.procErr) {
			continue
		}

		if p.procErr != nil {
			skippedProcesses = fmt.Errorf("Processes were skipped, the process list may be incomplete (last reason was %w)", err)

			continue
		}

		status := facts.PsStat2Status(p.procStat.State)

		executable, ok := exeCache[p.ID]
		if !ok {
			psProc, err := process.NewProcess(int32(p.ID.Pid)) //nolint:gosec
			if err == nil {
				executable, _ = psProc.ExeWithContext(ctx)
			}
		}

		newExeCache[p.ID] = executable

		username, ok := userCache[p.ID]
		if !ok {
			static, err := p.GetStatic()
			if err == nil {
				username, _ = pwdLookup(static.EffectiveUID)
			}
		}

		newUserCache[p.ID] = username

		startTime := time.Unix(int64(c.source.BootTime), 0).UTC()                             //nolint:gosec
		startTime = startTime.Add(time.Second / userHZ * time.Duration(p.procStat.Starttime)) //nolint:gosec

		cmdline, err := p.getCmdline()
		if err != nil {
			logger.V(2).Printf("getCmdline failed: %v", err)
		}

		// If there is no command line, default to the process name.
		if len(cmdline) == 0 {
			cmdline = []string{p.procStat.Comm}
		}

		result = append(result, facts.Process{
			PID:             p.ID.Pid,
			PPID:            p.procStat.PPID,
			CreateTime:      startTime,
			CreateTimestamp: startTime.Unix(),
			CmdLineList:     cmdline,
			CmdLine:         strings.Join(cmdline, " "),
			Name:            p.procStat.Comm,
			MemoryRSS:       uint64(p.procStat.ResidentMemory()) / 1024, //nolint: gosec
			CPUTime:         float64(p.procStat.UTime+p.procStat.STime) / userHZ,
			Status:          status,
			Username:        username,
			Executable:      executable,
			NumThreads:      p.procStat.NumThreads,
		})
	}

	if skippedProcesses != nil {
		logger.V(1).Println(skippedProcesses.Error())
	}

	c.l.Lock()
	c.userCache = newUserCache
	c.exeCache = newExeCache
	c.l.Unlock()

	return result, factory, nil
}

func (c *Processes) getProcs() ([]*procValue, error) {
	c.l.Lock()
	defer c.l.Unlock()

	start := time.Now()

	if c.source == nil {
		procPath := filepath.Join(c.HostRootPath, "proc")
		if c.HostRootPath == "" {
			procPath = "/proc"
		}

		fs, err := proc.NewFS(procPath, false)
		if err != nil {
			return nil, err
		}

		fs.GatherSMaps = true
		c.source = fs
	}

	procIter := c.source.AllProcs()

	var procs []*procValue

	for procIter.Next() {
		procs = append(procs, newProcValue(procIter))
	}

	err := procIter.Close()
	if err != nil {
		logger.V(1).Printf("listing process failed: %v", err)
	}

	logger.V(2).Printf("procfs listed %d processes in %v", len(procs), time.Since(start))

	return procs, err
}

type procValue struct {
	ID    proc.ID
	IDErr error

	l        sync.Mutex
	proc     proc.Proc
	procStat procfs.ProcStat
	procErr  error
}

func newProcValue(p proc.Proc) *procValue {
	var result procValue

	result.ID, result.IDErr = p.GetProcID()
	if result.IDErr != nil {
		result.ID.Pid = p.GetPid()
	}

	result.proc, result.procErr = getProc(p)
	if result.procErr == nil {
		result.procStat, result.procErr = getStat(result.proc)
	}

	return &result
}

// getProc extract the proc.proc from proc.procIterator
//
// We want to do that to get a reference to a specific process to lazily call GetStatic(), GetMetric() or GetThreads().
func getProc(p proc.Proc) (proc.Proc, error) {
	value := reflect.ValueOf(p)

	if value.Kind() != reflect.Ptr {
		return nil, expectedError("pointer")
	}

	value = value.Elem()

	if value.Type().Name() != "procIterator" {
		return nil, fmt.Errorf("%w, expected procIterator, got %v", errChangedInternal, value.Type().Name())
	}

	if value.Kind() != reflect.Struct {
		return nil, expectedError("struct")
	}

	procValue := value.FieldByName("Proc")

	result, ok := procValue.Interface().(proc.Proc)

	if !ok {
		return nil, expectedError("proc.Proc")
	}

	return result, nil
}

// getProcCache extract the unsafePointer to proccache object.
func getProcCache(p proc.Proc) (unsafe.Pointer, error) {
	value := reflect.ValueOf(p)

	if value.Kind() != reflect.Ptr {
		return nil, expectedError("pointer")
	}

	value = value.Elem()

	if value.Type().Name() != "proc" {
		return nil, fmt.Errorf("%w, expected proc, got %v", errChangedInternal, value.Type().Name())
	}

	if value.Kind() != reflect.Struct {
		return nil, expectedError("struct")
	}

	value = value.FieldByName("proccache")

	if value.Type().Name() != "proccache" {
		return nil, fmt.Errorf("%w, expected proccache, got %v", errChangedInternal, value.Type().Name())
	}

	ptr := unsafe.Pointer(value.UnsafeAddr())
	if uintptr(ptr) == 0 {
		return nil, errNullPointer
	}

	return ptr, nil
}

// getStat extract the procfs.ProcStat from proc.proccache
//
// ProcStat allow to fill nearly all fields from facts.Process.
func getStat(p proc.Proc) (procfs.ProcStat, error) {
	ptr, err := getProcCache(p)
	if err != nil {
		return procfs.ProcStat{}, err
	}

	return getStatPrivateMethod(ptr)
}

// getCmdline extract the command line from proc.proccache.
func (p *procValue) getCmdline() ([]string, error) {
	if p.procErr != nil {
		return nil, p.procErr
	}

	ptr, err := getProcCache(p.proc)
	if err != nil {
		return nil, err
	}

	p.l.Lock()
	defer p.l.Unlock()

	return getCmdLinePrivateMethod(ptr)
}

func (p *procValue) GetThreads() ([]proc.Thread, error) {
	if p.procErr != nil {
		return nil, p.procErr
	}

	p.l.Lock()
	defer p.l.Unlock()

	return p.proc.GetThreads()
}

// GetPid implements Proc.
func (p *procValue) GetPid() int {
	return p.ID.Pid
}

// GetProcID implements Proc.
func (p *procValue) GetProcID() (proc.ID, error) {
	return p.ID, nil
}

// GetStatic implements Proc.
func (p *procValue) GetStatic() (proc.Static, error) {
	if p.procErr != nil {
		return proc.Static{}, p.procErr
	}

	p.l.Lock()
	defer p.l.Unlock()

	return p.proc.GetStatic()
}

// GetCounts implements Proc.
func (p *procValue) GetCounts() (proc.Counts, int, error) {
	if p.procErr != nil {
		return proc.Counts{}, 0, p.procErr
	}

	p.l.Lock()
	defer p.l.Unlock()

	return p.proc.GetCounts()
}

// GetMetrics implements Proc.
func (p *procValue) GetMetrics() (proc.Metrics, int, error) {
	if p.procErr != nil {
		return proc.Metrics{}, 0, p.procErr
	}

	p.l.Lock()
	defer p.l.Unlock()

	return p.proc.GetMetrics()
}

// GetStates implements Proc.
func (p *procValue) GetStates() (proc.States, error) {
	if p.procErr != nil {
		return proc.States{}, p.procErr
	}

	p.l.Lock()
	defer p.l.Unlock()

	return p.proc.GetStates()
}

func (p *procValue) GetWchan() (string, error) {
	if p.procErr != nil {
		return "", p.procErr
	}

	p.l.Lock()
	defer p.l.Unlock()

	return p.proc.GetWchan()
}

type iter struct {
	*procValue

	list  []*procValue
	err   error
	index int
}

func (i *iter) Next() bool {
	if i.err != nil {
		return false
	}

	if i.index >= len(i.list) {
		return false
	}

	i.procValue = i.list[i.index]
	i.index++

	return true
}

func (i *iter) Close() error {
	return i.err
}
