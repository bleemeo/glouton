// Copyright 2015-2018 Bleemeo
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
	"agentgo/inputs/internal"
	"errors"
	"strings"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/disk"
)

type diskTransformer struct {
	mountPoint string
	blacklist  []string
}

// New initialise disk.Input
//
// mountPoint is the root path to monitor. Useful when running inside a Docker.
//
// blacklist is a list of path-prefix to ignore. Path prefix means that "/mnt" and "/mnt/disk" both have "/mnt"
// as prefix, but "/mnt-disk" does not.
func New(mountPoint string, blacklist []string) (i telegraf.Input, err error) {
	blacklistTrimed := make([]string, len(blacklist))
	for i, v := range blacklist {
		blacklistTrimed[i] = strings.TrimRight(v, "/")
	}
	var input, ok = telegraf_inputs.Inputs["disk"]
	if ok {
		diskInput := input().(*disk.DiskStats)
		diskInput.IgnoreFS = []string{
			"tmpfs", "devtmpfs", "devfs", "overlay", "aufs", "squashfs",
		}
		dt := diskTransformer{
			strings.TrimRight(mountPoint, "/"),
			blacklistTrimed,
		}
		i = &internal.Input{
			Input: diskInput,
			Accumulator: internal.Accumulator{
				TransformMetrics: dt.transformMetrics,
				TransformTags:    dt.transformTags,
			},
		}
	} else {
		err = errors.New("Telegraf don't have \"disk\" input")
	}
	return
}

func (dt diskTransformer) transformTags(tags map[string]string) (newTags map[string]string, drop bool) {
	newTags = make(map[string]string)
	item, ok := tags["path"]
	if !ok {
		drop = true
		return
	}
	if !strings.HasPrefix(item, dt.mountPoint) {
		// partition don't start with mountPoint, so it's a parition
		// which is only inside the container. Ignore it
		drop = true
		return
	}
	item = strings.TrimPrefix(item, dt.mountPoint)
	if item == "" {
		item = "/"
	}
	for _, v := range dt.blacklist {
		if v == item || strings.HasPrefix(item, v+"/") {
			drop = true
			return
		}
	}
	newTags["item"] = item
	return
}

func (dt diskTransformer) transformMetrics(fields map[string]float64, tags map[string]string) map[string]float64 {
	usedPerc, ok := fields["used_percent"]
	delete(fields, "used_percent")
	if ok {
		fields["used_perc"] = usedPerc
	}
	return fields
}
