// Copyright 2015-2019 Bleemeo
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
	"math"
	"reflect"
	"testing"
	"time"
)

func TestPsStat2Status(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
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

func (m *mockProcessQuerier) Processes(ctx context.Context, maxAge time.Duration) (processes []Process, err error) {
	return m.processesResult, nil
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

func (m *mockContainerRuntime) Processes(ctx context.Context) ([]Process, error) {
	m.ProcessesCallCount++

	return m.processesResult, nil
}

func (m *mockContainerRuntime) ContainerFromCGroup(ctx context.Context, cgroupData string) (Container, error) {
	m.ContainerFromCGroupCalls = append(m.ContainerFromCGroupCalls, cgroupData)

	c := m.cgroup2Container[cgroupData]
	if c != nil {
		return c, nil
	}

	if m.cgroup2seemsContainer[cgroupData] {
		return nil, ErrContainerDoesNotExists
	}

	return nil, nil
}

func (m *mockContainerRuntime) ContainerFromPID(ctx context.Context, parentContainerID string, pid int) (Container, error) {
	m.ContainerFromPIDCalls = append(m.ContainerFromPIDCalls, pid)

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

	err := pp.updateProcesses(context.Background(), now, 0)
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

// TestUpdateProcessesShortLived check that short lived process are correctly handled.
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

	err := pp.updateProcesses(context.Background(), now, 0)
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

	if wantPID := []int{1, 101, 102}; !reflect.DeepEqual(cr.ContainerFromPIDCalls, wantPID) {
		t.Errorf("ContainerFromPIDCalls = %v, want %v", cr.ContainerFromPIDCalls, wantPID)
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
func TestUpdateProcessesOptimization(t *testing.T) { //nolint:cyclop
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
			42: "container-slice-postgresql",
			49: "container-slice-postgresql",
		},
	}
	cr := &mockContainerRuntime{
		cgroup2Container: map[string]Container{
			"container-slice-postgresql": FakeContainer{
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

	err := pp.updateProcesses(context.Background(), now, 0)
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

	err = pp.updateProcesses(context.Background(), t1.Add(time.Minute), 0)
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

	if len(psutil.CGroupCalls) != 5 {
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

	err = pp.updateProcesses(context.Background(), t2.Add(time.Minute), 0)
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

	err = pp.updateProcesses(context.Background(), t2.Add(time.Hour), 0)
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

	if len(psutil.CGroupCalls) != 0 {
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
				Name:       "disapear",
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

	err := pp.updateProcesses(context.Background(), now, 0)
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
