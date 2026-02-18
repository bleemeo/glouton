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

package disk

import (
	"testing"

	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/google/go-cmp/cmp"
)

func Test_deduplicate(t *testing.T) { //nolint:maintidx
	tests := []struct {
		name     string
		hostroot string
		input    []internal.Measurement
		want     []internal.Measurement
	}{
		{
			name: "simple",
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "sda2",
						"mode":   "ro",
						"path":   "/boot",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-1",
						"mode":   "rw",
						"path":   "/",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "sda2",
						"mode":   "ro",
						"path":   "/boot",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-1",
						"mode":   "rw",
						"path":   "/",
					},
				},
			},
		},
		{
			name:     "simple-hostroot",
			hostroot: "/",
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "sda2",
						"mode":   "ro",
						"path":   "/boot",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-1",
						"mode":   "rw",
						"path":   "/",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "sda2",
						"mode":   "ro",
						"path":   "/boot",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-1",
						"mode":   "rw",
						"path":   "/",
					},
				},
			},
		},
		{
			name: "bind-mount", // Before gopsutil v4.25.12
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-1",
						"mode":   "rw",
						"path":   "/mnt/bind",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-1",
						"mode":   "rw",
						"path":   "/",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-1",
						"mode":   "rw",
						"path":   "/mnt/bind/etc/resolv.conf",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-1",
						"mode":   "rw",
						"path":   "/",
					},
				},
			},
		},
		{
			name: "bind-mount-new", // After gopsutil v4.25.12
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "/",
						"mode":   "rw",
						"path":   "/mnt/bind",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-1",
						"mode":   "rw",
						"path":   "/",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "/mnt/bind",
						"mode":   "rw",
						"path":   "/mnt/bind/etc/resolv.conf",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-1",
						"mode":   "rw",
						"path":   "/",
					},
				},
			},
		},
		{
			name: "mount-same-path",
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "nvme0n1p1",
						"mode":   "rw",
						"path":   "/boot/efi",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-3",
						"mode":   "rw",
						"path":   "/boot/efi",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-3",
						"mode":   "rw",
						"path":   "/boot/efi",
					},
				},
			},
		},
		{
			name: "both",
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "nvme0n1p1",
						"mode":   "rw",
						"path":   "/boot/efi",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-3",
						"mode":   "rw",
						"path":   "/boot/efi",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-3",
						"mode":   "rw",
						"path":   "/mnt/abc", // shorter than /boot/efi
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-3",
						"mode":   "rw",
						"path":   "/mnt/abc",
					},
				},
			},
		},
		{
			name:     "both-hostroot",
			hostroot: "/",
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "nvme0n1p1",
						"mode":   "rw",
						"path":   "/boot/efi",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-3",
						"mode":   "rw",
						"path":   "/boot/efi",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-3",
						"mode":   "rw",
						"path":   "/mnt/abc", // shorter than /boot/efi
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-3",
						"mode":   "rw",
						"path":   "/mnt/abc",
					},
				},
			},
		},
		{
			name:     "both-hostroot2",
			hostroot: "/does-not-exists",
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "nvme0n1p1",
						"mode":   "rw",
						"path":   "/boot/efi",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-3",
						"mode":   "rw",
						"path":   "/boot/efi",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-3",
						"mode":   "rw",
						"path":   "/mnt/abc", // shorter than /boot/efi
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-3",
						"mode":   "rw",
						"path":   "/mnt/abc",
					},
				},
			},
		},
		{
			name: "both2",
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "nvme0n1p1",
						"mode":   "rw",
						"path":   "/boot/efi",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-3",
						"mode":   "rw",
						"path":   "/boot/efi",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-3",
						"mode":   "rw",
						"path":   "/abc/abcd", // same length as /boot/efi
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "dm-3",
						"mode":   "rw",
						"path":   "/boot/efi",
					},
				},
			},
		},
		{
			name:     "with-hostroot",
			hostroot: "/hostroot",
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "nvme0n1p1",
						"mode":   "rw",
						"path":   "/etc/hosts",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "nvme0n1p1",
						"mode":   "rw",
						"path":   "/hostroot/tmp/hostpath-provisioner",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "nvme0n1p1",
						"mode":   "rw",
						"path":   "/hostroot/var",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "nvme0n1p1",
						"mode":   "rw",
						"path":   "/hostroot/var/lib",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "nvme0n1p1",
						"mode":   "rw",
						"path":   "/hostroot/var",
					},
				},
			},
		},
		{
			name:     "with-hostroot2",
			hostroot: "/hostroot",
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "mapper/vg0-root",
						"mode":   "rw",
						"path":   "/hostroot/tmp/hostpath_pv",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "mapper/vg0-root",
						"mode":   "rw",
						"path":   "/hostroot/var",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "mapper/vg0-root",
						"mode":   "rw",
						"path":   "/hostroot/data",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "mapper/vg0-root",
						"mode":   "rw",
						"path":   "/dev/termination-log",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "mapper/vg0-root",
						"mode":   "rw",
						"path":   "/etc/hosts",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "mapper/vg0-root",
						"mode":   "rw",
						"path":   "/var/lib/glouton",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "mapper/vg0-root",
						"mode":   "rw",
						"path":   "/hostroot/var",
					},
				},
			},
		},
		{
			// This test mostly ensure deduplication don't eliminate some "special" device,
			// since we made some assumption on what is a bind mount vs a real device.
			name: "special-device",
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "apfs",
						"device": "disk3s1s1",
						"mode":   "rw",
						"path":   "/",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "NTFS",
						"device": "C:",
						"mode":   "rw",
						"path":   "C:",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "zfs",
						"device": "boot-pool/ROOT/13.0-U6.8",
						"mode":   "rw",
						"path":   "/home",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "apfs",
						"device": "disk3s1s1",
						"mode":   "rw",
						"path":   "/",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "NTFS",
						"device": "C:",
						"mode":   "rw",
						"path":   "C:",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "zfs",
						"device": "boot-pool/ROOT/13.0-U6.8",
						"mode":   "rw",
						"path":   "/home",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acc := &internal.StoreAccumulator{Measurement: tt.input}

			deduplicate(acc, tt.hostroot)

			if diff := cmp.Diff(tt.want, acc.Measurement); diff != "" {
				t.Errorf("deduplicate mismatch (-want +got)\n%s", diff)
			}
		})
	}
}
