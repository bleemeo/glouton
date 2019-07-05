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

package elasticsearch

import (
	"agentgo/inputs/internal"
	"errors"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/elasticsearch"
)

// New initialise elasticsearch.Input
func New(url string) (i telegraf.Input, err error) {
	var input, ok = telegraf_inputs.Inputs["elasticsearch"]
	if ok {
		elasticsearchInput, ok := input().(*elasticsearch.Elasticsearch)
		if ok {
			elasticsearchInput.Servers = append(make([]string, 0), url)
			elasticsearchInput.Local = true
			elasticsearchInput.ClusterHealth = false
			i = &internal.Input{
				Input: elasticsearchInput,
				Accumulator: internal.Accumulator{
					RenameGlobal:     renameGlobal,
					DerivatedMetrics: []string{"search_query_total", "search_query_time_in_millis", "gc_collectors_old_collection_count", "gc_collectors_young_collection_count", "gc_collectors_old_collection_time_in_millis", "gc_collectors_young_collection_time_in_millis"},
					TransformMetrics: transformMetrics,
				},
			}
		} else {
			err = errors.New("input Elasticsearch is not the exepcted type")
		}
	} else {
		err = errors.New("input Elasticsearch not enabled in Telegraf")
	}
	return
}

func renameGlobal(originalContext internal.GatherContext) (newContext internal.GatherContext, drop bool) {
	newContext.Tags = originalContext.Tags
	newContext.Measurement = "elasticsearch"
	return
}

func transformMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	newFields := make(map[string]float64)
	switch originalContext.Measurement {
	case "elasticsearch_indices":
		if value, ok := fields["docs_count"]; ok {
			newFields["docs_count"] = value
		}
		if value, ok := fields["store_size_in_bytes"]; ok {
			newFields["size"] = value
		}
		if searchCount, ok := fields["search_query_total"]; ok {
			newFields["search"] = searchCount
			if searchTime, ok2 := fields["search_query_time_in_millis"]; ok2 {
				newFields["search_time"] = searchTime / searchCount
			}
		}
	case "elasticsearch_jvm":
		jvmGcTime := 0.0
		jvmGCCount := 0.0
		for name, value := range fields {
			switch name {
			case "mem_heap_used_in_bytes":
				newFields["jvm_heap_used"] = value
			case "mem_non_heap_used_in_bytes":
				newFields["jvm_non_heap_used"] = value
			case "gc_collectors_old_collection_count", "gc_collectors_young_collection_count":
				jvmGCCount += value
			case "gc_collectors_old_collection_time_in_millis", "gc_collectors_young_collection_time_in_millis":
				jvmGcTime += value
			}
		}
		newFields["jvm_gc_time"] = jvmGcTime
		newFields["jvm_gc_utilization"] = jvmGcTime / 10.
		newFields["jvm_gc"] = jvmGCCount
	}
	return newFields
}
