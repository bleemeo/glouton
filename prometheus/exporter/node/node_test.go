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

package node

import (
	"reflect"
	"regexp"
	"testing"
	"unsafe"

	"github.com/bleemeo/glouton/config"

	"github.com/alecthomas/kingpin/v2"
)

// Prevent gofmt from removing "unsafe", //go:linkname is only allowed in Go files that import "unsafe".
var _ unsafe.Pointer

//go:linkname rootfsStripPrefix github.com/prometheus/node_exporter/collector.rootfsStripPrefix
func rootfsStripPrefix(path string) string

// Test_optionsToFlags ensure the option still existing in node_exporter.
func Test_optionsToFlags(t *testing.T) {
	tests := []struct {
		name   string
		option Option
	}{
		{
			name:   "empty-option",
			option: Option{},
		},
		{
			name: "full-option",
			option: Option{
				RootFS:                       "something",
				FilesystemIgnoredMountPoints: "something",
				FilesystemIgnoredType:        "something",
				NetworkIgnoredDevices:        "something",
				DiskStatsIgnoredDevices:      "something",
				EnabledCollectors:            []string{"something"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := optionsToFlags(tt.option)

			for k := range got {
				if flag := kingpin.CommandLine.GetFlag(k); flag == nil {
					t.Errorf("flag --%s does not exists", k)
				}
			}
		})
	}
}

// Test_newCollector ensure that settings kingpin options works.
func Test_newCollector(t *testing.T) {
	// We can't have multiple test case. Parsing argument only works once (at very least
	// second call to Parse() won't do the same, because some code are already initialized).
	fullOptions := Option{
		RootFS:                       "/hostroot",
		FilesystemIgnoredMountPoints: "something",
		FilesystemIgnoredType:        "something",
		NetworkIgnoredDevices:        "something",
		DiskStatsIgnoredDevices:      "something",
		EnabledCollectors:            config.DefaultConfig().Agent.NodeExporter.Collectors,
	}

	c, err := newCollector(fullOptions)
	if err != nil {
		t.Fatal(err)
	}

	fsCollectors := c.Collectors["filesystem"]
	if fsCollectors == nil {
		t.Error("filesystem collectors isn't present")
	} else {
		value := reflect.ValueOf(fsCollectors)
		value = value.Elem()

		mountPointFilter := value.FieldByName("mountPointFilter")
		if mountPointFilter.Type().String() != "collector.deviceFilter" {
			t.Errorf("mountPointFilter is a %s, want a collector.deviceFilter", mountPointFilter.Type())
		}

		ignorePattern := mountPointFilter.FieldByName("ignorePattern")
		if ignorePattern.Type() != reflect.TypeFor[*regexp.Regexp]() {
			t.Errorf("ignorePattern is a %s, want a *regexp.Regexp", ignorePattern.Type())
		}

		mountPointFilterIgnorePattern := (*regexp.Regexp)(ignorePattern.UnsafePointer())
		if mountPointFilterIgnorePattern.String() != fullOptions.FilesystemIgnoredMountPoints {
			t.Errorf("mountPointFilterIgnorePattern = %s, want a %s", mountPointFilterIgnorePattern.String(), fullOptions.FilesystemIgnoredMountPoints)
		}
	}

	got := rootfsStripPrefix("/hostroot/var/lib")
	if got != "/var/lib" {
		t.Errorf("rootfsStripPrefix=%s, want /var/lib", got)
	}
}
