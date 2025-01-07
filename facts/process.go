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

package facts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/version"

	"github.com/AstromechZA/etcpwdparse"
	"github.com/cespare/xxhash/v2"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
)

type ProcessStatus string

const (
	ProcessStatusRunning     ProcessStatus = "running"
	ProcessStatusSleeping    ProcessStatus = "sleeping"
	ProcessStatusStopped     ProcessStatus = "stopped"
	ProcessStatusIdle        ProcessStatus = "idle"
	ProcessStatusZombie      ProcessStatus = "zombie"
	ProcessStatusIOWait      ProcessStatus = "disk-sleep"
	ProcessStatusTracingStop ProcessStatus = "tracing-stop"
	ProcessStatusDead        ProcessStatus = "dead"
	ProcessStatusUnknown     ProcessStatus = "?"
)

const (
	defaultLowProcessThreshold = 5
	maxTopInfoProcesses        = 2000
)

var errNotAvailable = errors.New("feature not available on this system")

// ProcessProvider provider information about processes.
type ProcessProvider struct {
	l sync.Mutex

	containerRuntime containerRuntime
	startedAt        time.Time
	ps               processQuerier

	processes              map[int]Process
	processesDiscoveryInfo map[int]processDiscoveryInfo
	topinfo                TopInfo
	lastCPUtimes           cpu.TimesStat
	lastProcessesUpdate    time.Time
	pendingUpdateCond      *sync.Cond
	pendingUpdate          bool
}

// Process describe one Process.
type Process struct {
	PID             int           `json:"pid"`
	PPID            int           `json:"ppid"`
	CreateTime      time.Time     `json:"-"`
	CreateTimestamp int64         `json:"create_time"`
	CmdLineList     []string      `json:"-"`
	CmdLine         string        `json:"cmdline"`
	Name            string        `json:"name"`
	MemoryRSS       uint64        `json:"memory_rss"`
	CPUPercent      float64       `json:"cpu_percent"`
	CPUTime         float64       `json:"cpu_times"`
	Status          ProcessStatus `json:"status"`
	Username        string        `json:"username"`
	Executable      string        `json:"exe"`
	ContainerID     string        `json:"-"`
	ContainerName   string        `json:"instance"`
	NumThreads      int           `json:"num_threads"`
}

// TopInfo contains all information to show a top-like view.
type TopInfo struct {
	Time                   int64                 `json:"time"`
	Uptime                 int                   `json:"uptime"`
	Loads                  []float64             `json:"loads"`
	Users                  int                   `json:"users"`
	Processes              []Process             `json:"processes"`
	CPU                    CPUUsage              `json:"cpu"`
	Memory                 MemoryUsage           `json:"memory"`
	Swap                   SwapUsage             `json:"swap"`
	ProcessListTruncatedAt *int                  `json:"process_list_truncated_at"`
	ProcessesCount         map[ProcessStatus]int `json:"processes_count"`
}

// CPUUsage contains usage of CPU.
type CPUUsage struct {
	User      float64 `json:"user"`
	Nice      float64 `json:"nice"`
	System    float64 `json:"system"`
	Idle      float64 `json:"idle"`
	IOWait    float64 `json:"iowait"`
	Guest     float64 `json:"guest"`
	GuestNice float64 `json:"guest_nice"`
	IRQ       float64 `json:"irq"`
	SoftIRQ   float64 `json:"softirq"`
	Steal     float64 `json:"steal"`
}

// MemoryUsage contains usage of Memory.
type MemoryUsage struct {
	Total   float64 `json:"total"`
	Used    float64 `json:"used"`
	Free    float64 `json:"free"`
	Buffers float64 `json:"buffers"`
	Cached  float64 `json:"cached"`
}

// SwapUsage contains usage of Swap.
type SwapUsage struct {
	Total float64 `json:"total"`
	Used  float64 `json:"used"`
	Free  float64 `json:"free"`
}

type processDiscoveryInfo struct {
	cgroupHash uint64
	hadError   bool
}

// NewPsUtilLister creates and populate a PsUtilLister.
func NewPsUtilLister(hostRootPath string) PsutilLister {
	ps := PsutilLister{}

	if hostRootPath != "" && hostRootPath != "/" {
		pwdCache := etcpwdparse.NewEtcPasswdCache(true)
		fileName := filepath.Join(hostRootPath, "etc/passwd")

		if err := pwdCache.LoadFromPath(fileName); err != nil {
			logger.V(1).Printf("Unable to load %#v, username lookup may fail: %v", fileName, err)
		} else {
			ps.pwdCache = pwdCache
		}
	}

	return ps
}

// NewProcess creates a new Process provider
//
// Docker provider should be given to allow processes to be associated with a Docker container.
// useProc should be true if the Agent see all processes (running outside container or with host PID namespace).
func NewProcess(pslister ProcessLister, cr containerRuntime) *ProcessProvider {
	pp := &ProcessProvider{
		containerRuntime: cr,
		ps: psListerWrapper{
			ProcessLister: pslister,
		},
		startedAt: time.Now(),
	}

	pp.pendingUpdateCond = sync.NewCond(&pp.l)

	return pp
}

// Processes returns the list of processes present on this system.
//
// It may use a cached value as old as maxAge.
func (pp *ProcessProvider) Processes(ctx context.Context, maxAge time.Duration) (processes map[int]Process, err error) {
	processes, _, err = pp.ProcessesWithTime(ctx, maxAge)

	return
}

// TopInfo returns a topinfo object
//
// It may use a cached value as old as maxAge.
func (pp *ProcessProvider) TopInfo(ctx context.Context, maxAge time.Duration) (topinfo TopInfo, err error) {
	pp.l.Lock()
	defer pp.l.Unlock()

	if time.Since(pp.lastProcessesUpdate) >= maxAge {
		for pp.pendingUpdate {
			pp.pendingUpdateCond.Wait()
		}

		pp.pendingUpdate = true
		defer func() {
			pp.pendingUpdate = false
			pp.pendingUpdateCond.Signal()
		}()

		if time.Since(pp.lastProcessesUpdate) >= maxAge {
			pp.l.Unlock()
			err = pp.updateProcesses(ctx, time.Now(), maxAge, defaultLowProcessThreshold)
			pp.l.Lock()

			if err != nil {
				return
			}
		}
	}

	return pp.topinfo, nil
}

// ProcessesWithTime returns the list of processes present on this system and the date of last update
//
// It the same as Processes but also return the date of last update.
func (pp *ProcessProvider) ProcessesWithTime(ctx context.Context, maxAge time.Duration) (processes map[int]Process, updateAt time.Time, err error) {
	pp.l.Lock()
	defer pp.l.Unlock()

	if time.Since(pp.lastProcessesUpdate) >= maxAge {
		for pp.pendingUpdate {
			pp.pendingUpdateCond.Wait()
		}

		pp.pendingUpdate = true
		defer func() {
			pp.pendingUpdate = false
			pp.pendingUpdateCond.Signal()
		}()

		if time.Since(pp.lastProcessesUpdate) >= maxAge {
			pp.l.Unlock()
			err = pp.updateProcesses(ctx, time.Now(), maxAge, defaultLowProcessThreshold)
			pp.l.Lock()

			if err != nil {
				return nil, time.Time{}, err
			}
		}
	}

	if ctx.Err() != nil {
		return nil, time.Time{}, ctx.Err()
	}

	return pp.processes, pp.lastProcessesUpdate, nil
}

// PsStat2Status convert status (value in ps output - or in /proc/pid/stat) to human status.
func PsStat2Status(psStat string) ProcessStatus {
	if psStat == "" {
		return ProcessStatusUnknown
	}

	switch psStat {
	case process.Running:
		return ProcessStatusRunning
	case process.Sleep:
		return ProcessStatusSleeping
	case process.Stop:
		return ProcessStatusStopped
	case process.Idle:
		return ProcessStatusIdle
	case process.Zombie:
		return ProcessStatusZombie
	case process.Wait:
		return ProcessStatusIOWait
	case process.Lock:
		return ProcessStatusUnknown
	}

	return convertPSStatusOneChar(psStat[0])
}

func convertPSStatusOneChar(letter byte) ProcessStatus {
	switch letter {
	case 'D':
		return ProcessStatusIOWait
	case 'R':
		return ProcessStatusRunning
	case 'S':
		return ProcessStatusSleeping
	case 'T':
		return ProcessStatusStopped
	case 't':
		return ProcessStatusTracingStop
	case 'X':
		return ProcessStatusDead
	case 'Z':
		return ProcessStatusZombie
	case 'I':
		return ProcessStatusIdle
	default:
		return ProcessStatusUnknown
	}
}

// Only one updateProcesses should be running at a time (the pendingUpdateCond ensure this).
// The lock should not be held, updateDiscovery take care of taking lock before access to mutable fields.
func (pp *ProcessProvider) updateProcesses(ctx context.Context, now time.Time, maxAge time.Duration, lowProcessesThreshold int) error { //nolint:maintidx
	// Process creation time is accurate up to 1/SC_CLK_TCK seconds,
	// usually 1/100th of seconds.
	// Process must be started at least 1/100th before t0.
	// Keep some additional margin by doubling this value.
	//
	// Also because other fields might be set after process creation (cgroup ?),
	// avoid processing young processes. This shouldn't be an issue for discovery because:
	// * The discovery is run 5 seconds after the trigger (apt-get install or docker run)
	// * There is an additional discovery 30 seconds later for slow to start service anyway.
	onlyStartedBefore := now.Add(1 * time.Second)
	newProcessesMap := make(map[int]Process, len(pp.processes))
	newProcessesDiscoveryInfoMap := make(map[int]processDiscoveryInfo, len(pp.processesDiscoveryInfo))

	var queryContainerRuntime ContainerRuntimeProcessQuerier

	if pp.containerRuntime != nil {
		queryContainerRuntime = pp.containerRuntime.ProcessWithCache()
	}

	if pp.ps != nil {
		psProcesses, err := pp.ps.Processes(ctx, maxAge)
		if err != nil {
			return err
		}

		for _, p := range psProcesses {
			if !p.CreateTime.Before(onlyStartedBefore) {
				continue
			}

			newProcessesMap[p.PID] = p
		}
	}

	if queryContainerRuntime != nil && len(newProcessesMap) < lowProcessesThreshold {
		// If we have too few processes listed by gopsutil, it probably means
		// we don't have access to root PID namespace. In this case do a processes
		// listing using containerRuntime. We avoid it if possible as it's rather slow.
		dockerProcesses, err := queryContainerRuntime.Processes(ctx)
		if err != nil && !errors.As(err, &NoRuntimeError{}) {
			logger.V(2).Printf("listing process from container runtime failed: %v", err)
		}

		for _, p := range dockerProcesses {
			if pOld, ok := newProcessesMap[p.PID]; ok {
				p.Update(pOld) // we prefer keeping information coming from gopsutil
				newProcessesMap[p.PID] = p
			}

			newProcessesMap[p.PID] = p
		}
	}

	// Complet ContainerID/ContainerName
	if pp.containerRuntime != nil && pp.ps != nil {
		newProcesses := make([]Process, 0, len(newProcessesMap))
		pid2Cgroup := make(map[int]string)

		for _, p := range newProcessesMap {
			newProcesses = append(newProcesses, p)
		}

		newProcesses = sortParentFirst(newProcesses)

		for _, p := range newProcesses {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			var fromCgroupErr error

			if p.ContainerID != "" || queryContainerRuntime == nil {
				continue
			}

			cgroupData, err := pp.ps.CGroupFromPID(p.PID)
			if err != nil && !errors.Is(err, errNotAvailable) {
				logger.V(2).Printf("No cgroup data for process %d (%s): can't read cgroup data: %v", p.PID, p.Name, err)
			} else if err == nil {
				pid2Cgroup[p.PID] = cgroupData
			}

			cgroupHash := xxhash.Sum64String(cgroupData)

			// Reuse the previous discovered time if:
			// * If the same PID & CreateTime (that is, it's the same process)
			// * AND cgroup didn't changed. A process may change cgroup during its lifetime
			// * AND previous discovery din't had error
			if oldP, ok := pp.processes[p.PID]; ok && oldP.CreateTime.Equal(p.CreateTime) && pp.processesDiscoveryInfo[p.PID].cgroupHash == cgroupHash && !pp.processesDiscoveryInfo[p.PID].hadError {
				p.ContainerID = oldP.ContainerID
				p.ContainerName = oldP.ContainerName
				newProcessesMap[p.PID] = p
				newProcessesDiscoveryInfoMap[p.PID] = processDiscoveryInfo{cgroupHash: cgroupHash}

				continue
			}

			if cgroupData != "" {
				// Test with parent, if cgroupData if the same, it belong to the same container
				// Note that because the list is sorted by creation time:
				// * the parent was processed BEFORE the current process
				// * which means parent already checked with its parent (grand-parent of the current process)
				// * parent did the full check, so the parent container name/id is filled (at least we can't have more details).
				if parent, ok := newProcessesMap[p.PPID]; ok {
					parentCGroupData := pid2Cgroup[parent.PID]
					if parentCGroupData == "" {
						parentCGroupData, err = pp.ps.CGroupFromPID(parent.PID)
						if err != nil {
							if !errors.Is(err, errNotAvailable) {
								logger.V(2).Printf("No cgroup data for parent of process %d (%s): can't read cgroup data: %v", p.PID, p.Name, err)
							}

							parentCGroupData = ""
						} else {
							pid2Cgroup[parent.PID] = parentCGroupData
						}
					}

					if parentCGroupData != "" && parentCGroupData == cgroupData {
						if parent.ContainerName != "" && parent.ContainerID != "" {
							logger.V(2).Printf("Based on parent %d, process %d (%s) belong to container %s", p.PPID, p.PID, p.Name, parent.ContainerName)

							p.ContainerID = parent.ContainerID
							p.ContainerName = parent.ContainerName
						}

						newProcessesMap[p.PID] = p
						newProcessesDiscoveryInfoMap[p.PID] = processDiscoveryInfo{cgroupHash: cgroupHash}

						continue
					}
				}

				container, err := queryContainerRuntime.ContainerFromCGroup(ctx, cgroupData)
				fromCgroupErr = err

				if errors.Is(fromCgroupErr, ErrContainerDoesNotExists) && (now.Sub(p.CreateTime) < time.Minute || now.Sub(pp.startedAt) < 5*time.Minute) {
					logger.V(2).Printf("Skipping process %d (%s) created recently and seems to belong to a container", p.PID, p.Name)
					delete(newProcessesMap, p.PID)

					continue
				}

				if fromCgroupErr == nil && container != nil {
					logger.V(2).Printf("Based on cgroup, process %d (%s) belong to container %s", p.PID, p.Name, container.ContainerName())

					p.ContainerID = container.ID()
					p.ContainerName = container.ContainerName()
					newProcessesMap[p.PID] = p
					newProcessesDiscoveryInfoMap[p.PID] = processDiscoveryInfo{cgroupHash: cgroupHash}

					continue
				}
			}

			if exists, _ := pp.ps.PidExists(int32(p.PID)); !exists { //nolint:gosec
				logger.V(2).Printf("Skipping process %d (%s) terminated recently", p.PID, p.Name)
				delete(newProcessesMap, p.PID)

				continue
			}

			if p.ContainerID == "" {
				container, fromPIDErr := queryContainerRuntime.ContainerFromPID(ctx, newProcessesMap[p.PPID].ContainerID, p.PID)

				switch {
				case container != nil:
					p.ContainerID = container.ID()
					p.ContainerName = container.ContainerName()

					newProcessesMap[p.PID] = p
					newProcessesDiscoveryInfoMap[p.PID] = processDiscoveryInfo{cgroupHash: cgroupHash}
				default:
					// Check another time because the process may have terminated while findContainerOfProcess is running
					if exists, _ := pp.ps.PidExists(int32(p.PID)); !exists { //nolint:gosec
						logger.V(2).Printf("Skipping process %d (%s) terminated very recently (2nd check)", p.PID, p.Name)
						delete(newProcessesMap, p.PID)

						continue
					}

					age := now.Sub(p.CreateTime)
					if now.Sub(pp.startedAt) < age {
						age = now.Sub(pp.startedAt)
					}

					switch {
					case fromCgroupErr == nil && fromPIDErr == nil:
						// no error, this process doesn't belong to a container
					case (errors.As(fromCgroupErr, &NoRuntimeError{}) || fromCgroupErr == nil) && errors.As(fromPIDErr, &NoRuntimeError{}):
						// Wait a bit to be sure on reboot some process don't get wrongly detected.
						// This mostly means that any process will be delayed by 10 seconds when Docker isn't used.
						if age < 10*time.Second {
							logger.V(2).Printf("Skipping process %d (%s) because FromCgroup & FromPID fail with NoRuntime: %v", p.PID, p.Name, fromPIDErr)
							delete(newProcessesMap, p.PID)

							continue
						}
					case fromCgroupErr == nil && fromPIDErr != nil:
						// ContainerFromCGroup could not fail even if fromPID does, because based on cgroup data it
						// could "know" that the process doesn't belong to a container.
						// Because this knowledge isn't guaranteed, we still delay the discovery a bit.
						if age < 20*time.Second {
							logger.V(2).Printf("Skipping process %d (%s) because FromPID failed with %v", p.PID, p.Name, fromPIDErr)
							delete(newProcessesMap, p.PID)

							continue
						}
					case errors.As(fromCgroupErr, &NoRuntimeError{}) || errors.As(fromPIDErr, &NoRuntimeError{}):
						// Not sure this case could happen. Wait more than previous, since it means another error happened.
						if age < time.Minute {
							logger.V(2).Printf("Skipping process %d (%s) because FromCgroup OR FromPID failed with NoRuntime: %v / %s", p.PID, p.Name, fromCgroupErr, fromPIDErr)
							delete(newProcessesMap, p.PID)

							continue
						}
					case errors.Is(fromCgroupErr, context.DeadlineExceeded) || errors.Is(fromPIDErr, context.DeadlineExceeded):
						if age < 5*time.Minute {
							logger.V(2).Printf("Skipping process %d (%s) because FromCgroup or FromPID failed with timeout: %v / %s", p.PID, p.Name, fromCgroupErr, fromPIDErr)
							delete(newProcessesMap, p.PID)

							continue
						}
					default:
						if age < time.Minute {
							logger.V(2).Printf("Skipping process %d (%s) because FromCgroup / FromPID failed: %v / %s", p.PID, p.Name, fromCgroupErr, fromPIDErr)
							delete(newProcessesMap, p.PID)

							continue
						}
					}

					hadError := false
					if fromCgroupErr != nil && !errors.As(fromCgroupErr, &NoRuntimeError{}) {
						hadError = true
					}

					if fromPIDErr != nil && !errors.As(fromPIDErr, &NoRuntimeError{}) {
						hadError = true
					}

					newProcessesDiscoveryInfoMap[p.PID] = processDiscoveryInfo{cgroupHash: cgroupHash, hadError: hadError}
				}
			}
		}
	}

	// Update CPU percent
	for pid, p := range newProcessesMap {
		if oldP, ok := pp.processes[pid]; ok && oldP.CreateTime.Equal(p.CreateTime) {
			deltaT := time.Since(pp.lastProcessesUpdate)
			deltaCPU := p.CPUTime - oldP.CPUTime

			if deltaCPU > 0 && deltaT > 0 {
				p.CPUPercent = deltaCPU / deltaT.Seconds() * 100
				newProcessesMap[pid] = p
			}
		} else if !p.CreateTime.IsZero() {
			deltaT := time.Since(p.CreateTime)
			deltaCPU := p.CPUTime

			if deltaCPU > 0 && deltaT > 0 {
				p.CPUPercent = deltaCPU / deltaT.Seconds() * 100
				newProcessesMap[pid] = p
			}
		}
	}

	topinfo, err := pp.baseTopinfo()
	if err != nil {
		return err
	}

	topinfo.Time = now.Unix()
	topinfo.Processes = make([]Process, 0, len(newProcessesMap))
	topinfo.ProcessesCount = make(map[ProcessStatus]int)

	for _, p := range newProcessesMap {
		topinfo.Processes = append(topinfo.Processes, p)
		topinfo.ProcessesCount[p.Status]++
	}

	if len(topinfo.Processes) > maxTopInfoProcesses {
		// Limit the number of processes, because a too large list of processes
		// won't be usable and will be rejected by the Bleemeo Cloud.
		// We start by sorting them to always return the same processes.
		sortProcessesArbitrarily(topinfo.Processes, os.Getpid(), topinfo.Memory.Total)

		topinfo.Processes = topinfo.Processes[:maxTopInfoProcesses]
		at := maxTopInfoProcesses
		topinfo.ProcessListTruncatedAt = &at
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	pp.l.Lock()
	defer pp.l.Unlock()

	pp.topinfo = topinfo
	pp.processes = newProcessesMap
	pp.processesDiscoveryInfo = newProcessesDiscoveryInfoMap
	pp.lastProcessesUpdate = now

	logger.V(2).Printf("Completed %d processes update in %v", len(pp.processes), time.Since(now))

	return nil
}

func sortParentFirst(processes []Process) []Process {
	pidToIndex := make(map[int]int, len(processes))
	pidToChildrenCount := make(map[int]int, len(processes))
	indexToChildrens := make([][]int, len(processes))
	rootIdx := make([]int, 0, 10)
	tmp := make([]int, len(processes))
	tmpIdx := 0

	for i, p := range processes {
		pidToIndex[p.PID] = i
		pidToChildrenCount[p.PPID]++
	}

	for i, p := range processes {
		i2, ok := pidToIndex[p.PPID]
		if !ok || p.PID == p.PPID {
			rootIdx = append(rootIdx, i)

			continue
		}

		if indexToChildrens[i2] == nil {
			l := pidToChildrenCount[p.PPID]
			indexToChildrens[i2] = tmp[tmpIdx : tmpIdx : tmpIdx+l]
			tmpIdx += l
		}

		indexToChildrens[i2] = append(indexToChildrens[i2], i)
	}

	sort.Slice(rootIdx, func(i, j int) bool {
		p1 := processes[rootIdx[i]]
		p2 := processes[rootIdx[j]]

		return p1.CreateTime.Before(p2.CreateTime)
	})

	return addChildrens(indexToChildrens, processes, make([]Process, 0, len(processes)), rootIdx)
}

func addChildrens(childrens [][]int, proccesses []Process, result []Process, indexes []int) []Process {
	for _, childI := range indexes {
		result = append(result, proccesses[childI])

		result = addChildrens(childrens, proccesses, result, childrens[childI])
	}

	return result
}

// sortProcessesArbitrarily sorts the given processes by CPU & memory usage,
// while putting on top the PID 1 process, the Glouton process,
// and the first (oldest) process of each container.
func sortProcessesArbitrarily(processes []Process, gloutonPID int, memTotal float64) {
	type pidComp struct {
		pid  int
		date time.Time
	}

	firstPIDByContainer := make(map[string]pidComp)

	for _, p := range processes {
		if p.ContainerName != "" {
			if comp, found := firstPIDByContainer[p.ContainerName]; !found || p.CreateTime.Before(comp.date) {
				firstPIDByContainer[p.ContainerName] = pidComp{p.PID, p.CreateTime}
			}
		}
	}

	sort.Slice(processes, func(i, j int) bool {
		switch processI, processJ := processes[i], processes[j]; {
		case processI.PID == 1:
			return true
		case processJ.PID == 1:
			return false
		case processI.PID == gloutonPID:
			return true
		case processJ.PID == gloutonPID:
			return false
		case processI.PID == firstPIDByContainer[processI.ContainerName].pid:
			return true
		case processJ.PID == firstPIDByContainer[processJ.ContainerName].pid:
			return false
		default:
			memI := (float64(processI.MemoryRSS) / memTotal) * 100
			memJ := (float64(processJ.MemoryRSS) / memTotal) * 100
			// If process I consumes more resources than J, it must be sorted before J.
			return processI.CPUPercent+memI > processJ.CPUPercent+memJ
		}
	})
}

func (pp *ProcessProvider) baseTopinfo() (result TopInfo, err error) {
	uptime, err := host.Uptime()
	if err != nil {
		return result, err
	}

	result.Uptime = int(uptime) //nolint:gosec

	result.Loads, err = getCPULoads()
	if err != nil {
		return result, err
	}

	users, err := host.Users()
	if err != nil {
		logger.V(2).Printf("Unable to get users count: %v", err)

		users = nil
	}

	result.Users = len(users)

	memUsage, err := mem.VirtualMemory()
	if err != nil {
		return result, err
	}

	result.Memory.Total = float64(memUsage.Total) / 1024.
	result.Memory.Used = float64(memUsage.Used) / 1024.
	result.Memory.Free = float64(memUsage.Free) / 1024.
	result.Memory.Buffers = float64(memUsage.Buffers) / 1024.
	result.Memory.Cached = float64(memUsage.Cached) / 1024.

	// swap is a complex topic on windows
	if !version.IsWindows() {
		swapUsage, err := mem.SwapMemory()
		if err == nil {
			result.Swap.Total = float64(swapUsage.Total) / 1024.
			result.Swap.Used = float64(swapUsage.Used) / 1024.
			result.Swap.Free = float64(swapUsage.Free) / 1024.
		}
	}

	cpusTimes, err := cpu.Times(false)
	if err != nil {
		return result, err
	}

	// cpu.Times may return an empty list and no error, so we need to check the list length.
	var timesStat cpu.TimesStat

	if len(cpusTimes) > 0 {
		timesStat = cpusTimes[0]
	} else {
		logger.V(1).Println("Failed to get cpu times: got empty result and no error")
	}

	total1 := timeStatTotal(pp.lastCPUtimes)
	total2 := timeStatTotal(timesStat)

	if delta := total2 - total1; delta >= 0 {
		between0and100 := func(input float64) float64 {
			if input < 0 {
				return 0
			}

			if input > 100 {
				return 100
			}

			return input
		}

		result.CPU.User = between0and100((timesStat.User - pp.lastCPUtimes.User) / delta * 100)
		result.CPU.Nice = between0and100((timesStat.Nice - pp.lastCPUtimes.Nice) / delta * 100)
		result.CPU.System = between0and100((timesStat.System - pp.lastCPUtimes.System) / delta * 100)
		result.CPU.Idle = between0and100((timesStat.Idle - pp.lastCPUtimes.Idle) / delta * 100)
		result.CPU.IOWait = between0and100((timesStat.Iowait - pp.lastCPUtimes.Iowait) / delta * 100)
		result.CPU.Guest = between0and100((timesStat.Guest - pp.lastCPUtimes.Guest) / delta * 100)
		result.CPU.GuestNice = between0and100((timesStat.GuestNice - pp.lastCPUtimes.GuestNice) / delta * 100)
		result.CPU.IRQ = between0and100((timesStat.Irq - pp.lastCPUtimes.Irq) / delta * 100)
		result.CPU.SoftIRQ = between0and100((timesStat.Softirq - pp.lastCPUtimes.Softirq) / delta * 100)
		result.CPU.Steal = between0and100((timesStat.Steal - pp.lastCPUtimes.Steal) / delta * 100)
	}

	pp.lastCPUtimes = timesStat

	return result, nil
}

// timeStatTotal returns the total number of seconds in a CPUTimesStat.
func timeStatTotal(c cpu.TimesStat) float64 {
	return c.User + c.System + c.Idle + c.Nice + c.Iowait + c.Irq + c.Softirq + c.Steal
}

// Update update self taking any non-zero fields from other.
func (p *Process) Update(other Process) {
	if other.PPID != 0 {
		p.PPID = other.PPID
	}

	if !other.CreateTime.IsZero() {
		p.CreateTime = other.CreateTime
		p.CreateTimestamp = other.CreateTimestamp
	}

	if len(other.CmdLineList) > 0 {
		p.CmdLineList = other.CmdLineList
		p.CmdLine = other.CmdLine
	}

	if other.Name != "" {
		p.Name = other.Name
	}

	if other.MemoryRSS != 0 {
		p.MemoryRSS = other.MemoryRSS
	}

	if other.CPUPercent != 0 {
		p.CPUPercent = other.CPUPercent
	}

	if other.CPUTime != 0 {
		p.CPUTime = other.CPUTime
	}

	if other.Status != "" {
		p.Status = other.Status
	}

	if other.Username != "" {
		// Don't overwrite existing Username with a numeric Username
		if _, err := strconv.ParseInt(other.Username, 10, 0); err != nil || p.Username == "" {
			p.Username = other.Username
		}
	}

	if other.Executable != "" {
		p.Executable = other.Executable
	}

	if other.ContainerID != "" {
		p.ContainerID = other.ContainerID
	}

	if other.ContainerName != "" {
		p.ContainerName = other.ContainerName
	}
}

// ProcessLister return a list of Process. Some fields won't be used and will be filled by ProcessProvider.
// For example Container or CPUPercent.
type ProcessLister interface {
	Processes(ctx context.Context, maxAge time.Duration) (processes []Process, err error)
}

type processQuerier interface {
	Processes(ctx context.Context, maxAge time.Duration) (processes []Process, err error)
	CGroupFromPID(pid int) (string, error)
	PidExists(pid int32) (bool, error)
}

type psListerWrapper struct {
	ProcessLister
}

func (p psListerWrapper) CGroupFromPID(pid int) (string, error) {
	if !version.IsLinux() {
		return "", errNotAvailable
	}

	path := filepath.Join("/proc", strconv.Itoa(pid), "cgroup")

	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func (p psListerWrapper) PidExists(pid int32) (bool, error) {
	return process.PidExists(pid)
}

// PsutilLister contains the passwd cache information.
type PsutilLister struct {
	pwdCache *etcpwdparse.EtcPasswdCache
}

// SystemProcessInformationStruct is windows-specific, necessary for running assertions on its size
//
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
