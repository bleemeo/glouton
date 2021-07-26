package disk

import (
	"glouton/inputs/internal"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func Test_deduplicate(t *testing.T) {
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
			name: "bind-mount",
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
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			acc := &internal.StoreAccumulator{Measurement: tt.input}

			deduplicate(acc, tt.hostroot)

			if diff := cmp.Diff(acc.Measurement, tt.want); diff != "" {
				t.Error(diff)
			}
		})
	}
}
