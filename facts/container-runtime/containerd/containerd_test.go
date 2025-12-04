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

package containerd

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/facts/container-runtime/internal/testutil"

	pbEvents "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/protobuf"
	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/events"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
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
			name: "minikube-1.20.0",
			dir:  "testdata/minikube-1.20.0",
			want: map[string]string{
				"containerd_version": "1.4.3",
				"container_runtime":  "containerd",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl, err := NewMockFromFile(filepath.Join(tt.dir, "containerd.json"))
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
			name: "minikube-1.20.0",
			dir:  "testdata/minikube-1.20.0",
			want: []facts.FakeContainer{
				{
					FakeID:            "default/notRunning",
					FakeContainerName: "notRunning",
					FakeState:         facts.ContainerStopped,
					FakeCommand:       []string{"true"},
					FakeImageName:     "docker.io/library/rabbitmq:latest",
					FakeFinishedAt:    time.Date(2021, 1, 7, 19, 8, 54, 676875154, time.UTC),
				},
				{
					FakeID:            "default/rabbitLabels",
					FakeContainerName: "rabbitLabels",
					FakeState:         facts.ContainerRunning,
					FakeImageName:     "docker.io/library/rabbitmq:latest",
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
					FakeImageName:     "docker.io/library/rabbitmq:latest",
				},
				{
					FakeID:            "default/gloutonIgnore",
					FakeContainerName: "gloutonIgnore",
					FakeState:         facts.ContainerRunning,
					FakeLabels: map[string]string{
						"glouton.enable":                         "off",
						"io.containerd.image.config.stop-signal": "SIGTERM",
					},
					FakeImageName: "docker.io/library/rabbitmq:latest",
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
			cl, err := NewMockFromFile(filepath.Join(tt.dir, "containerd.json"))
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
			name: "minikube-1.20.0",
			dir:  "testdata/minikube-1.20.0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			cl, err := NewMockFromFile(filepath.Join(tt.dir, "containerd.json"))
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
			dir:  "testdata/minikube-1.20.0",
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
			cl, err := NewMockFromFile(filepath.Join(tt.dir, "containerd.json"))
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
			dir:  "testdata/minikube-1.20.0",
			wantContainerFromPID: map[int]facts.FakeContainer{
				5094: {
					FakeContainerName: "redis-server",
					FakeID:            "example/redis-server",
					FakeState:         facts.ContainerRunning,
					FakeImageName:     "docker.io/library/redis:alpine",
				},
				5044: {
					FakeContainerName: "gloutonIgnore",
					FakeState:         facts.ContainerRunning,
				},
				4455: {
					FakeContainerName: "gloutonIgnore",
				},
				4133: {
					FakeContainerName: "gloutonIgnore",
				},
			},
			notContainerForPID: []int{1, 42, 163},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl, err := NewMockFromFile(filepath.Join(tt.dir, "containerd.json"))
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
