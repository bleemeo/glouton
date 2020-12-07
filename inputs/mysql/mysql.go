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

package mysql

import (
	"errors"
	"glouton/inputs/internal"
	"strings"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/mysql"
)

// New initialise mysql.Input.
func New(server string) (i telegraf.Input, err error) {
	var input, ok = telegraf_inputs.Inputs["mysql"]
	if ok {
		mysqlInput, ok := input().(*mysql.Mysql)
		if ok {
			slice := append(make([]string, 0), server)
			mysqlInput.Servers = slice
			mysqlInput.GatherInnoDBMetrics = true
			mysqlInput.Log = internal.Logger{}
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
			commands := metricName[len("commands_"):]
			switch commands {
			case "admin_commands", "alter_db", "alter_db_upgrade", "alter_event",
				"alter_function", "alter_instance", "alter_procedure", "alter_server",
				"alter_table", "alter_tablespace", "alter_user", "analyze", "assign_to_keycache",
				"begin", "binlog", "call_procedure", "change_db", "change_master", "change_repl_filter",
				"check", "checksum", "commit", "create_db", "create_event", "create_function",
				"create_index", "create_procedure", "create_server", "create_table", "create_trigger",
				"create_udf", "create_user", "create_view", "dealloc_sql", "delete", "delete_multi",
				"do", "drop_db", "drop_event", "drop_function", "drop_index", "drop_procedure",
				"drop_server", "drop_table", "drop_trigger", "drop_user", "drop_view", "empty_query",
				"execute_sql", "explain_other", "flush", "get_diagnostics", "grant", "group_replication_start",
				"group_replication_stop", "ha_close", "ha_open", "ha_read", "help", "insert", "insert_select",
				"install_plugin", "kill", "load", "lock_tables", "optimize", "preload_keys", "prepare_sql", "purge",
				"purge_before_date", "release_savepoint", "rename_table", "rename_user", "repair", "replace",
				"replace_select", "reset", "resignal", "revoke", "revoke_all", "rollback", "rollback_to_savepoint",
				"savepoint", "select", "set_option", "show_binlog_events", "show_binlogs", "show_charsets", "show_collations",
				"show_create_db", "show_create_event", "show_create_func", "show_create_proc", "show_create_table",
				"show_create_trigger", "show_create_user", "show_databases", "show_engine_logs", "show_engine_mutex",
				"show_engine_status", "show_errors", "show_events", "show_fields", "show_function_code", "show_function_status",
				"show_grants", "show_keys", "show_master_status", "show_open_tables", "show_plugins", "show_privileges",
				"show_procedure_code", "show_procedure_status", "show_processlist", "show_profile", "show_profiles",
				"show_relaylog_events", "show_slave_hosts", "show_slave_status", "show_status", "show_storage_engines",
				"show_table_status", "show_tables", "show_triggers", "show_variables", "show_warnings", "shutdown", "signal",
				"slave_start", "slave_stop", "stmt_close", "stmt_execute", "stmt_fetch", "stmt_prepare", "stmt_reprepare",
				"stmt_reset", "stmt_send_long_data", "truncate", "uninstall_plugin", "unlock_tables", "update", "update_multi",
				"xa_commit", "xa_end", "xa_prepare", "xa_recover", "xa_rollback", "xa_start":
				newFields[metricName] = value
				continue
			default:
				// ignore all other
				continue
			}
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
			newFields["innodb_locked_transaction"] = value
		case "trx_rseg_history_len":
			newFields["history_list_len"] = value
		}
	}

	return newFields
}
