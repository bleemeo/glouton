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

package common_test

import (
	"regexp"
	"testing"

	"github.com/bleemeo/glouton/prometheus/exporter/common"
)

func TestMergeREs(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		matches    []string
		noMatchers []string
	}{
		{
			name: "two-choice",
			args: []string{
				"sda[0-9]?$",
				"^nvme0n1",
			},
			matches: []string{
				"sda",
				"sda5",
				"nvme0n12",
			},
			noMatchers: []string{
				"value",
				"hda",
				"sda15",
				"onvmen1",
			},
		},
		{
			name: "disk_monitor",
			args: []string{
				"^(hd|sd|vd|xvd)[a-z]$",
				"^mmcblk[0-9]$",
				"^[A-Z]:$",
			},
			matches: []string{
				"hda",
				"hdb",
				"xvdc",
				"mmcblk8",
				"C:",
			},
			noMatchers: []string{
				"hda1",
				"mmcblk10",
				"AB:",
				"ram0",
				"sd",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := common.CompileREs(tt.args)
			if err != nil {
				t.Fatalf("failed to compile %#v: %v", tt.args, err)
			}

			got, err := common.MergeREs(res)
			if err != nil {
				t.Fatalf("MergeREs failed: %v", err)
			}

			gotRE, err := regexp.Compile(got)
			if err != nil {
				t.Fatalf("failed to compile %#v: %v", got, err)

				return
			}

			for _, v := range tt.matches {
				if !gotRE.MatchString(v) {
					t.Errorf("MatchString(%s) == false, want true", v)
				}
			}

			for _, v := range tt.noMatchers {
				if gotRE.MatchString(v) {
					t.Errorf("MatchString(%s) == true, want false", v)
				}
			}
		})
	}
}

func TestReFromREs(t *testing.T) {
	type args struct {
		input []string
	}

	tests := []struct {
		name       string
		args       args
		matches    []string
		noMatchers []string
	}{
		{
			name: "simple",
			args: args{[]string{
				"sda",
			}},
			matches: []string{
				"sda",
			},
			noMatchers: []string{
				"value",
				"hda",
			},
		},
		{
			name: "two-choice",
			args: args{[]string{
				"sda",
				"nvme0n1",
			}},
			matches: []string{
				"sda",
				"nvme0n1",
			},
			noMatchers: []string{
				"value",
				"hda",
			},
		},
		{
			name: "disk_monitor",
			args: args{[]string{
				"^(hd|sd|vd|xvd)[a-z]$",
				"^mmcblk[0-9]$",
				"^[A-Z]:$",
			}},
			matches: []string{
				"hda",
				"hdb",
				"xvdc",
				"mmcblk8",
				"C:",
			},
			noMatchers: []string{
				"hda1",
				"mmcblk10",
				"AB:",
				"ram0",
				"sd",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := common.ReFromREs(tt.args.input)
			if err != nil {
				t.Errorf("ReFromREs failed: %v", err)

				return
			}

			gotRE, err := regexp.Compile(got)
			if err != nil {
				t.Errorf("failed to compile %#v: %v", got, err)

				return
			}

			for _, v := range tt.matches {
				if !gotRE.MatchString(v) {
					t.Errorf("MatchString(%s) == false, want true", v)
				}
			}

			for _, v := range tt.noMatchers {
				if gotRE.MatchString(v) {
					t.Errorf("MatchString(%s) == true, want false", v)
				}
			}
		})
	}
}

func TestReFromPrefixes(t *testing.T) {
	type args struct {
		prefix string
	}

	tests := []struct {
		name       string
		args       args
		matches    []string
		noMatchers []string
	}{
		{
			name: "lo",
			args: args{"lo"},
			matches: []string{
				"lo",
				"lo0",
			},
			noMatchers: []string{
				"wlan0",
				"eth0",
				"wlp2s0",
				"wlo",
			},
		},
		{
			name: "with-dot",
			args: args{"the.lo"},
			matches: []string{
				"the.lo",
				"the.lo0",
			},
			noMatchers: []string{
				"theAlo",
				"the lo",
			},
		},
		{
			name: "more-special",
			args: args{"^\\(["},
			matches: []string{
				"^\\([",
				"^\\([0",
			},
			noMatchers: []string{
				"^\\(",
				"coin^\\([",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := common.ReFromPrefix(tt.args.prefix)
			if err != nil {
				t.Errorf("ReFromREs failed: %v", err)

				return
			}

			gotRE, err := regexp.Compile(got)
			if err != nil {
				t.Errorf("failed to compile %#v: %v", got, err)

				return
			}

			for _, v := range tt.matches {
				if !gotRE.MatchString(v) {
					t.Errorf("MatchString(%s) == false, want true", v)
				}
			}

			for _, v := range tt.noMatchers {
				if gotRE.MatchString(v) {
					t.Errorf("MatchString(%s) == true, want false", v)
				}
			}
		})
	}
}

func TestReFromPathPrefix(t *testing.T) {
	type args struct {
		prefix string
	}

	tests := []struct {
		name       string
		args       args
		matches    []string
		noMatchers []string
	}{
		{
			name: "/mnt",
			args: args{"/mnt"},
			matches: []string{
				"/mnt",
				"/mnt/disk",
			},
			noMatchers: []string{
				"/mnt2",
				"/mnt-disk",
			},
		},
		{
			name: "with-dot",
			args: args{"/srv/www.domain"},
			matches: []string{
				"/srv/www.domain",
				"/srv/www.domain/htdocs",
			},
			noMatchers: []string{
				"/srv/www.domain.fr",
				"/rootfs/srv/www.domain",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := common.ReFromPathPrefix(tt.args.prefix)
			if err != nil {
				t.Errorf("ReFromREs failed: %v", err)

				return
			}

			gotRE, err := regexp.Compile(got)
			if err != nil {
				t.Errorf("failed to compile %#v: %v", got, err)

				return
			}

			for _, v := range tt.matches {
				if !gotRE.MatchString(v) {
					t.Errorf("MatchString(%s) == false, want true", v)
				}
			}

			for _, v := range tt.noMatchers {
				if gotRE.MatchString(v) {
					t.Errorf("MatchString(%s) == true, want false", v)
				}
			}
		})
	}
}
