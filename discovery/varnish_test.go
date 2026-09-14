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

package discovery

import (
	"errors"
	"testing"

	"github.com/bleemeo/glouton/facts"

	"github.com/google/go-cmp/cmp"
)

// errNoSuchBinary is what a runtime answers for a container carrying no varnishstat.
var errNoSuchBinary = errors.New("exec: \"varnishstat\": executable file not found in $PATH")

// varnishInstanceService builds the containerised Varnish service the cases below differ
// on, with pid as the container's init process.
func varnishInstanceService(pid int) Service {
	return Service{ //nolint:exhaustruct
		Name:        string(VarnishService),
		Instance:    "test-varnish",
		ServiceType: VarnishService,
		ContainerID: "varnish1",
		container:   facts.FakeContainer{FakePID: pid}, //nolint:exhaustruct
	}
}

// TestCanReadVarnish covers whether a Varnish can be read at all, which for a
// containerised one means asking its own varnishstat to run.
//
// "varnishstat -V" is the probe: it prints the version and exits without needing a running
// instance, so it answers whether the binary is there and runnable without reading any
// statistics. An image carrying only varnishd -- plenty do -- gets no input rather than one
// failing on every gather.
func TestCanReadVarnish(t *testing.T) {
	const pid = 4242

	cases := []struct {
		testName string
		service  Service
		exec     func(containerID string, cmd []string) ([]byte, error)
		want     bool
	}{
		{
			// A Varnish installed on the machine is read with the machine's binary, so
			// there is nothing to ask a container about.
			testName: "host varnish needs no probe",
			service: Service{ //nolint:exhaustruct
				Name:        string(VarnishService),
				ServiceType: VarnishService,
			},
			exec: func(_ string, _ []string) ([]byte, error) {
				t.Error("canReadVarnish() ran a command in a container for a host service")

				return nil, nil
			},
			want: true,
		},
		{
			testName: "container carrying varnishstat is read",
			service:  varnishInstanceService(pid),
			exec: func(_ string, _ []string) ([]byte, error) {
				return []byte("varnishstat (varnish-7.1.1 revision abc)\n"), nil
			},
			want: true,
		},
		{
			testName: "container carrying only the daemon is not gathered",
			service:  varnishInstanceService(pid),
			exec: func(_ string, _ []string) ([]byte, error) {
				return nil, errNoSuchBinary
			},
			want: false,
		},
	}

	for _, c := range cases {
		t.Run(c.testName, func(t *testing.T) {
			d := &Discovery{ //nolint:exhaustruct
				containerInfo: mockContainerInfo{containers: nil, exec: c.exec},
			}

			if got := d.canReadVarnish(c.service); got != c.want {
				t.Errorf("canReadVarnish() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestCanReadVarnishProbesTheRightCommand pins what the probe actually runs. "-V" is the
// flag that answers without a running instance; "-1" would report a container whose
// varnishd is merely still starting as carrying no varnishstat at all.
func TestCanReadVarnishProbesTheRightCommand(t *testing.T) {
	var gotContainerID string

	var gotCmd []string

	d := &Discovery{ //nolint:exhaustruct
		containerInfo: mockContainerInfo{
			containers: nil,
			exec: func(containerID string, cmd []string) ([]byte, error) {
				gotContainerID, gotCmd = containerID, cmd

				return nil, nil
			},
		},
	}

	d.canReadVarnish(varnishInstanceService(4242))

	if gotContainerID != "varnish1" {
		t.Errorf("probed container %q, want %q", gotContainerID, "varnish1")
	}

	if want := []string{varnishStatBinary, "-V"}; !cmp.Equal(gotCmd, want) {
		t.Errorf("probed with %v, want %v", gotCmd, want)
	}
}

// TestServiceNeedUpdateOnContainerRestart checks a restarted container is seen as needing
// its inputs rebuilt.
//
// Nothing else in the comparison catches it: the container keeps its ID and its name, and
// a compose network hands back the same IP, while the container-event debounce can
// coalesce the stop and the start into one discovery that sees it running both times. Only
// the PID changes -- and the Postfix input holds a /proc/<pid> path, so missing
// this leaves it reading a process that no longer exists.
func TestServiceNeedUpdateOnContainerRestart(t *testing.T) {
	withPID := func(pid int) Service {
		service := varnishInstanceService(pid)
		service.IPAddress = "172.20.0.20"

		return service
	}

	before, after := withPID(4242), withPID(5353)

	if !serviceNeedUpdate(before, after, facts.ContainerRunning, facts.ContainerRunning) {
		t.Error("serviceNeedUpdate() = false after a restart, the inputs would keep the dead PID")
	}

	// And an unchanged container must not churn its inputs on every discovery run.
	if serviceNeedUpdate(before, withPID(4242), facts.ContainerRunning, facts.ContainerRunning) {
		t.Error("serviceNeedUpdate() = true for an unchanged container")
	}

	// A service with no container has no PID to compare, and must not look changed either.
	host := Service{Name: string(VarnishService), ServiceType: VarnishService} //nolint:exhaustruct

	if serviceNeedUpdate(host, host, facts.ContainerRunning, facts.ContainerRunning) {
		t.Error("serviceNeedUpdate() = true for an unchanged host service")
	}
}
