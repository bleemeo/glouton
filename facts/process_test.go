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
	"encoding/csv"
	"errors"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

var errArbitraryErrorForTest = errors.New("arbitraryErrorForTest")

func TestPsStat2Status(t *testing.T) {
	cases := []struct {
		in   string
		want ProcessStatus
	}{
		// Note: the test use strings and not ProcessStatus* constant, because
		// the value of the constant is exposed (used in topinfo sent to Bleemeo).
		{"D", "disk-sleep"},
		{"I", "idle"},
		{"I<", "idle"},
		{"R+", "running"},
		{"Rl", "running"},
		{"S", "sleeping"},
		{"S<", "sleeping"},
		{"S<l", "sleeping"},
		{"SLl+", "sleeping"},
		{"Ss", "sleeping"},
		{"Z+", "zombie"},
		{"T", "stopped"},
		{"t", "tracing-stop"},
	}
	for _, c := range cases {
		got := PsStat2Status(c.in)
		if got != c.want {
			t.Errorf("psStat2Status(%#v) == %#v, want %#v", c.in, got, c.want)
		}
	}
}

type mockProcessQuerier struct {
	processesResult []Process
	processCGroup   map[int]string

	CGroupCalls []int
}

func (m *mockProcessQuerier) Processes(_ context.Context) (processes []Process, factory func() types.ProcIter, err error) {
	return m.processesResult, nil, nil
}

func (m *mockProcessQuerier) PidExists(pid int32) (bool, error) {
	for _, p := range m.processesResult {
		if p.PID == int(pid) {
			return true, nil
		}
	}

	return false, nil
}

func (m *mockProcessQuerier) CGroupFromPID(pid int) (string, error) {
	m.CGroupCalls = append(m.CGroupCalls, pid)

	return m.processCGroup[pid], nil
}

type mockContainerRuntime struct {
	WithCacheHook   func()
	processesResult []Process

	// Errors returned by each function.
	// fromCGroupErr is actually only returned if the cgroup data match a container or a seemsContainer.
	fromCGroupErr         error
	fromPIDErr            error
	otherErr              error
	cgroup2seemsContainer map[string]bool
	cgroup2Container      map[string]Container
	pid2Containers        map[int]Container

	ProcessesCallCount       int
	ContainerFromCGroupCalls []string
	ContainerFromPIDCalls    []int
}

// makePID2Containers create pid2Containers from processesResult.
func (m *mockContainerRuntime) makePID2Containers() {
	m.pid2Containers = make(map[int]Container)

	for _, p := range m.processesResult {
		m.pid2Containers[p.PID] = FakeContainer{
			FakeID:            p.ContainerID,
			FakeContainerName: p.ContainerName,
		}
	}
}

func (m *mockContainerRuntime) ProcessWithCache() ContainerRuntimeProcessQuerier {
	if m.WithCacheHook != nil {
		m.WithCacheHook()
	}

	return m
}

func (m *mockContainerRuntime) Processes(_ context.Context) ([]Process, error) {
	m.ProcessesCallCount++

	if m.otherErr != nil {
		return nil, m.otherErr
	}

	return m.processesResult, nil
}

func (m *mockContainerRuntime) ContainerFromCGroup(_ context.Context, cgroupData string) (Container, error) {
	m.ContainerFromCGroupCalls = append(m.ContainerFromCGroupCalls, cgroupData)

	c := m.cgroup2Container[cgroupData]
	if c != nil {
		if m.fromCGroupErr != nil {
			return nil, m.fromCGroupErr
		}

		return c, nil
	}

	if m.cgroup2seemsContainer[cgroupData] {
		if m.fromCGroupErr != nil {
			return nil, m.fromCGroupErr
		}

		return nil, ErrContainerDoesNotExists
	}

	return nil, nil //nolint: nilnil // here first nil means not found and its not an error.
}

func (m *mockContainerRuntime) ContainerFromPID(_ context.Context, parentContainerID string, pid int) (Container, error) {
	_ = parentContainerID

	m.ContainerFromPIDCalls = append(m.ContainerFromPIDCalls, pid)

	if m.fromPIDErr != nil {
		return nil, m.fromPIDErr
	}

	return m.pid2Containers[pid], nil
}

func TestUpdateProcesses(t *testing.T) {
	now := time.Now()
	t0 := now.Add(-time.Hour)
	psutil := &mockProcessQuerier{
		processesResult: []Process{
			{
				PID:         1,
				Name:        "init",
				CreateTime:  t0,
				ContainerID: "",
				CPUTime:     999,
			},
			{
				PID:         2,
				Name:        "[kthreadd]",
				CreateTime:  t0,
				ContainerID: "",
				CPUTime:     999,
			},
			{
				PID:         3,
				Name:        "[kworker/]",
				CreateTime:  t0,
				ContainerID: "",
				CPUTime:     999,
			},
			{
				PID:         12,
				Name:        "redis2",
				CreateTime:  t0,
				ContainerID: "",
				CPUTime:     44.2,
			},
			{
				PID:         42,
				Name:        "mysql",
				ContainerID: "",
				CPUPercent:  42.6,
			},
			{
				PID:         1337,
				Name:        "golang",
				CreateTime:  t0,
				ContainerID: "",
			},
		},
		processCGroup: map[int]string{
			1337: "cgroup-data-from-1337",
		},
	}
	cr := &mockContainerRuntime{
		cgroup2Container: map[string]Container{
			"cgroup-data-from-1337": FakeContainer{
				FakeID:            "golang-container-id",
				FakeContainerName: "golang-name",
			},
		},
		processesResult: []Process{
			{
				PID:         12,
				ContainerID: "redis-container-id",
			},
			{
				PID:         42,
				ContainerID: "mysql-container-id",
			},
		},
	}
	pp := ProcessProvider{
		containerRuntime: cr,
		ps:               psutil,
	}

	cr.makePID2Containers()

	err := pp.updateProcesses(t.Context(), now, defaultLowProcessThreshold)
	if err != nil {
		t.Error(err)
	}

	cases := []Process{
		{
			PID:         12,
			Name:        "redis2",
			ContainerID: "redis-container-id",
			CreateTime:  t0,
			CPUTime:     44.2,
			CPUPercent:  1.227, // 44.2s over 1h
		},
		{
			PID:         42,
			Name:        "mysql",
			ContainerID: "mysql-container-id",
			CPUTime:     0,
			CPUPercent:  42.6,
		},
		{
			PID:         1,
			Name:        "init",
			ContainerID: "",
			CreateTime:  t0,
			CPUTime:     999,
			CPUPercent:  27.75, // 999s over 1h
		},
		{
			PID:         2,
			Name:        "[kthreadd]",
			ContainerID: "",
			CreateTime:  t0,
			CPUTime:     999,
			CPUPercent:  27.75, // 999s over 1h
		},
		{
			PID:         3,
			Name:        "[kworker/]",
			ContainerID: "",
			CreateTime:  t0,
			CPUTime:     999,
			CPUPercent:  27.75, // 999s over 1h
		},
		{
			PID:           1337,
			Name:          "golang",
			ContainerID:   "golang-container-id",
			ContainerName: "golang-name",
			CreateTime:    t0,
			CPUTime:       0,
			CPUPercent:    0,
		},
	}
	if len(pp.processes) != len(cases) {
		t.Errorf("len(pp.processe) == %v, want %v", len(pp.processes), len(cases))
	}

	if pp.lastProcessesUpdate.After(time.Now()) || time.Since(pp.lastProcessesUpdate) > time.Second {
		t.Errorf("pp.lastProcessesUpdate = %v, want %v", pp.lastProcessesUpdate, time.Now())
	}

	if cr.ProcessesCallCount > 0 {
		t.Errorf("containerRuntime.Processes() called %d times, want 0", cr.ProcessesCallCount)
	}

	if len(cr.ContainerFromPIDCalls) == 0 {
		t.Errorf("containerRuntime.ContainerFromPID() called %d times, want > 0", len(cr.ContainerFromPIDCalls))
	}

	epsilon := 0.01

	for _, c := range cases {
		got := pp.processes[c.PID]
		if math.Abs(got.CPUPercent-c.CPUPercent) < epsilon {
			got.CPUPercent = c.CPUPercent
		}

		if !reflect.DeepEqual(got, c) {
			t.Errorf("pp.processes[%v] == %v, want %v", c.PID, got, c)
		}
	}
}

// TestUpdateProcessesWithTerminated checks that short lived process are correctly handled.
func TestUpdateProcessesWithTerminated(t *testing.T) {
	t0 := time.Now()
	t1 := t0.Add(time.Hour)
	now := t1.Add(2 * time.Second)

	psutil := &mockProcessQuerier{
		processesResult: []Process{
			{
				PID:        1,
				PPID:       0,
				Name:       "init",
				CreateTime: t0,
			},
			{
				PID:        100,
				PPID:       1,
				Name:       "terminated-right-after-ps",
				CreateTime: t0,
			},
			{
				PID:        101,
				PPID:       1,
				Name:       "cgroup-read-error",
				CreateTime: t0,
			},
			{
				PID:        102,
				PPID:       1,
				Name:       "cgroup-container-not-found",
				CreateTime: t0,
			},
			{
				PID:        103,
				PPID:       1,
				Name:       "recent-cgroup-container-not-found",
				CreateTime: t1,
			},
		},
		processCGroup: map[int]string{
			1:   "this is init",
			100: "this-wont-be-used",
			101: "", // empty string is handled like error
			102: "a-docker-slice",
			103: "a-docker-slice",
		},
	}
	cr := &mockContainerRuntime{
		cgroup2Container: map[string]Container{},
		cgroup2seemsContainer: map[string]bool{
			"a-docker-slice": true,
		},
		pid2Containers: map[int]Container{
			101: FakeContainer{
				FakeID:            "no-cgroup-id",
				FakeContainerName: "no-cgroup-name",
			},
			102: FakeContainer{
				FakeID:            "myredis-id",
				FakeContainerName: "myredis-name",
			},
		},
	}
	cr.WithCacheHook = func() {
		psutil.processesResult = []Process{
			{
				PID:        1,
				PPID:       0,
				Name:       "init",
				CreateTime: t0,
			},
			{
				PID:        101,
				PPID:       1,
				Name:       "cgroup-read-error",
				CreateTime: t0,
			},
			{
				PID:        102,
				PPID:       1,
				Name:       "cgroup-container-not-found",
				CreateTime: t0,
			},
			{
				PID:        103,
				PPID:       1,
				Name:       "recent-cgroup-container-not-found",
				CreateTime: t1,
			},
		}
	}
	pp := ProcessProvider{
		containerRuntime: cr,
		ps:               psutil,
	}

	err := pp.updateProcesses(t.Context(), now, defaultLowProcessThreshold)
	if err != nil {
		t.Error(err)
	}

	cases := []Process{
		{
			PID:        1,
			Name:       "init",
			CreateTime: t0,
		},
		{
			PID:           101,
			PPID:          1,
			Name:          "cgroup-read-error",
			ContainerID:   "no-cgroup-id",
			ContainerName: "no-cgroup-name",
			CreateTime:    t0,
		},
		{
			PID:           102,
			PPID:          1,
			Name:          "cgroup-container-not-found",
			ContainerID:   "myredis-id",
			ContainerName: "myredis-name",
			CreateTime:    t0,
		},
	}
	if len(pp.processes) != len(cases) {
		t.Errorf("len(pp.processe) == %v, want %v", len(pp.processes), len(cases))
	}

	sortInts := func(i, j int) bool {
		return i < j
	}
	wantPID := []int{1, 101, 102}

	if diff := cmp.Diff(wantPID, cr.ContainerFromPIDCalls, cmpopts.SortSlices(sortInts)); diff != "" {
		t.Errorf("ContainerFromPIDCalls mismatch (-want+got)\n%s", diff)
	}

	for _, c := range cases {
		got := pp.processes[c.PID]
		if !reflect.DeepEqual(got, c) {
			t.Errorf("pp.processes[%v] == %v, want %v", c.PID, got, c)
		}
	}
}

// TestUpdateProcessesOptimization check that ContainerRuntime is not called too much
// In case of Processes updates, reuse as much as existing information as possible.
func TestUpdateProcessesOptimization(t *testing.T) { //nolint:maintidx
	now := time.Now()
	t0 := now.Add(-time.Hour)
	t1 := now.Add(time.Minute)
	t2 := t1.Add(time.Minute)

	psutil := &mockProcessQuerier{
		processesResult: []Process{
			{
				PID:        1,
				PPID:       0,
				Name:       "init",
				CreateTime: t0,
				CPUTime:    999,
			},
			{
				PID:        2,
				PPID:       0,
				Name:       "[kthreadd]",
				CreateTime: t0,
			},
			{
				PID:        3,
				PPID:       2,
				Name:       "[kworker/]",
				CreateTime: t0,
			},
			{
				PID:        12,
				PPID:       1,
				Name:       "redis2",
				CreateTime: t0,
				CPUTime:    44.2,
			},
			{
				PID:        20,
				PPID:       1,
				Name:       "bash",
				CreateTime: t0,
			},
			{
				PID:        42,
				PPID:       1,
				Name:       "postgresql",
				CreateTime: t0,
			},
			{
				PID:        49,
				PPID:       42,
				Name:       "postgresql session",
				CreateTime: t0,
			},
		},
		processCGroup: map[int]string{
			1:  "cgroup data for init",
			2:  "cgroup for kernel",
			3:  "well, this value doesn't matter",
			20: "cgroup-for-user-session",
			// 12 is absent, simulate "error"
			42: "slice-postgresql",
			49: "slice-postgresql",
		},
	}
	cr := &mockContainerRuntime{
		cgroup2Container: map[string]Container{
			"slice-postgresql": FakeContainer{
				FakeID:            "postgresql-container-id",
				FakeContainerName: "postgresql-name",
			},
		},
		pid2Containers: map[int]Container{
			12: FakeContainer{FakeContainerName: "redis-name", FakeID: "redis-container-id"},
		},
	}
	pp := ProcessProvider{
		containerRuntime: cr,
		ps:               psutil,
	}

	err := pp.updateProcesses(t.Context(), now, defaultLowProcessThreshold)
	if err != nil {
		t.Error(err)
	}

	cases := []Process{
		{
			PID:           12,
			PPID:          1,
			Name:          "redis2",
			ContainerID:   "redis-container-id",
			ContainerName: "redis-name",
			CreateTime:    t0,
			CPUTime:       44.2,
			CPUPercent:    1.227, // 44.2s over 1h
		},
		{
			PID:           42,
			PPID:          1,
			Name:          "postgresql",
			ContainerID:   "postgresql-container-id",
			ContainerName: "postgresql-name",
			CreateTime:    t0,
		},
		{
			PID:           49,
			PPID:          42,
			Name:          "postgresql session",
			ContainerID:   "postgresql-container-id",
			ContainerName: "postgresql-name",
			CreateTime:    t0,
		},
		{
			PID:         1,
			PPID:        0,
			Name:        "init",
			ContainerID: "",
			CreateTime:  t0,
			CPUTime:     999,
			CPUPercent:  27.75, // 999s over 1h
		},
		{
			PID:         2,
			PPID:        0,
			Name:        "[kthreadd]",
			ContainerID: "",
			CreateTime:  t0,
		},
		{
			PID:         3,
			PPID:        2,
			Name:        "[kworker/]",
			ContainerID: "",
			CreateTime:  t0,
		},
		{
			PID:        20,
			PPID:       1,
			Name:       "bash",
			CreateTime: t0,
		},
	}
	if len(pp.processes) != len(cases) {
		t.Errorf("len(pp.processe) == %v, want %v", len(pp.processes), len(cases))
	}

	if pp.lastProcessesUpdate.After(time.Now()) || time.Since(pp.lastProcessesUpdate) > time.Second {
		t.Errorf("pp.lastProcessesUpdate = %v, want %v", pp.lastProcessesUpdate, time.Now())
	}

	if cr.ProcessesCallCount > 0 {
		t.Errorf("containerRuntime.Processes() called %d times, want 0", cr.ProcessesCallCount)
	}

	if len(cr.ContainerFromPIDCalls) != 5 {
		t.Errorf("containerRuntime.ContainerFromPID() called %d times, want 5", len(cr.ContainerFromPIDCalls))
	}

	if len(cr.ContainerFromCGroupCalls) != 5 {
		t.Errorf("containerRuntime.ContainerFromCGroupCalls() called %d times, want 5", len(cr.ContainerFromCGroupCalls))
	}

	if len(psutil.CGroupCalls) != len(pp.processes) {
		t.Errorf("ps.CGroupFromPID() called %d times, want %d", len(psutil.CGroupCalls), len(pp.processes))
	}

	epsilon := 0.01

	for _, c := range cases {
		got := pp.processes[c.PID]
		if math.Abs(got.CPUPercent-c.CPUPercent) < epsilon {
			got.CPUPercent = c.CPUPercent
		}

		if !reflect.DeepEqual(got, c) {
			t.Errorf("pp.processes[%v] == %v, want %v", c.PID, got, c)
		}
	}

	// Change:
	// * drop kernel thread (not needed for test)
	// * add "ls" process in user session (outside container)
	// * stopped one PG session and started a new one (in PG container)
	*psutil = mockProcessQuerier{
		processesResult: []Process{
			{
				PID:        1,
				PPID:       0,
				Name:       "init",
				CreateTime: t0,
			},
			{
				PID:        12,
				PPID:       1,
				Name:       "redis2",
				CreateTime: t0,
			},
			{
				PID:        20,
				PPID:       1,
				Name:       "bash",
				CreateTime: t0,
			},
			{
				PID:        6,
				PPID:       20,
				Name:       "ls",
				CreateTime: t1,
			},
			{
				PID:        42,
				PPID:       1,
				Name:       "postgresql",
				CreateTime: t0,
			},
			{
				PID:        40,
				PPID:       42,
				Name:       "postgresql session",
				CreateTime: t1,
			},
			{
				PID:        4000,
				PPID:       42,
				Name:       "postgresql session",
				CreateTime: t1,
			},
		},
		processCGroup: map[int]string{
			1:    "cgroup data for init",
			12:   "slice-redis",
			20:   "cgroup-for-user-session",
			6:    "cgroup-for-user-session",
			42:   "slice-postgresql",
			40:   "slice-postgresql",
			4000: "slice-postgresql",
		},
	}
	*cr = mockContainerRuntime{
		cgroup2Container: map[string]Container{
			"slice-postgresql": FakeContainer{
				FakeID:            "postgresql-container-id",
				FakeContainerName: "postgresql-name",
			},
			"slice-redis": FakeContainer{
				FakeID:            "redis-container-id",
				FakeContainerName: "redis-name",
			},
		},
	}
	cases = []Process{
		{
			PID:        1,
			PPID:       0,
			Name:       "init",
			CreateTime: t0,
		},
		{
			PID:           12,
			PPID:          1,
			Name:          "redis2",
			ContainerID:   "redis-container-id",
			ContainerName: "redis-name",
			CreateTime:    t0,
		},
		{
			PID:           40,
			PPID:          42,
			Name:          "postgresql session",
			ContainerID:   "postgresql-container-id",
			ContainerName: "postgresql-name",
			CreateTime:    t1,
		},
		{
			PID:           4000,
			PPID:          42,
			Name:          "postgresql session",
			ContainerID:   "postgresql-container-id",
			ContainerName: "postgresql-name",
			CreateTime:    t1,
		},
		{
			PID:           42,
			PPID:          1,
			Name:          "postgresql",
			ContainerID:   "postgresql-container-id",
			ContainerName: "postgresql-name",
			CreateTime:    t0,
		},
		{
			PID:        20,
			PPID:       1,
			Name:       "bash",
			CreateTime: t0,
		},
		{
			PID:        6,
			PPID:       20,
			Name:       "ls",
			CreateTime: t1,
		},
	}

	err = pp.updateProcesses(t.Context(), t1.Add(20*time.Second), defaultLowProcessThreshold)
	if err != nil {
		t.Error(err)
	}

	if len(pp.processes) != len(cases) {
		t.Errorf("len(pp.processes) == %v, want %v", len(pp.processes), len(cases))
	}

	for _, c := range cases {
		got := pp.processes[c.PID]
		if math.Abs(got.CPUPercent-c.CPUPercent) < epsilon {
			got.CPUPercent = c.CPUPercent
		}

		if !reflect.DeepEqual(got, c) {
			t.Errorf("pp.processes[%v] == %v, want %v", c.PID, got, c)
		}
	}

	if cr.ProcessesCallCount != 0 {
		t.Errorf("containerRuntime.Processes() called %d times, want 0", cr.ProcessesCallCount)
	}

	if len(cr.ContainerFromPIDCalls) != 0 {
		t.Errorf("containerRuntime.ContainerFromPID() called %d times, want 0", len(cr.ContainerFromPIDCalls))
	}

	// One call, because cgroup failed for redis on previous run
	if len(cr.ContainerFromCGroupCalls) != 1 {
		t.Errorf("containerRuntime.ContainerFromCGroupCalls() called %d times, want 1", len(cr.ContainerFromCGroupCalls))
	}

	if len(psutil.CGroupCalls) != 7 {
		t.Log(psutil.CGroupCalls)
		t.Errorf("ps.CGroupFromPID() called %d times, want 5", len(psutil.CGroupCalls))
	}

	// Change:
	// * redis restart
	// * on redis child process run
	// * one new PG session reuse PID of redis
	// * grant-children of bash created (3 processes)
	// * new shell started
	*psutil = mockProcessQuerier{
		processesResult: []Process{
			{
				PID:        1,
				PPID:       0,
				Name:       "init",
				CreateTime: t0,
			},
			{
				PID:        4010,
				PPID:       1,
				Name:       "redis2",
				CreateTime: t2,
			},
			{
				PID:        4009,
				PPID:       4010,
				Name:       "redis-background",
				CreateTime: t2.Add(time.Millisecond),
			},
			{
				PID:        20,
				PPID:       1,
				Name:       "bash",
				CreateTime: t0,
			},
			{
				PID:        21,
				PPID:       1,
				Name:       "zsh",
				CreateTime: t2,
			},
			{
				PID:        2010,
				PPID:       20,
				Name:       "grand-parent",
				CreateTime: t2,
			},
			{
				PID:        2011,
				PPID:       2010,
				Name:       "parent",
				CreateTime: t2,
			},
			{
				PID:        2012,
				PPID:       2011,
				Name:       "children",
				CreateTime: t2,
			},
			{
				PID:        42,
				PPID:       1,
				Name:       "postgresql",
				CreateTime: t0,
			},
			{
				PID:        12,
				PPID:       42,
				Name:       "postgresql session",
				CreateTime: t2,
			},
		},
		processCGroup: map[int]string{
			1:    "cgroup data for init",
			4010: "slice-redis",
			4009: "slice-redis",
			20:   "cgroup-for-user-session",
			21:   "cgroup-for-user-session",
			2010: "cgroup-for-user-session",
			2011: "cgroup-for-user-session",
			2012: "cgroup-for-user-session",
			42:   "slice-postgresql",
			12:   "slice-postgresql",
		},
	}
	*cr = mockContainerRuntime{
		cgroup2Container: map[string]Container{
			"slice-postgresql": FakeContainer{
				FakeID:            "postgresql-container-id",
				FakeContainerName: "postgresql-name",
			},
			"slice-redis": FakeContainer{
				FakeID:            "redis-container-id",
				FakeContainerName: "redis-name",
			},
		},
	}
	cases = []Process{
		{
			PID:        1,
			PPID:       0,
			Name:       "init",
			CreateTime: t0,
		},
		{
			PID:           4010,
			PPID:          1,
			Name:          "redis2",
			ContainerID:   "redis-container-id",
			ContainerName: "redis-name",
			CreateTime:    t2,
		},
		{
			PID:           4009,
			PPID:          4010,
			Name:          "redis-background",
			ContainerID:   "redis-container-id",
			ContainerName: "redis-name",
			CreateTime:    t2.Add(time.Millisecond),
		},
		{
			PID:        20,
			PPID:       1,
			Name:       "bash",
			CreateTime: t0,
		},
		{
			PID:        21,
			PPID:       1,
			Name:       "zsh",
			CreateTime: t2,
		},
		{
			PID:        2010,
			PPID:       20,
			Name:       "grand-parent",
			CreateTime: t2,
		},
		{
			PID:        2011,
			PPID:       2010,
			Name:       "parent",
			CreateTime: t2,
		},
		{
			PID:        2012,
			PPID:       2011,
			Name:       "children",
			CreateTime: t2,
		},
		{
			PID:           42,
			PPID:          1,
			Name:          "postgresql",
			ContainerID:   "postgresql-container-id",
			ContainerName: "postgresql-name",
			CreateTime:    t0,
		},
		{
			PID:           12,
			PPID:          42,
			Name:          "postgresql session",
			ContainerID:   "postgresql-container-id",
			ContainerName: "postgresql-name",
			CreateTime:    t2,
		},
	}

	err = pp.updateProcesses(t.Context(), t2.Add(time.Minute).Add(20*time.Second), defaultLowProcessThreshold)
	if err != nil {
		t.Error(err)
	}

	if len(pp.processes) != len(cases) {
		t.Errorf("len(pp.processes) == %v, want %v", len(pp.processes), len(cases))
	}

	for _, c := range cases {
		got := pp.processes[c.PID]
		if math.Abs(got.CPUPercent-c.CPUPercent) < epsilon {
			got.CPUPercent = c.CPUPercent
		}

		if !reflect.DeepEqual(got, c) {
			t.Errorf("pp.processes[%v] == %v, want %v", c.PID, got, c)
		}
	}

	if cr.ProcessesCallCount != 0 {
		t.Errorf("containerRuntime.Processes() called %d times, want 0", cr.ProcessesCallCount)
	}

	if len(cr.ContainerFromPIDCalls) != 1 { // one for new shell
		t.Errorf("containerRuntime.ContainerFromPID() called %d times, want 1", len(cr.ContainerFromPIDCalls))
	}

	if len(cr.ContainerFromCGroupCalls) != 2 { // one for new shells + one for redis
		t.Errorf("containerRuntime.ContainerFromCGroupCalls() called %d times, want 2", len(cr.ContainerFromCGroupCalls))
	}

	// One for each new process (2 redis, 1 PG session, 1 shells, 3 for grand-parent, parent, child) + for each parent of
	// them: init, bash, PG
	if len(psutil.CGroupCalls) != 10 {
		t.Log(psutil.CGroupCalls)
		t.Errorf("ps.CGroupFromPID() called %d times, want 10", len(psutil.CGroupCalls))
	}

	// With NO change, re-update processes
	psutil.CGroupCalls = nil
	cr.ProcessesCallCount = 0
	cr.ContainerFromCGroupCalls = nil
	cr.ContainerFromPIDCalls = nil

	err = pp.updateProcesses(t.Context(), t2.Add(time.Hour), defaultLowProcessThreshold)
	if err != nil {
		t.Error(err)
	}

	if len(pp.processes) != len(cases) {
		t.Errorf("len(pp.processes) == %v, want %v", len(pp.processes), len(cases))
	}

	for _, c := range cases {
		got := pp.processes[c.PID]
		if math.Abs(got.CPUPercent-c.CPUPercent) < epsilon {
			got.CPUPercent = c.CPUPercent
		}

		if !reflect.DeepEqual(got, c) {
			t.Errorf("pp.processes[%v] == %v, want %v", c.PID, got, c)
		}
	}

	if cr.ProcessesCallCount != 0 {
		t.Errorf("containerRuntime.Processes() called %d times, want 0", cr.ProcessesCallCount)
	}

	if len(cr.ContainerFromPIDCalls) != 0 {
		t.Errorf("containerRuntime.ContainerFromPID() called %d times, want 0", len(cr.ContainerFromPIDCalls))
	}

	if len(cr.ContainerFromCGroupCalls) != 0 {
		t.Errorf("containerRuntime.ContainerFromCGroupCalls() called %d times, want 0", len(cr.ContainerFromCGroupCalls))
	}

	if len(psutil.CGroupCalls) != 10 {
		t.Errorf("ps.CGroupFromPID() called %d times, want 0", len(psutil.CGroupCalls))
	}
}

func TestDeltaCPUPercent(t *testing.T) {
	// In this test:
	// * t0 is the date of create of most process
	// * t1 is the date used to fill pp.processes and pp.lastProcessesUpdate as if updateProcesses was called on t1
	// * t2 is the date one new process is created
	// * t3 (now) is the date when updateProcesses is called (and use value from mockDockerProcess)
	// * we will check that CPU percent is updated based on t2 - t1 interval.
	now := time.Now()
	t0 := now.Add(-time.Hour)
	t1 := now.Add(-time.Minute)
	t2 := now.Add(-20 * time.Second)
	pp := ProcessProvider{
		lastProcessesUpdate: t1,
		processes: map[int]Process{
			1: {
				PID:        1,
				Name:       "init",
				CreateTime: t0,
				CPUTime:    0.012,
			},
			2: {
				PID:        2,
				Name:       "redis",
				CreateTime: t0,
				CPUTime:    0.0,
			},
			3: {
				PID:        3,
				Name:       "busy",
				CreateTime: t0,
				CPUTime:    13.37,
			},
			4: {
				PID:        4,
				Name:       "disappear",
				CreateTime: t0,
				CPUTime:    75.6,
			},
		},
		ps: &mockProcessQuerier{
			processesResult: []Process{
				{
					PID:        1,
					Name:       "init",
					CreateTime: t0,
					CPUTime:    0.012, // unchanged => 0%
				},
				{
					PID:        2,
					Name:       "redis",
					CreateTime: t0,
					CPUTime:    42.0, // +42 => 70%
				},
				{
					PID:        3,
					Name:       "busy",
					CreateTime: t0,
					CPUTime:    92.37, // +79 => 131.6%
				},
				{
					PID:        4,
					Name:       "new-with-name-pid",
					CreateTime: t2,
					CPUTime:    78.6, // new process. +78.6 in 20s => 392.9%
				},
			},
		},
	}

	err := pp.updateProcesses(t.Context(), now, defaultLowProcessThreshold)
	if err != nil {
		t.Error(err)
	}

	cases := []Process{
		{
			PID:        1,
			Name:       "init",
			CreateTime: t0,
			CPUTime:    0.012, // unchanged => 0%
			CPUPercent: 0.0,
		},
		{
			PID:        2,
			Name:       "redis",
			CreateTime: t0,
			CPUTime:    42.0, // +42s over 1min => 70%
			CPUPercent: 70.0,
		},
		{
			PID:        3,
			Name:       "busy",
			CreateTime: t0,
			CPUTime:    92.37, // +79 over 1min => 131.666%
			CPUPercent: 131.666,
		},
		{
			PID:        4,
			Name:       "new-with-name-pid",
			CreateTime: t2,
			CPUTime:    78.6, // new process. +78.6 in 20s => 393%
			CPUPercent: 393,
		},
	}
	if len(pp.processes) != len(cases) {
		t.Errorf("len(pp.processe) == %v, want %v", len(pp.processes), len(cases))
	}

	if pp.lastProcessesUpdate.After(time.Now()) || time.Since(pp.lastProcessesUpdate) > time.Second {
		t.Errorf("pp.lastProcessesUpdate = %v, want %v", pp.lastProcessesUpdate, now)
	}

	epsilon := 0.01

	for _, c := range cases {
		got := pp.processes[c.PID]
		if math.Abs(got.CPUPercent-c.CPUPercent) < epsilon {
			got.CPUPercent = c.CPUPercent
		}

		if !reflect.DeepEqual(got, c) {
			t.Errorf("pp.processes[%v] == %v, want %v", c.PID, got, c)
		}
	}
}

// TestUpdateProcessesTime tests that update_processes behave as expected in the times.
// For example it ensure that container is correctly associated in case of error with container runtime.
func TestUpdateProcessesTime(t *testing.T) { //nolint: maintidx
	type addRemoveProcess struct {
		proc      Process
		cgroup    string
		container Container
		// seemsCGroup is true when we only add to the cgroup2seemsContainer of container runtime.
		// This will happen when the container runtime recognizes the cgroup value but didn't find the containers.
		seemsCGroup   bool
		skipPSUtil    bool
		skipCRProcess bool
		skipCRCGroup  bool
	}

	type step struct {
		timeAddedFromT0          time.Duration
		addProcesses             []addRemoveProcess
		removeProcesses          []addRemoveProcess
		wantProcesseses          []Process
		fromCGroupErr            error
		fromPIDErr               error
		otherErr                 error
		checkCallsCount          bool
		ContainerFromPIDCalls    int
		ContainerFromCGroupCalls int
		ProcessesCallCount       int
		lowProcessesThreshold    int
	}

	t0 := time.Date(2022, 2, 2, 3, 4, 5, 6, time.UTC)
	now := time.Now()

	tests := []struct {
		name  string
		t0    time.Time
		steps []step
	}{
		{
			name: "no container",
			t0:   now,
			steps: []step{
				{
					timeAddedFromT0: 5 * time.Second,
					addProcesses: []addRemoveProcess{
						{
							proc: Process{
								PID:        1,
								Name:       "init",
								CreateTime: now,
							},
							cgroup: "not in docker",
						},
						{
							proc: Process{
								PID:        2,
								Name:       "kthread",
								CreateTime: t0,
							},
							cgroup: "not in docker",
						},
					},
					wantProcesseses: []Process{
						{
							PID:        1,
							Name:       "init",
							CreateTime: now,
						},
						{
							PID:        2,
							Name:       "kthread",
							CreateTime: t0,
						},
					},
				},
				{
					timeAddedFromT0: 11 * time.Second,
					addProcesses: []addRemoveProcess{
						{
							proc: Process{
								PID:        3,
								Name:       "bash",
								CreateTime: now,
							},
						},
					},
					removeProcesses: []addRemoveProcess{
						{
							proc: Process{
								PID:        2,
								Name:       "kthread",
								CreateTime: t0,
							},
							cgroup: "not in docker",
						},
					},
					wantProcesseses: []Process{
						{
							PID:        1,
							Name:       "init",
							CreateTime: now,
						},
						{
							PID:        3,
							Name:       "bash",
							CreateTime: now,
						},
					},
				},
			},
		},
		{
			name: "container",
			t0:   t0,
			steps: []step{
				{
					timeAddedFromT0: 5 * time.Second,
					addProcesses: []addRemoveProcess{
						{
							proc: Process{
								PID:        1,
								Name:       "init",
								CreateTime: t0,
							},
							cgroup: "not in docker",
						},
						{
							proc: Process{
								PID:        2,
								Name:       "redis",
								CreateTime: t0,
							},
							container: FakeContainer{
								FakeID:            "1",
								FakeContainerName: "redis-name",
							},
							cgroup: "containerID/1",
						},
					},
					wantProcesseses: []Process{
						{
							PID:        1,
							Name:       "init",
							CreateTime: t0,
						},
						{
							PID:           2,
							Name:          "redis",
							CreateTime:    t0,
							ContainerID:   "1",
							ContainerName: "redis-name",
						},
					},
				},
			},
		},
		{
			name: "container-no-bycgroup",
			t0:   t0,
			steps: []step{
				{
					timeAddedFromT0: 5 * time.Second,
					addProcesses: []addRemoveProcess{
						{
							proc: Process{
								PID:        1,
								Name:       "init",
								CreateTime: t0,
							},
							cgroup: "not in docker",
						},
						{
							proc: Process{
								PID:        2,
								Name:       "redis",
								CreateTime: t0,
							},
							container: FakeContainer{
								FakeID:            "1",
								FakeContainerName: "redis-name",
							},
							cgroup:       "containerID/1",
							skipCRCGroup: true,
						},
						{
							proc: Process{
								PID:        3,
								Name:       "memcached",
								CreateTime: t0,
							},
							container: FakeContainer{
								FakeID:            "2",
								FakeContainerName: "memcached-name",
							},
							cgroup:       "",
							skipCRCGroup: true,
						},
					},
					wantProcesseses: []Process{
						{
							PID:        1,
							Name:       "init",
							CreateTime: t0,
						},
						{
							PID:           2,
							Name:          "redis",
							CreateTime:    t0,
							ContainerID:   "1",
							ContainerName: "redis-name",
						},
						{
							PID:           3,
							Name:          "memcached",
							CreateTime:    t0,
							ContainerID:   "2",
							ContainerName: "memcached-name",
						},
					},
				},
			},
		},
		{
			name: "container-no-bypid",
			t0:   t0,
			steps: []step{
				{
					timeAddedFromT0: 5 * time.Second,
					addProcesses: []addRemoveProcess{
						{
							proc: Process{
								PID:        1,
								Name:       "init",
								CreateTime: t0,
							},
							cgroup: "not in docker",
						},
						{
							proc: Process{
								PID:        2,
								Name:       "redis",
								CreateTime: t0,
							},
							container: FakeContainer{
								FakeID:            "1",
								FakeContainerName: "redis-name",
							},
							cgroup:        "containerID/1",
							skipCRProcess: true,
						},
					},
					wantProcesseses: []Process{
						{
							PID:        1,
							Name:       "init",
							CreateTime: t0,
						},
						{
							PID:           2,
							Name:          "redis",
							CreateTime:    t0,
							ContainerID:   "1",
							ContainerName: "redis-name",
						},
					},
				},
			},
		},
		{
			name: "container-missing-info",
			t0:   t0,
			steps: []step{
				{
					timeAddedFromT0: 5 * time.Second,
					addProcesses: []addRemoveProcess{
						{
							proc: Process{
								PID:        1,
								Name:       "init",
								CreateTime: t0,
							},
							cgroup: "not in docker",
						},
						{
							proc: Process{
								PID:        2,
								Name:       "redis",
								CreateTime: t0,
							},
							container: FakeContainer{
								FakeID:            "1",
								FakeContainerName: "redis-name",
							},
							cgroup:        "containerID/1",
							skipCRProcess: true,
							skipCRCGroup:  true,
							seemsCGroup:   true,
						},
					},
					wantProcesseses: []Process{
						{
							PID:        1,
							Name:       "init",
							CreateTime: t0,
						},
						// Redis isn't present: the FromCGroup detect that it's a container but don't find which one
					},
				},
				{
					timeAddedFromT0: 4 * time.Minute,
					wantProcesseses: []Process{
						{
							PID:        1,
							Name:       "init",
							CreateTime: t0,
						},
						// Redis is still not present after 4 minutes.
					},
				},
				{
					timeAddedFromT0: 5*time.Minute + time.Second,
					wantProcesseses: []Process{
						{
							PID:        1,
							Name:       "init",
							CreateTime: t0,
						},
						// after 5 minutes, we assume that FromCGroup is wrong and it don't belong to a container.
						{
							PID:           2,
							Name:          "redis",
							CreateTime:    t0,
							ContainerID:   "",
							ContainerName: "",
						},
					},
				},
			},
		},
		{
			name: "container-context-deadline",
			t0:   t0,
			steps: []step{
				{
					timeAddedFromT0: 5 * time.Second,
					addProcesses: []addRemoveProcess{
						{
							proc: Process{
								PID:        1,
								Name:       "init",
								CreateTime: t0,
							},
							cgroup: "not in docker",
						},
						{
							proc: Process{
								PID:        2,
								Name:       "redis",
								CreateTime: t0,
							},
							container: FakeContainer{
								FakeID:            "1",
								FakeContainerName: "redis-name",
							},
							cgroup: "containerID/1",
						},
						{
							proc: Process{
								PID:        3,
								Name:       "unknown container",
								CreateTime: t0,
							},
							cgroup:      "containerID/2",
							seemsCGroup: true,
						},
					},
					fromCGroupErr:   context.DeadlineExceeded,
					fromPIDErr:      context.DeadlineExceeded,
					otherErr:        context.DeadlineExceeded,
					wantProcesseses: []Process{
						// No process are present because container runtime had error
					},
				},
				{
					timeAddedFromT0: 21 * time.Second,
					fromCGroupErr:   context.DeadlineExceeded,
					fromPIDErr:      context.DeadlineExceeded,
					otherErr:        context.DeadlineExceeded,
					wantProcesseses: []Process{
						// After just 20 seconds, init is present because the cgroup data don't match any container
						{
							PID:        1,
							Name:       "init",
							CreateTime: t0,
						},
						// Other are still absent, because based on cgroup we think they belong to a container
					},
				},
				{
					timeAddedFromT0: 5*time.Minute + time.Second,
					fromCGroupErr:   context.DeadlineExceeded,
					fromPIDErr:      context.DeadlineExceeded,
					otherErr:        context.DeadlineExceeded,
					wantProcesseses: []Process{
						{
							PID:        1,
							Name:       "init",
							CreateTime: t0,
						},
						// after 5 minutes, we assume that the context deadline is in fact that Docker isn't running at all
						{
							PID:           2,
							Name:          "redis",
							CreateTime:    t0,
							ContainerID:   "",
							ContainerName: "",
						},
						{
							PID:           3,
							Name:          "unknown container",
							CreateTime:    t0,
							ContainerID:   "",
							ContainerName: "",
						},
					},
				},
			},
		},
		{
			name: "container-other-error",
			t0:   t0,
			steps: []step{
				{
					timeAddedFromT0: 5 * time.Second,
					addProcesses: []addRemoveProcess{
						{
							proc: Process{
								PID:        1,
								Name:       "init",
								CreateTime: t0,
							},
							cgroup: "not in docker",
						},
						{
							proc: Process{
								PID:        2,
								Name:       "redis",
								CreateTime: t0,
							},
							container: FakeContainer{
								FakeID:            "1",
								FakeContainerName: "redis-name",
							},
							cgroup: "containerID/1",
						},
						{
							proc: Process{
								PID:        3,
								Name:       "unknown container",
								CreateTime: t0,
							},
							cgroup:      "containerID/2",
							seemsCGroup: true,
						},
					},
					fromCGroupErr:   errArbitraryErrorForTest,
					fromPIDErr:      errArbitraryErrorForTest,
					otherErr:        errArbitraryErrorForTest,
					wantProcesseses: []Process{
						// No process are present because container runtime had error
					},
				},
				{
					timeAddedFromT0: 21 * time.Second,
					fromCGroupErr:   errArbitraryErrorForTest,
					fromPIDErr:      errArbitraryErrorForTest,
					otherErr:        errArbitraryErrorForTest,
					wantProcesseses: []Process{
						// After just 20 seconds, init is present because the cgroup data don't match any container
						{
							PID:        1,
							Name:       "init",
							CreateTime: t0,
						},
						// Other are still absent, because based on cgroup we think they belong to a container
					},
				},
				{
					timeAddedFromT0: 1*time.Minute + time.Second,
					fromCGroupErr:   errArbitraryErrorForTest,
					fromPIDErr:      errArbitraryErrorForTest,
					otherErr:        errArbitraryErrorForTest,
					wantProcesseses: []Process{
						{
							PID:        1,
							Name:       "init",
							CreateTime: t0,
						},
						// after 1 minutes, we assume that this unknown error means that Docker isn't installed.
						{
							PID:           2,
							Name:          "redis",
							CreateTime:    t0,
							ContainerID:   "",
							ContainerName: "",
						},
						{
							PID:           3,
							Name:          "unknown container",
							CreateTime:    t0,
							ContainerID:   "",
							ContainerName: "",
						},
					},
				},
			},
		},
		{
			name: "no-container-runtime",
			t0:   t0,
			steps: []step{
				{
					timeAddedFromT0: 5 * time.Second,
					addProcesses: []addRemoveProcess{
						{
							proc: Process{
								PID:        1,
								Name:       "init",
								CreateTime: t0,
							},
							cgroup: "not in docker",
						},
						{
							proc: Process{
								PID:        2,
								PPID:       1,
								Name:       "bash",
								CreateTime: t0.Add(time.Millisecond),
							},
							cgroup: "not in docker",
						},
						{
							proc: Process{
								PID:        3,
								Name:       "unknown container",
								CreateTime: t0,
							},
							cgroup:      "containerID/2",
							seemsCGroup: true,
						},
					},
					// With fromCGroup we use facts.ErrContainerDoesNotExists, because a container runtime should return
					// the ErrContainerDoesNotExists when the cgroup data matches a container and NOT NoRuntimeError.
					// The mock will behave as such: for init it returns nil because cgroup matches nothing and error is only returned
					// when cgroup data matches something.
					fromCGroupErr:   ErrContainerDoesNotExists,
					fromPIDErr:      NewNoRuntimeError(errArbitraryErrorForTest),
					otherErr:        NewNoRuntimeError(errArbitraryErrorForTest),
					wantProcesseses: []Process{
						// No process are present because container runtime had an error.
					},
					checkCallsCount:          true,
					ContainerFromCGroupCalls: 3,
					ContainerFromPIDCalls:    2,
				},
				{
					timeAddedFromT0: 11 * time.Second,
					fromCGroupErr:   ErrContainerDoesNotExists,
					fromPIDErr:      NewNoRuntimeError(errArbitraryErrorForTest),
					otherErr:        NewNoRuntimeError(errArbitraryErrorForTest),
					wantProcesseses: []Process{
						// After just 10 seconds, init is present because the cgroup data don't match any container
						// and runtime is not running.
						{
							PID:        1,
							Name:       "init",
							CreateTime: t0,
						},
						{
							PID:        2,
							PPID:       1,
							Name:       "bash",
							CreateTime: t0.Add(time.Millisecond),
						},
						// Other are still absent, because based on cgroup we think they belong to a container
					},
					checkCallsCount:          true,
					ContainerFromCGroupCalls: 2,
					ContainerFromPIDCalls:    1,
				},
				{
					timeAddedFromT0: 4*time.Minute + time.Second,
					fromCGroupErr:   ErrContainerDoesNotExists,
					fromPIDErr:      NewNoRuntimeError(errArbitraryErrorForTest),
					otherErr:        NewNoRuntimeError(errArbitraryErrorForTest),
					wantProcesseses: []Process{
						{
							PID:        1,
							Name:       "init",
							CreateTime: t0,
						},
						{
							PID:        2,
							PPID:       1,
							Name:       "bash",
							CreateTime: t0.Add(time.Millisecond),
						},
						// Other are still absent, because we believe they belong to a container.
					},
					checkCallsCount:          true,
					ContainerFromCGroupCalls: 1,
					ContainerFromPIDCalls:    0,
				},
				{
					timeAddedFromT0: 5*time.Minute + time.Second,
					fromCGroupErr:   ErrContainerDoesNotExists,
					fromPIDErr:      NewNoRuntimeError(errArbitraryErrorForTest),
					otherErr:        NewNoRuntimeError(errArbitraryErrorForTest),
					wantProcesseses: []Process{
						{
							PID:        1,
							Name:       "init",
							CreateTime: t0,
						},
						{
							PID:        2,
							PPID:       1,
							Name:       "bash",
							CreateTime: t0.Add(time.Millisecond),
						},
						{
							PID:           3,
							Name:          "unknown container",
							CreateTime:    t0,
							ContainerID:   "",
							ContainerName: "",
						},
					},
					checkCallsCount:          true,
					ContainerFromCGroupCalls: 1,
					ContainerFromPIDCalls:    1,
				},
				{
					timeAddedFromT0: 6 * time.Minute,
					fromCGroupErr:   ErrContainerDoesNotExists,
					fromPIDErr:      NewNoRuntimeError(errArbitraryErrorForTest),
					otherErr:        NewNoRuntimeError(errArbitraryErrorForTest),
					wantProcesseses: []Process{
						{
							PID:        1,
							Name:       "init",
							CreateTime: t0,
						},
						{
							PID:        2,
							PPID:       1,
							Name:       "bash",
							CreateTime: t0.Add(time.Millisecond),
						},
						// Other are still absent, because we believe they belong to a container.
						{
							PID:           3,
							Name:          "unknown container",
							CreateTime:    t0,
							ContainerID:   "",
							ContainerName: "",
						},
					},
					checkCallsCount:          true,
					ContainerFromCGroupCalls: 1,
					ContainerFromPIDCalls:    1,
				},
				{
					timeAddedFromT0: 7 * time.Minute,
					addProcesses: []addRemoveProcess{
						{
							proc: Process{
								PID:        20,
								PPID:       1,
								Name:       "bash",
								CreateTime: t0.Add(7 * time.Minute),
							},
							cgroup: "not in docker",
						},
					},
					fromCGroupErr: ErrContainerDoesNotExists,
					fromPIDErr:    NewNoRuntimeError(errArbitraryErrorForTest),
					otherErr:      NewNoRuntimeError(errArbitraryErrorForTest),
					wantProcesseses: []Process{
						{
							PID:        1,
							Name:       "init",
							CreateTime: t0,
						},
						{
							PID:        2,
							PPID:       1,
							Name:       "bash",
							CreateTime: t0.Add(time.Millisecond),
						},
						{
							PID:        20,
							PPID:       1,
							Name:       "bash",
							CreateTime: t0.Add(7 * time.Minute),
						},
						// Other are still absent, because we believe they belong to a container.
						{
							PID:           3,
							Name:          "unknown container",
							CreateTime:    t0,
							ContainerID:   "",
							ContainerName: "",
						},
					},
					checkCallsCount:          true,
					ContainerFromCGroupCalls: 1,
					ContainerFromPIDCalls:    1,
				},
			},
		},
	}

	deleteProc := func(processes []Process, pid int) []Process {
		i := 0

		for _, p := range processes {
			if p.PID == pid {
				continue
			}

			processes[i] = p
			i++
		}

		return processes[:i]
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			currentTime := tt.t0
			psutil := &mockProcessQuerier{
				processCGroup: make(map[int]string),
			}
			cr := &mockContainerRuntime{
				cgroup2seemsContainer: make(map[string]bool),
				cgroup2Container:      make(map[string]Container),
			}
			pp := ProcessProvider{
				startedAt:        currentTime,
				containerRuntime: cr,
				ps:               psutil,
			}

			for stepIdx, step := range tt.steps {
				cr.fromCGroupErr = step.fromCGroupErr
				cr.fromPIDErr = step.fromPIDErr
				cr.otherErr = step.otherErr

				for _, proc := range step.removeProcesses {
					if !proc.skipPSUtil {
						psutil.processesResult = deleteProc(psutil.processesResult, proc.proc.PID)
					}

					if !proc.skipCRProcess {
						cr.processesResult = deleteProc(cr.processesResult, proc.proc.PID)
					}

					cgroup := proc.cgroup
					if cgroup == "" {
						cgroup = psutil.processCGroup[proc.proc.PID]
					}

					if cgroup != "" {
						delete(psutil.processCGroup, proc.proc.PID)

						if !proc.skipCRProcess {
							delete(cr.cgroup2Container, cgroup)
							delete(cr.cgroup2seemsContainer, cgroup)
						}
					}
				}

				for _, proc := range step.addProcesses {
					if !proc.skipPSUtil {
						psutil.processesResult = append(psutil.processesResult, proc.proc)
					}

					if !proc.skipCRProcess && proc.container != nil {
						crProc := proc.proc
						crProc.ContainerID = proc.container.ID()
						crProc.ContainerName = proc.container.ContainerName()

						cr.processesResult = append(cr.processesResult, crProc)
					}

					if proc.cgroup != "" {
						psutil.processCGroup[proc.proc.PID] = proc.cgroup

						if proc.container != nil && !proc.skipCRCGroup {
							cr.cgroup2Container[proc.cgroup] = proc.container
						} else if proc.seemsCGroup {
							cr.cgroup2seemsContainer[proc.cgroup] = true
						}
					}
				}

				cr.makePID2Containers()

				currentTime = tt.t0.Add(step.timeAddedFromT0)

				cr.ContainerFromCGroupCalls = nil
				cr.ContainerFromPIDCalls = nil
				cr.ProcessesCallCount = 0

				err := pp.updateProcesses(t.Context(), currentTime, step.lowProcessesThreshold)
				if err != nil {
					// This error is only an issue if container runtime don't return errors
					if step.fromCGroupErr == nil && step.fromPIDErr == nil && step.otherErr == nil {
						t.Fatal(err)
					}
				}

				if step.checkCallsCount {
					if len(cr.ContainerFromCGroupCalls) != step.ContainerFromCGroupCalls {
						t.Errorf("step #%d, len(ContainerFromCGroupCalls) = %d, want %d", stepIdx, len(cr.ContainerFromCGroupCalls), step.ContainerFromCGroupCalls)
					}

					if len(cr.ContainerFromPIDCalls) != step.ContainerFromPIDCalls {
						t.Errorf("step #%d, len(ContainerFromPIDCalls) = %d, want %d", stepIdx, len(cr.ContainerFromPIDCalls), step.ContainerFromPIDCalls)
					}

					if cr.ProcessesCallCount != step.ProcessesCallCount {
						t.Errorf("step #%d, ProcessesCallCount = %d, want %d", stepIdx, cr.ProcessesCallCount, step.ProcessesCallCount)
					}
				}

				got := make([]Process, 0, len(pp.processes))
				for _, p := range pp.processes {
					got = append(got, p)
				}

				sortOpts := cmpopts.SortSlices(func(x Process, y Process) bool { return x.PID < y.PID })
				if diff := cmp.Diff(step.wantProcesseses, got, cmpopts.EquateApprox(0.001, 0), sortOpts); diff != "" {
					t.Errorf("step #%d, processes mismatch: (-want +got):\n%s", stepIdx, diff)
				}
			}
		})
	}
}

func Test_sortParentFirst(t *testing.T) {
	ts0 := time.Now().UnixMilli()

	generatedHoleProcesses1, generatedHoleWant1 := makeHole(makeProcesses(20, 42, false, true), 3, 42)
	generatedHoleProcesses2, generatedHoleWant2 := makeHole(makeProcesses(200, 43, false, false), 20, 43)

	tests := []struct {
		name         string
		processes    []Process
		want         []processOrder
		autoFillWant bool // add want entry with PIDBefore=PPID & PIDAfter=PID (unless PPID=PID). Don't add it if PPID don't exists.
	}{
		{
			name: "linear",
			processes: []Process{
				{PID: 1, PPID: 1, CreateTime: time.UnixMilli(ts0)},
				{PID: 2, PPID: 1, CreateTime: time.UnixMilli(ts0 + 1)},
				{PID: 3, PPID: 2, CreateTime: time.UnixMilli(ts0 + 2)},
			},
			want: []processOrder{
				{PIDBefore: 1, PIDAfter: 2},
				{PIDBefore: 1, PIDAfter: 3},
				{PIDBefore: 2, PIDAfter: 3},
			},
		},
		{
			name: "tree",
			processes: []Process{
				{PID: 1, PPID: 0, CreateTime: time.UnixMilli(ts0), Name: "init"},
				{PID: 2, PPID: 0, CreateTime: time.UnixMilli(ts0), Name: "kthreadd"},
				{PID: 3, PPID: 1, CreateTime: time.UnixMilli(ts0 + 2), Name: "child of init"},
				{PID: 4, PPID: 1, CreateTime: time.UnixMilli(ts0 + 3), Name: "child of init"},
				{PID: 5, PPID: 3, CreateTime: time.UnixMilli(ts0 + 4), Name: "grand-child of init"},
				{PID: 10, PPID: 2, CreateTime: time.UnixMilli(ts0 + 5), Name: "child of kthread"},
			},
			autoFillWant: true,
		},
		{
			name: "tree-same-time-rnd-pid",
			processes: []Process{
				{PID: 1661, PPID: 0, CreateTime: time.UnixMilli(ts0), Name: "init"},
				{PID: 5785, PPID: 0, CreateTime: time.UnixMilli(ts0), Name: "kthreadd"},
				{PID: 12837, PPID: 1661, CreateTime: time.UnixMilli(ts0), Name: "child of init"},
				{PID: 14195, PPID: 1661, CreateTime: time.UnixMilli(ts0), Name: "child of init"},
				{PID: 3370, PPID: 12837, CreateTime: time.UnixMilli(ts0), Name: "grand-child of init"},
				{PID: 5573, PPID: 5785, CreateTime: time.UnixMilli(ts0), Name: "child of kthread"},
			},
			autoFillWant: true,
		},
		{
			name: "partial-tree",
			processes: []Process{
				{PID: 700, PPID: 400, CreateTime: time.UnixMilli(ts0 + 4), Name: "grand-grand-child of init"},
				{PID: 1, PPID: 0, CreateTime: time.UnixMilli(ts0), Name: "init"},
				{PID: 2, PPID: 0, CreateTime: time.UnixMilli(ts0), Name: "kthreadd"},
				{PID: 500, PPID: 1, CreateTime: time.UnixMilli(ts0 + 1), Name: "child of init"},
				// {PID: 400, PPID: 500, CreateTime: time.UnixMilli(ts0+2), Name: "grand-child of init, missing in PS"},
				{PID: 300, PPID: 400, CreateTime: time.UnixMilli(ts0 + 3), Name: "grand-grand-child of init"},
				{PID: 301, PPID: 300, CreateTime: time.UnixMilli(ts0), Name: "child of PID 300 with create-time BEFORE parent"},
			},
			autoFillWant: true,
			want: []processOrder{
				{PIDBefore: 500, PIDAfter: 300},
				{PIDBefore: 500, PIDAfter: 700},
				{PIDBefore: 1, PIDAfter: 700},
			},
		},
		{
			name:         "generated-30",
			processes:    makeProcesses(300, 42, true, true),
			autoFillWant: true,
		},
		{
			name:         "generated-100",
			processes:    makeProcesses(100, 42, true, false),
			autoFillWant: true,
		},
		{
			name:         "generated-200",
			processes:    makeProcesses(200, 42, true, false),
			autoFillWant: true,
		},
		{
			name:         "generated-20-hole",
			processes:    generatedHoleProcesses1,
			autoFillWant: true,
			want:         generatedHoleWant1,
		},
		{
			name:         "generated-200-hole",
			processes:    generatedHoleProcesses2,
			autoFillWant: true,
			want:         generatedHoleWant2,
		},
		{
			name:         "ps-laptop",
			processes:    loadProcessTestData(t, "ps-laptop.csv"),
			autoFillWant: true,
			want: []processOrder{
				// The easiest to generate this entries is using (on the same system + same state that was used to create the CSV file):
				// pstree -sTp 1487053
				{PIDBefore: 1, PIDAfter: 1487053},
				{PIDBefore: 2663, PIDAfter: 1487053},
				{PIDBefore: 2747, PIDAfter: 1487053},
				{PIDBefore: 3420, PIDAfter: 1487053},
				{PIDBefore: 61316, PIDAfter: 1487053},
				{PIDBefore: 1486849, PIDAfter: 1487053},
				{PIDBefore: 1486912, PIDAfter: 1487053},
			},
		},
		{
			name:         "ps-server",
			processes:    loadProcessTestData(t, "ps-server.csv"),
			autoFillWant: true,
			want: []processOrder{
				// The easiest to generate this entries is using (on the same system + same state that was used to create the CSV file):
				// pstree -sTp 2878733
				{PIDBefore: 1, PIDAfter: 2877733},
				{PIDBefore: 2877733, PIDAfter: 2877756},
				{PIDBefore: 2877756, PIDAfter: 2878685},
				{PIDBefore: 2878685, PIDAfter: 2878687},
				{PIDBefore: 2878687, PIDAfter: 2878721},
				{PIDBefore: 2878721, PIDAfter: 2878732},
				{PIDBefore: 2878732, PIDAfter: 2878733},
				{PIDBefore: 1, PIDAfter: 2878733},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := tt.want

			if tt.autoFillWant {
				want = append(want, generateProcessOrder(tt.processes)...)
			}

			tt.processes = sortParentFirst(tt.processes)

			for _, w := range want {
				firstIdx := -1
				secondIdx := -1

				for i, p := range tt.processes {
					if p.PID == w.PIDBefore {
						if firstIdx == -1 {
							firstIdx = i
						} else {
							t.Errorf("PID %d is present at index %d and %d", p.PID, firstIdx, i)
						}
					}

					if p.PID == w.PIDAfter {
						if secondIdx == -1 {
							secondIdx = i
						} else {
							t.Errorf("PID %d is present at index %d and %d", p.PID, secondIdx, i)
						}
					}
				}

				switch {
				case firstIdx == -1:
					t.Errorf("PID %d is not present", w.PIDBefore)
				case secondIdx == -1:
					t.Errorf("PID %d is not present", w.PIDAfter)
				case firstIdx >= secondIdx:
					t.Errorf("PID %d is after PID %d (%d >= %d)", w.PIDBefore, w.PIDAfter, firstIdx, secondIdx)
				}
			}
		})
	}
}

//nolint: gosmopolitan,gofmt,gofumpt,goimports
func TestSortProcessesArbitrarily(t *testing.T) {
	const (
		gloutonPID = 7
		memTotal   = 1024
	)

	processes := []Process{
		{
			PID:           8,
			CreateTime:    time.Date(2024, time.July, 19, 10, 20, 0, 0, time.Local),
			MemoryRSS:     50,
			CPUPercent:    2,
			ContainerName: "C-1",
		},
		{
			PID:           3,
			CreateTime:    time.Date(2024, time.July, 19, 10, 21, 0, 0, time.Local),
			MemoryRSS:     20,
			CPUPercent:    1,
			ContainerName: "C-2",
		},
		{
			PID:        1,
			CreateTime: time.Date(2024, time.July, 19, 10, 20, 0, 0, time.Local),
			MemoryRSS:  20,
			CPUPercent: 5,
		},
		{
			PID:           6,
			CreateTime:    time.Date(2024, time.July, 19, 10, 22, 0, 0, time.Local),
			MemoryRSS:     100,
			CPUPercent:    15,
			ContainerName: "C-1",
		},
		{
			PID:        9,
			CreateTime: time.Date(2024, time.July, 19, 10, 21, 0, 0, time.Local),
			MemoryRSS:  20,
			CPUPercent: 50,
		},
		{
			PID:        10,
			CreateTime: time.Date(2024, time.July, 19, 10, 23, 0, 0, time.Local),
			MemoryRSS:  50,
			CPUPercent: 40,
		},
		{
			PID:           4,
			CreateTime:    time.Date(2024, time.July, 19, 10, 22, 0, 0, time.Local),
			MemoryRSS:     7,
			CPUPercent:    5,
			ContainerName: "C-1",
		},
		{
			PID:        5,
			CreateTime: time.Date(2024, time.July, 19, 10, 22, 0, 0, time.Local),
			MemoryRSS:  20,
			CPUPercent: 30,
		},
		{
			PID:        2,
			CreateTime: time.Date(2024, time.July, 19, 10, 21, 0, 0, time.Local),
			MemoryRSS:  30,
			CPUPercent: 5,
		},
		{
			PID:        7, // Glouton
			CreateTime: time.Date(2024, time.July, 19, 10, 23, 0, 0, time.Local),
			MemoryRSS:  16,
			CPUPercent: 6,
		},
	}

	expectedPIDOrder := []int{
		1,  // PID 1
		7,  // Glouton
		3,  // Oldest (and only) process of container C-2
		8,  // Oldest process of container C-1
		9,  // %CPU + %Mem ~= 52
		10, // %CPU + %Mem ~= 45
		5,  // %CPU + %Mem ~= 32
		6,  // %CPU + %Mem ~= 25
		2,  // %CPU + %Mem ~= 8
		4,  // %CPU + %Mem ~= 6
	}

	sortProcessesArbitrarily(processes, gloutonPID, memTotal)

	resultPIDOrder := make([]int, len(processes))

	for i, p := range processes {
		resultPIDOrder[i] = p.PID
	}

	if diff := cmp.Diff(expectedPIDOrder, resultPIDOrder); diff != "" {
		t.Fatalf("Unexpected process order:\nwant %v\n got %v", expectedPIDOrder, resultPIDOrder)
	}
}

type bOrT interface {
	Helper()
	Fatal(args ...any)
}

// loadProcessTestData convert CSV testdata file to list of Process.
// ps xa -o pid,ppid,lstart | tail -n +2| awk '{date=$3 " " $4 " " $5 " " $6 " " $7; "date -d \"" date "\" +%s"|getline dateS; print $1 "," $2 "," dateS}' > facts/testdata/ps-laptop.csv
// The CSV is generated with the above command (it above to allow comment to end with a period).
func loadProcessTestData(t bOrT, filename string) []Process {
	t.Helper()

	fd, err := os.Open(filepath.Join("testdata", filename))
	if err != nil {
		t.Fatal(err)
	}

	reader := csv.NewReader(fd)

	data, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}

	result := make([]Process, 0, len(data))

	for _, row := range data {
		pid, err := strconv.ParseInt(row[0], 10, 0)
		if err != nil {
			t.Fatal(err)
		}

		ppid, err := strconv.ParseInt(row[1], 10, 0)
		if err != nil {
			t.Fatal(err)
		}

		ts, err := strconv.ParseInt(row[2], 10, 0)
		if err != nil {
			t.Fatal(err)
		}

		createdAt := time.Unix(ts, 0)

		result = append(result, Process{
			PID:        int(pid),
			PPID:       int(ppid),
			CreateTime: createdAt,
		})
	}

	return result
}

func Benchmark_sortParentFirst(b *testing.B) {
	tests := []struct {
		name             string
		processesFactory func(b *testing.B) []Process
	}{
		{name: "ps-laptop", processesFactory: func(b *testing.B) []Process {
			b.Helper()

			return loadProcessTestData(b, "ps-laptop.csv")
		}},
		{name: "ps-server", processesFactory: func(b *testing.B) []Process {
			b.Helper()

			return loadProcessTestData(b, "ps-server.csv")
		}},
		{name: "count-15", processesFactory: func(b *testing.B) []Process {
			b.Helper()

			return makeProcesses(15, 43, false, true)
		}},
		{name: "count-150", processesFactory: func(b *testing.B) []Process {
			b.Helper()

			return makeProcesses(150, 44, false, true)
		}},
		{name: "count-1500", processesFactory: func(b *testing.B) []Process {
			b.Helper()

			return makeProcesses(1500, 45, false, true)
		}},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			for b.Loop() {
				b.StopTimer()

				processes := tt.processesFactory(b)

				b.StartTimer()

				_ = sortParentFirst(processes)
			}
		})
	}
}

type processOrder struct {
	PIDBefore int
	PIDAfter  int
}

func makeProcesses(count int, seed int64, sameTime bool, sorted bool) []Process {
	rnd := rand.New(rand.NewSource(seed)) //nolint: gosec
	pidList := make([]int, 0, count)
	procMap := make(map[int]Process, count)
	ts0 := time.Now().UnixMilli()

	for range count {
		var (
			pid  int
			ppid int
		)

		if !sameTime {
			ts0 += 10
		}

		for pid == 0 || procMap[pid].PID != 0 {
			pid = rnd.Intn(32768)
		}

		if len(pidList) > 0 {
			ppid = pidList[rnd.Intn(len(pidList))]
		}

		pidList = append(pidList, pid)
		procMap[pid] = Process{
			PID:             pid,
			PPID:            ppid,
			CreateTime:      time.UnixMilli(ts0),
			CreateTimestamp: ts0 / 1000,
		}
	}

	result := make([]Process, 0, len(procMap))

	for _, p := range procMap {
		result = append(result, p)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].PID < result[j].PID
	})

	if !sorted {
		rnd.Shuffle(len(result), func(i, j int) {
			result[i], result[j] = result[j], result[i]
		})
	}

	if len(result) != count {
		panic("result size mismatch")
	}

	return result
}

func generateProcessOrder(processes []Process) []processOrder {
	var result []processOrder

	existingsPids := make(map[int]bool, len(processes))

	for _, p := range processes {
		existingsPids[p.PID] = true
	}

	for _, p := range processes {
		if p.PID != p.PPID && existingsPids[p.PPID] {
			result = append(result, processOrder{PIDBefore: p.PPID, PIDAfter: p.PID})
		}
	}

	return result
}

func makeHole(processes []Process, removeCount int, seed int64) ([]Process, []processOrder) {
	rnd := rand.New(rand.NewSource(seed)) //nolint: gosec
	ppidOf := make(map[int]int, removeCount)
	indices := rnd.Perm(len(processes))[:removeCount]
	indicesMap := make(map[int]bool, len(indices))
	pidDropped := make(map[int]bool, len(indices))
	order := make([]processOrder, 0, removeCount)

	for _, i := range indices {
		p := processes[i]

		indicesMap[i] = true
		ppidOf[p.PID] = p.PPID
		pidDropped[p.PID] = true
	}

	i := 0

	for oldI, p := range processes {
		if indicesMap[oldI] {
			continue
		}

		ancetrorPid := p.PPID
		for pidDropped[ancetrorPid] {
			tmp, ok := ppidOf[ancetrorPid]
			if !ok {
				ancetrorPid = 0

				break
			}

			ancetrorPid = tmp
		}

		if ancetrorPid != 0 {
			order = append(order, processOrder{PIDBefore: ancetrorPid, PIDAfter: p.PID})
		}

		processes[i] = p
		i++
	}

	return processes[:i], order
}
