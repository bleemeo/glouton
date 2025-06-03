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

//go:build windows

package windows

import (
	"reflect"
	"regexp"
	"testing"
	"unsafe"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/inputs"

	"github.com/prometheus-community/windows_exporter/pkg/collector"
)

func compareRE(t *testing.T, gotRE *regexp.Regexp, wantRE *regexp.Regexp, values []string) {
	t.Helper()

	for _, value := range values {
		got := gotRE.MatchString(value)
		want := wantRE.MatchString(value)

		if got != want {
			t.Errorf("gotRE.Match(%s) = %v, want %v", value, got, want)
		}
	}
}

// Test_newCollector ensure that settings kingpin options works.
func Test_newCollector(t *testing.T) {
	diskFilter, err := config.NewDiskIOMatcher(config.Config{
		DiskIgnore: []string{"D:"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// We can't directly compare RE (i.e. volumeExcludePatternRE.String() value), because
	// windows_exporter will slightly change it (e.g. wrap the RE inside `(?:%s)`).
	// It result in RE that match the same thing, but can't compare it string representation,
	// so this is a list of value that must either match both REs or not match both REs.
	valuesToTest := []string{
		"C:",
		"D:",
		"eth0",
		"lo",
		"eth1",
	}

	// We can't have multiple test case. Parsing argument only works once (at very least
	// second call to Parse() won't do the same, because some code are already initialized).
	fullOptions := inputs.CollectorConfig{
		DFRootPath:      "/hostroot",
		DFPathMatcher:   config.NewDFPathMatcher(config.DefaultConfig()),
		DFIgnoreFSTypes: []string{"something"},
		NetIfMatcher:    config.NewNetworkInterfaceMatcher(config.Config{NetworkInterfaceDenylist: []string{"eth0"}}),
		IODiskMatcher:   diskFilter,
	}

	c, err := newCollector([]string{"logical_disk", "net"}, fullOptions)
	if err != nil {
		t.Fatal(err)
	}

	rc := reflect.ValueOf(&c).Elem()
	rf := rc.FieldByName("collectors")
	rf = reflect.NewAt(rf.Type(), unsafe.Pointer(rf.UnsafeAddr())).Elem()
	collectors := rf.Interface().(collector.Map) //nolint: forcetypeassert

	ldCollectors := collectors["logical_disk"]
	if ldCollectors == nil {
		t.Error("logical_disk collectors isn't present")
	} else {
		value := reflect.ValueOf(ldCollectors)
		value = value.Elem()

		volumeExcludePattern := value.FieldByName("volumeExcludePattern")
		if volumeExcludePattern.Type() != reflect.TypeOf(&regexp.Regexp{}) {
			t.Errorf("volumeExcludePattern is a %s, want a *regexp.Regexp", volumeExcludePattern.Type())
		}

		volumeExcludePatternRE := (*regexp.Regexp)(volumeExcludePattern.UnsafePointer())
		compareRE(t, volumeExcludePatternRE, regexp.MustCompile(diskFilter.AsDenyRegexp()), valuesToTest)
	}

	netCollectors := collectors["net"]
	if netCollectors == nil {
		t.Error("net collectors isn't present")
	} else {
		value := reflect.ValueOf(netCollectors)
		value = value.Elem()

		nicExcludePattern := value.FieldByName("nicExcludePattern")
		if nicExcludePattern.Type() != reflect.TypeOf(&regexp.Regexp{}) {
			t.Errorf("nicExcludePattern is a %s, want a *regexp.Regexp", nicExcludePattern.Type())
		}

		nicExcludePatternRE := (*regexp.Regexp)(nicExcludePattern.UnsafePointer())
		compareRE(t, nicExcludePatternRE, regexp.MustCompile(fullOptions.NetIfMatcher.AsDenyRegexp()), valuesToTest)
	}
}
