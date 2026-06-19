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

package internal

import "strings"

// KubeletPodsPrefix is the prefix of kubelet mount paths for pod volumes.
const KubeletPodsPrefix = "/var/lib/kubelet/pods/"

// CSIPlugin is the volume plugin directory used by CSI volumes. For these, the path
// holds the PersistentVolume name; for every other plugin it holds the in-pod volume
// name (.spec.volumes[].name).
const CSIPlugin = "kubernetes.io~csi"

// ParsePodVolumePath parses a kubelet pod volume mount path of the form
// /var/lib/kubelet/pods/<uid>/volumes/<plugin>/<dir>[/...]. It returns the pod UID,
// the volume plugin and the volume directory, with ok=true on a match.
func ParsePodVolumePath(path string) (podUID, plugin, dir string, ok bool) {
	rest, found := strings.CutPrefix(path, KubeletPodsPrefix)
	if !found {
		return "", "", "", false
	}

	parts := strings.Split(rest, "/")
	if len(parts) < 4 || parts[1] != "volumes" {
		return "", "", "", false
	}

	return parts[0], parts[2], parts[3], true
}
