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
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestMakeCmdSudo pins the command lines the sudoers file has to allow: a sudoers rule
// matches the exact argv, so any change here must be reflected in glouton.sudoers.
func TestMakeCmdSudo(t *testing.T) {
	const wrapper = "/usr/lib/glouton/glouton-sudo-logs"

	cases := []struct {
		testName string
		runner   *Runner
		option   Option
		want     []string
	}{
		{
			testName: "no root needed",
			runner:   &Runner{hostRootPath: "/", gloutonRunAsRoot: false},
			option:   Option{SudoWrapper: wrapper}, //nolint:exhaustruct
			want:     []string{"stat", "--printf=%s", "/var/log/syslog"},
		},
		{
			testName: "sudo without wrapper",
			runner:   &Runner{hostRootPath: "/", gloutonRunAsRoot: false},
			option:   Option{RunAsRoot: true}, //nolint:exhaustruct
			want:     []string{"sudo", "-n", "stat", "--printf=%s", "/var/log/syslog"},
		},
		{
			testName: "sudo with wrapper",
			runner:   &Runner{hostRootPath: "/", gloutonRunAsRoot: false},
			option:   Option{RunAsRoot: true, SudoWrapper: wrapper}, //nolint:exhaustruct
			want:     []string{"sudo", "-n", wrapper, "stat", "--printf=%s", "/var/log/syslog"},
		},
		{
			// The wrapper is part of the package, it isn't there when Glouton runs as root.
			testName: "already root, wrapper unused",
			runner:   &Runner{hostRootPath: "/hostroot", gloutonRunAsRoot: true},
			option:   Option{RunAsRoot: true, SudoWrapper: wrapper}, //nolint:exhaustruct
			want:     []string{"stat", "--printf=%s", "/var/log/syslog"},
		},
		{
			// The wrapper only knows the plain command, not one wrapped in chroot.
			testName: "chroot, wrapper unused",
			runner:   &Runner{hostRootPath: "/hostroot", gloutonRunAsRoot: false},
			option:   Option{RunAsRoot: true, RunOnHost: true, SudoWrapper: wrapper}, //nolint:exhaustruct
			want:     []string{"sudo", "-n", "chroot", "/hostroot", "stat", "--printf=%s", "/var/log/syslog"},
		},
	}

	for _, c := range cases {
		t.Run(c.testName, func(t *testing.T) {
			cmd, _, err := c.runner.makeCmd(context.Background(), c.option, "stat", "--printf=%s", "/var/log/syslog")
			if err != nil {
				t.Fatalf("makeCmd() = %v", err)
			}

			if diff := cmp.Diff(c.want, cmd.Args); diff != "" {
				t.Errorf("command mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
