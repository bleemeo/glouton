package process

import (
	"context"
	"errors"
	"glouton/facts"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AstromechZA/etcpwdparse"
	"github.com/ncabatoff/process-exporter/proc"
	"github.com/shirou/gopsutil/process"
)

// Processes allow to list process and kept in cache the last value
// It allow to query list of processes for multiple usage without re-doing the list.
type Processes struct {
	HostRootPath    string
	DefaultValidity time.Duration

	l          sync.Mutex
	source     proc.Source
	cache      []procValue
	exeCache   map[proc.ID]string
	userCache  map[proc.ID]string
	lastUpdate time.Time
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
			return "", errors.New("user not found")
		}

		return u.Username(), nil
	}
}

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

	result := make([]facts.Process, len(procs))

	for i, p := range procs {
		status := "?"

		switch {
		case p.Metrics.States.Running > 0:
			status = "running"
		case p.Metrics.States.Sleeping > 0:
			status = "sleeping"
		case p.Metrics.States.Waiting > 0:
			status = "disk-sleep"
		case p.Metrics.States.Zombie > 0:
			status = "zombie"
		}

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
			username, _ = pwdLookup(p.Static.EffectiveUID)
		}

		newUserCache[p.ID] = username

		result[i] = facts.Process{
			PID:             p.ID.Pid,
			PPID:            p.Static.ParentPid,
			CreateTime:      p.Static.StartTime,
			CreateTimestamp: p.Static.StartTime.Unix(),
			CmdLineList:     p.Static.Cmdline,
			CmdLine:         strings.Join(p.Static.Cmdline, " "),
			Name:            p.Static.Name,
			MemoryRSS:       p.Metrics.Memory.ResidentBytes / 1024,
			CPUTime:         p.Metrics.CPUSystemTime + p.Metrics.CPUUserTime,
			Status:          status,
			Username:        username,
			Executable:      executable,
		}
	}

	c.l.Lock()
	c.userCache = newUserCache
	c.exeCache = newExeCache
	c.l.Unlock()

	return result, nil
}

func (c *Processes) getProcs(validity time.Duration) ([]procValue, error) {
	c.l.Lock()
	defer c.l.Unlock()

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

	var procs []procValue

	for procIter.Next() {
		procs = append(procs, newProcValue(procIter))
	}

	err := procIter.Close()

	if err == nil {
		c.cache = procs
		c.lastUpdate = time.Now()
	}

	return procs, err
}

type procValue struct {
	ID    proc.ID
	IDErr error

	Static    proc.Static
	StaticErr error

	Metrics          proc.Metrics
	MetricsSoftError int
	MetricsErr       error

	Threads   []proc.Thread
	ThreadErr error
}

func newProcValue(p proc.Proc) procValue {
	var (
		result procValue
	)

	result.ID, result.IDErr = p.GetProcID()
	if result.IDErr != nil {
		result.ID.Pid = p.GetPid()
	}

	result.Static, result.StaticErr = p.GetStatic()
	result.Metrics, result.MetricsSoftError, result.MetricsErr = p.GetMetrics()
	result.Threads, result.ThreadErr = p.GetThreads()

	return result
}

func (p procValue) GetThreads() ([]proc.Thread, error) {
	return p.Threads, p.ThreadErr
}

// GetPid implements Proc.
func (p procValue) GetPid() int {
	return p.ID.Pid
}

// GetProcID implements Proc.
func (p procValue) GetProcID() (proc.ID, error) {
	return p.ID, nil
}

// GetStatic implements Proc.
func (p procValue) GetStatic() (proc.Static, error) {
	return p.Static, p.StaticErr
}

// GetCounts implements Proc.
func (p procValue) GetCounts() (proc.Counts, int, error) {
	return p.Metrics.Counts, p.MetricsSoftError, p.MetricsErr
}

// GetMetrics implements Proc.
func (p procValue) GetMetrics() (proc.Metrics, int, error) {
	return p.Metrics, p.MetricsSoftError, p.MetricsErr
}

// GetStates implements Proc.
func (p procValue) GetStates() (proc.States, error) {
	return p.Metrics.States, p.MetricsErr
}

func (p procValue) GetWchan() (string, error) {
	return p.Metrics.Wchan, p.MetricsErr
}

type iter struct {
	procValue

	list  []procValue
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
