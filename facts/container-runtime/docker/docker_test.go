package docker

import (
	"context"
	"errors"
	"glouton/facts"
	"glouton/facts/container-runtime/internal/testutil"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	containerTypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/google/go-cmp/cmp"
)

func TestDocker_RuntimeFact(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		want map[string]string
	}{
		{
			name: "docker-20.10",
			dir:  "testdata/docker-20.10.0",
			want: map[string]string{
				"docker_version":     "20.10.0",
				"docker_api_version": "1.41",
				"container_runtime":  "Docker",
			},
		},
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			cl, err := NewDockerMock(tt.dir)
			if err != nil {
				t.Error(err)

				return
			}

			d := FakeDocker(cl)

			if got := d.RuntimeFact(context.Background(), nil); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Docker.RuntimeFact() = %v, want %v", got, tt.want)
			}
		})
	}
}

func unindent(input string) string {
	lines := strings.Split(input, "\n")
	kept := make([]string, 0, len(lines))

	for _, l := range lines {
		l := strings.TrimLeft(l, " \t")
		if len(l) > 0 {
			kept = append(kept, l)
		}
	}

	return strings.Join(kept, "\n") + "\n"
}

func string2TopBody(input string) containerTypes.ContainerTopOKBody {
	procList := containerTypes.ContainerTopOKBody{}

	lines := strings.Split(unindent(input), "\n")
	procList.Titles = strings.Fields(lines[0])

	for _, l := range lines[1:] {
		if l == "" {
			continue
		}

		fields := strings.Fields(l)
		process := fields[:len(procList.Titles)-1]
		process = append(process, strings.Join(fields[len(procList.Titles)-1:], " "))
		procList.Processes = append(procList.Processes, process)
	}

	return procList
}

func TestDocker_Containers(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		now  time.Time
		want []facts.FakeContainer
	}{
		{
			name: "docker-20.10",
			dir:  "testdata/docker-20.10.0",
			want: []facts.FakeContainer{
				{
					FakeID:            "3b252c3f4b6dc25f2a727dbe1c6b24ceaff577942251e183e2300db0de4a9860",
					FakeContainerName: "testdata_notRunning_1",
					FakeState:         facts.ContainerStopped,
					FakeCommand:       []string{"true"},
					FakeImageName:     "rabbitmq",
				},
				{
					FakeID:             "2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882",
					FakeContainerName:  "testdata_rabbitmqExposed_1",
					FakeState:          facts.ContainerRunning,
					FakeImageName:      "rabbitmq",
					FakePrimaryAddress: "172.18.0.2",
					FakeListenAddresses: []facts.ListenAddress{
						{
							Address:       "172.18.0.2",
							Port:          5671,
							NetworkFamily: "tcp",
						},
					},
					FakeListenAddressesExplicit: true,
				},
				{
					FakeID:            "33600bb7b4d62f43e87839e514a4235bb72f66dcfca35a7df5c900361a2c4d6e",
					FakeContainerName: "testdata_rabbitLabels_1",
					FakeState:         facts.ContainerRunning,
					FakeImageName:     "rabbitmq",
					FakeLabels: map[string]string{
						"com.docker.compose.config-hash":      "afc5fc62031297be2c22523c44960c2e3d0964cf58562864955781725b4e1d82",
						"com.docker.compose.container-number": "1",
						"com.docker.compose.oneoff":           "False",
						"com.docker.compose.project":          "testdata",
						"com.docker.compose.service":          "rabbitLabels",
						"com.docker.compose.version":          "1.24.0",
						"glouton.check.ignore.port.5672":      "false",
						"glouton.check.ignore.port.4369":      "true",
						"glouton.check.ignore.port.25672":     "TrUe",
					},
					FakeListenAddresses: []facts.ListenAddress{
						{
							Address:       "172.18.0.4",
							Port:          4369,
							NetworkFamily: "tcp",
						},
						{
							Address:       "172.18.0.4",
							Port:          5671,
							NetworkFamily: "tcp",
						},
						{
							Address:       "172.18.0.4",
							Port:          5672,
							NetworkFamily: "tcp",
						},
						{
							Address:       "172.18.0.4",
							Port:          15691,
							NetworkFamily: "tcp",
						},
						{
							Address:       "172.18.0.4",
							Port:          15692,
							NetworkFamily: "tcp",
						},
						{
							Address:       "172.18.0.4",
							Port:          25672,
							NetworkFamily: "tcp",
						},
					},
					FakeListenAddressesExplicit: false,
				},
				{
					FakeID:            "b59746cf51fa8b08eb228e5f4fc4bc28446a6f7ca19cdc3c23016f932b56003f",
					FakeContainerName: "testdata_rabbitmqInternal_1",
					FakeState:         facts.ContainerRunning,
					FakeImageName:     "rabbitmq",
				},
				{
					FakeID:            "54f7b691664eb41bb2ccef8f8f79c432621e1234522d88940075b07d8bbed997",
					FakeContainerName: "testdata_gloutonIgnore_1",
					FakeLabels: map[string]string{
						"com.docker.compose.config-hash":      "1fcd49fedd6ce041b698a40160a61f9b18a288d942e76bbb2f66760699459864",
						"com.docker.compose.container-number": "1",
						"com.docker.compose.oneoff":           "False",
						"com.docker.compose.project":          "testdata",
						"com.docker.compose.service":          "gloutonIgnore",
						"com.docker.compose.version":          "1.24.0",
						"glouton.enable":                      "off",
					},
					FakeState:     facts.ContainerRunning,
					FakeImageName: "rabbitmq",
					TestIgnored:   true,
				},
			},
		},
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			cl, err := NewDockerMock(tt.dir)
			if err != nil {
				t.Error(err)
				return
			}

			d := FakeDocker(cl)

			if d.IsRuntimeRunning(context.Background()) {
				t.Errorf("IsRuntimeRunning = true, want false (because connection not yet established")
			}

			containers, err := d.Containers(context.Background(), 0, true)
			if err != nil {
				t.Error(err)
			}

			containersWithoutExclude, err := d.Containers(context.Background(), 0, false)
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
					t.Errorf("Docker.Containers() don't have container %v", want.ID())

					continue
				}

				if diff := want.Diff(got); diff != "" {
					t.Errorf("Docker.Containers()[%v]: %s", want.ID(), diff)
				}

				got, ok := d.CachedContainer(want.ID())
				if !ok {
					t.Errorf("CachedContainer() don't have container %v", want.ID())
				} else if diff := want.Diff(got); diff != "" {
					t.Errorf("CachedContainer(%s): %s", want.ID(), diff)
				}

				if want.TestIgnored {
					_, ok := gotWithoutExcludeMap[want.FakeID]
					if ok {
						t.Errorf("container %s is listed by Containers()", want.FakeID)
					}

					if !facts.ContainerIgnored(got) {
						t.Errorf("ContainerIgnored(%s) = false, want true", want.FakeID)
					}
				} else {
					_, ok := gotWithoutExcludeMap[want.FakeID]
					if !ok {
						t.Errorf("container %s is not listed by Containers()", want.FakeID)
					}
				}
			}

			if !d.IsRuntimeRunning(context.Background()) {
				t.Errorf("IsRuntimeRunning = false, want true")
			}
		})
	}
}

// TestDocker_Run do not test much, but at least it execute code to ensure it don't crash.
func TestDocker_Run(t *testing.T) {
	start := time.Now()

	tests := []struct {
		name string
		dir  string
		now  time.Time
		want []facts.FakeContainer
	}{
		{
			name: "docker-20.10",
			dir:  "testdata/docker-20.10.0",
		},
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			cl, err := NewDockerMock(tt.dir)
			if err != nil {
				t.Error(err)

				return
			}

			cl.EventChanMaker = func() <-chan events.Message {
				ch := make(chan events.Message, 10)
				ch <- events.Message{
					Type:   "network",
					Action: "connect",
					Actor:  events.Actor{ID: "1235"},
				}
				ch <- events.Message{
					Type:   "volume",
					Action: "mount",
					Actor:  events.Actor{ID: "5678"},
				}
				ch <- events.Message{
					Type:   "container",
					Action: "start",
					Actor:  events.Actor{ID: cl.Containers[0].ID},
				}
				ch <- events.Message{
					Action: "health_status:test",
					Actor:  events.Actor{ID: cl.Containers[0].ID},
				}
				ch <- events.Message{
					Type:   "container",
					Action: "kill",
					Actor:  events.Actor{ID: cl.Containers[0].ID},
				}
				ch <- events.Message{
					Type:   "container",
					Action: "die",
					Actor:  events.Actor{ID: cl.Containers[0].ID},
				}
				ch <- events.Message{
					Type:   "container",
					Action: "destroy",
					Actor:  events.Actor{ID: cl.Containers[0].ID},
				}

				return ch
			}

			eventSeen := 0

			d := FakeDocker(cl)

			var (
				wg     sync.WaitGroup
				runErr error
			)

			wg.Add(1)
			go func() {
				defer wg.Done()
				runErr = d.Run(ctx)
			}()

			deadline := time.After(time.Second)

		outterloop:
			for {
				select {
				case ev, ok := <-d.Events():
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

			if !d.IsRuntimeRunning(context.Background()) {
				t.Errorf("IsRuntimeRunning = false, want true")
			}

			got := d.ContainerLastKill(cl.Containers[0].ID)
			now := time.Now()

			if got.Before(start) || got.After(now) {
				t.Errorf(
					"ContainerLastKill = %s, want between %s and %s",
					got.Format(time.RFC3339Nano),
					start.Format("15:04:05.999999999Z07:00"),
					now.Format("15:04:05.999999999Z07:00"),
				)
			}

			cancel()
			wg.Wait()

			if runErr != nil {
				t.Error(err)
			}

			if eventSeen != 5 {
				t.Errorf("eventSeen = %d, want 5", eventSeen)
			}
		})
	}
}

// TestDocker_unindent check that helper function unindent works as intended.
func TestDocker_unindent(t *testing.T) {
	indented := `
		12:cpuset:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2
		11:memory:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
		10:pids:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope`

	want := `12:cpuset:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2
11:memory:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
10:pids:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
`

	got := unindent(indented)
	if got != want {
		t.Errorf("got = %#v, want %#v", got, want)
	}
}

func TestDocker_ContainerFromCGroup(t *testing.T) {
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
			name: "docker-20.10 (Docker in Docker)",
			dir:  "testdata/docker-20.10.0",
			wants: []check{
				{
					name: "init",
					cgroupData: `
						12:cpuset:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2
						11:memory:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
						10:pids:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
						9:freezer:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2
						8:blkio:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
						7:net_cls,net_prio:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2
						6:perf_event:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2
						5:rdma:/
						4:devices:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
						3:hugetlb:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2
						2:cpu,cpuacct:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
						1:name=systemd:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
						0::/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope`,
					containerID: "",
				},
				{
					name: "rabbitmq",
					cgroupData: `
					12:cpuset:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882
					11:memory:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882
					10:pids:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882
					9:freezer:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882
					8:blkio:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882
					7:net_cls,net_prio:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882
					6:perf_event:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882
					5:rdma:/
					4:devices:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882
					3:hugetlb:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882
					2:cpu,cpuacct:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882
					1:name=systemd:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882
					0::/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/system.slice/containerd.service`,
					containerID:   "2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882",
					containerName: "testdata_rabbitmqExposed_1",
				},
				{
					name: "minikube v0.28.2",
					cgroupData: `
						11:freezer:/kubepods/besteffort/pod8f469a2e-bcd6-11e8-abe9-080027ae1159/2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882
						10:perf_event:/kubepods/besteffort/pod8f469a2e-bcd6-11e8-abe9-080027ae1159/2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882
						[...]
						1:name=systemd:/kubepods/besteffort/pod8f469a2e-bcd6-11e8-abe9-080027ae1159/2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882`,
					containerID:   "2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882",
					containerName: "testdata_rabbitmqExposed_1",
				},
				{
					name: "Docker on Ubuntu",
					cgroupData: `
						12:pids:/docker/2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882
						11:hugetlb:/docker/2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882
						[...]
						1:cpuset:/docker/2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882`,
					containerID:   "2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882",
					containerName: "testdata_rabbitmqExposed_1",
				},
				{
					name: "Docker on CentOS",
					cgroupData: `
						11:cpuset:/system.slice/docker-2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882.scope
						10:devices:/system.slice/docker-2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882.scope
						[...]
						1:name=systemd:/system.slice/docker-2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882.scope`,
					containerID:   "2faf78372d542468d4616d7cb85f03994a1d7ea60a42749e7114c506b8282882",
					containerName: "testdata_rabbitmqExposed_1",
				},
			},
		},
		{
			name: "Host of minikube",
			dir:  "testdata/minikube-host",
			wants: []check{
				{
					name: "init",
					cgroupData: `
						12:cpuset:/
						11:memory:/
						10:pids:/
						9:freezer:/
						8:blkio:/
						7:net_cls,net_prio:/
						6:perf_event:/
						5:rdma:/
						4:devices:/
						3:hugetlb:/
						2:cpu,cpuacct:/
						1:name=systemd:/init.scope
						0::/init.scope`,
					containerID: "",
				},
				{
					name: "bash on Ubuntu",
					cgroupData: `
						12:cpuset:/
						11:memory:/user.slice/user-1000.slice/user@1000.service
						10:pids:/user.slice/user-1000.slice/user@1000.service
						9:freezer:/
						8:blkio:/user.slice
						7:net_cls,net_prio:/
						6:perf_event:/
						5:rdma:/
						4:devices:/user.slice
						3:hugetlb:/
						2:cpu,cpuacct:/user.slice
						1:name=systemd:/user.slice/user-1000.slice/user@1000.service/gnome-launched-code.desktop-68023.scope
						0::/user.slice/user-1000.slice/user@1000.service/gnome-launched-code.desktop-68023.scope`,
				},
				{
					name: "redis",
					cgroupData: `
						12:cpuset:/docker/336cbb75226f60b65dec62e0e2aab86e4678a1da1f77013d40e13cc619e26363
						11:memory:/docker/336cbb75226f60b65dec62e0e2aab86e4678a1da1f77013d40e13cc619e26363
						10:pids:/docker/336cbb75226f60b65dec62e0e2aab86e4678a1da1f77013d40e13cc619e26363
						9:freezer:/docker/336cbb75226f60b65dec62e0e2aab86e4678a1da1f77013d40e13cc619e26363
						8:blkio:/docker/336cbb75226f60b65dec62e0e2aab86e4678a1da1f77013d40e13cc619e26363
						7:net_cls,net_prio:/docker/336cbb75226f60b65dec62e0e2aab86e4678a1da1f77013d40e13cc619e26363
						6:perf_event:/docker/336cbb75226f60b65dec62e0e2aab86e4678a1da1f77013d40e13cc619e26363
						5:rdma:/
						4:devices:/docker/336cbb75226f60b65dec62e0e2aab86e4678a1da1f77013d40e13cc619e26363
						3:hugetlb:/docker/336cbb75226f60b65dec62e0e2aab86e4678a1da1f77013d40e13cc619e26363
						2:cpu,cpuacct:/docker/336cbb75226f60b65dec62e0e2aab86e4678a1da1f77013d40e13cc619e26363
						1:name=systemd:/docker/336cbb75226f60b65dec62e0e2aab86e4678a1da1f77013d40e13cc619e26363
						0::/system.slice/containerd.service`,
					containerID:   "336cbb75226f60b65dec62e0e2aab86e4678a1da1f77013d40e13cc619e26363",
					containerName: "bleemeo-redis",
				},
				{
					name: "init of minikube",
					cgroupData: `
						12:cpuset:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2
						11:memory:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
						10:pids:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
						9:freezer:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2
						8:blkio:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
						7:net_cls,net_prio:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2
						6:perf_event:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2
						5:rdma:/
						4:devices:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
						3:hugetlb:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2
						2:cpu,cpuacct:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
						1:name=systemd:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
						0::/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope`,
					containerID:   "72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2",
					containerName: "minikube",
				},
				// TODO: For now, docker-in-docker does not work perfectly
				// {
				// 	name: "rabbitmq of minikube",
				// 	cgroupData: `
				// 		12:cpuset:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/0960ba858a48d7f15b1b5d0a278d62fb025d4d4ec1a0bae0f1df253bffc1253b
				// 		11:memory:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/0960ba858a48d7f15b1b5d0a278d62fb025d4d4ec1a0bae0f1df253bffc1253b
				// 		10:pids:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/0960ba858a48d7f15b1b5d0a278d62fb025d4d4ec1a0bae0f1df253bffc1253b
				// 		9:freezer:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/0960ba858a48d7f15b1b5d0a278d62fb025d4d4ec1a0bae0f1df253bffc1253b
				// 		8:blkio:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/0960ba858a48d7f15b1b5d0a278d62fb025d4d4ec1a0bae0f1df253bffc1253b
				// 		7:net_cls,net_prio:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/0960ba858a48d7f15b1b5d0a278d62fb025d4d4ec1a0bae0f1df253bffc1253b
				// 		6:perf_event:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/0960ba858a48d7f15b1b5d0a278d62fb025d4d4ec1a0bae0f1df253bffc1253b
				// 		5:rdma:/
				// 		4:devices:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/0960ba858a48d7f15b1b5d0a278d62fb025d4d4ec1a0bae0f1df253bffc1253b
				// 		3:hugetlb:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/0960ba858a48d7f15b1b5d0a278d62fb025d4d4ec1a0bae0f1df253bffc1253b
				// 		2:cpu,cpuacct:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/0960ba858a48d7f15b1b5d0a278d62fb025d4d4ec1a0bae0f1df253bffc1253b
				// 		1:name=systemd:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/docker/0960ba858a48d7f15b1b5d0a278d62fb025d4d4ec1a0bae0f1df253bffc1253b
				// 		0::/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/system.slice/containerd.service`,
				// 	containerID:   "72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2",
				// 	containerName: "minikube",
				// },
				{
					name: "unexisting id",
					cgroupData: `
						12:cpuset:/docker/5913ec626ab91bcdbfecb964c4455f5de10ceccf6ce4642ca7efed97a8b07766
						11:memory:/docker/5913ec626ab91bcdbfecb964c4455f5de10ceccf6ce4642ca7efed97a8b07766
						10:pids:/docker/5913ec626ab91bcdbfecb964c4455f5de10ceccf6ce4642ca7efed97a8b07766
						9:freezer:/docker/5913ec626ab91bcdbfecb964c4455f5de10ceccf6ce4642ca7efed97a8b07766
						8:blkio:/docker/5913ec626ab91bcdbfecb964c4455f5de10ceccf6ce4642ca7efed97a8b07766
						7:net_cls,net_prio:/docker/5913ec626ab91bcdbfecb964c4455f5de10ceccf6ce4642ca7efed97a8b07766
						6:perf_event:/docker/5913ec626ab91bcdbfecb964c4455f5de10ceccf6ce4642ca7efed97a8b07766
						5:rdma:/
						4:devices:/docker/5913ec626ab91bcdbfecb964c4455f5de10ceccf6ce4642ca7efed97a8b07766
						3:hugetlb:/docker/5913ec626ab91bcdbfecb964c4455f5de10ceccf6ce4642ca7efed97a8b07766
						2:cpu,cpuacct:/docker/5913ec626ab91bcdbfecb964c4455f5de10ceccf6ce4642ca7efed97a8b07766
						1:name=systemd:/docker/5913ec626ab91bcdbfecb964c4455f5de10ceccf6ce4642ca7efed97a8b07766
						0::/system.slice/containerd.service`,
					containerID:         "",
					containerName:       "",
					mustErrDoesNotExist: true,
				},
			},
		},
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			cl, err := NewDockerMock(tt.dir)
			if err != nil {
				t.Error(err)
				return
			}

			d := FakeDocker(cl)

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			for _, c := range tt.wants {
				c := c

				t.Run(c.name, func(t *testing.T) {
					querier := d.ProcessWithCache()
					container, err := querier.ContainerFromCGroup(ctx, unindent(c.cgroupData))

					if c.mustErrDoesNotExist && !errors.Is(err, facts.ErrContainerDoesNotExists) {
						t.Errorf("err = %v want ErrContainerDoesNotExists", err)
					}

					if c.containerID == "" {
						if container != nil {
							t.Errorf("container.ID = %v (err=%v) want no container", container.ID(), err)
						}

						return
					}

					if err != nil {
						t.Errorf("err = %v want nil", err)
					}
					if container == nil {
						t.Errorf("container = nil, want %s", c.containerName)

						return
					}

					if container.ContainerName() != c.containerName {
						t.Errorf("ContainerName = %v, want %s", container.ContainerName(), c.containerName)
					}
					if container.ID() != c.containerID {
						t.Errorf("ID = %v, want %s", container.ID(), c.containerID)
					}
				})
			}
		})
	}
}

func TestDocker_Processes(t *testing.T) {
	tests := []struct {
		name                 string
		dir                  string
		top                  map[string]string
		topWaux              map[string]string
		wantProcesses        []facts.Process
		wantContainerFromPID map[int]facts.FakeContainer
		notContainerForPID   []int
	}{
		{
			name:          "docker-20.10 (no top)",
			dir:           "testdata/docker-20.10.0",
			wantProcesses: []facts.Process{},
		},
		{
			name: "docker-20.10",
			dir:  "testdata/docker-20.10.0",
			top: map[string]string{
				"33600bb7b4d62f43e87839e514a4235bb72f66dcfca35a7df5c900361a2c4d6e": `
					UID                 PID                 PPID                C                   STIME               TTY                 TIME                CMD
					999                 4001                3906                0                   10:25               ?                   00:00:10            /bin/sh /opt/rabbitmq/sbin/rabbitmq-server
					999                 4511                4001                0                   10:25               ?                   00:00:00            /usr/local/lib/erlang/erts-11.1.5/bin/epmd -daemon`,
				"b59746cf51fa8b08eb228e5f4fc4bc28446a6f7ca19cdc3c23016f932b56003f": `
					UID                 PID                 PPID                C                   STIME               TTY                 TIME                CMD
					999                 3863                3840                0                   10:25               ?                   00:01:00            /bin/sh /opt/rabbitmq/sbin/rabbitmq-server
					999                 4471                3863                0                   10:25               ?                   00:00:00            /usr/local/lib/erlang/erts-11.1.5/bin/epmd -daemon`,
			},
			topWaux: map[string]string{
				"33600bb7b4d62f43e87839e514a4235bb72f66dcfca35a7df5c900361a2c4d6e": `
					USER                PID                 %CPU                %MEM                VSZ                 RSS                 TTY                 STAT                START               TIME                COMMAND
					999                 4001                1.0                 0.0                 4632                836                 ?                   Ss                  10:25               0:00                /bin/sh /opt/rabbitmq/sbin/rabbitmq-server
					999                 4511                0.0                 0.0                 8408                1444                ?                   R                   10:25               0:00                /usr/local/lib/erlang/erts-11.1.5/bin/epmd -daemon`,
			},
			wantProcesses: []facts.Process{
				{
					PID:           4001,
					PPID:          3906,
					CmdLine:       "/bin/sh /opt/rabbitmq/sbin/rabbitmq-server",
					CmdLineList:   []string{"/bin/sh", "/opt/rabbitmq/sbin/rabbitmq-server"},
					CPUPercent:    1.0,
					CPUTime:       10,
					MemoryRSS:     836,
					Name:          "sh",
					Username:      "999",
					Status:        "sleeping",
					ContainerID:   "33600bb7b4d62f43e87839e514a4235bb72f66dcfca35a7df5c900361a2c4d6e",
					ContainerName: "testdata_rabbitLabels_1",
				},
				{
					PID:           4511,
					PPID:          4001,
					CPUPercent:    0,
					CPUTime:       0,
					CmdLine:       "/usr/local/lib/erlang/erts-11.1.5/bin/epmd -daemon",
					CmdLineList:   []string{"/usr/local/lib/erlang/erts-11.1.5/bin/epmd", "-daemon"},
					MemoryRSS:     1444,
					Name:          "epmd",
					Status:        "running",
					Username:      "999",
					ContainerID:   "33600bb7b4d62f43e87839e514a4235bb72f66dcfca35a7df5c900361a2c4d6e",
					ContainerName: "testdata_rabbitLabels_1",
				},
				{
					PID:           3863,
					PPID:          3840,
					CPUTime:       60,
					CmdLine:       "/bin/sh /opt/rabbitmq/sbin/rabbitmq-server",
					CmdLineList:   []string{"/bin/sh", "/opt/rabbitmq/sbin/rabbitmq-server"},
					Name:          "sh",
					Status:        "?",
					Username:      "999",
					ContainerID:   "b59746cf51fa8b08eb228e5f4fc4bc28446a6f7ca19cdc3c23016f932b56003f",
					ContainerName: "testdata_rabbitmqInternal_1",
				},
				{
					PID:           4471,
					PPID:          3863,
					CmdLine:       "/usr/local/lib/erlang/erts-11.1.5/bin/epmd -daemon",
					CmdLineList:   []string{"/usr/local/lib/erlang/erts-11.1.5/bin/epmd", "-daemon"},
					Name:          "epmd",
					Status:        "?",
					Username:      "999",
					ContainerID:   "b59746cf51fa8b08eb228e5f4fc4bc28446a6f7ca19cdc3c23016f932b56003f",
					ContainerName: "testdata_rabbitmqInternal_1",
				},
			},
			wantContainerFromPID: map[int]facts.FakeContainer{
				4471: {
					FakeContainerName: "testdata_rabbitmqInternal_1",
					FakeState:         facts.ContainerRunning,
					FakeImageName:     "rabbitmq",
				},
				4511: {
					FakeContainerName: "testdata_rabbitLabels_1",
					FakeState:         facts.ContainerRunning,
					FakeImageName:     "rabbitmq",
				},
				3863: {
					FakeContainerName: "testdata_rabbitmqInternal_1",
				},
			},
			notContainerForPID: []int{1, 42, 9999},
		},
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			cl, err := NewDockerMock(tt.dir)
			if err != nil {
				t.Error(err)

				return
			}

			cl.Top = make(map[string]containerTypes.ContainerTopOKBody)
			cl.TopWaux = make(map[string]containerTypes.ContainerTopOKBody)

			for id, s := range tt.top {
				cl.Top[id] = string2TopBody(s)
			}

			for id, s := range tt.topWaux {
				cl.TopWaux[id] = string2TopBody(s)
			}

			d := FakeDocker(cl)

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			querier := d.ProcessWithCache()
			procs, err := querier.Processes(ctx)

			if err != nil {
				t.Error(err)
			}

			sort.Slice(procs, func(i, j int) bool {
				return procs[i].PID < procs[j].PID
			})
			sort.Slice(tt.wantProcesses, func(i, j int) bool {
				return tt.wantProcesses[i].PID < tt.wantProcesses[j].PID
			})

			if diff := cmp.Diff(tt.wantProcesses, procs); diff != "" {
				t.Errorf("procs diff: %v", diff)
			}

			for pid, c := range tt.wantContainerFromPID {
				got, err := querier.ContainerFromPID(ctx, "", pid)
				if err != nil {
					t.Error(err)

					return
				}

				if diff := c.Diff(got); diff != "" {
					t.Errorf("ContainerFromPID(%d]): %v", pid, diff)
				}

				// yes we always pass the SAME containerID. This container ID is only a hint, wrong value should be an issue.
				got, err = querier.ContainerFromPID(ctx, "b59746cf51fa8b08eb228e5f4fc4bc28446a6f7ca19cdc3c23016f932b56003f", pid)
				if err != nil {
					t.Error(err)

					return
				}

				if diff := c.Diff(got); diff != "" {
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

// TestContainer_ListenAddresses check that listen addresses return something correct.
//
// The following container are used
// docker run -d --name noport busybox sleep 99d
// docker run -d --name my_nginx -p 8080:80 nginx
// docker run -d --name my_redis redis
// docker run -d --name multiple-port -p 5672:5672 rabbitmq
// docker run -d --name multiple-port2 rabbitmq
// docker run -d --name non-standard-port -p 4242:4343 -p 1234:1234 rabbitmq.
func TestContainer_ListenAddresses(t *testing.T) {
	docker1_13_1, err := NewDockerMockFromFile("testdata/docker-v1.13.1.json")
	if err != nil {
		t.Fatal(err)
	}

	docker19_03, err := NewDockerMockFromFile("testdata/docker-v19.03.5.json")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name          string
		dockerClient  *MockDockerClient
		containerName string
		want          []facts.ListenAddress
		wantExplicit  bool
	}{
		{
			name:          "docker-noport",
			dockerClient:  docker19_03,
			containerName: "noport",
			want:          []facts.ListenAddress{},
		},
		{
			name:          "docker-oneport",
			dockerClient:  docker19_03,
			containerName: "my_redis",
			want: []facts.ListenAddress{
				{Address: "172.17.0.3", NetworkFamily: "tcp", Port: 6379},
			},
		},
		{
			name:          "docker-oneport-exposed",
			dockerClient:  docker19_03,
			containerName: "my_nginx",
			want: []facts.ListenAddress{
				{Address: "172.17.0.2", NetworkFamily: "tcp", Port: 80},
			},
			wantExplicit: true,
		},
		{
			name:          "docker-multiple-port-one-exposed",
			dockerClient:  docker19_03,
			containerName: "multiple-port",
			want: []facts.ListenAddress{
				{Address: "172.17.0.5", NetworkFamily: "tcp", Port: 5672},
			},
			wantExplicit: true,
		},
		{
			name:          "docker-multiple-port-none-exposed",
			dockerClient:  docker19_03,
			containerName: "multiple-port2",
			want: []facts.ListenAddress{
				{Address: "172.17.0.6", NetworkFamily: "tcp", Port: 4369},
				{Address: "172.17.0.6", NetworkFamily: "tcp", Port: 5671},
				{Address: "172.17.0.6", NetworkFamily: "tcp", Port: 5672},
				{Address: "172.17.0.6", NetworkFamily: "tcp", Port: 25672},
			},
			wantExplicit: false,
		},
		{
			name:          "docker-multiple-port-other-exposed",
			dockerClient:  docker19_03,
			containerName: "non-standard-port",
			want: []facts.ListenAddress{
				{Address: "172.17.0.7", NetworkFamily: "tcp", Port: 1234},
				{Address: "172.17.0.7", NetworkFamily: "tcp", Port: 4343},
			},
			wantExplicit: true,
		},
		{
			name:          "docker-multiple-port-one-exposed",
			dockerClient:  docker1_13_1,
			containerName: "multiple-port",
			want: []facts.ListenAddress{
				{Address: "172.17.0.3", NetworkFamily: "tcp", Port: 5672},
			},
			wantExplicit: true,
		},
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			d := FakeDocker(tt.dockerClient)

			containers, err := d.Containers(ctx, 0, false)
			if err != nil {
				t.Error(err)
			}

			var container facts.Container

			for _, c := range containers {
				if c.ContainerName() == tt.containerName {
					container = c
					break
				}
			}

			if container == nil {
				t.Errorf("container %s not found", tt.containerName)

				return
			}

			if got, explicit := container.ListenAddresses(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Container.ListenAddresses() = %v, want %v", got, tt.want)
			} else if explicit != tt.wantExplicit {
				t.Errorf("Container.ListenAddresses() explicit = %v, want %v", explicit, tt.wantExplicit)
			}
		})
	}
}

func TestDecodeDocker(t *testing.T) {
	// Test case are generated using:
	// docker run --rm -ti --name test \
	//     -v /var/run/docker.sock:/var/run/docker.sock \
	//     bleemeo/bleemeo-agent \
	//     python3 -c 'import docker;
	//     print(docker.APIClient(version="1.21").top("test"))'
	cases := []containerTypes.ContainerTopOKBody{
		// Boot2Docker 1.12.3 first boot
		{
			Processes: [][]string{
				{
					"3216", "root",
					"python3 -c import docker;print(docker.Client(version=\"1.21\").top(\"test\"))",
				},
			},
			Titles: []string{"PID", "USER", "COMMAND"},
		},
		// Boot2Docker 1.12.3 second boot
		{
			Titles: []string{
				"UID", "PID", "PPID", "C", "STIME", "TTY", "TIME", "CMD",
			},
			Processes: [][]string{
				{
					"root", "1551", "1542", "0", "14:13", "pts/1", "00:00:00",
					"python3 -c import docker;print(docker.Client(version=\"1.21\").top(\"test\"))",
				},
			},
		},
		// Ubuntu 16.04
		{
			Processes: [][]string{
				{
					"root", "5017", "4988", "0", "15:15", "pts/29", "00:00:00",
					"python3 -c import docker;print(docker.Client(version=\"1.21\").top(\"test\"))"},
			},
			Titles: []string{
				"UID", "PID", "PPID", "C", "STIME", "TTY", "TIME", "CMD",
			},
		},
		// With ps_args="waux" added. On Ubuntu 18.04
		{
			Processes: [][]string{
				{"root", "28554", "39.0", "0.1", "85640", "28496", "pts/0", "Ss+", "11:43", "0:00", "python3 -c import docker;print(docker.APIClient(version=\"1.21\").top(\"test\", ps_args=\"waux\"))"},
			},
			Titles: []string{"USER", "PID", "%CPU", "%MEM", "VSZ", "RSS", "TTY", "STAT", "START", "TIME", "COMMAND"}},
	}
	for i, c := range cases {
		got := decodeDocker(c, facts.FakeContainer{FakeID: "theDockerID", FakeContainerName: "theDockerName"})
		if len(got) != 1 {
			t.Errorf("Case #%v: len(got) == %v, want 1", i, len(got))
		}

		if got[0].CmdLineList[0] != "python3" {
			t.Errorf("Case #%v: CmdLine[0] == %v, want %v", i, got[0].CmdLineList[0], "python3")
		}

		if got[0].ContainerID != "theDockerID" {
			t.Errorf("Case #%v: ContainerID == %v, want %v", i, got[0].ContainerID, "theDockerID")
		}
	}
}

func TestPsTime2Second(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"00:16:42", 16*60 + 42},
		{"16:42", 16*60 + 42},
		{"1-02:27:14", 24*3600 + 2*3600 + 27*60 + 14},
		{"1587:14", 1587*60 + 14},

		// busybox time
		{"12h27", 12*3600 + 27*60},
		{"6d09", 6*24*3600 + 9*3600},
		{"18d12", 18*24*3600 + 12*3600},
	}
	for _, c := range cases {
		got, err := psTime2Second(c.in)
		if err != nil {
			t.Errorf("psTime2second(%#v) raise %v", c.in, err)
		}

		if got != c.want {
			t.Errorf("psTime2second(%#v) == %v, want %v", c.in, got, c.want)
		}
	}
}
