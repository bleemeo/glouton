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

// TestMakeCmdOnHost pins the command line a host-targeted run produces from inside the
// agent's container: without the chroot it would run in the agent's own filesystem, which
// carries none of the machine's binaries.
func TestMakeCmdOnHost(t *testing.T) {
	// As Glouton runs in its own container: hostroot mounted, and root inside it, so the
	// runner adds no sudo.
	r := &Runner{hostRootPath: "/hostroot", gloutonRunAsRoot: true}

	cmd, _, err := r.makeCmd(
		context.Background(),
		Option{RunAsRoot: true, RunOnHost: true}, //nolint:exhaustruct
		"/usr/bin/varnishstat", "-1",
	)
	if err != nil {
		t.Fatalf("makeCmd() = %v", err)
	}

	want := []string{"chroot", "/hostroot", "/usr/bin/varnishstat", "-1"}

	if !cmp.Equal(cmd.Args, want) {
		t.Errorf("command = %v, want %v", cmd.Args, want)
	}
}

// TestMakeCmdOnHostNeedsHostroot checks an unknown hostroot is refused rather than
// answered with Glouton's own filesystem, which would silently run against the wrong
// machine.
func TestMakeCmdOnHostNeedsHostroot(t *testing.T) {
	r := &Runner{hostRootPath: "", gloutonRunAsRoot: true}

	_, _, err := r.makeCmd(
		context.Background(),
		Option{RunOnHost: true}, //nolint:exhaustruct
		"/usr/bin/varnishstat", "-1",
	)

	if !errors.Is(err, ErrUnknownHostroot) {
		t.Errorf("makeCmd() = %v, want %v", err, ErrUnknownHostroot)
	}
}

// TestChrootPath covers which mount namespace a command is run in.
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
			// An unset hostroot means Glouton cannot tell where the machine's filesystem
			// is, so no directory is named: reading its own instead would silently answer
			// about the wrong machine. makeCmd turns this into ErrUnknownHostroot.
			testName:     "unknown hostroot, run on host",
			hostRootPath: "",
			option:       Option{RunOnHost: true}, //nolint:exhaustruct
			want:         "",
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
