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
	"strings"

	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/inputs/disk"
)

type deduplicator struct {
	Input *disk.Disk
}

func (d deduplicator) Gather(acc telegraf.Accumulator) error {
	tmp := &internal.StoreAccumulator{}
	err := d.Input.Gather(tmp)

	deduplicate(tmp)
	tmp.Send(acc)

	return err
}

// SampleConfig returns the default configuration of the Processor.
func (d deduplicator) SampleConfig() string {
	return d.Input.SampleConfig()
}

func (d deduplicator) Init() error {
	return d.Input.Init()
}

func deduplicate(acc *internal.StoreAccumulator) {
	// For bind-mount, we will:
	//  * Report the first mount-point used... unless the 1st mount-point is on /var/lib/kubelet/plugins/kubernetes.io/csi,
	//    because Kubernetes with CSI will mount on CSI "internal" path the real device then kubelet mount on
	//    /var/lib/kubelet/pods using bind-mount. The later mount-point is more interesting to user.
	//  * Always report "real" device (device of first mount-point), in case of bind-mount
	//  * We assume mounts are ordered
	//
	// When two mount occur on same mount-point, the last mount win.
	deleted := make([]bool, len(acc.Measurement))
	devToIndx := make(map[string]int, len(acc.Measurement))
	pathToIndx := make(map[string]int, len(acc.Measurement))

	for i, m := range acc.Measurement {
		// look at *previous* mounts (because we assume mount are ordered) if they Tags["path"] is our device
		for oldI := range i {
			if m.Tags["device"] == acc.Measurement[oldI].Tags["path"] {
				m.Tags["device"] = acc.Measurement[oldI].Tags["device"]
			}
		}
	}

	// when multiple mount on same path, last mount win
	for i, m := range acc.Measurement {
		if oldI, ok := pathToIndx[m.Tags["path"]]; ok {
			deleted[oldI] = true
		}

		pathToIndx[m.Tags["path"]] = i
	}

	for i, m := range acc.Measurement {
		if deleted[i] {
			continue
		}

		if oldI, ok := devToIndx[m.Tags["device"]]; ok {
			oldPath := acc.Measurement[oldI].Tags["path"]

			if strings.HasPrefix(oldPath, "/var/lib/kubelet/plugins/kubernetes.io/csi") {
				deleted[oldI] = true
			} else {
				deleted[i] = true

				continue
			}
		}

		devToIndx[m.Tags["device"]] = i
	}

	n := 0

	for i, x := range acc.Measurement {
		if !deleted[i] {
			acc.Measurement[n] = x
			n++
		}
	}

	acc.Measurement = acc.Measurement[:n]
}
