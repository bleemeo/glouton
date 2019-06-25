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

package diskio

import (
	"agentgo/inputs/internal"
	"errors"
	"fmt"
	"regexp"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/diskio"
)

type diskIOTransformer struct {
	whitelist []*regexp.Regexp
}

// New initialise diskio.Input
//
// whitelist is a list of regular expretion for device to include
func New(whitelist []string) (i telegraf.Input, err error) {
	var input, ok = telegraf_inputs.Inputs["diskio"]
	whitelistRE := make([]*regexp.Regexp, len(whitelist))
	for index, v := range whitelist {
		whitelistRE[index], err = regexp.Compile(v)
		if err != nil {
			err = fmt.Errorf("diskio whitelist RE compile fail: %s", err)
			return
		}
	}
	if ok {
		diskioInput := input().(*diskio.DiskIO)
		dt := diskIOTransformer{
			whitelist: whitelistRE,
		}
		i = &internal.Input{
			Input: diskioInput,
			Accumulator: internal.Accumulator{
				NewMeasurement:   "io",
				DerivatedMetrics: []string{"read_bytes", "read_time", "reads", "write_bytes", "writes", "write_time", "io_time"},
				TransformMetrics: dt.transformMetrics,
				TransformTags:    dt.transformTags,
			},
		}
	} else {
		err = errors.New("Telegraf don't have \"diskio\" input")
	}
	return
}

func (dt diskIOTransformer) transformTags(tags map[string]string) (newTags map[string]string, drop bool) {
	newTags = make(map[string]string)
	item, ok := tags["name"]
	if !ok {
		drop = true
		return
	}
	match := false
	for _, r := range dt.whitelist {
		if r.MatchString(item) {
			match = true
			break
		}
	}
	if !match {
		drop = true
		return
	}
	newTags["item"] = item
	return
}

func (dt diskIOTransformer) transformMetrics(fields map[string]float64, tags map[string]string) map[string]float64 {
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
