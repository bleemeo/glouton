// Copyright 2015-2023 Bleemeo
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
	"glouton/inputs"
	"glouton/inputs/internal"
	"glouton/types"
	"glouton/version"
	"os"
	"strings"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/disk"
)

type diskTransformer struct {
	mountPoint string
	matcher    types.Matcher
}

// IgnoredFSType should be dropped and use the config DF.IgnoreFSType.
var IgnoredFSType = []string{ //nolint:gochecknoglobals
	"autofs", "binfmt_misc", "bpf", "cgroup", "cgroup2", "configfs", "debugfs", "devpts", "devtmpfs",
	"fusectl", "hugetlbfs", "iso9660", "mqueue", "nsfs", "overlay", "proc", "procfs", "pstore", "rpc_pipefs",
	"securityfs", "selinuxfs", "squashfs", "sysfs", "sysfs", "tracefs", "tmpfs", "devfs", "aufs", "efivarfs",
}

// New initialise disk.Input
//
// mountPoint is the root path to monitor. Useful when running inside a Docker.
func New(mountPoint string, pathMatcher types.Matcher) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["disk"]

	if ok {
		diskInput, _ := input().(*disk.DiskStats)
		diskInput.IgnoreFS = IgnoredFSType

		diskDeduplicateInput := deduplicator{
			Input:    diskInput,
			hostroot: mountPoint,
		}

		dt := diskTransformer{
			strings.TrimRight(mountPoint, "/"),
			pathMatcher,
		}
		i = &internal.Input{
			Input: diskDeduplicateInput,
			Accumulator: internal.Accumulator{
				RenameGlobal:     dt.renameGlobal,
				TransformMetrics: dt.transformMetrics,
			},
			Name: "disk",
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

	gatherContext.Annotations.BleemeoItem = item
	gatherContext.Tags["mountpoint"] = item

	return gatherContext, false
}

func (dt diskTransformer) transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	usedPerc, ok := fields["used_percent"]
	delete(fields, "used_percent")

	if ok {
		fields["used_perc"] = usedPerc
	}

	return fields
}
