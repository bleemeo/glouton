//go:build linux
// +build linux

package process

import (
	"context"
	"errors"
	"fmt"
	"glouton/facts"
	"glouton/logger"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/AstromechZA/etcpwdparse"
	"github.com/ncabatoff/process-exporter/proc"
	"github.com/prometheus/procfs"
	"github.com/shirou/gopsutil/process"
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

// Processes allow to list process and kept in cache the last value
// It allow to query list of processes for multiple usage without re-doing the list.
type Processes struct {
	HostRootPath    string
	DefaultValidity time.Duration

	l          sync.Mutex
	source     *proc.FS
	cache      []*procValue
	exeCache   map[proc.ID]string
	userCache  map[proc.ID]string
	lastUpdate time.Time
}

func expectedError(expected string) error {
	return fmt.Errorf("%w, expected a %s", errChangedInternal, expected)
}

// NewProcessLister creates a new ProcessLister using the specified parameters.
func NewProcessLister(hostRootPath string, defaultValidity time.Duration) facts.ProcessLister {
	return &Processes{
		HostRootPath:    hostRootPath,
		DefaultValidity: defaultValidity,
	}
}

// AllProcs return all processes.
func (c *Processes) AllProcs() proc.Iter {
	procs, err := c.getProcs(c.DefaultValidity)

	return &iter{
		err:  err,
		list: procs,
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

// Processes lists all the processes.
func (c *Processes) Processes(ctx context.Context, maxAge time.Duration) (processes []facts.Process, err error) {
	procs, err := c.getProcs(maxAge)

	c.l.Lock()
	exeCache := c.exeCache
	userCache := c.userCache
	c.l.Unlock()

	if err != nil {
		return nil, err
	}

	newExeCache := make(map[proc.ID]string, len(exeCache))
	newUserCache := make(map[proc.ID]string, len(userCache))
	pwdLookup := c.getPwdlookup()

	result := make([]facts.Process, 0, len(procs))

	var skippedProcesses error

	for _, p := range procs {
		if os.IsNotExist(p.procErr) {
			continue
		}

		if p.procErr != nil {
			skippedProcesses = fmt.Errorf("Processes were skipped, the process list may be incomplete (last reason was %w)", err)

			continue
		}

		status := facts.PsStat2Status(p.procStat.State)

		executable, ok := exeCache[p.ID]
		if !ok {
			psProc, err := process.NewProcess(int32(p.ID.Pid))
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

		startTime := time.Unix(int64(c.source.BootTime), 0).UTC()
		startTime = startTime.Add(time.Second / userHZ * time.Duration(p.procStat.Starttime))

		cmdline, err := p.getCmdline()
		if err != nil {
			logger.V(2).Printf("getCmdline failed: %v", err)
		}

		result = append(result, facts.Process{
			PID:             p.ID.Pid,
			PPID:            p.procStat.PPID,
			CreateTime:      startTime,
			CreateTimestamp: startTime.Unix(),
			CmdLineList:     cmdline,
			CmdLine:         strings.Join(cmdline, " "),
			Name:            p.procStat.Comm,
			MemoryRSS:       uint64(p.procStat.ResidentMemory()) / 1024,
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

	return result, nil
}

func (c *Processes) getProcs(validity time.Duration) ([]*procValue, error) {
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

	if time.Since(c.lastUpdate) < validity {
		return c.cache, nil
	}

	procIter := c.source.AllProcs()

	var procs []*procValue

	for procIter.Next() {
		procs = append(procs, newProcValue(procIter))
	}

	err := procIter.Close()

	if err == nil {
		c.cache = procs
		c.lastUpdate = start
	}

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
