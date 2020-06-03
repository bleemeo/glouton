// Copyright 2015-2019 Bleemeo
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
	"errors"
	"fmt"
	"glouton/inputs/internal"
	"regexp"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/diskio"
)

type diskIOTransformer struct {
	whitelist []*regexp.Regexp
	blacklist []*regexp.Regexp
}

// New initialise diskio.Input.
//
// whitelist is a list of regular expretion for device to include
// blacklist is a list of regular expretion for device to include
//
// If blacklist is provided (not empty) it's used and whitelist is ignored.
func New(whitelist []string, blacklist []string) (i telegraf.Input, err error) {
	var input, ok = telegraf_inputs.Inputs["diskio"]

	whitelistRE := make([]*regexp.Regexp, len(whitelist))

	for index, v := range whitelist {
		whitelistRE[index], err = regexp.Compile(v)
		if err != nil {
			err = fmt.Errorf("diskio whitelist RE compile fail: %s", err)
			return
		}
	}

	blacklistRE := make([]*regexp.Regexp, len(blacklist))

	for index, v := range blacklist {
		blacklistRE[index], err = regexp.Compile(v)
		if err != nil {
			err = fmt.Errorf("diskio blacklist RE compile fail: %s", err)
			return
		}
	}

	if ok {
		diskioInput := input().(*diskio.DiskIO)
		diskioInput.Log = internal.Logger{}
		dt := diskIOTransformer{
			whitelist: whitelistRE,
			blacklist: blacklistRE,
		}
		i = &internal.Input{
			Input: diskioInput,
			Accumulator: internal.Accumulator{
				RenameGlobal:     dt.renameGlobal,
				DerivatedMetrics: []string{"read_bytes", "read_time", "reads", "write_bytes", "writes", "write_time", "io_time"},
				TransformMetrics: dt.transformMetrics,
			},
		}
	} else {
		err = errors.New("input diskio not enabled in Telegraf")
	}

	return i, err
}

func (dt diskIOTransformer) renameGlobal(originalContext internal.GatherContext) (newContext internal.GatherContext, drop bool) {
	newContext.Measurement = "io"
	newContext.Tags = make(map[string]string)
	item, ok := originalContext.Tags["name"]

	if !ok {
		return newContext, true
	}

	match := false

	if len(dt.blacklist) > 0 {
		match = true

		for _, r := range dt.blacklist {
			if r.MatchString(item) {
				match = false
				break
			}
		}
	} else {
		for _, r := range dt.whitelist {
			if r.MatchString(item) {
				match = true
				break
			}
		}
	}

	if !match {
		return newContext, true
	}

	newContext.Annotations.BleemeoItem = item
	newContext.Tags["device"] = item

	return newContext, false
}

func (dt diskIOTransformer) transformMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	if ioTime, ok := fields["io_time"]; ok {
		delete(fields, "io_time")
		fields["time"] = ioTime
		// io_time is millisecond per second.
		fields["utilization"] = ioTime / 1000. * 100.
	}

	delete(fields, "weighted_io_time")
	delete(fields, "iops_in_progress")

	return fields
}
