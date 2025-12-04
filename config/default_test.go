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

package config

import (
	"regexp"
	"slices"
	"testing"

	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp/cmpopts"
)

func testMatcher(t *testing.T, matcher types.Matcher, allowedItems []string, deniedItem []string) {
	t.Helper()

	var (
		denyRE *regexp.Regexp
		err    error
	)

	if reMatcher, ok := matcher.(types.MatcherRegexp); ok {
		denyREString := reMatcher.AsDenyRegexp()

		denyRE, err = regexp.Compile(denyREString)
		if err != nil {
			t.Fatal(err)
		}
	}

	allItems := make([]string, 0, len(allowedItems)+len(deniedItem))
	allItems = append(allItems, allowedItems...)
	allItems = append(allItems, deniedItem...)

	for i, item := range allItems {
		t.Run(item, func(t *testing.T) {
			t.Parallel()

			match := matcher.Match(item)
			shouldMatch := i < len(allowedItems)

			if match != shouldMatch {
				t.Errorf("Item %s is allowed=%v, want %v", item, match, shouldMatch)
			}

			if denyRE != nil {
				matchRE := !denyRE.MatchString(item)
				if match != matchRE {
					t.Errorf("AsRegexp.Match() = %v, want %v", matchRE, match)
				}
			}
		})
	}
}

// TestDiskIgnore check that disk ignore regexp match expected disks.
func TestDefaultDiskIgnore(t *testing.T) {
	cfg := DefaultConfig()

	filter, err := NewDiskIOMatcher(cfg)
	if err != nil {
		t.Fatal(err)
	}

	allowedDisk := []string{
		"ada0",
		"C:",
		"da0",
		"disk0", // Value seen on MacOS
		"drbd0",
		"fioa",
		"hda",
		"md0",
		"md127",
		"mmcblk0",
		"nbd0", // Network block device, usually qemu-nbd
		"nbd15",
		"nvme0c0n1",
		"nvme0n1",
		"rbd0",
		"rssda",
		"rsxx0",
		"sda",
		"sdaa",
		"sdcr",
		"sdz",
		"skd0",
		"vda",
		"vtbd0",
		"xvda",

		// Edge case. We probably should like to ignore this, but it seems a
		// really specific situation.
		"mmcblk2boot1",
	}

	deniedDisk := []string{
		"bcache0",
		"dm-0",
		"drbd0p1",
		"fd0",
		"fioa1",
		"hda1",
		"loop0",
		"md102p2",
		"mmcblk0p1",
		"nbd0p1",
		"nbd0p15",
		"nbd15p12",
		"nvme0n1p1",
		"ram0",
		"rbd0p1",
		"rssda1",
		"rsxx0p1",
		"sda1",
		"sdaa1",
		"sdaa10",
		"sdba10",
		"skd0p1",
		"sr0", // SCSI cdrom
		"vda1",
		"xvda1",
		"zd0", // zs are ZFS volume
		"zd48",
		"zram0", // compressed tmpfs, usually for compressed swap
		"cd0",   // cdrom on FreeBSD
		"pass0", // fuse for block device on FreeBSD
		"pass2", // fuse for block device on FreeBSD
	}

	testMatcher(t, filter, allowedDisk, deniedDisk)
}

// TestDefaultNetworkIgnore check that network ignore pattern match expected network interfaces.
func TestDefaultNetworkIgnore(t *testing.T) {
	cfg := DefaultConfig()

	filter := NewNetworkInterfaceMatcher(cfg)

	allowedInterface := []string{
		"bond0.82",
		"bond0",
		"br-088b71fff3aa",
		"br0",
		"bridge0",
		"em0",
		"en0",
		"en7",
		"eno0",
		"eno1.104",
		"eno50.514",
		"enp101s0f1np1",
		"enp130s0f1.888",
		"eth0",
		"tap0",
		"tun0",
		"vlan10",
		"vtnet0",
		"wlan0",
		"wlp12s0",
	}

	deniedInterface := []string{
		"docker0",
		"lo",
		"lo0",
		"veth034056c",
	}

	testMatcher(t, filter, allowedInterface, deniedInterface)
}

// TestDefaultDFPathIgnore check that df ignore pattern match expected filesystem path.
func TestDefaultDFPathIgnore(t *testing.T) {
	cfg := DefaultConfig()

	filter := NewDFPathMatcher(cfg)

	allowedPath := []string{
		"/",
		"/home",
		"/snapshot", // This is allowed, because it's not below "/snap"
		"/srv",
		"/var/lib",
		"C:",
		"D:",
	}

	deniedPath := []string{
		"/run/snapd/ns",
		"/snap",
		"/snap/core/14447",
		"/snap/shot",
		"/var/lib/docker/containers/dee975a2411bf87e954ecd1f4e3dd61b80e1c2b72072e98547bcb955ca441d56/mounts/shm",
		"/var/lib/docker/overlay2/1ed9f56b42e012a558634a03a336640922a59aae6f831ef7824564c18f6b4662/merged/dev/shm",
		"/var/lib/docker/overlay2/dee975a2411bf87e954ecd1f4e3dd61b80e1c2b72072e98547bcb955ca441d56/merged",
	}

	testMatcher(t, filter, allowedPath, deniedPath)
}

// TestDFFSTypeMatcher checks that the df type matcher considers the types as strings and not regex.
func TestDFFSTypeMatcher(t *testing.T) {
	cfg := Config{
		DF: DF{
			IgnoreFSType: []string{
				// Should be considered as a string, so it shouldn't match "fuse-ext4".
				"fuse.ext4",
				"devtmpfs",
			},
		},
	}

	fsTypeMatcher, err := NewDFFSTypeMatcher(cfg)
	if err != nil {
		t.Fatal(err)
	}

	allowedType := []string{"ext4", "fuse-ext4"}
	deniedType := []string{"fuse.ext4", "devtmpfs"}

	testMatcher(t, fsTypeMatcher, allowedType, deniedType)
}

// TestDefaultDFFSTypeIgnore check that df ignore pattern match expected filesystem type.
func TestDefaultDFFSTypeIgnore(t *testing.T) {
	cfg := DefaultConfig()

	filter, err := NewDFFSTypeMatcher(cfg)
	if err != nil {
		t.Fatal(err)
	}

	allowedType := []string{
		"ext4",
		"vfat",
		"ufs",
	}

	deniedType := []string{
		"autofs",
		"bpf",
		"cgroup",
		"cgroup2",
		"configfs",
		"debugfs",
		"devfs",
		"devpts",
		"devtmpfs",
		"fdescfs",
		"fusectl",
		"hugetlbfs",
		"linprocfs",
		"linsysfs",
		"mqueue",
		"nsfs",
		"nullfs",
		"overlay",
		"pstore",
		"securityfs",
		"sysfs",
		"tmpfs",
		"tracefs",
		"zfs",
	}

	testMatcher(t, filter, allowedType, deniedType)

	// Glouton uses the list of ignored FS in two places:
	// * in node_exporter, where the ignored FS are interpreted as regular expressions, this is tested above.
	// * in glouton/inputs/disk, where the ignored FS are simple strings.

	// Here we test the second ignore FS list to ensure both are consistent at least on testcases.
	allItems := make([]string, 0, len(allowedType)+len(deniedType))
	allItems = append(allItems, allowedType...)
	allItems = append(allItems, deniedType...)

	for i, item := range allItems {
		t.Run(item, func(t *testing.T) {
			t.Parallel()

			match := !slices.Contains(cfg.DF.IgnoreFSType, item)

			shouldMatch := i < len(allowedType)

			if match != shouldMatch {
				t.Errorf("Item %s is allowed=%v, want %v", item, match, shouldMatch)
			}
		})
	}
}

// TestLoadFile check that loading the etc/glouton.conf file works and yield the default settings.
func TestLoadFile(t *testing.T) {
	cfg, warnings, err := load(&configLoader{}, true, false, "../etc/glouton.conf")
	if err != nil {
		t.Fatal(err)
	}

	if len(warnings) > 0 {
		t.Errorf("config had warning: %v", warnings)
	}

	defaultCfg := DefaultConfig()

	if diff := compareConfig(defaultCfg, cfg, cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("Default value modified:\n%s", diff)
	}
}
