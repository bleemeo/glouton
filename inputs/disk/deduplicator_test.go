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

//nolint:dupl
package disk

import (
	"testing"

	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/google/go-cmp/cmp"
)

func Test_deduplicate(t *testing.T) { //nolint:maintidx
	tests := []struct {
		name  string
		input []internal.Measurement
		want  []internal.Measurement
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

		// --------- Test below are generated using go run ./inputs/disk/testdata/mk-disk-deduplicator-data.go
		// Generated with Glouton 26.02.18.091640
		{
			// On Debian 13, a qemu VM with already mounted /boot/efi (vdb15) + and extra disk (vdc1)
			// This is mostly debian-13-generic-arm64.qcow2 with two qcow2 attached
			// Run: mount --bind /boot/efi /mnt/path; mount /dev/vdc1 /mnt/d1; mount --bind /mnt/d1 /mnt/path
			// And yes /boot/efi become content of vdc1... (probably mount propagation ?)
			name: "two-mount-same-path",
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/",
						"device": "vdb1",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/boot/efi",
						"device": "vdb15",
						"fstype": "vfat",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/mnt/path",
						"device": "/boot/efi",
						"fstype": "vfat",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"device": "vdc1",
						"fstype": "ext2",
						"mode":   "rw",
						"path":   "/mnt/d1",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/mnt/path",
						"device": "/mnt/d1",
						"fstype": "ext2",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/boot/efi",
						"device": "/mnt/path",
						"fstype": "ext2",
						"mode":   "rw",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/",
						"device": "vdb1",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/mnt/d1",
						"device": "vdc1",
						"fstype": "ext2",
						"mode":   "rw",
					},
				},
			},
		},
		{
			// Debian 13 with mount /dev/vdc1 /mnt/d1; mount --bind /mnt/d1 /mnt/path
			name: "single-bind",
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"device": "vdb1",
						"fstype": "ext4",
						"mode":   "rw",
						"path":   "/",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/boot/efi",
						"device": "vdb15",
						"fstype": "vfat",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/mnt/d1",
						"device": "vdc1",
						"fstype": "ext2",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/mnt/path",
						"device": "/mnt/d1",
						"fstype": "ext2",
						"mode":   "rw",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/",
						"device": "vdb1",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/boot/efi",
						"device": "vdb15",
						"fstype": "vfat",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/mnt/d1",
						"device": "vdc1",
						"fstype": "ext2",
						"mode":   "rw",
					},
				},
			},
		},
		{
			// Same as single-bind + unmount original mount point, e.g.
			// mount /dev/vdc1 /mnt/d1; mount --bind /mnt/d1 /mnt/path; umount /mnt/d1
			name: "single-bind-then-unmount",
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/",
						"device": "vdb1",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/boot/efi",
						"device": "vdb15",
						"fstype": "vfat",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/mnt/path",
						"device": "vdc1",
						"fstype": "ext2",
						"mode":   "rw",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/",
						"device": "vdb1",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/boot/efi",
						"device": "vdb15",
						"fstype": "vfat",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/mnt/path",
						"device": "vdc1",
						"fstype": "ext2",
						"mode":   "rw",
					},
				},
			},
		},
		{
			// Debian 13 with mount /dev/vdc1 /mnt/this-path-is-rather-long; mount --bind /mnt/this-path-is-rather-long /mnt/path
			name: "single-bind-2",
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/",
						"device": "vdb1",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/boot/efi",
						"device": "vdb15",
						"fstype": "vfat",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"mode":   "rw",
						"path":   "/mnt/this-path-is-rather-long",
						"device": "vdc1",
						"fstype": "ext2",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"device": "/mnt/this-path-is-rather-long",
						"fstype": "ext2",
						"mode":   "rw",
						"path":   "/mnt/path",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/",
						"device": "vdb1",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/boot/efi",
						"device": "vdb15",
						"fstype": "vfat",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/mnt/this-path-is-rather-long",
						"device": "vdc1",
						"fstype": "ext2",
						"mode":   "rw",
					},
				},
			},
		},
		{
			name: "k8s-with-longhorn",
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/",
						"device": "sda2",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/var/lib/longhorn",
						"device": "sdb1",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"device": "/",
						"fstype": "ext4",
						"mode":   "rw",
						"path":   "/var/lib/kubelet/pods/e6a29bd7-8754-4f70-93cd-c32b3bc1cf41/volume-subpaths/tigera-ca-bundle/calico-node/1",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/var/lib/kubelet/plugins/kubernetes.io/csi/driver.longhorn.io/75bb6f12d3144c67bd218e5453fd86761b0089f51deb41e2b7a67be4e46ec8c1/globalmount",
						"device": "longhorn/pvc-2d560fc3-1fb8-4635-9ba1-fb75cf55d0b8",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/var/lib/kubelet/pods/e6a29bd7-8754-4f70-93cd-c32b3bc1cf41/volumes/kubernetes.io~csi/pvc-2d560fc3-1fb8-4635-9ba1-fb75cf55d0b8/mount",
						"device": "/var/lib/kubelet/plugins/kubernetes.io/csi/driver.longhorn.io/75bb6f12d3144c67bd218e5453fd86761b0089f51deb41e2b7a67be4e46ec8c1/globalmount",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/",
						"device": "sda2",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/var/lib/longhorn",
						"device": "sdb1",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "longhorn/pvc-2d560fc3-1fb8-4635-9ba1-fb75cf55d0b8",
						"mode":   "rw",
						"path":   "/var/lib/kubelet/plugins/kubernetes.io/csi/driver.longhorn.io/75bb6f12d3144c67bd218e5453fd86761b0089f51deb41e2b7a67be4e46ec8c1/globalmount",
						// TODO: we prefer the /var/lib/kubelet/pods mount point
						// "path":   "/var/lib/kubelet/pods/e6a29bd7-8754-4f70-93cd-c32b3bc1cf41/volumes/kubernetes.io~csi/pvc-2d560fc3-1fb8-4635-9ba1-fb75cf55d0b8/mount",
					},
				},
			},
		},
		{
			name: "k8s-with-aws-ebs",
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/",
						"device": "nvme0n1p1",
						"fstype": "ext4",
						"mode":   "rw",
						"label":  "cloudimg-rootfs",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"label":  "UEFI",
						"path":   "/boot/efi",
						"device": "nvme0n1p15",
						"fstype": "vfat",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/var/lib/kubelet/pods/e6a29bd7-8754-4f70-93cd-c32b3bc1cf41/volume-subpaths/tigera-ca-bundle/calico-node/1",
						"device": "/",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/var/lib/kubelet/plugins/kubernetes.io/csi/ebs.csi.aws.com/75bb6f12d3144c67bd218e5453fd86761b0089f51deb41e2b7a67be4e46ec8c1/globalmount",
						"device": "nvme1n1",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/var/lib/kubelet/pods/e6a29bd7-8754-4f70-93cd-c32b3bc1cf41/volumes/kubernetes.io~csi/pvc-2d560fc3-1fb8-4635-9ba1-fb75cf55d0b8/mount",
						"device": "/var/lib/kubelet/plugins/kubernetes.io/csi/ebs.csi.aws.com/75bb6f12d3144c67bd218e5453fd86761b0089f51deb41e2b7a67be4e46ec8c1/globalmount",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/",
						"device": "nvme0n1p1",
						"fstype": "ext4",
						"mode":   "rw",
						"label":  "cloudimg-rootfs",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"label":  "UEFI",
						"path":   "/boot/efi",
						"device": "nvme0n1p15",
						"fstype": "vfat",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"fstype": "ext4",
						"device": "nvme1n1",
						"mode":   "rw",
						"path":   "/var/lib/kubelet/plugins/kubernetes.io/csi/ebs.csi.aws.com/75bb6f12d3144c67bd218e5453fd86761b0089f51deb41e2b7a67be4e46ec8c1/globalmount",
						// TODO: we prefer the /var/lib/kubelet/pods mount point
						// "path":   "/var/lib/kubelet/pods/e6a29bd7-8754-4f70-93cd-c32b3bc1cf41/volumes/kubernetes.io~csi/pvc-2d560fc3-1fb8-4635-9ba1-fb75cf55d0b8/mount",
					},
				},
			},
		},
		{
			name: "windows",
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "\\C:",
						"device": "C:",
						"fstype": "NTFS",
						"mode":   "rw",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "\\C:",
						"device": "C:",
						"fstype": "NTFS",
						"mode":   "rw",
					},
				},
			},
		},
		{
			// This is Glouton running as package (.deb) on a server with Docker
			name: "server-with-docker",
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/",
						"device": "nvme0n1p1",
						"fstype": "ext4",
						"mode":   "rw",
						"label":  "cloudimg-rootfs",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/",
						"device": "nvme0n1p1",
						"fstype": "ext4",
						"mode":   "rw",
						"label":  "cloudimg-rootfs",
					},
				},
			},
		},
		{
			name: "minikube",
			input: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/var",
						"device": "vda1",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"mode":   "rw",
						"path":   "/etc/resolv.conf",
						"device": "/var",
						"fstype": "ext4",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/etc/hostname",
						"device": "/etc/resolv.conf",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/etc/hosts",
						"device": "/etc/hostname",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/data",
						"device": "/etc/hosts",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"device": "/data",
						"fstype": "ext4",
						"mode":   "rw",
						"path":   "/tmp/hostpath_pv",
					},
				},
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/tmp/hostpath-provisioner",
						"device": "/tmp/hostpath_pv",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   "disk",
					Fields: nil,
					Tags: map[string]string{
						"path":   "/var",
						"device": "vda1",
						"fstype": "ext4",
						"mode":   "rw",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acc := &internal.StoreAccumulator{Measurement: tt.input}

			deduplicate(acc)

			if diff := cmp.Diff(tt.want, acc.Measurement); diff != "" {
				t.Errorf("deduplicate mismatch (-want +got)\n%s", diff)
			}
		})
	}
}
