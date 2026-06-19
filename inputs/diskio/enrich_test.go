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

//nolint:goconst
package diskio

import (
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
)

// fakeResolver is a test KubernetesPodResolver returning labels for known pod UIDs.
type fakeResolver struct {
	byUID map[string]map[string]string
	// volumeByPV maps a PersistentVolume name to its in-pod volume name.
	volumeByPV map[string]string
}

func (r fakeResolver) PodVolumeLabels(podUID string) (map[string]string, bool) {
	labels, ok := r.byUID[podUID]

	return labels, ok
}

func (r fakeResolver) CSIVolumeLabels(_, dir string) map[string]string {
	if name, ok := r.volumeByPV[dir]; ok {
		return map[string]string{types.LabelPV: dir, types.LabelVolume: name}
	}

	return map[string]string{types.LabelPV: dir}
}

func TestParseMountinfoLine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		line          string
		wantOK        bool
		wantMajMin    string
		wantMountPath string
	}{
		{
			name:          "csi mount with optional fields",
			line:          "2202 1234 259:1 / /var/lib/kubelet/pods/abc/volumes/kubernetes.io~csi/pvc-x/mount rw,relatime shared:123 - ext4 /dev/nvme1n1 rw\n",
			wantOK:        true,
			wantMajMin:    "259:1",
			wantMountPath: "/var/lib/kubelet/pods/abc/volumes/kubernetes.io~csi/pvc-x/mount",
		},
		{
			name:          "root mount without optional fields",
			line:          "30 1 259:0 / / rw - ext4 /dev/nvme0n1p1 rw",
			wantOK:        true,
			wantMajMin:    "259:0",
			wantMountPath: "/",
		},
		{
			name:   "too few fields",
			line:   "30 1 259:0 /",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			majMin, mountPoint, ok := parseMountinfoLine(tc.line)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}

			if ok && (majMin != tc.wantMajMin || mountPoint != tc.wantMountPath) {
				t.Errorf("got (%q, %q), want (%q, %q)", majMin, mountPoint, tc.wantMajMin, tc.wantMountPath)
			}
		})
	}
}

// realisticMountinfo holds a node with: the root disk, a dedicated EBS PVC, a CSI
// globalmount of that same PVC, a ReadWriteMany PVC shared by two pods, and a non-CSI
// pod volume.
const realisticMountinfo = `30 1 259:0 / / rw,relatime - ext4 /dev/nvme0n1p1 rw
2200 1234 259:1 / /var/lib/kubelet/plugins/kubernetes.io/csi/pv/pvc-data/globalmount rw shared:1 - ext4 /dev/nvme1n1 rw
2202 1234 259:1 / /var/lib/kubelet/pods/pod-uid-1/volumes/kubernetes.io~csi/pvc-data/mount rw shared:1 - ext4 /dev/nvme1n1 rw
2210 1234 259:2 / /var/lib/kubelet/pods/pod-uid-2/volumes/kubernetes.io~csi/pvc-shared/mount rw shared:2 - ext4 /dev/nvme2n1 rw
2211 1234 259:2 / /var/lib/kubelet/pods/pod-uid-3/volumes/kubernetes.io~csi/pvc-shared/mount rw shared:2 - ext4 /dev/nvme2n1 rw
2220 1234 259:3 / /var/lib/kubelet/pods/pod-uid-4/volumes/kubernetes.io~empty-dir/cache rw - ext4 /dev/nvme3n1 rw
`

// deviceMajMins maps the diskio device names of realisticMountinfo to their major:minor.
//
//nolint:gochecknoglobals
var deviceMajMins = map[string]string{
	"nvme0n1p1": "259:0",
	"nvme1n1":   "259:1",
	"nvme2n1":   "259:2",
	"nvme3n1":   "259:3",
}

func newTestEnricher(resolver KubernetesPodResolver, mountinfo string) *k8sEnricher {
	return &k8sEnricher{
		resolver:      resolver,
		ttl:           time.Minute,
		now:           func() time.Time { return time.Unix(0, 0) },
		readMountinfo: func() ([]byte, error) { return []byte(mountinfo), nil },
		statDevice: func(name string) (string, bool) {
			majMin, ok := deviceMajMins[name]

			return majMin, ok
		},
	}
}

func TestBuildLocked(t *testing.T) {
	t.Parallel()

	e := newTestEnricher(fakeResolver{}, realisticMountinfo)
	got := e.buildLocked()

	want := map[string]csiVolume{
		// Only the dedicated EBS PVC is kept. The globalmount of the same device shares
		// the (pod, pv) so it is not ambiguous; the shared RWX device (two distinct
		// pods) is dropped; the non-CSI plugin is ignored; the root disk is ignored.
		"259:1": {podUID: "pod-uid-1", dir: "pvc-data"},
	}

	if diff := cmp.Diff(want, got, cmp.AllowUnexported(csiVolume{})); diff != "" {
		t.Errorf("buildLocked() mismatch (-want +got):\n%s", diff)
	}
}

func TestEnrich(t *testing.T) {
	t.Parallel()

	resolver := fakeResolver{
		byUID: map[string]map[string]string{
			"pod-uid-1": {
				types.LabelNamespace: "prod",
				types.LabelPodName:   "postgres-0",
				types.LabelOwnerKind: "statefulset",
				types.LabelOwnerName: "postgres",
			},
		},
		volumeByPV: map[string]string{"pvc-data": "data"},
	}

	cases := []struct {
		name     string
		device   string
		resolver KubernetesPodResolver
		want     map[string]string
	}{
		{
			name:     "dedicated PVC device gets pod and volume labels",
			device:   "nvme1n1",
			resolver: resolver,
			want: map[string]string{
				types.LabelItem:      "nvme1n1",
				types.LabelNamespace: "prod",
				types.LabelPodName:   "postgres-0",
				types.LabelOwnerKind: "statefulset",
				types.LabelOwnerName: "postgres",
				types.LabelPV:        "pvc-data",
				types.LabelVolume:    "data",
			},
		},
		{
			name:     "root disk keeps a bare item",
			device:   "nvme0n1p1",
			resolver: resolver,
			want:     map[string]string{types.LabelItem: "nvme0n1p1"},
		},
		{
			name:     "shared RWX device is not attributed",
			device:   "nvme2n1",
			resolver: resolver,
			want:     map[string]string{types.LabelItem: "nvme2n1"},
		},
		{
			name:     "PVC device whose pod is not yet known keeps a bare item",
			device:   "nvme1n1",
			resolver: fakeResolver{volumeByPV: map[string]string{"pvc-data": "data"}},
			want:     map[string]string{types.LabelItem: "nvme1n1"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newTestEnricher(tc.resolver, realisticMountinfo)
			tags := map[string]string{types.LabelItem: tc.device}

			e.enrich(tc.device, tags)

			if diff := cmp.Diff(tc.want, tags); diff != "" {
				t.Errorf("enrich() tags mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestEnrichCachesDeviceLookup(t *testing.T) {
	t.Parallel()

	var statCalls int

	now := time.Unix(0, 0)
	e := &k8sEnricher{
		resolver:      fakeResolver{},
		ttl:           time.Minute,
		now:           func() time.Time { return now },
		readMountinfo: func() ([]byte, error) { return []byte(realisticMountinfo), nil },
		statDevice: func(name string) (string, bool) {
			statCalls++
			majMin, ok := deviceMajMins[name]

			return majMin, ok
		},
	}

	// Several gathers within the TTL: the sysfs lookup must happen only once per device.
	for range 5 {
		e.enrich("nvme1n1", map[string]string{})
	}

	if statCalls != 1 {
		t.Fatalf("statDevice called %d times within TTL, want 1", statCalls)
	}

	// Past the TTL the caches are rebuilt, so the device is looked up again.
	now = now.Add(2 * time.Minute)

	e.enrich("nvme1n1", map[string]string{})

	if statCalls != 2 {
		t.Fatalf("statDevice called %d times total, want 2 after TTL expiry", statCalls)
	}
}

func TestEnrichCachesNegativeLookup(t *testing.T) {
	t.Parallel()

	var statCalls int

	e := &k8sEnricher{
		resolver:      fakeResolver{},
		ttl:           time.Minute,
		now:           func() time.Time { return time.Unix(0, 0) },
		readMountinfo: func() ([]byte, error) { return []byte(realisticMountinfo), nil },
		statDevice: func(string) (string, bool) {
			statCalls++

			return "", false
		},
	}

	// A device with no sysfs entry must be stat'd only once within the TTL.
	for range 5 {
		e.enrich("ghost0", map[string]string{})
	}

	if statCalls != 1 {
		t.Fatalf("statDevice called %d times for a missing device, want 1", statCalls)
	}
}
