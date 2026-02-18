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
	Input    *disk.Disk
	hostroot string
}

func (d deduplicator) Gather(acc telegraf.Accumulator) error {
	tmp := &internal.StoreAccumulator{}
	err := d.Input.Gather(tmp)

	deduplicate(tmp, d.hostroot)
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

func deduplicate(acc *internal.StoreAccumulator, hostroot string) {
	deleted := make([]bool, len(acc.Measurement))
	pathToIndx := make(map[string]int, len(acc.Measurement))
	devToIndx := make(map[string]int, len(acc.Measurement))

	// when multiple mount on same path, last mount win
	for i, m := range acc.Measurement {
		if oldI, ok := pathToIndx[m.Tags["path"]]; ok {
			deleted[oldI] = true
		}

		pathToIndx[m.Tags["path"]] = i
	}

	// when multiple devices are mounted on multiple paths (bind mount):
	// * the shortest path wins... unless it doesn't start by the hostroot while the others do
	// Newer gopsutil report bind mount differently. device is the path of previous mount point, therefor we will
	// ignore all device that are actually a mount point. A mount point will be anything that start with "/" and not "/dev/"
	for i, m := range acc.Measurement {
		if deleted[i] {
			continue
		}

		if strings.HasPrefix(m.Tags["device"], "/") && !strings.HasPrefix(m.Tags["device"], "/dev/") {
			deleted[i] = true

			continue
		}

		if oldI, ok := devToIndx[m.Tags["device"]]; ok {
			oldIsShortest := len(acc.Measurement[oldI].Tags["path"]) <= len(m.Tags["path"])
			oldNoHostRootWhileNewHad := !strings.HasPrefix(acc.Measurement[oldI].Tags["path"], hostroot) && strings.HasPrefix(m.Tags["path"], hostroot)
			newNoHostRootWhileOldHad := !strings.HasPrefix(m.Tags["path"], hostroot) && strings.HasPrefix(acc.Measurement[oldI].Tags["path"], hostroot)

			if oldIsShortest && !oldNoHostRootWhileNewHad || newNoHostRootWhileOldHad {
				deleted[i] = true

				continue
			}

			deleted[oldI] = true
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
