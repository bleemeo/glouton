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
package disk

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

func TestEnrich(t *testing.T) {
	t.Parallel()

	const podUID = "e6a29bd7-8754-4f70-93cd-c32b3bc1cf41"

	podLabels := map[string]string{
		types.LabelNamespace: "prod",
		types.LabelPodName:   "postgres-0",
		types.LabelOwnerKind: "statefulset",
		types.LabelOwnerName: "postgres",
	}

	resolver := fakeResolver{
		byUID:      map[string]map[string]string{podUID: podLabels},
		volumeByPV: map[string]string{"pvc-2d560fc3": "data"},
	}

	cases := []struct {
		name        string
		path        string
		wantHandled bool
		wantDrop    bool
		wantTags    map[string]string
	}{
		{
			name:        "non pod volume is not handled",
			path:        "/",
			wantHandled: false,
		},
		{
			name:        "csi pod volume gets pv and volume labels, item=pv",
			path:        "/var/lib/kubelet/pods/" + podUID + "/volumes/kubernetes.io~csi/pvc-2d560fc3/mount",
			wantHandled: true,
			wantDrop:    false,
			wantTags: map[string]string{
				types.LabelNamespace: "prod",
				types.LabelPodName:   "postgres-0",
				types.LabelOwnerKind: "statefulset",
				types.LabelOwnerName: "postgres",
				types.LabelVolume:    "data",
				types.LabelItem:      "pvc-2d560fc3",
			},
		},
		{
			name:        "csi pod volume without PVC access gets only pv, item=pv",
			path:        "/var/lib/kubelet/pods/" + podUID + "/volumes/kubernetes.io~csi/pvc-no-access/mount",
			wantHandled: true,
			wantDrop:    false,
			wantTags: map[string]string{
				types.LabelNamespace: "prod",
				types.LabelPodName:   "postgres-0",
				types.LabelOwnerKind: "statefulset",
				types.LabelOwnerName: "postgres",
				types.LabelItem:      "pvc-no-access",
			},
		},
		{
			name:        "empty-dir pod volume gets volume label, item=pod/volume",
			path:        "/var/lib/kubelet/pods/" + podUID + "/volumes/kubernetes.io~empty-dir/cache",
			wantHandled: true,
			wantDrop:    false,
			wantTags: map[string]string{
				types.LabelNamespace: "prod",
				types.LabelPodName:   "postgres-0",
				types.LabelOwnerKind: "statefulset",
				types.LabelOwnerName: "postgres",
				types.LabelVolume:    "cache",
				types.LabelItem:      "postgres-0/cache",
			},
		},
		{
			name:        "unknown pod is held back",
			path:        "/var/lib/kubelet/pods/unknown-uid/volumes/kubernetes.io~csi/pvc-x/mount",
			wantHandled: true,
			wantDrop:    true,
			wantTags:    map[string]string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			enricher := &k8sEnricher{
				resolver:     resolver,
				holdTimeout:  holdTimeout,
				pendingSince: make(map[string]pendingEntry),
				now:          time.Now,
			}

			tags := make(map[string]string)

			drop, handled := enricher.enrich(tc.path, tags)
			if handled != tc.wantHandled {
				t.Fatalf("enrich() handled = %v, want %v", handled, tc.wantHandled)
			}

			if !tc.wantHandled {
				return
			}

			if drop != tc.wantDrop {
				t.Errorf("enrich() drop = %v, want %v", drop, tc.wantDrop)
			}

			if diff := cmp.Diff(tc.wantTags, tags); diff != "" {
				t.Errorf("tags mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestEnrichHoldTimeout(t *testing.T) {
	t.Parallel()

	const path = "/var/lib/kubelet/pods/unknown-uid/volumes/kubernetes.io~csi/pvc-x/mount"

	now := time.Now()
	enricher := &k8sEnricher{
		resolver:     fakeResolver{byUID: map[string]map[string]string{}},
		holdTimeout:  holdTimeout,
		pendingSince: make(map[string]pendingEntry),
		now:          func() time.Time { return now },
	}

	// First sighting: held back (dropped).
	tags := make(map[string]string)
	if drop, handled := enricher.enrich(path, tags); !handled || !drop {
		t.Fatalf("first sighting: drop=%v handled=%v, want both true", drop, handled)
	}

	// Still within the hold window.
	now = now.Add(holdTimeout - time.Second)

	tags = make(map[string]string)
	if drop, _ := enricher.enrich(path, tags); !drop {
		t.Fatalf("within hold window: drop=%v, want true", drop)
	}

	// Past the hold timeout: degraded emission with the raw mount path as item.
	now = now.Add(2 * time.Second)

	tags = make(map[string]string)

	drop, handled := enricher.enrich(path, tags)
	if !handled || drop {
		t.Fatalf("past timeout: drop=%v handled=%v, want handled=true drop=false", drop, handled)
	}

	want := map[string]string{types.LabelItem: path}
	if diff := cmp.Diff(want, tags); diff != "" {
		t.Errorf("degraded tags mismatch (-want +got):\n%s", diff)
	}
}
