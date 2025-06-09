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

package mysql

import (
	"strings"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	telegraf_config "github.com/influxdata/telegraf/config"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/mysql"
)

// New initialise mysql.Input.
func New(server string) (telegraf.Input, error) {
	input, ok := telegraf_inputs.Inputs["mysql"]
	if !ok {
		return nil, inputs.ErrDisabledInput
	}

	mysqlInput, ok := input().(*mysql.Mysql)
	if !ok {
		return nil, inputs.ErrUnexpectedType
	}

	secretServer := telegraf_config.NewSecret([]byte(server))
	mysqlInput.Servers = []*telegraf_config.Secret{&secretServer}
	mysqlInput.GatherInnoDBMetrics = true
	mysqlInput.Log = internal.NewLogger()
	i := &internal.Input{
		Input: mysqlWrapper{mysqlInput},
		Accumulator: internal.Accumulator{
			DifferentiatedMetrics: []string{
				"bytes_received", "bytes_sent", "threads_created", "queries", "slow_queries",
			},
			ShouldDifferentiateMetrics: shouldDerivativeMetrics,
			TransformMetrics:           transformMetrics,
		},
		Name: "mysql",
	}

	return internal.InputWithSecrets{Input: i, Count: 1}, nil
}

// mysqlWrapper wraps the MySQL Telegraf input and implements telegraf.ServiceInput
// to destroy the secrets when the input is stopped.
type mysqlWrapper struct {
	input *mysql.Mysql
}

func (m mysqlWrapper) Gather(acc telegraf.Accumulator) error {
	return m.input.Gather(acc)
}

func (m mysqlWrapper) SampleConfig() string {
	return m.input.SampleConfig()
}

func (m mysqlWrapper) Init() error {
	return m.input.Init()
}

func (m mysqlWrapper) Start(telegraf.Accumulator) (err error) {
	return nil
}

func (m mysqlWrapper) Stop() {
}

func shouldDerivativeMetrics(currentContext internal.GatherContext, metricName string) bool {
	_ = currentContext

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

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]any) map[string]float64 {
	_ = currentContext
	_ = originalFields
	newFields := make(map[string]float64)

	for metricName, value := range fields {
		if strings.HasPrefix(metricName, "qcache_") {
			metricName = strings.ReplaceAll(metricName, "qcache_", "cache_result_qcache_")
			assignCacheMetrics(newFields, value, metricName)

			continue
		}

		if strings.HasPrefix(metricName, "table_locks_") {
			metricName = strings.ReplaceAll(metricName, "table_locks_", "locks_")
			newFields[metricName] = value

			continue
		}

		if strings.HasPrefix(metricName, "commands_") {
			commands := metricName[len("commands_"):]
			switch commands {
			case "assign_to_keycache",
				"begin", "binlog", "call_procedure", "change_master", "change_repl_filter",
				"check", "checksum", "commit", "dealloc_sql", "delete", "delete_multi",
				"do", "execute_sql", "flush", "group_replication_start",
				"group_replication_stop", "ha_close", "ha_open", "ha_read", "insert", "insert_select",
				"kill", "load", "lock_tables", "optimize", "preload_keys", "prepare_sql", "purge",
				"purge_before_date", "release_savepoint", "repair", "replace",
				"replace_select", "reset", "resignal", "rollback", "rollback_to_savepoint",
				"savepoint", "select", "signal",
				"slave_start", "slave_stop", "stmt_close", "stmt_execute", "stmt_fetch", "stmt_prepare", "stmt_reprepare",
				"stmt_reset", "stmt_send_long_data", "truncate", "unlock_tables", "update", "update_multi",
				"xa_commit", "xa_end", "xa_prepare", "xa_recover", "xa_rollback", "xa_start":
				newFields[metricName] = value

				continue
			default:
				// ignore all other
				continue
			}
		}

		if strings.HasPrefix(metricName, "handler_") {
			handler := metricName[len("handler_"):]
			switch handler {
			case "commit", "delete", "rollback", "update", "write":
				newFields[metricName] = value

				continue
			default:
				// ignore all other
				continue
			}
		}

		if strings.HasPrefix(metricName, "threads_") {
			if metricName == "threads_created" {
				newFields["total_threads_created"] = value
			} else {
				newFields[metricName] = value
			}

			continue
		}

		assignIOFields(newFields, value, metricName)
	}

	return newFields
}

func assignCacheMetrics(newFields map[string]float64, value float64, metricName string) {
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
}

func assignIOFields(newFields map[string]float64, value float64, metricName string) {
	switch metricName {
	case "bytes_received":
		newFields["octets_rx"] = value
	case "bytes_sent":
		newFields["octets_tx"] = value
	case "queries", "slow_queries":
		newFields[metricName] = value
	case "innodb_row_lock_current_waits":
		newFields["innodb_locked_transaction"] = value
	case "trx_rseg_history_len":
		newFields["history_list_len"] = value
	}
}
