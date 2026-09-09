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

package gloutonexec

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestMakeCmdInContainer pins the command line a container-targeted run produces, which
// is the one thing that has to be right: the same shape read a containerised Varnish's
// counters by hand, and the shape without the chroot failed with "No such file or
// directory" on any machine with no Varnish installed.
func TestMakeCmdInContainer(t *testing.T) {
	// As Glouton runs in its own container: hostroot mounted, and root inside it, so the
	// runner adds no sudo.
	r := &Runner{hostRootPath: "/hostroot", gloutonRunAsRoot: true}

	cmd, _, err := r.makeCmd(
		context.Background(),
		Option{RunAsRoot: true, InContainerPID: 4242}, //nolint:exhaustruct
		"/usr/bin/varnishstat", "-1",
	)
	if err != nil {
		t.Fatalf("makeCmd() = %v", err)
	}

	want := []string{"chroot", "/hostroot/proc/4242/root", "/usr/bin/varnishstat", "-1"}

	if !cmp.Equal(cmd.Args, want) {
		t.Errorf("command = %v, want %v", cmd.Args, want)
	}
}

// TestMakeCmdInContainerNeedsHostroot checks an unknown hostroot is refused rather than
// answered with Glouton's own /proc, which would run the command against whatever
// container that PID happens to be in the agent's namespace -- or nothing at all.
func TestMakeCmdInContainerNeedsHostroot(t *testing.T) {
	r := &Runner{hostRootPath: "", gloutonRunAsRoot: true}

	_, _, err := r.makeCmd(
		context.Background(),
		Option{InContainerPID: 4242}, //nolint:exhaustruct
		"/usr/bin/varnishstat", "-1",
	)

	if !errors.Is(err, ErrUnknownHostroot) {
		t.Errorf("makeCmd() = %v, want %v", err, ErrUnknownHostroot)
	}
}

// TestChrootPath covers which mount namespace a command is run in.
//
// The case worth protecting is InContainerPID with hostRootPath "/": a Glouton installed
// on the machine chroots nowhere for RunOnHost, since it is already there, but a container
// is never the namespace it runs in -- so that one still has to chroot.
func TestChrootPath(t *testing.T) {
	cases := []struct {
		testName     string
		hostRootPath string
		option       Option
		want         string
	}{
		{
			testName:     "nothing asked for, no chroot",
			hostRootPath: "/hostroot",
			option:       Option{}, //nolint:exhaustruct
			want:         "",
		},
		{
			testName:     "glouton in a container, run on host",
			hostRootPath: "/hostroot",
			option:       Option{RunOnHost: true}, //nolint:exhaustruct
			want:         "/hostroot",
		},
		{
			// Already in the host's namespace, so there is nothing to enter.
			testName:     "glouton on the machine, run on host",
			hostRootPath: "/",
			option:       Option{RunOnHost: true}, //nolint:exhaustruct
			want:         "",
		},
		{
			testName:     "glouton in a container, run in a container",
			hostRootPath: "/hostroot",
			option:       Option{InContainerPID: 4242}, //nolint:exhaustruct
			want:         "/hostroot/proc/4242/root",
		},
		{
			// The container is a different namespace whether or not Glouton is in one.
			testName:     "glouton on the machine, run in a container",
			hostRootPath: "/",
			option:       Option{InContainerPID: 4242}, //nolint:exhaustruct
			want:         "/proc/4242/root",
		},
		{
			// Asking for both is asking for opposite namespaces. The container wins, as
			// the more specific of the two.
			testName:     "a container beats run on host",
			hostRootPath: "/hostroot",
			option:       Option{RunOnHost: true, InContainerPID: 4242}, //nolint:exhaustruct
			want:         "/hostroot/proc/4242/root",
		},
		{
			// An unset hostroot means Glouton cannot tell where the machine's filesystem
			// is, so no directory is named: reading its own /proc instead would silently
			// answer about the wrong machine. makeCmd turns this into ErrUnknownHostroot.
			testName:     "unknown hostroot, run in a container",
			hostRootPath: "",
			option:       Option{InContainerPID: 4242}, //nolint:exhaustruct
			want:         "",
		},
		{
			testName:     "a zero pid is not a container",
			hostRootPath: "/hostroot",
			option:       Option{InContainerPID: 0, RunOnHost: true}, //nolint:exhaustruct
			want:         "/hostroot",
		},
	}

	for _, c := range cases {
		t.Run(c.testName, func(t *testing.T) {
			r := &Runner{hostRootPath: c.hostRootPath} //nolint:exhaustruct

			if got := r.chrootPath(c.option); got != c.want {
				t.Errorf("chrootPath() = %q, want %q", got, c.want)
			}
		})
	}
}
