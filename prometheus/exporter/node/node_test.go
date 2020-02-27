// nolint: scopelint
package node

import (
	"regexp"
	"testing"
)

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
			got, err := reFromREs(tt.args.input)
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
			got, err := reFromPrefix(tt.args.prefix)
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
			got, err := reFromPathPrefix(tt.args.prefix)
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
