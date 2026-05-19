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

package disk

import (
	"testing"

	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/google/go-cmp/cmp"
)

// Tag key constants used in disk measurements.
const (
	tagDevice = "device"
	tagFSType = "fstype"
	tagMode   = "mode"
	tagPath   = "path"
)

// Filesystem type constants.
const (
	fsExt2 = "ext2"
	fsExt4 = "ext4"
	fsVFAT = "vfat"
	fsNTFS = "NTFS"
)

// Device name constants.
const (
	devDM1       = "dm-1"
	devDM3       = "dm-3"
	devNvme0n1p1 = "nvme0n1p1"
	devSDA2      = "sda2"
	devVDB1      = "vdb1"
	devVDB15     = "vdb15"
	devVDC1      = "vdc1"
)

// Tag key for label.
const tagLabel = "label"

// Mount path constants.
const (
	pathBootEFI = "/boot/efi"
	pathMntPath = "/mnt/path"
	pathMntD1   = "/mnt/d1"
	pathMntLong = "/mnt/this-path-is-rather-long"
	pathVar     = "/var"
)

// Label constant.
const labelCloudimgRootFS = "cloudimg-rootfs"

// Kubelet path constant for CSI volume mount.
const pathKubeletCSIMount = "/var/lib/kubelet/pods/e6a29bd7-8754-4f70-93cd-c32b3bc1cf41/volumes/kubernetes.io~csi/pvc-2d560fc3-1fb8-4635-9ba1-fb75cf55d0b8/mount" //nolint:lll

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
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: devSDA2,
						tagMode:   "ro",
						tagPath:   "/boot",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: devDM1,
						tagMode:   "rw",
						tagPath:   "/",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: devSDA2,
						tagMode:   "ro",
						tagPath:   "/boot",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: devDM1,
						tagMode:   "rw",
						tagPath:   "/",
					},
				},
			},
		},
		{
			name: "bind-mount", // After gopsutil v4.25.12
			input: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: devDM1,
						tagMode:   "rw",
						tagPath:   "/",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: "/",
						tagMode:   "rw",
						tagPath:   "/mnt/bind",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: "/mnt/bind",
						tagMode:   "rw",
						tagPath:   "/mnt/bind/etc/resolv.conf",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: devDM1,
						tagMode:   "rw",
						tagPath:   "/",
					},
				},
			},
		},
		{
			name: "mount-same-path",
			input: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: devNvme0n1p1,
						tagMode:   "rw",
						tagPath:   pathBootEFI,
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: devDM3,
						tagMode:   "rw",
						tagPath:   pathBootEFI,
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: devDM3,
						tagMode:   "rw",
						tagPath:   pathBootEFI,
					},
				},
			},
		},
		{
			name: "both",
			input: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: devNvme0n1p1,
						tagMode:   "rw",
						tagPath:   pathBootEFI,
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: devDM3,
						tagMode:   "rw",
						tagPath:   pathBootEFI,
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: devDM3,
						tagMode:   "rw",
						tagPath:   "/mnt/abc",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: devDM3,
						tagMode:   "rw",
						tagPath:   pathBootEFI,
					},
				},
			},
		},
		{
			name: "both2",
			input: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: devNvme0n1p1,
						tagMode:   "rw",
						tagPath:   pathBootEFI,
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: devDM3,
						tagMode:   "rw",
						tagPath:   pathBootEFI,
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: devDM3,
						tagMode:   "rw",
						tagPath:   "/abc/abcd",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: devDM3,
						tagMode:   "rw",
						tagPath:   pathBootEFI,
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
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: "apfs",
						tagDevice: "disk3s1s1",
						tagMode:   "rw",
						tagPath:   "/",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsNTFS,
						tagDevice: "C:",
						tagMode:   "rw",
						tagPath:   "C:",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: "zfs",
						tagDevice: "boot-pool/ROOT/13.0-U6.8",
						tagMode:   "rw",
						tagPath:   "/home",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: "apfs",
						tagDevice: "disk3s1s1",
						tagMode:   "rw",
						tagPath:   "/",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsNTFS,
						tagDevice: "C:",
						tagMode:   "rw",
						tagPath:   "C:",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: "zfs",
						tagDevice: "boot-pool/ROOT/13.0-U6.8",
						tagMode:   "rw",
						tagPath:   "/home",
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
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/",
						tagDevice: devVDB1,
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathBootEFI,
						tagDevice: devVDB15,
						tagFSType: fsVFAT,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathMntPath,
						tagDevice: pathBootEFI,
						tagFSType: fsVFAT,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagDevice: devVDC1,
						tagFSType: fsExt2,
						tagMode:   "rw",
						tagPath:   pathMntD1,
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathMntPath,
						tagDevice: pathMntD1,
						tagFSType: fsExt2,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathBootEFI,
						tagDevice: pathMntPath,
						tagFSType: fsExt2,
						tagMode:   "rw",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/",
						tagDevice: devVDB1,
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathMntD1,
						tagDevice: devVDC1,
						tagFSType: fsExt2,
						tagMode:   "rw",
					},
				},
				{
					// This one is questionnable: /boot/efi is vdc1, but vdb15 is still mounted in
					// kernel (and possibly an application could have file openned on this filesystem).
					// This is really an edge case with 2 devices and 2 mount-point which probably don't exists in
					// real situation, so feels free to add/drop this wanted Measurement to makes test pass.
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathBootEFI,
						tagDevice: devVDB15,
						tagFSType: fsExt2,
						tagMode:   "rw",
					},
				},
			},
		},
		{
			// Debian 13 with mount /dev/vdc1 /mnt/d1; mount --bind /mnt/d1 /mnt/path
			name: "single-bind",
			input: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagDevice: devVDB1,
						tagFSType: fsExt4,
						tagMode:   "rw",
						tagPath:   "/",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathBootEFI,
						tagDevice: devVDB15,
						tagFSType: fsVFAT,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathMntD1,
						tagDevice: devVDC1,
						tagFSType: fsExt2,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathMntPath,
						tagDevice: pathMntD1,
						tagFSType: fsExt2,
						tagMode:   "rw",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/",
						tagDevice: devVDB1,
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathBootEFI,
						tagDevice: devVDB15,
						tagFSType: fsVFAT,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathMntD1,
						tagDevice: devVDC1,
						tagFSType: fsExt2,
						tagMode:   "rw",
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
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/",
						tagDevice: devVDB1,
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathBootEFI,
						tagDevice: devVDB15,
						tagFSType: fsVFAT,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathMntPath,
						tagDevice: devVDC1,
						tagFSType: fsExt2,
						tagMode:   "rw",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/",
						tagDevice: devVDB1,
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathBootEFI,
						tagDevice: devVDB15,
						tagFSType: fsVFAT,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathMntPath,
						tagDevice: devVDC1,
						tagFSType: fsExt2,
						tagMode:   "rw",
					},
				},
			},
		},
		{
			// Debian 13 with mount /dev/vdc1 /mnt/this-path-is-rather-long; mount --bind /mnt/this-path-is-rather-long /mnt/path
			name: "single-bind-2",
			input: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/",
						tagDevice: devVDB1,
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathBootEFI,
						tagDevice: devVDB15,
						tagFSType: fsVFAT,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagMode:   "rw",
						tagPath:   pathMntLong,
						tagDevice: devVDC1,
						tagFSType: fsExt2,
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagDevice: pathMntLong,
						tagFSType: fsExt2,
						tagMode:   "rw",
						tagPath:   pathMntPath,
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/",
						tagDevice: devVDB1,
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathBootEFI,
						tagDevice: devVDB15,
						tagFSType: fsVFAT,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathMntLong,
						tagDevice: devVDC1,
						tagFSType: fsExt2,
						tagMode:   "rw",
					},
				},
			},
		},
		{
			name: "k8s-with-longhorn",
			input: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/",
						tagDevice: devSDA2,
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/var/lib/longhorn",
						tagDevice: "sdb1",
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagDevice: "/",
						tagFSType: fsExt4,
						tagMode:   "rw",
						tagPath:   "/var/lib/kubelet/pods/e6a29bd7-8754-4f70-93cd-c32b3bc1cf41/volume-subpaths/tigera-ca-bundle/calico-node/1",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/var/lib/kubelet/plugins/kubernetes.io/csi/driver.longhorn.io/75bb6f12d3144c67bd218e5453fd86761b0089f51deb41e2b7a67be4e46ec8c1/globalmount",
						tagDevice: "longhorn/pvc-2d560fc3-1fb8-4635-9ba1-fb75cf55d0b8",
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathKubeletCSIMount,
						tagDevice: "/var/lib/kubelet/plugins/kubernetes.io/csi/driver.longhorn.io/75bb6f12d3144c67bd218e5453fd86761b0089f51deb41e2b7a67be4e46ec8c1/globalmount",
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/",
						tagDevice: devSDA2,
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/var/lib/longhorn",
						tagDevice: "sdb1",
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: "longhorn/pvc-2d560fc3-1fb8-4635-9ba1-fb75cf55d0b8",
						tagMode:   "rw",
						tagPath:   pathKubeletCSIMount,
					},
				},
			},
		},
		{
			name: "k8s-with-aws-ebs",
			input: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/",
						tagDevice: devNvme0n1p1,
						tagFSType: fsExt4,
						tagMode:   "rw",
						tagLabel:  labelCloudimgRootFS,
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagLabel:  "UEFI",
						tagPath:   pathBootEFI,
						tagDevice: "nvme0n1p15",
						tagFSType: fsVFAT,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/var/lib/kubelet/pods/e6a29bd7-8754-4f70-93cd-c32b3bc1cf41/volume-subpaths/tigera-ca-bundle/calico-node/1",
						tagDevice: "/",
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/var/lib/kubelet/plugins/kubernetes.io/csi/ebs.csi.aws.com/75bb6f12d3144c67bd218e5453fd86761b0089f51deb41e2b7a67be4e46ec8c1/globalmount",
						tagDevice: "nvme1n1",
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathKubeletCSIMount,
						tagDevice: "/var/lib/kubelet/plugins/kubernetes.io/csi/ebs.csi.aws.com/75bb6f12d3144c67bd218e5453fd86761b0089f51deb41e2b7a67be4e46ec8c1/globalmount",
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/",
						tagDevice: devNvme0n1p1,
						tagFSType: fsExt4,
						tagMode:   "rw",
						tagLabel:  labelCloudimgRootFS,
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagLabel:  "UEFI",
						tagPath:   pathBootEFI,
						tagDevice: "nvme0n1p15",
						tagFSType: fsVFAT,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagFSType: fsExt4,
						tagDevice: "nvme1n1",
						tagMode:   "rw",
						tagPath:   pathKubeletCSIMount,
					},
				},
			},
		},
		{
			name: "windows",
			input: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "\\C:",
						tagDevice: "C:",
						tagFSType: fsNTFS,
						tagMode:   "rw",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "\\C:",
						tagDevice: "C:",
						tagFSType: fsNTFS,
						tagMode:   "rw",
					},
				},
			},
		},
		{
			// This is Glouton running as package (.deb) on a server with Docker
			name: "server-with-docker",
			input: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/",
						tagDevice: devNvme0n1p1,
						tagFSType: fsExt4,
						tagMode:   "rw",
						tagLabel:  labelCloudimgRootFS,
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/",
						tagDevice: devNvme0n1p1,
						tagFSType: fsExt4,
						tagMode:   "rw",
						tagLabel:  labelCloudimgRootFS,
					},
				},
			},
		},
		{
			name: "minikube",
			input: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathVar,
						tagDevice: "vda1",
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagMode:   "rw",
						tagPath:   "/etc/resolv.conf",
						tagDevice: pathVar,
						tagFSType: fsExt4,
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/etc/hostname",
						tagDevice: "/etc/resolv.conf",
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/etc/hosts",
						tagDevice: "/etc/hostname",
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/data",
						tagDevice: "/etc/hosts",
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagDevice: "/data",
						tagFSType: fsExt4,
						tagMode:   "rw",
						tagPath:   "/tmp/hostpath_pv",
					},
				},
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   "/tmp/hostpath-provisioner",
						tagDevice: "/tmp/hostpath_pv",
						tagFSType: fsExt4,
						tagMode:   "rw",
					},
				},
			},
			want: []internal.Measurement{
				{
					Name:   inputName,
					Fields: nil,
					Tags: map[string]string{
						tagPath:   pathVar,
						tagDevice: "vda1",
						tagFSType: fsExt4,
						tagMode:   "rw",
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
