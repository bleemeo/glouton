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

//go:build !windows

package veth

import (
	"testing"

	"github.com/bleemeo/glouton/utils/gloutonexec"
)

// TestErrorKeepsEveryStderrLine checks an error of glouton-veths keeps every line of its stderr,
// one per container it could not read: the runner adds only the first.
func TestErrorKeepsEveryStderrLine(t *testing.T) {
	runner := gloutonexec.New("/")
	script := "echo 'nsenter: cannot open /proc/12/ns/net' >&2; echo 'nsenter: cannot open /proc/34/ns/net' >&2; exit 1"

	_, err := runner.Run(t.Context(), gloutonexec.Option{}, "sh", "-c", script)

	want := "exit status 1: nsenter: cannot open /proc/12/ns/net\nnsenter: cannot open /proc/34/ns/net"
	if got := withOtherStderrLines(err).Error(); got != want {
		t.Errorf("error = %q, want %q", got, want)
	}

	_, err = runner.Run(t.Context(), gloutonexec.Option{}, "sh", "-c", "echo 'only one' >&2; exit 1")

	if got := withOtherStderrLines(err).Error(); got != "exit status 1: only one" {
		t.Errorf("with one stderr line, error = %q", got)
	}
}
