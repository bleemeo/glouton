// Copyright 2015-2026 Bleemeo
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

package containerd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/facts/container-runtime/internal/testutil"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/archivewriter"

	v1 "github.com/containerd/cgroups/v3/cgroup1/stats"
	v2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	pbEvents "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/api/services/tasks/v1"
	containerdTypes "github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/protobuf"
	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/events"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/opencontainers/runtime-spec/specs-go"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	testDirMinikube    = "testdata/minikube-1.20.0"
	testNameMinikube   = "minikube-1.20.0"
	testContainerdJSON = "containerd.json"
	testImageRabbitmq  = "docker.io/library/rabbitmq:latest"
	testGloutonIgnore  = "gloutonIgnore"

	testThrottleContainerID = "container-id"
)

func mustMarshalAny(v any) *anypb.Any {
	r, err := protobuf.MarshalAnyToProto(v)
	if err != nil {
		panic(err)
	}

	return r
}

func TestContainerd_RuntimeFact(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		want map[string]string
	}{
		{
			name: testNameMinikube,
			dir:  testDirMinikube,
			want: map[string]string{
				"containerd_version": "1.4.3",
				"container_runtime":  runtimeName,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl, err := NewMockFromFile(filepath.Join(tt.dir, testContainerdJSON))
			if err != nil {
				t.Error(err)

				return
			}

			c := FakeContainerd(cl, facts.ContainerFilter{}.ContainerIgnored)

			if got := c.RuntimeFact(t.Context(), nil); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Containerd.RuntimeFact() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContainerd_Containers(t *testing.T) {
	tests := []struct {
		name   string
		dir    string
		now    time.Time
		want   []facts.FakeContainer
		filter facts.ContainerFilter
	}{
		{
			name: testNameMinikube,
			dir:  testDirMinikube,
			want: []facts.FakeContainer{
				{
					FakeID:            "default/notRunning",
					FakeContainerName: "notRunning",
					FakeState:         facts.ContainerStopped,
					FakeCommand:       []string{"true"},
					FakeImageName:     testImageRabbitmq,
					FakeFinishedAt:    time.Date(2021, 1, 7, 19, 8, 54, 676875154, time.UTC),
				},
				{
					FakeID:            "default/rabbitLabels",
					FakeContainerName: "rabbitLabels",
					FakeState:         facts.ContainerRunning,
					FakeImageName:     testImageRabbitmq,
					FakeLabels: map[string]string{
						"glouton.check.ignore.port.5672":         "false",
						"glouton.check.ignore.port.4369":         "TrUe",
						"io.containerd.image.config.stop-signal": "SIGTERM",
					},
					FakePrimaryAddress: "", // yes... containerd don't give IP address
				},
				{
					FakeID:            "default/rabbitmqInternal",
					FakeContainerName: "rabbitmqInternal",
					FakeState:         facts.ContainerRunning,
					FakeImageName:     testImageRabbitmq,
				},
				{
					FakeID:            "default/gloutonIgnore",
					FakeContainerName: testGloutonIgnore,
					FakeState:         facts.ContainerRunning,
					FakeLabels: map[string]string{
						"glouton.enable":                         "off",
						"io.containerd.image.config.stop-signal": "SIGTERM",
					},
					FakeImageName: testImageRabbitmq,
					FakeImageID:   "sha256:1fc999fbdd8054b0c34c285901474f136792ad4a42d39186f3793ac40de04dba",
					TestIgnored:   true,
				},
				{
					FakeID:            "example/redis-server",
					FakeContainerName: "redis-server",
					FakeState:         facts.ContainerRunning,
					FakeImageName:     "docker.io/library/redis:alpine",
					FakeEnvironment: map[string]string{
						"PATH":               "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
						"REDIS_VERSION":      "6.0.9",
						"REDIS_DOWNLOAD_URL": "http://download.redis.io/releases/redis-6.0.9.tar.gz",
						"REDIS_DOWNLOAD_SHA": "dc2bdcf81c620e9f09cfd12e85d3bc631c897b2db7a55218fd8a65eaa37f86dd",
					},
					FakeCreatedAt: time.Date(2021, 1, 7, 19, 9, 23, 873812520, time.UTC),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl, err := NewMockFromFile(filepath.Join(tt.dir, testContainerdJSON))
			if err != nil {
				t.Error(err)

				return
			}

			c := FakeContainerd(cl, facts.ContainerFilter{}.ContainerIgnored)

			containers, err := c.Containers(t.Context(), 0, true)
			if err != nil {
				t.Error(err)
			}

			containersWithoutExclude, err := c.Containers(t.Context(), 0, false)
			if err != nil {
				t.Error(err)
			}

			gotMap, err := testutil.ContainersToMap(containers)
			if err != nil {
				t.Error(err)
			}

			gotWithoutExcludeMap, err := testutil.ContainersToMap(containersWithoutExclude)
			if err != nil {
				t.Error(err)
			}

			for _, want := range tt.want {
				got := gotMap[want.ID()]
				if got == nil {
					t.Errorf("Containers() don't have container %v", want.ID())

					continue
				}

				if diff := want.Diff(t.Context(), got); diff != "" {
					t.Errorf("Containers()[%v]: %s", want.ID(), diff)
				}

				got, ok := c.CachedContainer(want.ID())
				if !ok {
					t.Errorf("CachedContainer() don't have container %v", want.ID())
				} else if diff := want.Diff(t.Context(), got); diff != "" {
					t.Errorf("CachedContainer(%s): %s", want.ID(), diff)
				}

				if want.TestIgnored {
					_, ok := gotWithoutExcludeMap[want.FakeID]
					if ok {
						t.Errorf("container %s is listed by Containers()", want.FakeID)
					}

					if !tt.filter.ContainerIgnored(got) {
						t.Errorf("ContainerIgnored(%s) = false, want true", want.FakeID)
					}
				} else {
					_, ok := gotWithoutExcludeMap[want.FakeID]
					if !ok {
						t.Errorf("container %s is not listed by Containers()", want.FakeID)
					}
				}
			}

			if !c.IsRuntimeRunning(t.Context()) {
				t.Errorf("IsRuntimeRunning = false, want true")
			}
		})
	}
}

// TestContainerd_Run do not test much, but at least it execute code to ensure it don't crash.
func TestContainerd_Run(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		now  time.Time
		want []facts.FakeContainer
	}{
		{
			name: testNameMinikube,
			dir:  testDirMinikube,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			cl, err := NewMockFromFile(filepath.Join(tt.dir, testContainerdJSON))
			if err != nil {
				t.Error(err)

				return
			}

			t0 := time.Now()

			cl.EventChanMaker = func() <-chan *events.Envelope {
				ch := make(chan *events.Envelope, 10)

				c0 := cl.Data.Namespaces[0].MockContainers[0]

				ch <- &events.Envelope{
					Timestamp: t0,
					Namespace: cl.Data.Namespaces[0].MockNamespace,
					Topic:     "/snapshot/prepare",
					Event: mustMarshalAny(&pbEvents.SnapshotPrepare{
						Key:    c0.MockInfo.ID,
						Parent: "sha256:ba0dae6243cc9fa2890df40a625721fdbea5c94ca6da897acdd814d710149770",
					}),
				}

				ch <- &events.Envelope{
					Timestamp: t0,
					Namespace: cl.Data.Namespaces[0].MockNamespace,
					Topic:     "/containers/create",
					Event: mustMarshalAny(&pbEvents.ContainerCreate{
						ID:    c0.MockInfo.ID,
						Image: c0.MockInfo.Image,
						Runtime: &pbEvents.ContainerCreate_Runtime{
							Name:    c0.MockInfo.Runtime.Name,
							Options: protobuf.FromAny(c0.MockInfo.Runtime.Options),
						},
					}),
				}

				ch <- &events.Envelope{
					Timestamp: t0,
					Namespace: cl.Data.Namespaces[0].MockNamespace,
					Topic:     "/tasks/create",
					Event: mustMarshalAny(&pbEvents.TaskCreate{
						ContainerID: c0.MockInfo.ID,
						Bundle:      "/run/containerd/io.cont...",
						Pid:         c0.MockTask.MockPID,
					}),
				}

				ch <- &events.Envelope{
					Timestamp: t0,
					Namespace: cl.Data.Namespaces[0].MockNamespace,
					Topic:     "/tasks/start",
					Event: mustMarshalAny(&pbEvents.TaskStart{
						ContainerID: c0.MockInfo.ID,
						Pid:         c0.MockTask.MockPID,
					}),
				}

				// on tasks exit, containerd will check is the container is really exited.
				// Update status of the container.
				cl.Data.Namespaces[0].MockContainers[0].MockTask.MockStatus = client.Status{Status: "stopped", ExitStatus: 0, ExitTime: t0}

				ch <- &events.Envelope{
					Timestamp: t0,
					Namespace: cl.Data.Namespaces[0].MockNamespace,
					Topic:     "/tasks/exit",
					Event: mustMarshalAny(&pbEvents.TaskExit{
						ContainerID: c0.MockInfo.ID,
						ID:          c0.MockTask.MockID,
						Pid:         c0.MockTask.MockPID,
						ExitStatus:  137,
						ExitedAt:    timestamppb.New(t0),
					}),
				}

				ch <- &events.Envelope{
					Timestamp: t0,
					Namespace: cl.Data.Namespaces[0].MockNamespace,
					Topic:     "/containers/delete",
					Event: mustMarshalAny(&pbEvents.ContainerDelete{
						ID: c0.MockInfo.ID,
					}),
				}

				return ch
			}

			eventSeen := 0

			c := FakeContainerd(cl, facts.ContainerFilter{}.ContainerIgnored)

			var (
				wg     sync.WaitGroup
				runErr error
			)

			wg.Go(func() {
				runErr = c.Run(ctx)
			})

			deadline := time.After(time.Second)

		outterloop:
			for {
				select {
				case ev, ok := <-c.Events():
					if !ok {
						break outterloop
					}

					eventSeen++

					if ev.Type == facts.EventTypeDelete {
						break outterloop
					}
				case <-deadline:
					break outterloop
				}
			}

			if !c.IsRuntimeRunning(t.Context()) {
				t.Errorf("IsRuntimeRunning = false, want true")
			}

			got := c.ContainerLastKill(cl.Data.Namespaces[0].MockContainers[0].ID())

			if !got.IsZero() {
				t.Errorf(
					"ContainerLastKill = %s, want zero",
					got.Format(time.RFC3339Nano),
				)
			}

			cancel()
			wg.Wait()

			if runErr != nil {
				t.Error(runErr)
			}

			if eventSeen != 4 {
				t.Errorf("eventSeen = %d, want 4", eventSeen)
			}
		})
	}
}

func TestContainerd_ContainerFromCGroup(t *testing.T) {
	type check struct {
		name                string
		cgroupData          string
		mustErrDoesNotExist bool
		containerID         string
		containerName       string
	}

	tests := []struct {
		name  string
		dir   string
		wants []check
	}{
		{
			name: "minikube-1.20.0 (containerd in Docker)",
			dir:  testDirMinikube,
			wants: []check{
				{
					name: "init",
					cgroupData: `
						12:perf_event:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840
						11:freezer:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840
						10:memory:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840/init.scope
						9:pids:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840/init.scope
						8:hugetlb:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840
						7:net_cls,net_prio:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840
						6:cpuset:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840
						5:blkio:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840/init.scope
						4:devices:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840/init.scope
						3:rdma:/
						2:cpu,cpuacct:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840/init.scope
						1:name=systemd:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840/init.scope
						0::/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840/init.scope`,
					containerID: "",
				},
				{
					name: "rabbitmq",
					cgroupData: `
						12:perf_event:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840/default/rabbitmqInternal
						11:freezer:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840/default/rabbitmqInternal
						10:memory:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840/default/rabbitmqInternal
						9:pids:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840/default/rabbitmqInternal
						8:hugetlb:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840/default/rabbitmqInternal
						7:net_cls,net_prio:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840/default/rabbitmqInternal
						6:cpuset:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840/default/rabbitmqInternal
						5:blkio:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840/default/rabbitmqInternal
						4:devices:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840/default/rabbitmqInternal
						3:rdma:/
						2:cpu,cpuacct:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840/default/rabbitmqInternal
						1:name=systemd:/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840/default/rabbitmqInternal
						0::/docker/ba49bcb3891b5bd16324c07ca244512f3ba6093983057f704bf6a4069bf52840/system.slice/containerd.service`,
					containerID:   "default/rabbitmqInternal",
					containerName: "rabbitmqInternal",
				},
			},
		},
		{
			name: "containerd with Kubernetes in vbox",
			dir:  "../kubernetes/testdata/containerd-in-vbox-v1.19.0",
			wants: []check{
				{
					name: "init",
					cgroupData: `
						,11:perf_event:/
						10:devices:/init.scope
						9:freezer:/
						8:hugetlb:/
						7:pids:/init.scope
						6:blkio:/init.scope
						5:cpuset:/
						4:net_cls,net_prio:/
						3:memory:/init.scope
						2:cpu,cpuacct:/init.scope
						1:name=systemd:/init.scope
						0::/`,
					containerID: "",
				},
				{
					name: "rabbitmq",
					cgroupData: `
						11:perf_event:/kubepods/besteffort/pod1aafd548-b1fa-492a-a0b5-c19fe12b05b0/fd5a2a65a67f64ff30969e09037b3918bc5bc3bdd82f56758ebc440d76849d22
						10:devices:/kubepods/besteffort/pod1aafd548-b1fa-492a-a0b5-c19fe12b05b0/fd5a2a65a67f64ff30969e09037b3918bc5bc3bdd82f56758ebc440d76849d22
						9:freezer:/kubepods/besteffort/pod1aafd548-b1fa-492a-a0b5-c19fe12b05b0/fd5a2a65a67f64ff30969e09037b3918bc5bc3bdd82f56758ebc440d76849d22
						8:hugetlb:/kubepods/besteffort/pod1aafd548-b1fa-492a-a0b5-c19fe12b05b0/fd5a2a65a67f64ff30969e09037b3918bc5bc3bdd82f56758ebc440d76849d22
						7:pids:/kubepods/besteffort/pod1aafd548-b1fa-492a-a0b5-c19fe12b05b0/fd5a2a65a67f64ff30969e09037b3918bc5bc3bdd82f56758ebc440d76849d22
						6:blkio:/kubepods/besteffort/pod1aafd548-b1fa-492a-a0b5-c19fe12b05b0/fd5a2a65a67f64ff30969e09037b3918bc5bc3bdd82f56758ebc440d76849d22
						5:cpuset:/kubepods/besteffort/pod1aafd548-b1fa-492a-a0b5-c19fe12b05b0/fd5a2a65a67f64ff30969e09037b3918bc5bc3bdd82f56758ebc440d76849d22
						4:net_cls,net_prio:/kubepods/besteffort/pod1aafd548-b1fa-492a-a0b5-c19fe12b05b0/fd5a2a65a67f64ff30969e09037b3918bc5bc3bdd82f56758ebc440d76849d22
						3:memory:/kubepods/besteffort/pod1aafd548-b1fa-492a-a0b5-c19fe12b05b0/fd5a2a65a67f64ff30969e09037b3918bc5bc3bdd82f56758ebc440d76849d22
						2:cpu,cpuacct:/kubepods/besteffort/pod1aafd548-b1fa-492a-a0b5-c19fe12b05b0/fd5a2a65a67f64ff30969e09037b3918bc5bc3bdd82f56758ebc440d76849d22
						1:name=systemd:/kubepods/besteffort/pod1aafd548-b1fa-492a-a0b5-c19fe12b05b0/fd5a2a65a67f64ff30969e09037b3918bc5bc3bdd82f56758ebc440d76849d22
						0::/`,
					containerID:   "k8s.io/fd5a2a65a67f64ff30969e09037b3918bc5bc3bdd82f56758ebc440d76849d22",
					containerName: "k8s_rabbitmq_rabbitmq-container-port-66fdd44ccd-7hsqr_default",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl, err := NewMockFromFile(filepath.Join(tt.dir, testContainerdJSON))
			if err != nil {
				t.Error(err)

				return
			}

			c := FakeContainerd(cl, facts.ContainerFilter{}.ContainerIgnored)

			ctx, cancel := context.WithTimeout(t.Context(), 1*time.Second)
			defer cancel()

			for _, ttt := range tt.wants {
				t.Run(ttt.name, func(t *testing.T) {
					querier := c.ProcessWithCache()

					container, err := querier.ContainerFromCGroup(ctx, testutil.Unindent(ttt.cgroupData))
					if ttt.mustErrDoesNotExist && !errors.Is(err, facts.ErrContainerDoesNotExists) {
						t.Errorf("err = %v want ErrContainerDoesNotExists", err)
					}

					if ttt.containerID == "" {
						if container != nil {
							t.Errorf("container.ID = %v (err=%v) want no container", container.ID(), err)
						}

						return
					}

					if err != nil {
						t.Errorf("err = %v want nil", err)
					}

					if container == nil {
						t.Errorf("container = nil, want %s", ttt.containerName)

						return
					}

					if container.ContainerName() != ttt.containerName {
						t.Errorf("ContainerName = %v, want %s", container.ContainerName(), ttt.containerName)
					}

					if container.ID() != ttt.containerID {
						t.Errorf("ID = %v, want %s", container.ID(), ttt.containerID)
					}
				})
			}
		})
	}
}

func TestContainerd_ContainerFromPID(t *testing.T) {
	tests := []struct {
		name                 string
		dir                  string
		wantContainerFromPID map[int]facts.FakeContainer
		notContainerForPID   []int
	}{
		{
			name: "minikube-1.20.0 (containerd in Docker)",
			dir:  testDirMinikube,
			wantContainerFromPID: map[int]facts.FakeContainer{
				5094: {
					FakeContainerName: "redis-server",
					FakeID:            "example/redis-server",
					FakeState:         facts.ContainerRunning,
					FakeImageName:     "docker.io/library/redis:alpine",
				},
				5044: {
					FakeContainerName: testGloutonIgnore,
					FakeState:         facts.ContainerRunning,
				},
				4455: {
					FakeContainerName: testGloutonIgnore,
				},
				4133: {
					FakeContainerName: testGloutonIgnore,
				},
			},
			notContainerForPID: []int{1, 42, 163},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl, err := NewMockFromFile(filepath.Join(tt.dir, testContainerdJSON))
			if err != nil {
				t.Error(err)

				return
			}

			c := FakeContainerd(cl, facts.ContainerFilter{}.ContainerIgnored)

			ctx, cancel := context.WithTimeout(t.Context(), 1*time.Second)
			defer cancel()

			querier := c.ProcessWithCache()

			procs, err := querier.Processes(ctx)
			if err != nil {
				t.Error(err)
			}

			if procs != nil {
				t.Errorf("procs = %v, want nil", procs)
			}

			for pid, c := range tt.wantContainerFromPID {
				got, err := querier.ContainerFromPID(ctx, "", pid)
				if err != nil {
					t.Error(err)

					return
				}

				if got == nil {
					t.Errorf("got = nil, for pid %d", pid)

					return
				}

				if diff := c.Diff(t.Context(), got); diff != "" {
					t.Errorf("ContainerFromPID(%d]): %v", pid, diff)
				}

				// yes we always pass the SAME containerID. This container ID is only a hint, wrong value should be an issue.
				got, err = querier.ContainerFromPID(ctx, "example/redis-server", pid)
				if err != nil {
					t.Error(err)

					return
				}

				if diff := c.Diff(t.Context(), got); diff != "" {
					t.Errorf("ContainerFromPID(%d]): %v", pid, diff)
				}
			}

			for _, pid := range tt.notContainerForPID {
				got, err := querier.ContainerFromPID(ctx, "", pid)
				if err != nil {
					t.Error(err)

					return
				}

				if got != nil {
					t.Errorf("found a container for PID=%d: %v, want none", pid, got)
				}
			}
		})
	}
}

func TestConvertMetric_Throttling(t *testing.T) {
	tests := []struct {
		name string
		data any
	}{
		{
			name: "cgroup-v1",
			data: &v1.Metrics{
				CPU: &v1.CPUStat{
					Usage:      &v1.CPUUsage{Total: 1000},
					Throttling: &v1.Throttle{Periods: 100, ThrottledPeriods: 25},
				},
			},
		},
		{
			name: "cgroup-v2",
			data: &v2.Metrics{
				CPU: &v2.CPUStat{
					UsageUsec:   1000,
					NrPeriods:   100,
					NrThrottled: 25,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := convertMetric(tt.data)
			if err != nil {
				t.Fatalf("convertMetric() error = %v", err)
			}

			if got["container_cpu_periods"] != 100 {
				t.Errorf("container_cpu_periods = %d, want 100", got["container_cpu_periods"])
			}

			if got["container_cpu_throttled_periods"] != 25 {
				t.Errorf("container_cpu_throttled_periods = %d, want 25", got["container_cpu_throttled_periods"])
			}
		})
	}
}

func TestRateFromMetricValue_ThrottledPerc(t *testing.T) {
	const id = "ns/" + testThrottleContainerID

	gloutonIDToName := map[string]string{id: "my-container"}

	pastValues := []metricValue{
		{
			ContainerNamespace: "ns",
			ContainerID:        testThrottleContainerID,
			Time:               time.Unix(0, 0),
			Values: map[string]uint64{
				metricCPUPeriods:          100,
				metricCPUThrottledPeriods: 10,
			},
		},
	}
	newValues := []metricValue{
		{
			ContainerNamespace: "ns",
			ContainerID:        testThrottleContainerID,
			Time:               time.Unix(10, 0),
			Values: map[string]uint64{
				// 40 new periods, 30 of them throttled => 75%.
				metricCPUPeriods:          140,
				metricCPUThrottledPeriods: 40,
			},
		},
	}

	points := rateFromMetricValue(gloutonIDToName, pastValues, newValues)

	var found bool

	for _, p := range points {
		if p.Labels[types.LabelName] == metricCPUPeriods || p.Labels[types.LabelName] == metricCPUThrottledPeriods {
			t.Errorf("raw counter %q should not be emitted", p.Labels[types.LabelName])
		}

		if p.Labels[types.LabelName] == metricCPUThrottledPerc {
			found = true

			if p.Value != 75 {
				t.Errorf("container_cpu_throttled_perc = %v, want 75", p.Value)
			}
		}
	}

	if !found {
		t.Error("container_cpu_throttled_perc was not emitted")
	}
}

func TestRateFromMetricValue_ThrottledPercNoPeriods(t *testing.T) {
	const id = "ns/" + testThrottleContainerID

	gloutonIDToName := map[string]string{id: "my-container"}

	// No new period elapsed (e.g. container without a CPU limit): no metric.
	values := []metricValue{
		{
			ContainerNamespace: "ns",
			ContainerID:        testThrottleContainerID,
			Time:               time.Unix(0, 0),
			Values:             map[string]uint64{metricCPUPeriods: 0, metricCPUThrottledPeriods: 0},
		},
	}
	newValues := []metricValue{
		{
			ContainerNamespace: "ns",
			ContainerID:        testThrottleContainerID,
			Time:               time.Unix(10, 0),
			Values:             map[string]uint64{metricCPUPeriods: 0, metricCPUThrottledPeriods: 0},
		},
	}

	points := rateFromMetricValue(gloutonIDToName, values, newValues)

	for _, p := range points {
		if p.Labels[types.LabelName] == metricCPUThrottledPerc {
			t.Errorf("container_cpu_throttled_perc should not be emitted when no period elapsed, got %v", p.Value)
		}
	}
}

func TestParseFIBTrieLocalAddress(t *testing.T) {
	const fibTrie = `Main:
  +-- 0.0.0.0/0 3 0 5
     |-- 0.0.0.0
        /0 universe UNICAST
     +-- 127.0.0.0/8 2 0 2
        +-- 127.0.0.0/31 1 0 0
           |-- 127.0.0.0
              /32 link BROADCAST
           |-- 127.0.0.1
              /32 host LOCAL
        |-- 127.255.255.255
           /32 link BROADCAST
     +-- 172.17.0.0/16 2 0 2
        |-- 172.17.0.0
           /16 link UNICAST
        |-- 172.17.0.5
           /32 host LOCAL
        |-- 172.17.255.255
           /32 link BROADCAST
`

	want := "172.17.0.5"
	if got := parseFIBTrieLocalAddress(fibTrie); got != want {
		t.Errorf("parseFIBTrieLocalAddress() = %q, want %q", got, want)
	}

	if got := parseFIBTrieLocalAddress("Main:\n  +-- 0.0.0.0/0 3 0 5\n"); got != "" {
		t.Errorf("parseFIBTrieLocalAddress() with no LOCAL entry = %q, want empty", got)
	}
}

func TestParseFIBTrieLocalAddress_SkipsLinkLocal(t *testing.T) {
	// A link-local (APIPA, 169.254.0.0/16) address that sorts before a real address in the
	// trie must not be returned as the primary address.
	const fibTrie = `Main:
  +-- 0.0.0.0/0 3 0 5
     |-- 0.0.0.0
        /0 universe UNICAST
     +-- 169.254.0.0/16 2 0 2
        |-- 169.254.0.0
           /16 link UNICAST
        |-- 169.254.1.2
           /32 host LOCAL
        |-- 169.254.255.255
           /32 link BROADCAST
     +-- 172.17.0.0/16 2 0 2
        |-- 172.17.0.0
           /16 link UNICAST
        |-- 172.17.0.5
           /32 host LOCAL
        |-- 172.17.255.255
           /32 link BROADCAST
`

	want := "172.17.0.5"
	if got := parseFIBTrieLocalAddress(fibTrie); got != want {
		t.Errorf("parseFIBTrieLocalAddress() = %q, want %q (link-local address should be skipped)", got, want)
	}
}

func TestParseIfInet6GlobalAddress(t *testing.T) {
	const ifInet6 = "" +
		"00000000000000000000000000000001 01 80 10 80       lo\n" +
		"fe80000000000000021fd3fffe0824cc 02 40 20 80      eth0\n" +
		"20010db8000000000000000000000005 02 40 00 00      eth0\n"

	want := "2001:db8::5"
	if got := parseIfInet6GlobalAddress(ifInet6); got != want {
		t.Errorf("parseIfInet6GlobalAddress() = %q, want %q", got, want)
	}

	const noGlobal = "" +
		"00000000000000000000000000000001 01 80 10 80       lo\n" +
		"fe80000000000000021fd3fffe0824cc 02 40 20 80      eth0\n"

	if got := parseIfInet6GlobalAddress(noGlobal); got != "" {
		t.Errorf("parseIfInet6GlobalAddress() with no global entry = %q, want empty", got)
	}
}

func TestHasHostNetwork(t *testing.T) {
	cases := []struct {
		name string
		spec oci.Spec
		want bool
	}{
		{
			name: "no linux section",
			spec: oci.Spec{},
			want: false,
		},
		{
			name: "network namespace present",
			spec: oci.Spec{
				Linux: &specs.Linux{
					Namespaces: []specs.LinuxNamespace{
						{Type: specs.PIDNamespace},
						{Type: specs.NetworkNamespace},
					},
				},
			},
			want: false,
		},
		{
			name: "network namespace absent (--net host equivalent)",
			spec: oci.Spec{
				Linux: &specs.Linux{
					Namespaces: []specs.LinuxNamespace{
						{Type: specs.PIDNamespace},
					},
				},
			},
			want: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasHostNetwork(&c.spec); got != c.want {
				t.Errorf("hasHostNetwork() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestPrimaryAddressFromProcHonorsHostRoot guards against a regression where primaryAddressFromProc read a
// hardcoded /proc/<pid>/net/..., ignoring the hostroot under which a containerized Glouton sees the host's
// procfs. Reading a bare /proc there resolves the PID in Glouton's own namespace instead of the host's --
// at best failing, at worst matching an unrelated process that happens to share the number and reporting
// its address as the container's, which then flows into Service.IPAddress and every check/input target
// built from it.
func TestPrimaryAddressFromProcHonorsHostRoot(t *testing.T) {
	fibTrieFor := func(addr string) string {
		return "Main:\n  +-- 172.17.0.0/16 2 0 2\n     |-- " + addr + "\n        /32 host LOCAL\n"
	}

	const pid = 4242

	writeFakeProc := func(t *testing.T, procDir, addr string) {
		t.Helper()

		netDir := filepath.Join(procDir, strconv.Itoa(pid), "net")
		if err := os.MkdirAll(netDir, 0o750); err != nil {
			t.Fatal("Can't create fake procfs:", err)
		}

		if err := os.WriteFile(filepath.Join(netDir, "fib_trie"), []byte(fibTrieFor(addr)), 0o600); err != nil {
			t.Fatal("Can't write fake fib_trie:", err)
		}
	}

	hostRoot := t.TempDir()
	writeFakeProc(t, filepath.Join(hostRoot, "proc"), "172.17.0.9")

	// hostRoot "" must resolve exactly like "/", reading the real /proc -- not filepath.Join("", "proc"),
	// which is the relative "proc" and would read whatever Glouton's working directory happens to contain.
	// "" is reachable in production: agent.go leaves hostRootPath empty when Glouton runs containerized
	// with GLOUTON_DF_HOST_MOUNT_POINT unset, which envsetup only warns about.
	//
	// A merely-missing fake tree wouldn't tell a relative resolution apart from the correct absolute one,
	// since both would fail to find pid 4242 under a made-up hostRoot -- so this plants a decoy tree at the
	// relative location instead, and asserts it is NOT what gets picked up.
	decoyWD := t.TempDir()
	writeFakeProc(t, filepath.Join(decoyWD, "proc"), "10.10.10.10")

	t.Chdir(decoyWD)

	if got := primaryAddressFromProc(pid, ""); got == "10.10.10.10" {
		t.Fatal("primaryAddressFromProc(pid, \"\") read the relative ./proc decoy instead of the real /proc")
	}

	if got, want := primaryAddressFromProc(pid, hostRoot), "172.17.0.9"; got != want {
		t.Errorf("primaryAddressFromProc() under hostRoot = %q, want %q", got, want)
	}
}

// TestConvertToContainerObjectPrimaryAddressByTaskStatus guards against a regression where the
// primary-address lookup was skipped for every task status short of client.Running. Only an exited
// (client.Stopped) task is a hazard -- its Pid() still reports a PID the host may have recycled for an
// unrelated process, whose network namespace would then be reported as this container's address. Created,
// Paused and Pausing all still have a live process behind that PID (containerd's own process.Delete refuses
// those alongside Running as "must be stopped first"), so skipping them silently dropped a merely paused
// container's address.
func TestConvertToContainerObjectPrimaryAddressByTaskStatus(t *testing.T) {
	t.Parallel()

	const (
		namespace = "default"
		pid       = 5150
		wantAddr  = "10.1.2.3"
		fibTrie   = `Main:
  +-- 10.1.2.0/24 2 0 2
     |-- 10.1.2.3
        /32 host LOCAL
`
	)

	// A fake procfs for pid, so a resolved address is deterministic instead of depending on the host.
	hostRoot := t.TempDir()

	netDir := filepath.Join(hostRoot, "proc", strconv.Itoa(pid), "net")
	if err := os.MkdirAll(netDir, 0o750); err != nil {
		t.Fatal("Can't create fake procfs:", err)
	}

	if err := os.WriteFile(filepath.Join(netDir, "fib_trie"), []byte(fibTrie), 0o600); err != nil {
		t.Fatal("Can't write fake fib_trie:", err)
	}

	testCases := []struct {
		status   client.ProcessStatus
		wantAddr string
	}{
		{status: client.Running, wantAddr: wantAddr},
		{status: client.Paused, wantAddr: wantAddr},
		{status: client.Pausing, wantAddr: wantAddr},
		{status: client.Created, wantAddr: wantAddr},
		{status: client.Unknown, wantAddr: wantAddr},
		{status: client.Stopped, wantAddr: ""},
	}

	for _, tc := range testCases {
		t.Run(string(tc.status), func(t *testing.T) {
			t.Parallel()

			ctr := MockContainer{
				namespace: namespace,
				MockInfo: ContainerOCISpec{
					// A network namespace of its own, so this isn't the host-network branch.
					Spec: &oci.Spec{
						Linux: &specs.Linux{
							Namespaces: []specs.LinuxNamespace{{Type: specs.NetworkNamespace}},
						},
					},
				},
				MockTask: MockTask{
					MockID:     "task-1",
					MockPID:    pid,
					MockStatus: client.Status{Status: tc.status},
				},
			}

			ctx := namespaces.WithNamespace(t.Context(), namespace)

			obj, err := convertToContainerObject(ctx, namespace, ctr, hostRoot)
			if err != nil {
				t.Fatal("convertToContainerObject failed:", err)
			}

			if obj.primaryAddress != tc.wantAddr {
				t.Errorf("status %q: primaryAddress = %q, want %q", tc.status, obj.primaryAddress, tc.wantAddr)
			}
		})
	}
}

// netDevHeader is the 2-line header of a real /proc/<pid>/net/dev.
const netDevHeader = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
`

// netDevLine formats a /proc/<pid>/net/dev line with the given received and sent bytes.
func netDevLine(iface string, recvBytes, sentBytes uint64) string {
	return fmt.Sprintf("%6s: %7d 10 0 0 0 0 0 0 %8d 20 0 0 0 0 0 0\n", iface, recvBytes, sentBytes)
}

// fakeProc is a fake procfs mounted under a fake host root.
type fakeProc struct {
	hostRoot string
}

func newFakeProc(t *testing.T) fakeProc {
	t.Helper()

	p := fakeProc{hostRoot: t.TempDir()}

	// The host's init, in the host network namespace: only its file identity matters.
	p.writeNetDev(t, 1)

	return p
}

func (p fakeProc) pidDir(pid int) string {
	return filepath.Join(p.hostRoot, "proc", strconv.Itoa(pid))
}

// writeNetDev writes the net/dev of pid, made of the header followed by the given lines.
func (p fakeProc) writeNetDev(t *testing.T, pid int, lines ...string) {
	t.Helper()

	netDev := netDevHeader + strings.Join(lines, "")

	netDir := filepath.Join(p.pidDir(pid), "net")
	if err := os.MkdirAll(netDir, 0o750); err != nil {
		t.Fatal("Can't create fake procfs:", err)
	}

	if err := os.WriteFile(filepath.Join(netDir, "dev"), []byte(netDev), 0o600); err != nil {
		t.Fatal("Can't write fake net/dev:", err)
	}
}

// addHostNetworkPID adds a process in the host's network namespace: it sees the very same procfs
// "net" entries as the host's init, which a symlink reproduces here.
func (p fakeProc) addHostNetworkPID(t *testing.T, pid int) {
	t.Helper()

	if err := os.MkdirAll(p.pidDir(pid), 0o750); err != nil {
		t.Fatal("Can't create fake procfs:", err)
	}

	if err := os.Symlink(filepath.Join(p.pidDir(1), "net"), filepath.Join(p.pidDir(pid), "net")); err != nil {
		t.Fatal("Can't create fake host network namespace:", err)
	}
}

func TestIsRuntimeInterface(t *testing.T) {
	cases := map[string]bool{
		"eth0":     true,
		"eth12":    true,
		"net1":     true,
		"lo":       false,
		"docker0":  false,
		"veth1a2b": false,
		"br-1a2b":  false,
		"tun0":     false,
		"wg0":      false,
		"tunl0":    false,
		"eth":      false,
		"ethx":     false,
		"eth0.100": false,
		"net":      false,
		"network0": false,
	}

	for name, want := range cases {
		if got := isRuntimeInterface(name); got != want {
			t.Errorf("isRuntimeInterface(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestNetTrafficFromProc(t *testing.T) {
	const (
		pid         = 4242
		onlyLoPID   = 4343
		missingPID  = 4444
		dindPodPID  = 4545
		multusPID   = 4646
		bigCountPID = 4747
	)

	p := newFakeProc(t)

	p.writeNetDev(t, pid, netDevLine("lo", 50, 50), netDevLine("eth0", 100, 200))
	p.writeNetDev(t, onlyLoPID, netDevLine("lo", 50, 50))
	// A docker:dind service: the same traffic crosses eth0, docker0 and the veth.
	p.writeNetDev(t, dindPodPID, netDevLine("lo", 50, 50), netDevLine("eth0", 100, 200),
		netDevLine("docker0", 100, 200), netDevLine("veth1a2b", 200, 100))
	p.writeNetDev(t, multusPID, netDevLine("eth0", 100, 200), netDevLine("net1", 1000, 2000))
	// A counter too wide for its column is glued to the colon.
	p.writeNetDev(t, bigCountPID, "eth0:12345678901 10 0 0 0 0 0 0 300 20 0 0 0 0 0 0\n")

	cases := []struct {
		name string
		pid  int
		want netTraffic
	}{
		{name: "loopback excluded", pid: pid, want: netTraffic{recvBytes: 100, sentBytes: 200, found: true}},
		{name: "only loopback", pid: onlyLoPID, want: netTraffic{}},
		{name: "missing PID", pid: missingPID, want: netTraffic{}},
		{name: "interfaces created inside the container excluded", pid: dindPodPID, want: netTraffic{recvBytes: 100, sentBytes: 200, found: true}},
		{name: "multus interfaces summed", pid: multusPID, want: netTraffic{recvBytes: 1100, sentBytes: 2200, found: true}},
		{name: "counter glued to the colon", pid: bigCountPID, want: netTraffic{recvBytes: 12345678901, sentBytes: 300, found: true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := netTrafficFromProc(tc.pid, p.hostRoot); got != tc.want {
				t.Errorf("netTrafficFromProc() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestNetNamespaceFromProc covers the cases where the network namespace can't be told, the host and
// isolated ones being covered through convertToContainerObject.
func TestNetNamespaceFromProc(t *testing.T) {
	const (
		pid        = 4242
		missingPID = 6464
	)

	p := newFakeProc(t)
	p.writeNetDev(t, pid, netDevLine("eth0", 100, 200))

	if got := netNamespaceFromProc(missingPID, p.hostRoot); got != netNamespaceUnknown {
		t.Errorf("netNamespaceFromProc() for a missing PID = %v, want %v", got, netNamespaceUnknown)
	}

	if got := netNamespaceFromProc(pid, t.TempDir()); got != netNamespaceUnknown {
		t.Errorf("netNamespaceFromProc() with the host init not visible = %v, want %v", got, netNamespaceUnknown)
	}
}

// TestConvertToContainerObjectHostNetNamespace guards against a regression where the containers of a
// Kubernetes hostNetwork pod were taken as having a network namespace of their own: their OCI spec
// doesn't omit the network namespace but joins the sandbox's one by path, which is the host's. They
// reported the node's traffic as theirs, and got the node's address as containerd primary address
// (under Kubernetes, the pod IP from the API takes precedence over it anyway).
func TestConvertToContainerObjectHostNetNamespace(t *testing.T) {
	const (
		namespace      = "k8s.io"
		pid            = 4242
		hostNetworkPID = 5353
	)

	p := newFakeProc(t)
	p.writeNetDev(t, pid, netDevLine("eth0", 100, 200))
	p.addHostNetworkPID(t, hostNetworkPID)

	// What the CRI plugin generates for every app container, hostNetwork pod or not.
	criSpec := &oci.Spec{
		Linux: &specs.Linux{
			Namespaces: []specs.LinuxNamespace{{Type: specs.NetworkNamespace, Path: "/proc/1234/ns/net"}},
		},
	}
	hostNetworkSpec := &oci.Spec{Linux: &specs.Linux{}}

	cases := []struct {
		name        string
		spec        *oci.Spec
		pid         uint32
		status      client.ProcessStatus
		wantNetNS   netNamespace
		wantAddress string
	}{
		{name: "pod network", spec: criSpec, pid: pid, status: client.Running, wantNetNS: netNamespaceIsolated},
		{name: "hostNetwork pod", spec: criSpec, pid: hostNetworkPID, status: client.Running, wantNetNS: netNamespaceHost, wantAddress: "127.0.0.1"},
		{name: "no network namespace in spec", spec: hostNetworkSpec, pid: pid, status: client.Running, wantNetNS: netNamespaceHost, wantAddress: "127.0.0.1"},
		{name: "stopped, PID may be recycled", spec: criSpec, pid: hostNetworkPID, status: client.Stopped, wantNetNS: netNamespaceUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctr := MockContainer{
				namespace: namespace,
				MockInfo:  ContainerOCISpec{Spec: tc.spec},
				MockTask: MockTask{
					MockID:     "task-1",
					MockPID:    tc.pid,
					MockStatus: client.Status{Status: tc.status},
				},
			}

			ctx := namespaces.WithNamespace(t.Context(), namespace)

			obj, err := convertToContainerObject(ctx, namespace, ctr, p.hostRoot)
			if err != nil {
				t.Fatal("convertToContainerObject failed:", err)
			}

			if obj.netNamespace != tc.wantNetNS {
				t.Errorf("netNamespace = %v, want %v", obj.netNamespace, tc.wantNetNS)
			}

			if obj.primaryAddress != tc.wantAddress {
				t.Errorf("primaryAddress = %q, want %q", obj.primaryAddress, tc.wantAddress)
			}
		})
	}
}

// metricsClient is a MockClient whose Metrics returns the same cgroup v2 metrics for every task.
type metricsClient struct {
	*MockClient
}

func (metricsClient) Metrics(_ context.Context, filters []string) (*tasks.MetricsResponse, error) {
	resp := &tasks.MetricsResponse{}

	for _, filter := range filters {
		resp.Metrics = append(resp.Metrics, &containerdTypes.Metric{
			ID:   strings.TrimPrefix(filter, "id=="),
			Data: mustMarshalAny(&v2.Metrics{Memory: &v2.MemoryStat{Usage: 1000}}),
		})
	}

	return resp, nil
}

// TestContainerdMetricsNetwork checks that Metrics reports the network traffic read from procfs for
// each container whose network namespace isn't the host's, every container of a pod reporting the
// pod's traffic -- even one whose PID can't be read anymore, whichever order they're processed in.
func TestContainerdMetricsNetwork(t *testing.T) {
	const (
		namespace  = "k8s.io"
		podPID     = 4242
		sidecarPID = 4244
		otherPID   = 4343
		exitedPID  = 9999
	)

	p := newFakeProc(t)

	// The sidecar joins the pod's network namespace, which a symlink reproduces here.
	if err := os.MkdirAll(p.pidDir(sidecarPID), 0o750); err != nil {
		t.Fatal("Can't create fake procfs:", err)
	}

	if err := os.Symlink(filepath.Join(p.pidDir(podPID), "net"), filepath.Join(p.pidDir(sidecarPID), "net")); err != nil {
		t.Fatal("Can't create fake pod network namespace:", err)
	}

	newContainer := func(id string, pid int, netNS netNamespace, labels map[string]string) containerObject {
		return containerObject{
			namespace:    namespace,
			info:         ContainerOCISpec{Container: containers.Container{ID: id, Labels: labels}},
			pid:          pid,
			state:        string(client.Running),
			netNamespace: netNS,
		}
	}

	podLabels := map[string]string{"io.kubernetes.pod.uid": "pod-uid"}

	c := newWithOpenner(nil, p.hostRoot, nil, func(facts.Container) bool { return false }, nil)
	c.client = metricsClient{MockClient: &MockClient{}}
	c.lastUpdate = time.Now()
	c.containers = map[string]containerObject{}

	for _, cont := range []containerObject{
		newContainer("app", podPID, netNamespaceIsolated, podLabels),
		newContainer("sidecar", sidecarPID, netNamespaceIsolated, podLabels),
		// Its task exited since the cache refresh: its PID is gone.
		newContainer("exited", exitedPID, netNamespaceIsolated, podLabels),
		newContainer("other", otherPID, netNamespaceIsolated, nil),
		newContainer("hostnet", 1, netNamespaceHost, nil),
		newContainer("unknown", otherPID, netNamespaceUnknown, nil),
	} {
		c.containers[cont.ID()] = cont
	}

	now := time.Now()

	p.writeNetDev(t, podPID, netDevLine("eth0", 1000, 2000))
	p.writeNetDev(t, otherPID, netDevLine("eth0", 5000, 5000))

	if _, err := c.Metrics(t.Context(), now); err != nil {
		t.Fatal("Metrics() failed:", err)
	}

	p.writeNetDev(t, podPID, netDevLine("eth0", 2000, 4000))
	p.writeNetDev(t, otherPID, netDevLine("eth0", 5100, 5200))

	points, err := c.Metrics(t.Context(), now.Add(10*time.Second))
	if err != nil {
		t.Fatal("Metrics() failed:", err)
	}

	got := make(map[string]float64)

	for _, point := range points {
		if name := point.Labels[types.LabelName]; strings.HasPrefix(name, "container_net_") {
			got[point.Labels[types.LabelMetaContainerID]+" "+name] = point.Value
		}
	}

	// 1000 bytes received in 10s = 800 bits/s.
	want := map[string]float64{
		namespace + "/app container_net_bits_recv":     800,
		namespace + "/app container_net_bits_sent":     1600,
		namespace + "/sidecar container_net_bits_recv": 800,
		namespace + "/sidecar container_net_bits_sent": 1600,
		namespace + "/exited container_net_bits_recv":  800,
		namespace + "/exited container_net_bits_sent":  1600,
		namespace + "/other container_net_bits_recv":   80,
		namespace + "/other container_net_bits_sent":   160,
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("network points = %v, want %v", got, want)
	}
}

// TestContainerdDiagnosticArchiveNetNamespace checks that the diagnostic archive tells why a container
// has no network metrics.
func TestContainerdDiagnosticArchiveNetNamespace(t *testing.T) {
	c := newWithOpenner(nil, "", nil, func(facts.Container) bool { return false }, nil)

	for _, cont := range []containerObject{
		{namespace: "k8s.io", info: ContainerOCISpec{Container: containers.Container{ID: "app"}}, pid: 4242, netNamespace: netNamespaceIsolated},
		{namespace: "k8s.io", info: ContainerOCISpec{Container: containers.Container{ID: "hostnet"}}, pid: 4343, netNamespace: netNamespaceHost},
		{namespace: "k8s.io", info: ContainerOCISpec{Container: containers.Container{ID: "stopped"}}, pid: 4444},
	} {
		c.containers[cont.ID()] = cont
	}

	var buffer bytes.Buffer

	if err := c.DiagnosticArchive(t.Context(), archivewriter.NewSingleFileWriter("containerd.json", &buffer)); err != nil {
		t.Fatal("DiagnosticArchive() failed:", err)
	}

	var archive struct {
		Containers []struct {
			ID           string
			PID          int
			NetNamespace string
		}
	}

	if err := json.Unmarshal(buffer.Bytes(), &archive); err != nil {
		t.Fatal("Can't decode containerd.json:", err)
	}

	got := make(map[string]string)

	for _, cont := range archive.Containers {
		got[cont.ID] = fmt.Sprintf("%d %s", cont.PID, cont.NetNamespace)
	}

	want := map[string]string{
		"k8s.io/app":     "4242 isolated",
		"k8s.io/hostnet": "4343 host",
		"k8s.io/stopped": "4444 unknown",
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("containers in containerd.json = %v, want %v", got, want)
	}
}
