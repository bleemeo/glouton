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
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestLsNames covers turning the output of "ls -1" back into entry names, which is how a
// directory is read when Glouton's own user may not read it.
func TestLsNames(t *testing.T) {
	cases := []struct {
		testName string
		output   string
		want     []string
	}{
		{
			testName: "one name per line",
			output:   "buildkitsandbox\nvarnishd\n",
			want:     []string{"buildkitsandbox", "varnishd"},
		},
		{
			// An empty directory prints nothing, which must read as no entries rather
			// than as one entry with an empty name -- that would be joined onto the
			// directory itself and probed as if it were a child.
			testName: "empty output has no entries",
			output:   "",
			want:     []string{},
		},
		{
			testName: "blank lines are not entries",
			output:   "\n\nvarnishd\n\n",
			want:     []string{"varnishd"},
		},
		{
			testName: "no trailing newline",
			output:   "varnishd",
			want:     []string{"varnishd"},
		},
	}

	for _, c := range cases {
		t.Run(c.testName, func(t *testing.T) {
			if got := lsNames([]byte(c.output)); !cmp.Equal(got, c.want) {
				t.Errorf("lsNames(%q) = %v, want %v", c.output, got, c.want)
			}
		})
	}
}

// TestSudoFileReaderNeedsHostRoot pins that both readers refuse to work without a
// hostroot: a Glouton in a container has no view of the machine's filesystem without one,
// and reading its own instead would answer about the wrong machine.
func TestSudoFileReaderNeedsHostRoot(t *testing.T) {
	reader := SudoFileReader{HostRootPath: "", Runner: nil}

	if _, err := reader.ReadFile(context.Background(), "/var/lib/varnish"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadFile() with no hostroot = %v, want %v", err, os.ErrNotExist)
	}

	if _, err := reader.ReadDir(context.Background(), "/var/lib/varnish"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadDir() with no hostroot = %v, want %v", err, os.ErrNotExist)
	}
}
