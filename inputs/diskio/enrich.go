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

package diskio

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/inputs/internal"
)

// mountinfoTTL is how long the parsed device->pod-volume map is cached before being
// rebuilt from the host mount table. Block device attribution only changes when a pod
// (and its volume) is scheduled or removed, so a short TTL is plenty.
const mountinfoTTL = 30 * time.Second

// KubernetesPodResolver resolves Kubernetes labels for a pod volume mount. It is
// satisfied by *kubernetes.Kubernetes, the same resolver used by the disk input.
type KubernetesPodResolver interface {
	// PodVolumeLabels returns the Kubernetes labels (namespace, pod_name, owner_kind,
	// owner_name) for the pod with the given UID. found is false when the pod is not
	// (yet) known.
	PodVolumeLabels(podUID string) (labels map[string]string, found bool)
	// CSIVolumeLabels returns the labels identifying a CSI volume mount whose kubelet
	// directory is dir: {pv} (+ {volume} when resolvable).
	CSIVolumeLabels(podUID, dir string) map[string]string
}

// csiVolume identifies the single CSI pod volume that backs a block device.
type csiVolume struct {
	podUID string
	dir    string // the PersistentVolume name (the kubelet CSI directory)
}

// k8sEnricher adds Kubernetes pod labels to the IO metrics of block devices that are
// dedicated to a single CSI PersistentVolume (the cloud block-volume case, e.g. EBS,
// GCE PD, Azure Disk). Unlike the filesystem case, the diskio input only exposes a
// device name (not a path), so the device is mapped to its backing pod volume through
// the host mount table.
type k8sEnricher struct {
	resolver KubernetesPodResolver
	ttl      time.Duration
	now      func() time.Time
	// readMountinfo returns the content of the host /proc/<pid>/mountinfo and
	// statDevice resolves a kernel device name to its "major:minor". Both are fields so
	// tests can inject fixtures instead of touching the real /proc and /sys.
	readMountinfo func() ([]byte, error)
	statDevice    func(name string) (majMin string, ok bool)

	l         sync.Mutex
	lastBuild time.Time
	// byMajMin maps a block device "major:minor" to the single CSI pod volume it backs.
	// Devices backing zero or more than one distinct pod volume (node root, shared
	// ReadWriteMany, local-path provisioner...) are absent: they keep a bare item.
	byMajMin map[string]csiVolume
	// deviceMajMin caches device-name -> "major:minor" lookups (a sysfs read each),
	// including negative results (empty string) so a device without a sysfs entry is not
	// re-stat'd every gather. The mapping is very stable, so it is only refreshed
	// together with byMajMin (same TTL), which also bounds staleness when a device name
	// is reused for another device.
	deviceMajMin map[string]string
}

func newK8sEnricher(resolver KubernetesPodResolver) *k8sEnricher {
	hostProc := os.Getenv("HOST_PROC")
	if hostProc == "" {
		hostProc = "/proc"
	}

	hostSys := os.Getenv("HOST_SYS")
	if hostSys == "" {
		hostSys = "/sys"
	}

	return &k8sEnricher{
		resolver:      resolver,
		ttl:           mountinfoTTL,
		now:           time.Now,
		readMountinfo: func() ([]byte, error) { return readMountinfo(hostProc) },
		statDevice:    func(name string) (string, bool) { return deviceMajMin(hostSys, name) },
	}
}

// enrich adds the Kubernetes pod labels of a block device dedicated to a single CSI
// PersistentVolume into tags. It is a no-op (the device keeps its bare item) when the
// device is not such a dedicated PVC device, or while its pod is not yet discovered.
func (e *k8sEnricher) enrich(deviceName string, tags map[string]string) {
	e.l.Lock()

	if e.byMajMin == nil || e.lastBuild.Add(e.ttl).Before(e.now()) {
		e.byMajMin = e.buildLocked()
		e.deviceMajMin = make(map[string]string)
		e.lastBuild = e.now()
	}

	majMin, cached := e.deviceMajMin[deviceName]
	if !cached {
		// Cache the result even on failure (empty string): a device that has no sysfs
		// entry won't grow one, so this avoids re-stat'ing it on every gather. The
		// negative entry is dropped at the next TTL rebuild like the rest.
		majMin, _ = e.statDevice(deviceName)
		e.deviceMajMin[deviceName] = majMin
	}

	vol, ok := e.byMajMin[majMin]
	e.l.Unlock()

	if majMin == "" || !ok {
		return
	}

	podLabels, found := e.resolver.PodVolumeLabels(vol.podUID)
	if !found {
		// The device is a PVC block device but its pod is not (yet) in the cache. Emit
		// the device with a bare item for now; labels will appear on a later cycle.
		return
	}

	for name, value := range podLabels {
		if value != "" {
			tags[name] = value
		}
	}

	for name, value := range e.resolver.CSIVolumeLabels(vol.podUID, vol.dir) {
		if value != "" {
			tags[name] = value
		}
	}
}

// buildLocked parses the host mount table and returns the device->pod-volume map,
// keeping only block devices that back exactly one CSI pod volume.
func (e *k8sEnricher) buildLocked() map[string]csiVolume {
	content, err := e.readMountinfo()
	if err != nil {
		return map[string]csiVolume{}
	}

	result := make(map[string]csiVolume)
	ambiguous := make(map[string]bool)

	for line := range strings.Lines(string(content)) {
		majMin, mountPoint, ok := parseMountinfoLine(line)
		if !ok {
			continue
		}

		podUID, plugin, dir, ok := internal.ParsePodVolumePath(mountPoint)
		if !ok || plugin != internal.CSIPlugin {
			continue
		}

		vol := csiVolume{podUID: podUID, dir: dir}

		if existing, seen := result[majMin]; seen && existing != vol {
			ambiguous[majMin] = true

			continue
		}

		result[majMin] = vol
	}

	for majMin := range ambiguous {
		delete(result, majMin)
	}

	return result
}

// parseMountinfoLine extracts the "major:minor" and the mount point from a line of
// /proc/<pid>/mountinfo. The format is:
//
//	36 35 98:0 /root /mountpoint options... - fstype source superopts
//
// The two fields we need are at fixed positions (3rd and 5th), before the variable
// number of optional fields, so there is no need to find the "-" separator.
func parseMountinfoLine(line string) (majMin, mountPoint string, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return "", "", false
	}

	// The mount point is escaped by the kernel (spaces as \040, etc.). Pod volume paths
	// never contain such characters, so an unescaped value simply won't match the
	// kubelet prefix, which is the behaviour we want.
	return fields[2], fields[4], true
}

// readMountinfo returns the content of the host mount table. It reads PID 1's
// mountinfo, which lives in the host mount namespace and therefore sees the kubelet
// mounts, falling back to the current process when PID 1 is not readable.
func readMountinfo(hostProc string) ([]byte, error) {
	content, err := os.ReadFile(filepath.Join(hostProc, "1", "mountinfo")) //nolint:gosec
	if err != nil {
		return os.ReadFile(filepath.Join(hostProc, "self", "mountinfo")) //nolint:gosec
	}

	return content, nil
}

// deviceMajMin resolves a kernel block device name (as reported by the diskio input,
// e.g. "nvme1n1" or "dm-3") to its "major:minor" through sysfs.
func deviceMajMin(hostSys, name string) (string, bool) {
	content, err := os.ReadFile(filepath.Join(hostSys, "class", "block", name, "dev")) //nolint:gosec
	if err != nil {
		return "", false
	}

	majMin := strings.TrimSpace(string(content))
	if majMin == "" {
		return "", false
	}

	return majMin, true
}
