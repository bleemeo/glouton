// Copyright 2015-2023 Bleemeo
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
	"testing"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/inputs"

	"github.com/alecthomas/kingpin/v2"
)

// Test_optionsToFlags ensure the option still existing in node_exporter.
func Test_optionsToFlags(t *testing.T) {
	diskFilter, err := config.NewDiskIOMatcher(config.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		option inputs.CollectorConfig
	}{
		{
			name:   "empty-option",
			option: inputs.CollectorConfig{},
		},
		{
			name: "full-option",
			option: inputs.CollectorConfig{
				DFRootPath:      "something",
				DFPathMatcher:   config.NewDFPathMatcher(config.DefaultConfig()),
				DFIgnoreFSTypes: []string{"something"},
				NetIfMatcher:    config.NewNetworkInterfaceMatcher(config.DefaultConfig()),
				IODiskMatcher:   diskFilter,
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
