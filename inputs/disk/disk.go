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
	"maps"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/version"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/disk"
)

// inputName is the name of the disk input plugin.
const inputName = "disk"

// kubeletPodsPrefix is the prefix of kubelet mount paths for pod volumes.
const kubeletPodsPrefix = "/var/lib/kubelet/pods/"

// csiPlugin is the volume plugin directory used by CSI volumes. For these, the
// path holds the PersistentVolume name; for every other plugin it holds the
// in-pod volume name (.spec.volumes[].name).
const csiPlugin = "kubernetes.io~csi"

// holdTimeout is how long a pod-volume metric is held back (dropped) while waiting
// for its pod to be discovered. Past this delay the metric is emitted in degraded
// mode (raw mount path as item, without Kubernetes labels) so a full disk stays
// visible even if the pod association is broken.
const holdTimeout = 5 * time.Minute

// KubernetesPodResolver resolves Kubernetes labels for a pod volume mount.
type KubernetesPodResolver interface {
	// PodVolumeLabels returns the Kubernetes labels (namespace, pod_name, owner_kind,
	// owner_name) for the pod with the given UID. found is false when the pod is not
	// (yet) known.
	PodVolumeLabels(podUID string) (labels map[string]string, found bool)
	// CSIVolumeLabels returns the labels identifying a CSI volume mount whose kubelet
	// directory is dir: {pv} (+ {volume} when resolvable) for a PVC-backed volume, or
	// {volume} for an inline (ephemeral) CSI volume.
	CSIVolumeLabels(podUID, dir string) map[string]string
}

type diskTransformer struct {
	mountPoint string
	matcher    types.Matcher
	enricher   *k8sEnricher
}

// New initializes disk.Input
//
// mountPoint is the root path to monitor. Useful when running inside a Docker.
// k8sResolver may be nil; when set, filesystem metrics of Kubernetes pod volumes
// are enriched with pod labels (namespace, pod_name, owner_*, volume/pv) and their
// item is rewritten to a short stable per-node identifier instead of the volatile
// kubelet mount path.
func New(mountPoint string, pathMatcher types.Matcher, ignoreFSTypes []string, k8sResolver KubernetesPodResolver) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs[inputName]

	if ok {
		diskInput, _ := input().(*disk.Disk)
		diskInput.IgnoreFS = ignoreFSTypes

		diskDeduplicateInput := deduplicator{
			Input: diskInput,
		}

		dt := diskTransformer{
			mountPoint: strings.TrimRight(mountPoint, "/"),
			matcher:    pathMatcher,
		}

		if k8sResolver != nil {
			dt.enricher = &k8sEnricher{
				resolver:     k8sResolver,
				holdTimeout:  holdTimeout,
				pendingSince: make(map[string]pendingEntry),
				now:          time.Now,
			}
		}

		i = &internal.Input{
			Input: diskDeduplicateInput,
			Accumulator: internal.Accumulator{
				RenameGlobal:     dt.renameGlobal,
				TransformMetrics: dt.transformMetrics,
			},
			Name: inputName,
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return i, err
}

func (dt diskTransformer) renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	item, ok := gatherContext.Tags["path"]
	gatherContext.Tags = make(map[string]string)

	if !ok {
		return gatherContext, true
	}

	// telegraf's 'disk' input add a backslash to disk names on Windows (https://github.com/influxdata/telegraf/blob/7ae240326bb2d3de80eab24088cf31cfa9da2f82/plugins/inputs/system/ps.go#L135)
	// (the forward slash is translated to a backslash by filepath.Join())
	// TODO: maybe we should do a PR ?
	if version.IsWindows() && len(item) > 0 && item[0] == os.PathSeparator {
		item = item[1:]
	}

	if item == "" {
		item = "/"
	}

	if !dt.matcher.Match(item) {
		return gatherContext, true
	}

	// Kubernetes pod volumes get pod labels and a short stable item instead of the
	// volatile kubelet mount path. handled is true when item is such a pod volume mount.
	if dt.enricher != nil {
		if drop, handled := dt.enricher.enrich(item, gatherContext.Tags); handled {
			return gatherContext, drop
		}
	}

	gatherContext.Tags[types.LabelItem] = item

	return gatherContext, false
}

type pendingEntry struct {
	firstSeen time.Time
	lastSeen  time.Time
}

// k8sEnricher adds Kubernetes pod labels to filesystem metrics of pod volumes.
type k8sEnricher struct {
	resolver    KubernetesPodResolver
	holdTimeout time.Duration
	lastPurge   time.Time

	l sync.Mutex
	// pendingSince records, per unresolved pod volume, when it was first seen, to
	// implement the hold timeout.
	pendingSince map[string]pendingEntry
	now          func() time.Time
}

// enrich sets the Kubernetes labels of a pod volume mount into tags, dropping the
// item label. handled is false when path is not a pod volume mount (the caller
// then keeps the normal item label). drop is true while the metric is held back
// waiting for its pod to be discovered.
func (e *k8sEnricher) enrich(path string, tags map[string]string) (drop, handled bool) {
	e.runPurge()

	podUID, plugin, dir, ok := parsePodVolumePath(path)
	if !ok {
		return false, false
	}

	podLabels, found := e.resolver.PodVolumeLabels(podUID)
	if !found {
		return e.holdOrDegrade(podUID, dir, path, tags), true
	}

	e.l.Lock()
	delete(e.pendingSince, podUID+"/"+dir)
	e.l.Unlock()

	for name, value := range podLabels {
		if value != "" {
			tags[name] = value
		}
	}

	if plugin == csiPlugin {
		// A CSI directory is the PersistentVolume name (PVC-backed) or the in-pod volume
		// name (inline ephemeral); the resolver classifies it. For other plugins the path
		// directory already is the volume name.
		maps.Copy(tags, e.resolver.CSIVolumeLabels(podUID, dir))
	} else {
		tags[types.LabelVolume] = dir
	}

	// item is the shortest stable identifier unique per node, kept for dashboards that
	// group disks by item. The PV name is unique per node; otherwise (inline ephemeral
	// volumes) the volume name is only unique within the pod, so prefix it with pod_name.
	switch {
	case tags[types.LabelPV] != "":
		tags[types.LabelItem] = tags[types.LabelPV]
		// no need to kept the same value in "item" and "pv"
		delete(tags, types.LabelPV)
	case tags[types.LabelVolume] != "":
		tags[types.LabelItem] = tags[types.LabelPodName] + "/" + tags[types.LabelVolume]
	}

	return false, true
}

func (e *k8sEnricher) runPurge() {
	e.l.Lock()
	defer e.l.Unlock()

	if e.lastPurge.Add(time.Hour).After(e.now()) {
		return
	}

	e.lastPurge = e.now()

	for k, entry := range e.pendingSince {
		if entry.lastSeen.Add(10 * time.Minute).Before(e.now()) {
			delete(e.pendingSince, k)
		}
	}
}

// holdOrDegrade decides whether to keep holding back an unresolved pod volume
// (drop=true) or, once the hold timeout elapsed, emit it in degraded mode (item =
// raw mount path, no Kubernetes labels).
func (e *k8sEnricher) holdOrDegrade(podUID, dir, path string, tags map[string]string) (drop bool) {
	key := podUID + "/" + dir

	e.l.Lock()

	entry, exists := e.pendingSince[key]
	if !exists {
		entry.firstSeen = e.now()
	}

	entry.lastSeen = e.now()
	e.pendingSince[key] = entry

	e.l.Unlock()

	if e.now().Sub(entry.firstSeen) < e.holdTimeout {
		return true
	}

	tags[types.LabelItem] = path

	return false
}

// parsePodVolumePath parses a kubelet pod volume mount path of the form
// /var/lib/kubelet/pods/<uid>/volumes/<plugin>/<dir>[/...]. It returns the pod
// UID, the volume plugin and the volume directory, with ok=true on a match.
func parsePodVolumePath(path string) (podUID, plugin, dir string, ok bool) {
	rest, found := strings.CutPrefix(path, kubeletPodsPrefix)
	if !found {
		return "", "", "", false
	}

	parts := strings.Split(rest, "/")
	if len(parts) < 4 || parts[1] != "volumes" {
		return "", "", "", false
	}

	return parts[0], parts[2], parts[3], true
}

func (dt diskTransformer) transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]any) map[string]float64 {
	_ = currentContext
	_ = originalFields

	usedPerc, ok := fields["used_percent"]
	delete(fields, "used_percent")

	if ok {
		fields["used_perc"] = usedPerc
	}

	return fields
}
