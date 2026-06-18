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
package kubernetes

import (
	"context"
	"maps"
	"strings"
	"testing"

	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
)

func podWithVolume(uid, name, namespace, volumeName, claimName string, owner metav1.OwnerReference) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:             apitypes.UID(uid),
			Name:            name,
			Namespace:       namespace,
			OwnerReferences: []metav1.OwnerReference{owner},
		},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: volumeName,
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claimName},
					},
				},
			},
		},
	}
}

func TestPodVolumeLabels(t *testing.T) {
	t.Parallel()

	const (
		ssUID  = "ss-uid"
		rsUID  = "rs-uid"
		depUID = "dep-uid"
	)

	statefulPod := podWithVolume(ssUID, "postgres-0", "prod", "data", "data-postgres-0",
		metav1.OwnerReference{Kind: "StatefulSet", Name: "postgres"})

	// A Deployment pod is owned by a ReplicaSet, itself owned by the Deployment.
	deployPod := podWithVolume(rsUID, "api-7c9f-x2", "prod", "cache", "cache-claim",
		metav1.OwnerReference{Kind: "ReplicaSet", Name: "api-7c9f", UID: "the-rs"})

	k := &Kubernetes{
		podID2Pod: map[string]corev1.Pod{
			ssUID: statefulPod,
			rsUID: deployPod,
		},
		replicasetOwnerByUID: map[string]metav1.OwnerReference{
			"the-rs": {Kind: "Deployment", Name: "api"},
		},
	}

	cases := []struct {
		name   string
		uid    string
		want   map[string]string
		wantOK bool
	}{
		{
			name:   "statefulset immediate owner",
			uid:    ssUID,
			wantOK: true,
			want: map[string]string{
				types.LabelNamespace: "prod",
				types.LabelPodName:   "postgres-0",
				types.LabelOwnerKind: "statefulset",
				types.LabelOwnerName: "postgres",
			},
		},
		{
			name:   "deployment owner resolved through replicaset",
			uid:    rsUID,
			wantOK: true,
			want: map[string]string{
				types.LabelNamespace: "prod",
				types.LabelPodName:   "api-7c9f-x2",
				types.LabelOwnerKind: "deployment",
				types.LabelOwnerName: "api",
			},
		},
		{
			name:   "unknown pod",
			uid:    "nope",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := k.PodVolumeLabels(tc.uid)
			if ok != tc.wantOK {
				t.Fatalf("PodVolumeLabels(%q) ok = %v, want %v", tc.uid, ok, tc.wantOK)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("PodVolumeLabels(%q) mismatch (-want +got):\n%s", tc.uid, diff)
			}
		})
	}
}

func TestCSIVolumeLabels(t *testing.T) {
	t.Parallel()

	const podUID = "ss-uid"

	// A pod with a PVC-backed volume "data" and an inline (ephemeral) CSI volume "secrets".
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{UID: apitypes.UID(podUID), Name: "postgres-0", Namespace: "prod"},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name:         "data",
					VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data-postgres-0"}},
				},
				{
					Name:         "secrets",
					VolumeSource: corev1.VolumeSource{CSI: &corev1.CSIVolumeSource{Driver: "secrets.csi"}},
				},
			},
		},
	}

	k := &Kubernetes{
		podID2Pod: map[string]corev1.Pod{podUID: pod},
		pvcByPVName: map[string]pvcReference{
			"pvc-abc": {namespace: "prod", name: "data-postgres-0"},
			// A PVC of the same name but in another namespace must not match.
			"pvc-other-ns": {namespace: "staging", name: "data-postgres-0"},
		},
	}

	cases := []struct {
		name string
		uid  string
		dir  string
		want map[string]string
	}{
		{
			name: "pvc-backed resolved to volume name",
			uid:  podUID,
			dir:  "pvc-abc",
			want: map[string]string{types.LabelPV: "pvc-abc", types.LabelVolume: "data"},
		},
		{
			name: "pvc-backed without PVC access keeps only pv",
			uid:  podUID,
			dir:  "pvc-missing",
			want: map[string]string{types.LabelPV: "pvc-missing"},
		},
		{
			name: "pvc in another namespace keeps only pv",
			uid:  podUID,
			dir:  "pvc-other-ns",
			want: map[string]string{types.LabelPV: "pvc-other-ns"},
		},
		{
			name: "inline ephemeral csi volume is a volume name, not a pv",
			uid:  podUID,
			dir:  "secrets",
			want: map[string]string{types.LabelVolume: "secrets"},
		},
		{
			name: "unknown pod falls back to pv",
			uid:  "nope",
			dir:  "pvc-abc",
			want: map[string]string{types.LabelPV: "pvc-abc"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := k.CSIVolumeLabels(tc.uid, tc.dir)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("CSIVolumeLabels(%q, %q) mismatch (-want +got):\n%s", tc.uid, tc.dir, diff)
			}
		})
	}
}

// TestResolverRealData runs the resolver against real `kubectl get pods/pvc -o yaml`
// output (testdata/pod_volume_labels) and the real `df -t ext4` mount paths from the
// node, to validate label resolution for genuine PVC and generic-ephemeral volumes.
func TestResolverRealData(t *testing.T) {
	t.Parallel()

	mock, err := newKubernetesMock("testdata/pod_volume_labels")
	if err != nil {
		t.Fatal(err)
	}

	k := &Kubernetes{
		NodeName: "node2",
		openConnection: func(context.Context, string, string) (kubeClient, error) {
			return mock, nil
		},
	}

	if err := k.updatePods(t.Context()); err != nil {
		t.Fatal(err)
	}

	const podUID = "e001081c-a249-4dce-a24a-b8b332cabe9d"

	wantPodLabels := map[string]string{
		types.LabelNamespace: "ns-testcase1",
		types.LabelPodName:   "volumes-demo-0",
		types.LabelOwnerKind: "statefulset",
		types.LabelOwnerName: "volumes-demo",
	}

	if got, ok := k.PodVolumeLabels(podUID); !ok || !maps.Equal(got, wantPodLabels) {
		t.Errorf("PodVolumeLabels(%q) = (%v, %v), want %v", podUID, got, ok, wantPodLabels)
	}

	// Real pod-volume mounts of volumes-demo-0 from `df -t ext4` on the node. Its other
	// volumes (emptyDir scratch, configMap, projected, image) are absent: only the two
	// CSI volumes are real filesystem mounts.
	cases := []struct {
		name string
		path string
		want map[string]string
	}{
		{
			name: "statefulset PVC (volumeClaimTemplate)",
			path: "/var/lib/kubelet/pods/e001081c-a249-4dce-a24a-b8b332cabe9d/volumes/kubernetes.io~csi/pvc-7de278db-ec0b-4393-bc18-938e64f4e687/mount",
			want: map[string]string{
				types.LabelPV:     "pvc-7de278db-ec0b-4393-bc18-938e64f4e687",
				types.LabelVolume: "data",
			},
		},
		{
			name: "generic ephemeral volume",
			path: "/var/lib/kubelet/pods/e001081c-a249-4dce-a24a-b8b332cabe9d/volumes/kubernetes.io~csi/pvc-ad29bb45-7d82-42cd-bb9c-b73e0b21e5c5/mount",
			want: map[string]string{
				types.LabelPV:     "pvc-ad29bb45-7d82-42cd-bb9c-b73e0b21e5c5",
				types.LabelVolume: "generic-eph",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uid, dir := podUIDAndDir(t, tc.path)

			got := k.CSIVolumeLabels(uid, dir)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("CSIVolumeLabels mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// podUIDAndDir extracts the pod UID and the volume directory from a kubelet pod volume
// mount path of the form /var/lib/kubelet/pods/<uid>/volumes/<plugin>/<dir>/...
func podUIDAndDir(t *testing.T, path string) (podUID, dir string) {
	t.Helper()

	parts := strings.Split(strings.TrimPrefix(path, "/var/lib/kubelet/pods/"), "/")
	if len(parts) < 4 {
		t.Fatalf("unexpected mount path %q", path)
	}

	return parts[0], parts[3]
}
