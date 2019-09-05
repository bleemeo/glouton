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

package mysql

import (
	"agentgo/inputs/internal"
	"errors"
	"strings"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/mysql"
)

// New initialise mysql.Input
func New(server string) (i telegraf.Input, err error) {
	var input, ok = telegraf_inputs.Inputs["mysql"]
	if ok {
		mysqlInput, ok := input().(*mysql.Mysql)
		if ok {
			slice := append(make([]string, 0), server)
			mysqlInput.Servers = slice
			mysqlInput.GatherInnoDBMetrics = true
			i = &internal.Input{
				Input: mysqlInput,
				Accumulator: internal.Accumulator{
					DerivatedMetrics: []string{
						"bytes_received", "bytes_sent", "threads_created", "queries", "slow_queries",
					},
					ShouldDerivateMetrics: shouldDerivateMetrics,
					TransformMetrics:      transformMetrics,
				},
			}
		} else {
			err = errors.New("input MySQL is not the expected type")
		}
	} else {
		err = errors.New("input MySQL is not enabled in Telegraf")
	}
	return
}

func shouldDerivateMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, metricName string) bool {
	if strings.HasPrefix(metricName, "qcache_") {
		switch metricName {
		case "qcache_queries_in_cache", "qcache_total_blocks", "qcache_free_blocks", "qcache_free_memory":
			return false
		default:
			return true
		}
	}
	if strings.HasPrefix(metricName, "table_locks_") {
		return true
	}
	if strings.HasPrefix(metricName, "commands_") {
		return true
	}
	if strings.HasPrefix(metricName, "handler_") {
		return true
	}
	return false
}

func transformMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	newFields := make(map[string]float64)
	for metricName, value := range fields {
		if strings.HasPrefix(metricName, "qcache_") {
			metricName = strings.ReplaceAll(metricName, "qcache_", "cache_result_qcache_")
			switch metricName {
			case "cache_result_qcache_lowmem_prunes":
				newFields["cache_result_qcache_prunes"] = value
			case "cache_result_qcache_queries_in_cache":
				newFields["cache_size_qcache"] = value
			case "cache_result_qcache_total_blocks":
				newFields["cache_blocksize_qcache"] = value
			case "cache_result_qcache_free_blocks":
				newFields["cache_free_blocks"] = value
			case "cache_result_qcache_free_memory":
				newFields["cache_free_memory"] = value
			default:
				newFields[metricName] = value
			}
			continue
		}
		if strings.HasPrefix(metricName, "table_locks_") {
			metricName = strings.ReplaceAll(metricName, "table_locks_", "locks_")
			newFields[metricName] = value
			continue
		}
		if strings.HasPrefix(metricName, "commands_") {
			newFields[metricName] = value
			continue
		}
		if strings.HasPrefix(metricName, "handler_") {
			newFields[metricName] = value
			continue
		}
		if strings.HasPrefix(metricName, "threads_") {
			if metricName == "threads_created" {
				newFields["total_threads_created"] = value
			} else {
				newFields[metricName] = value
			}
			continue
		}
		switch metricName {
		case "bytes_received":
			newFields["octets_rx"] = value
		case "bytes_sent":
			newFields["octets_tx"] = value
		case "queries", "slow_queries":
			newFields[metricName] = value
		case "innodb_row_lock_current_waits":
			newFields["innobdb_locked_transaction"] = value
		case "trx_rseg_history_len":
			newFields["history_list_len"] = value
		}
	}
	return newFields
}
