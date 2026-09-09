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
	"testing"

	"github.com/bleemeo/glouton/facts"
)

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

// TestVarnishTarget covers the choice between the two ways of reading a Varnish: the
// container's own varnishstat, or the machine's aimed at the container's instance.
//
// The container's own comes first because it needs nothing installed on the machine, which
// is the case that used to produce no metrics at all.
func TestVarnishTarget(t *testing.T) {
	const pid = 4242

	// A container carrying both the daemon and the tools, as the official image does,
	// with a running instance in the current default directory.
	withVarnishStat := mockFileReader{
		dirs: map[string][]string{
			"/proc/4242/root/usr/bin":         {"varnishadm", "varnishd", "varnishstat"},
			"/proc/4242/root/var/lib/varnish": {"varnishd"},
		},
		contents: map[string]string{
			"/proc/4242/root/var/lib/varnish/varnishd/_.pid": "1\n",
		},
	}

	// One carrying only the daemon, which has to be read the other way.
	daemonOnly := map[string][]string{
		"/proc/4242/root/usr/bin":         {"varnishd"},
		"/proc/4242/root/var/lib/varnish": {"varnishd"},
	}

	cases := []struct {
		testName     string
		service      Service
		reader       fileReader
		wantPID      int
		wantInstance string
		wantOK       bool
	}{
		{
			testName: "host varnish is read as it always was",
			service: Service{ //nolint:exhaustruct
				Name:        string(VarnishService),
				ServiceType: VarnishService,
			},
			reader:       mockFileReader{contents: nil, dirs: nil},
			wantPID:      0,
			wantInstance: "",
			wantOK:       true,
		},
		{
			// Its own binary, and the directory named as the container sees it: the /proc
			// form does not resolve inside the chroot varnishstat runs in.
			testName:     "container with varnishstat runs its own",
			service:      varnishInstanceService(pid),
			reader:       withVarnishStat,
			wantPID:      pid,
			wantInstance: "/var/lib/varnish/varnishd",
			wantOK:       true,
		},
		{
			// The directory is located before choosing a binary, so a container carrying
			// varnishstat but running no instance is not gathered -- rather than gathered
			// forever against an instance directory that does not exist.
			testName: "container with varnishstat but no running instance",
			service:  varnishInstanceService(pid),
			reader: mockFileReader{
				contents: nil,
				dirs: map[string][]string{
					"/proc/4242/root/usr/bin":         {"varnishd", "varnishstat"},
					"/proc/4242/root/var/lib/varnish": {"buildkitsandbox"},
				},
			},
			wantPID:      0,
			wantInstance: "",
			wantOK:       false,
		},
		{
			testName: "container without varnishstat falls back to -n",
			service:  varnishInstanceService(pid),
			reader: mockFileReader{
				dirs: daemonOnly,
				contents: map[string]string{
					"/proc/4242/root/var/lib/varnish/varnishd/_.pid": "1\n",
				},
			},
			wantPID:      0,
			wantInstance: "/proc/4242/root/var/lib/varnish/varnishd",
			wantOK:       true,
		},
		{
			// Neither way can work: no binary in the container, and no instance to aim
			// the machine's binary at.
			testName:     "container with neither is not gathered",
			service:      varnishInstanceService(pid),
			reader:       mockFileReader{contents: nil, dirs: daemonOnly},
			wantPID:      0,
			wantInstance: "",
			wantOK:       false,
		},
		{
			testName:     "container with no pid is not gathered",
			service:      varnishInstanceService(0),
			reader:       withVarnishStat,
			wantPID:      0,
			wantInstance: "",
			wantOK:       false,
		},
	}

	for _, c := range cases {
		t.Run(c.testName, func(t *testing.T) {
			d := &Discovery{fileReader: c.reader} //nolint:exhaustruct

			gotPID, gotInstance, gotOK := d.varnishTarget(c.service)

			if gotPID != c.wantPID {
				t.Errorf("varnishTarget() pid = %d, want %d", gotPID, c.wantPID)
			}

			if gotInstance != c.wantInstance {
				t.Errorf("varnishTarget() instance = %q, want %q", gotInstance, c.wantInstance)
			}

			if gotOK != c.wantOK {
				t.Errorf("varnishTarget() ok = %v, want %v", gotOK, c.wantOK)
			}
		})
	}
}

// TestVarnishInstanceDir covers locating the working directory of a containerised
// Varnish, named as the container itself sees it.
//
// It is recognised by the _.pid varnishd writes in it rather than by its name, so this
// does not have to know which naming scheme the running version uses -- nor whether the
// image started varnishd with a "-n" of its own.
func TestVarnishInstanceDir(t *testing.T) {
	const pid = 4242

	cases := []struct {
		testName string
		reader   fileReader
		want     string
		wantOK   bool
	}{
		{
			// Where the current release puts it: a subdirectory named after the daemon.
			testName: "instance in the default subdirectory",
			reader: mockFileReader{
				dirs: map[string][]string{
					"/proc/4242/root/var/lib/varnish": {"varnishd"},
				},
				contents: map[string]string{
					"/proc/4242/root/var/lib/varnish/varnishd/_.pid": "1\n",
				},
			},
			want:   "/var/lib/varnish/varnishd",
			wantOK: true,
		},
		{
			// An image that starts varnishd with "-n /var/lib/varnish": the state
			// directory is the instance directory, and what is in it are the instance's
			// own files rather than one directory per instance. Looking only one level
			// down would find nothing here and gather nothing.
			testName: "instance in the state directory itself",
			reader: mockFileReader{
				dirs: nil,
				contents: map[string]string{
					"/proc/4242/root/var/lib/varnish/_.pid": "1\n",
				},
			},
			want:   "/var/lib/varnish",
			wantOK: true,
		},
		{
			// Two instances would be ambiguous; the state directory is the one varnishd
			// was told to use, so it wins.
			testName: "the state directory wins over a subdirectory",
			reader: mockFileReader{
				dirs: map[string][]string{
					"/proc/4242/root/var/lib/varnish": {"varnishd"},
				},
				contents: map[string]string{
					"/proc/4242/root/var/lib/varnish/_.pid":          "1\n",
					"/proc/4242/root/var/lib/varnish/varnishd/_.pid": "2\n",
				},
			},
			want:   "/var/lib/varnish",
			wantOK: true,
		},
		{
			// The official image ships a directory named after the host that built it,
			// left over and empty. Taking directories in order would pick it.
			testName: "a leftover directory is not an instance",
			reader: mockFileReader{
				contents: nil,
				dirs: map[string][]string{
					"/proc/4242/root/var/lib/varnish": {"buildkitsandbox"},
				},
			},
			want:   "",
			wantOK: false,
		},
		{
			testName: "state directory that can't be listed",
			reader:   mockFileReader{contents: nil, dirs: nil},
			want:     "",
			wantOK:   false,
		},
		{
			testName: "no file reader",
			reader:   nil,
			want:     "",
			wantOK:   false,
		},
	}

	for _, c := range cases {
		t.Run(c.testName, func(t *testing.T) {
			d := &Discovery{fileReader: c.reader} //nolint:exhaustruct

			got, gotOK := d.varnishInstanceDir(varnishInstanceService(pid))

			if got != c.want {
				t.Errorf("varnishInstanceDir() directory = %q, want %q", got, c.want)
			}

			if gotOK != c.wantOK {
				t.Errorf("varnishInstanceDir() found = %v, want %v", gotOK, c.wantOK)
			}
		})
	}
}

// TestServiceNeedUpdateOnContainerRestart checks a restarted container is seen as needing
// its inputs rebuilt.
//
// Nothing else in the comparison catches it: the container keeps its ID and its name, and
// a compose network hands back the same IP, while the container-event debounce can
// coalesce the stop and the start into one discovery that sees it running both times. Only
// the PID changes -- and the Varnish and Postfix inputs hold a /proc/<pid> path, so missing
// this leaves them reading a process that no longer exists.
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
