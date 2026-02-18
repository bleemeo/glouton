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

package agent

import (
	"context"
	"fmt"
	"io"
	"maps"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/jmxtrans"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/matcher"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/types"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/model/labels"
)

//nolint:gochecknoglobals
var (
	commonDefaultSystemMetrics = []string{
		"agent_status",
		types.MetricServiceStatus,
		"system_pending_updates",
		"system_pending_security_updates",
		"time_drift",
		"agent_config_warning",

		// Services metrics that are not classified as a service in common.serviceType

		// Kubernetes
		"kubernetes_ca_day_left",
		"kubernetes_certificate_day_left",
		"kubernetes_certificate_left_perc",
		"kubernetes_kubelet_status",
		"kubernetes_api_status",
		"kubernetes_pods_count",
		"kubernetes_nodes_count",
		"kubernetes_namespaces_count",
		"kubernetes_cpu_requests",
		"kubernetes_cpu_limits",
		"kubernetes_memory_requests",
		"kubernetes_memory_limits",
		"kubernetes_pods_restart_count",

		// Key Processes
		"process_context_switch",
		"process_cpu_system",
		"process_cpu_user",
		"process_io_read_bytes",
		"process_io_write_bytes",
		"process_major_fault",
		"process_mem_bytes",
		"process_num_procs",
		"process_num_threads",
		"process_open_filedesc",
		"process_worst_fd_ratio",

		// Docker
		"containers_count",
		"container_cpu_used",
		"container_health_status",
		"container_io_read_bytes",
		"container_io_write_bytes",
		"container_mem_used",
		"container_mem_used_perc",
		"container_mem_used_perc_status",
		"container_net_bits_recv",
		"container_net_bits_sent",

		// Prometheus scrapper
		"process_cpu_seconds_total{scrape_job!=\"\"}",
		"process_resident_memory_bytes{scrape_job!=\"\"}",

		// Probes
		"probe_duration_seconds",
		"probe_dns_lookup_time_seconds",
		"probe_http_status_code",
		"probe_success",
		"probe_ssl_earliest_cert_expiry",
		"probe_ssl_last_chain_expiry_timestamp_seconds",
		"probe_ssl_leaf_certificate_lifespan",
		"probe_ssl_validation_success",
		"probe_http_duration_seconds",
		"probe_failed_due_to_tls_error",
	}

	promLinuxDefaultSystemMetrics = []string{
		"glouton_gatherer_execution_seconds_count",
		"glouton_gatherer_execution_seconds_sum",
		"node_load1",
		"node_cpu_seconds_global",
		"node_memory_MemTotal_bytes",
		"node_memory_MemAvailable_bytes",
		"node_memory_MemFree_bytes",
		"node_memory_Buffers_bytes",
		"node_memory_Cached_bytes",
		"node_disk_io_time_seconds_total",
		"node_disk_read_bytes_total",
		"node_disk_written_bytes_total",
		"node_disk_reads_completed_total",
		"node_disk_writes_completed_total",
		"node_filesystem_avail_bytes",
		"node_filesystem_size_bytes",
		"node_network_receive_bytes_total",
		"node_network_transmit_bytes_total",
		"node_network_receive_packets_total",
		"node_network_transmit_packets_total",
		"node_network_receive_errs_total",
		"node_network_transmit_errs_total",
	}

	promLinuxSwapMetrics = []string{
		"node_memory_SwapFree_bytes",
		"node_memory_SwapTotal_bytes",
	}

	promWindowsDefaultSystemMetrics = []string{
		"glouton_gatherer_execution_seconds_count",
		"glouton_gatherer_execution_seconds_sum",
		"windows_cpu_time_global",
		"windows_cs_physical_memory_bytes",
		"windows_memory_available_bytes",
		"windows_memory_standby_cache_bytes",
		"windows_os_paging_free_bytes",
		"windows_os_paging_limit_bytes",
		"windows_logical_disk_idle_seconds_total",
		"windows_logical_disk_read_bytes_total",
		"windows_logical_disk_write_bytes_total",
		"windows_logical_disk_reads_total",
		"windows_logical_disk_writes_total",
		"windows_logical_disk_free_bytes",
		"windows_logical_disk_size_bytes",
		"windows_net_bytes_received_total",
		"windows_net_bytes_sent_total",
		"windows_net_packets_received_total",
		"windows_net_packets_sent_total",
		"windows_net_packets_received_errors",
		"windows_net_packets_outbound_errors",
		"windows_cs_hostname",
	}

	bleemeoDefaultSystemMetrics = []string{
		// Operating system metrics
		"agent_gather_time",
		"cpu_idle",
		"cpu_interrupt",
		"cpu_nice",
		"cpu_other",
		"cpu_softirq",
		"cpu_steal",
		"cpu_system",
		"cpu_used",
		"cpu_used_status",
		"cpu_user",
		"cpu_wait",
		"cpu_guest_nice",
		"cpu_guest",
		"disk_free",
		"disk_inodes_free",
		"disk_inodes_total",
		"disk_inodes_used",
		"disk_total",
		"disk_used",
		"disk_used_perc",
		"disk_used_perc_status",
		"io_read_bytes",
		"io_reads",
		"io_read_merged",
		"io_read_utilization",
		"io_utilization",
		"io_write_bytes",
		"io_write_merged",
		"io_writes",
		"io_write_utilization",
		"mem_available",
		"mem_available_perc",
		"mem_buffered",
		"mem_cached",
		"mem_free",
		"mem_total",
		"mem_used",
		"mem_used_perc",
		"mem_used_perc_status",
		"mem_inactive",
		"net_bits_recv",
		"net_bits_sent",
		"net_drop_in",
		"net_drop_out",
		"net_err_in",
		"net_err_in_status",
		"net_err_out",
		"net_err_out_status",
		"net_packets_recv",
		"net_packets_sent",
		"process_status_blocked",
		"process_status_paging",
		"process_status_running",
		"process_status_sleeping",
		"process_status_stopped",
		"process_status_zombies",
		"process_total",
		"process_total_threads",
		"system_load1",
		"system_load5",
		"system_load15",
		"system_power_consumption",
		"uptime",
		"users_logged",
		"zfs_pool_health_status",
	}

	bleemeoSwapMetrics = []string{
		"swap_free",
		"swap_in",
		"swap_out",
		"swap_total",
		"swap_used",
		"swap_used_perc",
		"swap_used_perc_status",
	}

	snmpMetrics = []string{
		"snmp_scrape_duration_seconds",
		"snmp_device_status",
		"sysUpTime",
		"ifOperStatus",
		"total_interfaces",
		"connected_interfaces",
		"prtMarkerSuppliesLevel",
		"temperature",
	}

	// Input metrics that are not associated to a service.
	inputMetrics = []string{
		// SMART
		"smart_device_health_status",
		"smart_device_temp_c",
		"smart_device_media_wearout_indicator",
		"smart_device_percent_lifetime_remain",
		"smart_device_read_error_rate",
		"smart_device_seek_error_rate",
		"smart_device_udma_crc_errors",
		"smart_device_wear_leveling_count",

		// Mdstat
		"mdstat_health_status",
		"mdstat_disks_active_count",
		"mdstat_disks_down_count",
		"mdstat_disks_failed_count",
		"mdstat_disks_spare_count",
		"mdstat_disks_total_count",

		// Nvidia SMI
		"nvidia_smi_fan_speed",
		"nvidia_smi_fbc_stats_session_count",
		"nvidia_smi_fbc_stats_average_fps",
		"nvidia_smi_fbc_stats_average_latency",
		"nvidia_smi_memory_free",
		"nvidia_smi_memory_used",
		"nvidia_smi_memory_total",
		"nvidia_smi_power_draw",
		"nvidia_smi_temperature_gpu",
		"nvidia_smi_utilization_gpu",
		"nvidia_smi_utilization_memory",
		"nvidia_smi_utilization_encoder",
		"nvidia_smi_utilization_decoder",
		"nvidia_smi_pcie_link_gen_current",
		"nvidia_smi_pcie_link_width_current",
		"nvidia_smi_encoder_stats_session_count",
		"nvidia_smi_encoder_stats_average_fps",
		"nvidia_smi_encoder_stats_average_latency",
		"nvidia_smi_clocks_current_graphics",
		"nvidia_smi_clocks_current_sm",
		"nvidia_smi_clocks_current_memory",
		"nvidia_smi_clocks_current_video",

		// PSI
		"psi_cpu_some_perc",
		"psi_io_full_perc",
		"psi_io_some_perc",
		"psi_memory_full_perc",
		"psi_memory_some_perc",

		// Temperature
		`{__name__="sensor_temperature", sensor=~"coretemp_package_id_.*"}`,
		`{__name__="sensor_temperature", sensor="k10temp_tctl"}`,
		`{__name__="sensor_temperature", sensor="ACPI\\ThermalZone\\TZ00_0"}`,

		// vSphere
		"vsphere_vm_cpu_latency_perc",

		"vms_running_count",
		"vms_stopped_count",
		"hosts_running_count",
		"hosts_stopped_count",
	}

	defaultServiceMetrics = map[discovery.ServiceName][]string{
		discovery.ApacheService: {
			"apache_busy_workers",
			"apache_busy_workers_perc",
			"apache_bytes",
			"apache_connections",
			"apache_idle_workers",
			"apache_max_workers",
			"apache_requests",
			"apache_uptime",
			"apache_scoreboard_waiting",
			"apache_scoreboard_starting",
			"apache_scoreboard_reading",
			"apache_scoreboard_sending",
			"apache_scoreboard_keepalive",
			"apache_scoreboard_dnslookup",
			"apache_scoreboard_closing",
			"apache_scoreboard_logging",
			"apache_scoreboard_finishing",
			"apache_scoreboard_idle_cleanup",
			"apache_scoreboard_open",
		},

		discovery.BitBucketService: {
			"bitbucket_events",
			"bitbucket_io_tasks",
			"bitbucket_jvm_gc",
			"bitbucket_jvm_gc_utilization",
			"bitbucket_jvm_heap_used",
			"bitbucket_jvm_non_heap_used",
			"bitbucket_pulls",
			"bitbucket_pushes",
			"bitbucket_queued_events",
			"bitbucket_queued_scm_clients",
			"bitbucket_queued_scm_commands",
			"bitbucket_request_time",
			"bitbucket_requests",
			"bitbucket_ssh_connections",
			"bitbucket_tasks",
		},

		discovery.CassandraService: {
			"cassandra_bloom_filter_false_ratio",
			"cassandra_bloom_filter_false_ratio_sum",
			"cassandra_jvm_gc",
			"cassandra_jvm_gc_utilization",
			"cassandra_jvm_heap_used",
			"cassandra_jvm_non_heap_used",
			"cassandra_read_requests",
			"cassandra_read_requests_sum",
			"cassandra_read_time",
			"cassandra_read_time_average",
			"cassandra_sstable",
			"cassandra_sstable_sum",
			"cassandra_write_requests",
			"cassandra_write_requests_sum",
			"cassandra_write_time",
			"cassandra_write_time_average",
		},

		discovery.ConfluenceService: {
			"confluence_db_query_time",
			"confluence_jvm_gc",
			"confluence_jvm_gc_utilization",
			"confluence_jvm_heap_used",
			"confluence_jvm_non_heap_used",
			"confluence_last_index_time",
			"confluence_queued_error_mails",
			"confluence_queued_index_tasks",
			"confluence_queued_mails",
			"confluence_request_time",
			"confluence_requests",
		},

		discovery.ElasticSearchService: {
			"elasticsearch_docs_count",
			"elasticsearch_jvm_gc",
			"elasticsearch_jvm_gc_utilization",
			"elasticsearch_jvm_heap_used",
			"elasticsearch_jvm_non_heap_used",
			"elasticsearch_size",
			"elasticsearch_search",
			"elasticsearch_search_time",
			"elasticsearch_cluster_docs_count",
			"elasticsearch_cluster_size",
		},

		discovery.EximService: {
			"exim_queue_size",
		},

		discovery.Fail2banService: {
			"fail2ban_failed",
			"fail2ban_banned",
		},

		discovery.HAProxyService: {
			"haproxy_act",
			"haproxy_bin",
			"haproxy_bout",
			"haproxy_ctime",
			"haproxy_dreq",
			"haproxy_dresp",
			"haproxy_econ",
			"haproxy_ereq",
			"haproxy_eresp",
			"haproxy_qcur",
			"haproxy_qtime",
			"haproxy_req_tot",
			"haproxy_rtime",
			"haproxy_scur",
			"haproxy_stot",
			"haproxy_ttime",
		},
		discovery.JenkinsService: {
			"jenkins_busy_executors",
			"jenkins_total_executors",
			"jenkins_job_duration_seconds",
			"jenkins_job_number",
			"jenkins_job_result_code",
		},

		discovery.JIRAService: {
			"jira_jvm_gc",
			"jira_jvm_gc_utilization",
			"jira_jvm_heap_used",
			"jira_jvm_non_heap_used",
			"jira_request_time",
			"jira_requests",
		},

		discovery.KafkaService: {
			"kafka_jvm_gc",
			"kafka_jvm_gc_utilization",
			"kafka_jvm_heap_used",
			"kafka_jvm_non_heap_used",
		},

		discovery.MemcachedService: {
			"memcached_command_get",
			"memcached_command_set",
			"memcached_connections_current",
			"memcached_items_current",
			"memcached_octets_rx",
			"memcached_octets_tx",
			"memcached_ops_cas_hits",
			"memcached_ops_cas_misses",
			"memcached_ops_decr_hits",
			"memcached_ops_decr_misses",
			"memcached_ops_delete_hits",
			"memcached_ops_delete_misses",
			"memcached_ops_evictions",
			"memcached_ops_get_hits",
			"memcached_ops_get_misses",
			"memcached_ops_incr_hits",
			"memcached_ops_incr_misses",
			"memcached_ops_touch_hits",
			"memcached_ops_touch_misses",
			"memcached_percent_hitratio",
			"memcached_ps_count_threads",
			"memcached_uptime",
		},

		discovery.MongoDBService: {
			"mongodb_open_connections",
			"mongodb_net_in_bytes",
			"mongodb_net_out_bytes",
			"mongodb_queued_reads",
			"mongodb_queued_writes",
			"mongodb_active_reads",
			"mongodb_active_writes",
			"mongodb_queries",
		},

		discovery.MySQLService: {
			"mysql_cache_result_qcache_hits",
			"mysql_cache_result_qcache_inserts",
			"mysql_cache_result_qcache_not_cached",
			"mysql_cache_result_qcache_prunes",
			"mysql_cache_blocksize_qcache",
			"mysql_cache_free_blocks",
			"mysql_cache_free_memory",
			"mysql_cache_size_qcache",
			"mysql_locks_immediate",
			"mysql_locks_waited",
			"mysql_innodb_history_list_len",
			"mysql_innodb_locked_transaction",
			"mysql_octets_rx",
			"mysql_octets_tx",
			"mysql_queries",
			"mysql_slow_queries",
			"mysql_threads_cached",
			"mysql_threads_connected",
			"mysql_threads_running",
			"mysql_total_threads_created",
			"mysql_commands_begin",
			"mysql_commands_binlog",
			"mysql_commands_call_procedure",
			"mysql_commands_change_master",
			"mysql_commands_change_repl_filter",
			"mysql_commands_check",
			"mysql_commands_checksum",
			"mysql_commands_commit",
			"mysql_commands_dealloc_sql",
			"mysql_commands_stmt_close",
			"mysql_commands_delete_multi",
			"mysql_commands_delete",
			"mysql_commands_do",
			"mysql_commands_execute_sql",
			"mysql_commands_stmt_execute",
			"mysql_commands_explain_other",
			"mysql_commands_flush",
			"mysql_commands_ha_close",
			"mysql_commands_ha_open",
			"mysql_commands_ha_read",
			"mysql_commands_insert_select",
			"mysql_commands_insert",
			"mysql_commands_kill",
			"mysql_commands_preload_keys",
			"mysql_commands_load",
			"mysql_commands_lock_tables",
			"mysql_commands_optimize",
			"mysql_commands_prepare_sql",
			"mysql_commands_stmt_prepare",
			"mysql_commands_purge_before_date",
			"mysql_commands_purge",
			"mysql_commands_release_savepoint",
			"mysql_commands_repair",
			"mysql_commands_replace_select",
			"mysql_commands_replace",
			"mysql_commands_reset",
			"mysql_commands_resignal",
			"mysql_commands_rollback_to_savepoint",
			"mysql_commands_rollback",
			"mysql_commands_savepoint",
			"mysql_commands_select",
			"mysql_commands_signal",
			"mysql_commands_slave_start",
			"mysql_commands_group_replication_start",
			"mysql_commands_stmt_fetch",
			"mysql_commands_stmt_reprepare",
			"mysql_commands_stmt_reset",
			"mysql_commands_stmt_send_long_data",
			"mysql_commands_slave_stop",
			"mysql_commands_group_replication_stop",
			"mysql_commands_truncate",
			"mysql_commands_unlock_tables",
			"mysql_commands_update_multi",
			"mysql_commands_update",
			"mysql_commands_xa_commit",
			"mysql_commands_xa_end",
			"mysql_commands_xa_prepare",
			"mysql_commands_xa_recover",
			"mysql_commands_xa_rollback",
			"mysql_commands_xa_start",
			"mysql_commands_assign_to_keycache",
			"mysql_handler_commit",
			"mysql_handler_delete",
			"mysql_handler_write",
			"mysql_handler_update",
			"mysql_handler_rollback",
		},

		discovery.NatsService: {
			"nats_uptime_seconds",
			"nats_routes",
			"nats_slow_consumers",
			"nats_subscriptions",
			"nats_in_bytes",
			"nats_out_bytes",
			"nats_in_msgs",
			"nats_out_msgs",
			"nats_connections",
			"nats_total_connections",
		},

		discovery.NfsService: {
			"nfs_ops",
			"nfs_transmitted_bits",
			"nfs_rtt_per_op_seconds",
			"nfs_retrans",
		},

		discovery.NginxService: {
			"nginx_requests",
			"nginx_connections_accepted",
			"nginx_connections_handled",
			"nginx_connections_active",
			"nginx_connections_waiting",
			"nginx_connections_reading",
			"nginx_connections_writing",
		},

		discovery.OpenLDAPService: {
			"openldap_connections_current",
			"openldap_waiters_read",
			"openldap_waiters_write",
			"openldap_threads_active",
			"openldap_statistics_bytes",
			"openldap_statistics_entries",
			"openldap_operations_add_completed",
			"openldap_operations_bind_completed",
			"openldap_operations_delete_completed",
			"openldap_operations_modify_completed",
			"openldap_operations_search_completed",
		},

		discovery.PHPFPMService: {
			"phpfpm_accepted_conn",
			"phpfpm_active_processes",
			"phpfpm_idle_processes",
			"phpfpm_listen_queue",
			"phpfpm_listen_queue_len",
			"phpfpm_max_active_processes",
			"phpfpm_max_children_reached",
			"phpfpm_max_listen_queue",
			"phpfpm_slow_requests",
			"phpfpm_start_since",
			"phpfpm_total_processes",
		},

		discovery.PostfixService: {
			"postfix_queue_size",
		},

		discovery.PostgreSQLService: {
			"postgresql_blk_read_utilization",
			"postgresql_blk_read_utilization_sum",
			"postgresql_blk_write_utilization",
			"postgresql_blk_write_utilization_sum",
			"postgresql_blks_hit",
			"postgresql_blks_hit_sum",
			"postgresql_blks_read",
			"postgresql_blks_read_sum",
			"postgresql_commit",
			"postgresql_commit_sum",
			"postgresql_rollback",
			"postgresql_rollback_sum",
			"postgresql_temp_bytes",
			"postgresql_temp_bytes_sum",
			"postgresql_temp_files",
			"postgresql_temp_files_sum",
			"postgresql_tup_deleted",
			"postgresql_tup_deleted_sum",
			"postgresql_tup_fetched",
			"postgresql_tup_fetched_sum",
			"postgresql_tup_inserted",
			"postgresql_tup_inserted_sum",
			"postgresql_tup_returned",
			"postgresql_tup_returned_sum",
			"postgresql_tup_updated",
			"postgresql_tup_updated_sum",
		},

		discovery.RabbitMQService: {
			"rabbitmq_connections",
			"rabbitmq_consumers",
			"rabbitmq_messages_acked",
			"rabbitmq_messages_count",
			"rabbitmq_messages_delivered",
			"rabbitmq_messages_published",
			"rabbitmq_messages_unacked_count",
			"rabbitmq_queues",
		},

		discovery.RedisService: {
			"redis_current_connections_clients",
			"redis_current_connections_slaves",
			"redis_evicted_keys",
			"redis_expired_keys",
			"redis_keyspace_hits",
			"redis_keyspace_misses",
			"redis_keyspace_hitrate",
			"redis_memory",
			"redis_memory_lua",
			"redis_memory_peak",
			"redis_memory_rss",
			"redis_pubsub_channels",
			"redis_pubsub_patterns",
			"redis_total_connections",
			"redis_total_operations",
			"redis_uptime",
			"redis_volatile_changes",
		},

		discovery.UPSDService: {
			"upsd_battery_status",
			"upsd_status_flags",
			"upsd_battery_voltage",
			"upsd_input_voltage",
			"upsd_output_voltage",
			"upsd_load_percent",
			"upsd_battery_charge_percent",
			"upsd_internal_temp",
			"upsd_input_frequency",
			"upsd_time_left_seconds",
			"upsd_time_on_battery_seconds",
		},

		discovery.UWSGIService: {
			"uwsgi_requests",
			"uwsgi_transmitted",
			"uwsgi_avg_request_time",
			"uwsgi_memory_used",
			"uwsgi_exceptions",
			"uwsgi_harakiri_count",
		},

		discovery.ValkeyService: { // Valkey is a fork of Redis
			"redis_current_connections_clients",
			"redis_current_connections_slaves",
			"redis_evicted_keys",
			"redis_expired_keys",
			"redis_keyspace_hits",
			"redis_keyspace_misses",
			"redis_keyspace_hitrate",
			"redis_memory",
			"redis_memory_lua",
			"redis_memory_peak",
			"redis_memory_rss",
			"redis_pubsub_channels",
			"redis_pubsub_patterns",
			"redis_total_connections",
			"redis_total_operations",
			"redis_uptime",
			"redis_volatile_changes",
		},

		discovery.ZookeeperService: {
			"zookeeper_connections",
			"zookeeper_packets_received",
			"zookeeper_packets_sent",
			"zookeeper_ephemerals_count",
			"zookeeper_watch_count",
			"zookeeper_znode_count",
			"zookeeper_jvm_gc",
			"zookeeper_jvm_gc_utilization",
			"zookeeper_jvm_heap_used",
			"zookeeper_jvm_non_heap_used",
		},
	}
)

const (
	filterLogDuration = 50 * time.Millisecond
)

// metricFilter is a thread-safe holder of an allow / deny metrics list.
type metricFilter struct {
	includeDefaultMetrics bool

	// Static matchers generated from the config file.
	// They won't change at runtime, and don't need to be rebuilt.
	staticAllowList []matcher.Matchers
	staticDenyList  []matcher.Matchers

	l             sync.Mutex
	rulerMatchers []matcher.Matchers
	// Lists used while filtering.
	allowList map[labels.Matcher][]matcher.Matchers
	denyList  map[labels.Matcher][]matcher.Matchers
}

func buildMatchersList(metrics []string) ([]matcher.Matchers, prometheus.MultiError) {
	var errors prometheus.MultiError

	metricList := make([]matcher.Matchers, 0, len(metrics))

	for _, str := range metrics {
		matchers, err := matcher.NormalizeMetric(str)
		if err != nil {
			errors = append(errors, err)

			continue
		}

		metricList = append(metricList, matchers)
	}

	return metricList, errors
}

func matchersToMap(matchersList []matcher.Matchers) map[labels.Matcher][]matcher.Matchers {
	matchersMap := make(map[labels.Matcher][]matcher.Matchers, len(matchersList))

	for _, matchers := range matchersList {
		newName := matchers.Get(types.LabelName)

		switch newName.Type {
		case labels.MatchEqual, labels.MatchNotEqual:
			matchersMap[*newName] = append(matchersMap[*newName], matchers)
		case labels.MatchRegexp, labels.MatchNotRegexp:
			// labels.Matcher has a pointer to a regex so we can't use it as a key
			// to know if the matcher already exist in the map.
			found := false

			for name := range matchersMap {
				if name.Type == newName.Type && name.Value == newName.Value {
					matchersMap[name] = append(matchersMap[name], matchers)
					found = true

					break
				}
			}

			if !found {
				matchersMap[*newName] = []matcher.Matchers{matchers}
			}
		}
	}

	return matchersMap
}

func staticScrapperLists(configTargets []config.PrometheusTarget) ([]matcher.Matchers, []matcher.Matchers) {
	var allowList, denyList []matcher.Matchers

	targets, _ := prometheusConfigToURLs(configTargets)
	for _, target := range targets {
		allowMatchers := matchersForScrapperTarget(target.AllowList, target.ExtraLabels)
		allowList = append(allowList, allowMatchers...)

		denyMatchers := matchersForScrapperTarget(target.DenyList, target.ExtraLabels)
		denyList = append(denyList, denyMatchers...)
	}

	return allowList, denyList
}

func matchersForScrapperTarget(targetFilterList []string, extraLabels map[string]string) []matcher.Matchers {
	matchersList := make([]matcher.Matchers, 0, len(targetFilterList))

	for _, metric := range targetFilterList {
		matchers, err := matcher.NormalizeMetric(metric)
		if err != nil {
			logger.V(2).Printf(
				"Could not normalize metric %s with instance %s and job %s",
				metric, extraLabels[types.LabelMetaScrapeInstance], extraLabels[types.LabelMetaScrapeJob],
			)

			continue
		}

		if matchers.Get(types.LabelScrapeInstance) == nil {
			err := matchers.Add(types.LabelScrapeInstance, extraLabels[types.LabelMetaScrapeInstance], labels.MatchEqual)
			if err != nil {
				logger.V(2).Printf("Could not add label %s to metric %s", types.LabelScrapeInstance, metric)

				continue
			}
		}

		if matchers.Get(types.LabelScrapeJob) == nil {
			err := matchers.Add(types.LabelScrapeJob, extraLabels[types.LabelMetaScrapeJob], labels.MatchEqual)
			if err != nil {
				logger.V(2).Printf("Could not add label %s to metric %s", types.LabelScrapeJob, metric)

				continue
			}
		}

		matchersList = append(matchersList, matchers)
	}

	return matchersList
}

func getDefaultMetrics(forBleemeo bool, hasSwap bool) []string {
	res := commonDefaultSystemMetrics
	res = append(res, inputMetrics...)

	switch {
	case forBleemeo:
		res = append(res, bleemeoDefaultSystemMetrics...)
		if hasSwap {
			res = append(res, bleemeoSwapMetrics...)
		}
	case runtime.GOOS == "windows":
		res = append(res, promWindowsDefaultSystemMetrics...)
	default:
		res = append(res, promLinuxDefaultSystemMetrics...)
		if hasSwap {
			res = append(res, promLinuxSwapMetrics...)
		}
	}

	return res
}

func (m *metricFilter) DiagnosticArchive(_ context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("filter-metrics.txt")
	if err != nil {
		return err
	}

	m.l.Lock()
	defer m.l.Unlock()

	fmt.Fprintf(file, "# Allow list (%d entries)\n", len(m.allowList))
	printSortedMapOfList(file, m.allowList)

	fmt.Fprintf(file, "\n# Deny list (%d entries)\n", len(m.denyList))
	printSortedMapOfList(file, m.denyList)

	fmt.Fprintf(file, "\n# rulerMatchers (%d entries)\n", len(m.rulerMatchers))
	printSortedList(file, m.rulerMatchers)

	return nil
}

func printSortedList(file io.Writer, matchersList []matcher.Matchers) {
	sortedMatchers := make([]string, 0, len(matchersList))

	for _, matchers := range matchersList {
		sortedMatchers = append(sortedMatchers, matchers.String())
	}

	sort.Strings(sortedMatchers)

	for _, matchers := range sortedMatchers {
		fmt.Fprintf(file, "%s\n", matchers)
	}
}

func printSortedMapOfList(file io.Writer, matchersMap map[labels.Matcher][]matcher.Matchers) {
	sortedMatchers := make([]string, 0, len(matchersMap))

	for _, list := range matchersMap {
		for _, matchers := range list {
			sortedMatchers = append(sortedMatchers, matchers.String())
		}
	}

	sort.Strings(sortedMatchers)

	for _, matchers := range sortedMatchers {
		fmt.Fprintf(file, "%s\n", matchers)
	}
}

func newMetricFilter(metricCfg config.Metric, hasSNMP, hasSwap, forBleemeo bool) (*metricFilter, error) {
	rawAllowList := metricCfg.AllowMetrics

	if hasSNMP {
		rawAllowList = append(rawAllowList, snmpMetrics...)
	}

	if metricCfg.IncludeDefaultMetrics {
		rawAllowList = append(rawAllowList, getDefaultMetrics(forBleemeo, hasSwap)...)
	}

	var warnings prometheus.MultiError

	staticAllowList, warn := buildMatchersList(rawAllowList)
	if warn != nil {
		warnings = append(warnings, warn...)
	}

	staticDenyList, warn := buildMatchersList(metricCfg.DenyMetrics)
	if warn != nil {
		warnings = append(warnings, warn...)
	}

	scrapperAllowList, scrapperDenyList := staticScrapperLists(metricCfg.Prometheus.Targets)

	staticAllowList = append(staticAllowList, scrapperAllowList...)
	staticDenyList = append(staticDenyList, scrapperDenyList...)

	filter := &metricFilter{
		staticAllowList:       staticAllowList,
		staticDenyList:        staticDenyList,
		allowList:             matchersToMap(staticAllowList),
		denyList:              matchersToMap(staticDenyList),
		includeDefaultMetrics: metricCfg.IncludeDefaultMetrics,
	}

	return filter, warnings.MaybeUnwrap()
}

// mergeMetricFilters returns a new metricFilter
// that represents the union of both the given filters.
// This only works if the deny-list uses equal comparison (no regexp), or is identical.
func mergeMetricFilters(f1, f2 *metricFilter) *metricFilter {
	result := &metricFilter{}
	result.mergeInPlace(f1, f2)

	return result
}

// mergeInPlace merge the two input filter (f1 & f2) and update the filter m.
// All previous content of m is overwrite, the result only depend on f1 & f2.
// The merge result is the union of both result the given filters (metrics that are allowed by
// one of the filter).
// This only works if the deny-list uses equal comparison (no regexp), or is identical.
func (m *metricFilter) mergeInPlace(f1, f2 *metricFilter) {
	// Merging logic:
	// - to be allowed, a metric only needs to be present in the allowlist of one of the filters
	// - to be denied, a metric needs to be present in the deny-list of both the filters
	m.l.Lock()
	defer m.l.Unlock()

	f1.l.Lock()
	defer f1.l.Unlock()

	f2.l.Lock()
	defer f2.l.Unlock()

	var staticDenyList []matcher.Matchers

	for _, m1 := range f1.staticDenyList {
		for _, m2 := range f2.staticDenyList {
			if matchersEqual(m1, m2) {
				staticDenyList = append(staticDenyList, m1)

				break
			}
		}
	}

	denyList := make(map[labels.Matcher][]matcher.Matchers)

	for k1, v := range f1.denyList {
		if _, found := f2.denyList[k1]; found {
			denyList[k1] = v
		}
	}

	m.includeDefaultMetrics = f1.includeDefaultMetrics || f2.includeDefaultMetrics
	m.staticAllowList = slices.Concat(f1.staticAllowList, f2.staticAllowList)
	m.staticDenyList = staticDenyList
	m.rulerMatchers = slices.Concat(f1.rulerMatchers, f2.rulerMatchers)
	m.allowList = mergeMaps(f1.allowList, f2.allowList)
	m.denyList = denyList
}

func matchersEqual(m1, m2 matcher.Matchers) bool {
	if len(m1) != len(m2) {
		return false
	}

	for i, mt1 := range m1 {
		mt2 := m2[i]
		if mt1.Type != mt2.Type || mt1.Name != mt2.Name || mt1.Value != mt2.Value {
			return false
		}
	}

	return true
}

func mergeMaps[K comparable, V any](m1, m2 map[K]V) map[K]V {
	result := maps.Clone(m1)

	maps.Copy(result, m2)

	return result
}

func getMatchersList(list map[labels.Matcher][]matcher.Matchers, labelName string) []matcher.Matchers {
	// Matchers are used as key, thus a metric name can match multiple labels (eg: cpu* and cpu_process_count).
	// We aggregate all possible matchers based on the name, thus reducing the number of total matchers checked for each point.
	// This is used as a way to reduce complexity and calls in the filtersFamily function:
	// Nested points increased the complexity of the filters.
	matchers := []matcher.Matchers{}

	for key := range list {
		if key.Matches(labelName) {
			matchers = append(matchers, list[key]...)
		}
	}

	return matchers
}

func (m *metricFilter) FilterPoints(points []types.MetricPoint, allowNeededByRules bool) []types.MetricPoint {
	i := 0

	m.l.Lock()
	defer m.l.Unlock()

	start := time.Now()

	for _, point := range points {
		if m.isAllowedAndNotDeniedNoLock(point.Labels) {
			points[i] = point
			i++
		} else if allowNeededByRules && matcher.MatchesAny(point.Labels, m.rulerMatchers) {
			points[i] = point
			i++
		}
	}

	checkMaxDuration(start, points, i)

	points = points[:i]

	return points
}

func checkMaxDuration(start time.Time, points []types.MetricPoint, i int) {
	if duration := time.Since(start); duration > filterLogDuration {
		logger.V(2).Printf("filtering points took %v with %d points in and %d points out", duration, len(points), i)
	}
}

func (m *metricFilter) IsDenied(lbls map[string]string) bool {
	m.l.Lock()
	defer m.l.Unlock()

	return m.isDenied(lbls)
}

func (m *metricFilter) isDenied(lbls map[string]string) bool {
	if len(m.denyList) == 0 {
		return false
	}

	for key, denyVals := range m.denyList {
		if !key.Matches(lbls[types.LabelName]) {
			continue
		}

		for _, denyVal := range denyVals {
			matched := denyVal.Matches(lbls)
			if matched {
				return true
			}
		}
	}

	return false
}

func (m *metricFilter) IsAllowed(lbls map[string]string) bool {
	m.l.Lock()
	defer m.l.Unlock()

	return m.isAllowed(lbls)
}

func (m *metricFilter) isAllowed(lbls map[string]string) bool {
	for key, allowVals := range m.allowList {
		if !key.Matches(lbls[types.LabelName]) {
			continue
		}

		for _, allowVal := range allowVals {
			if allowVal.Matches(lbls) {
				return true
			}
		}
	}

	return false
}

// IsMetricAllowed returns whether this metric is in the allow list and not in the deny list.
func (m *metricFilter) IsMetricAllowed(lbls labels.Labels, allowNeededByRules bool) bool {
	m.l.Lock()
	defer m.l.Unlock()

	allowed := m.isAllowedAndNotDeniedNoLock(lbls.Map())
	if allowed {
		return true
	}

	if !allowNeededByRules {
		return false
	}

	return matcher.MatchesAnyLabels(lbls, m.rulerMatchers)
}

// Returns whether this metric is in the allow list and not in the deny list.
func (m *metricFilter) isAllowedAndNotDeniedMap(lbls map[string]string) bool {
	m.l.Lock()
	defer m.l.Unlock()

	return m.isAllowedAndNotDeniedNoLock(lbls)
}

func (m *metricFilter) isAllowedAndNotDeniedNoLock(lbls map[string]string) bool {
	return !m.isDenied(lbls) && m.isAllowed(lbls)
}

func (m *metricFilter) filterMetrics(mt []types.Metric) []types.Metric {
	i := 0

	m.l.Lock()
	defer m.l.Unlock()

	for _, metric := range mt {
		if m.isAllowedAndNotDeniedNoLock(metric.Labels()) {
			mt[i] = metric
			i++
		}
	}

	mt = mt[:i]

	return mt
}

func allowedMetric(lbls map[string]string, denyVals []matcher.Matchers, allowVals []matcher.Matchers) bool {
	if len(denyVals) > 0 {
		for _, denyVal := range denyVals {
			if denyVal.Matches(lbls) {
				return false
			}
		}
	}

	for _, allowVal := range allowVals {
		if allowVal.Matches(lbls) {
			return true
		}
	}

	return false
}

func (m *metricFilter) filterFamily(f *dto.MetricFamily, allowNeededByRules bool) {
	i := 0
	denyVals := getMatchersList(m.denyList, f.GetName())
	allowVals := getMatchersList(m.allowList, f.GetName())

	for _, metric := range f.GetMetric() {
		lbls := model.DTO2Labels(f.GetName(), metric.GetLabel())
		if allowedMetric(lbls, denyVals, allowVals) {
			f.Metric[i] = metric
			i++
		} else if allowNeededByRules && matcher.MatchesAny(lbls, m.rulerMatchers) {
			f.Metric[i] = metric
			i++
		}
	}

	f.Metric = f.GetMetric()[:i]
}

func (m *metricFilter) FilterFamilies(f []*dto.MetricFamily, allowNeededByRules bool) []*dto.MetricFamily {
	i := 0

	m.l.Lock()
	defer m.l.Unlock()

	start := time.Now()
	pointsIn := 0
	pointsOut := 0

	for _, family := range f {
		pointsIn += len(family.GetMetric())

		m.filterFamily(family, allowNeededByRules)

		pointsOut += len(family.GetMetric())

		if len(family.GetMetric()) != 0 {
			f[i] = family
			i++
		}
	}

	if duration := time.Since(start); duration > filterLogDuration {
		logger.V(2).Printf("filtering family took %v with %d points in and %d points out", duration, pointsIn, pointsOut)
	}

	f = f[:i]

	return f
}

type dynamicScrapper interface {
	GetRegisteredLabels() map[string]map[string]string
	GetContainersLabels() map[string]map[string]string
}

func (m *metricFilter) rebuildServicesMetrics(
	services []discovery.Service,
	allowedMetrics map[string]struct{},
) {
	for _, service := range services {
		if !service.Active {
			continue
		}

		for _, jmxMetric := range jmxtrans.GetJMXMetrics(service) {
			allowedMetrics[service.Name+"_"+jmxMetric.Name] = struct{}{}
		}

		if m.includeDefaultMetrics {
			for _, metric := range defaultServiceMetrics[service.ServiceType] {
				allowedMetrics[metric] = struct{}{}
			}
		}
	}
}

func (m *metricFilter) UpdateRulesMatchers(rulerMatchers []matcher.Matchers) {
	m.l.Lock()
	defer m.l.Unlock()

	m.rulerMatchers = rulerMatchers
}

func (m *metricFilter) RebuildDynamicLists(
	scrapper dynamicScrapper,
	services []discovery.Service,
	thresholdMetricNames []string,
	alertMetrics []string,
) error {
	m.l.Lock()
	defer m.l.Unlock()

	// Use a map of metric names to deduplicate them.
	allowedMetricMap := make(map[string]struct{}, len(thresholdMetricNames)+len(alertMetrics))

	// Allow alert metrics.
	for _, metric := range alertMetrics {
		allowedMetricMap[metric] = struct{}{}
	}

	m.rebuildServicesMetrics(services, allowedMetricMap)
	m.rebuildThresholdsMetrics(thresholdMetricNames, allowedMetricMap)

	// Convert allowed metric map to a matchers list.
	allowedMetric := make([]string, 0, len(allowedMetricMap))
	for k := range allowedMetricMap {
		allowedMetric = append(allowedMetric, k)
	}

	var (
		warnings prometheus.MultiError
		denyList []matcher.Matchers
	)

	allowList, moreWarnings := buildMatchersList(allowedMetric)
	warnings = append(warnings, moreWarnings...)

	// Add dynamic scrapper metrics.
	if scrapper != nil {
		registeredLabels := scrapper.GetRegisteredLabels()
		containersLabels := scrapper.GetContainersLabels()

		for key, val := range registeredLabels {
			scrapperAllow, scrapperDeny, err := rebuildDynamicScrapperLists(containersLabels[key], val)
			if err != nil {
				warnings = append(warnings, err)

				continue
			}

			allowList = append(allowList, scrapperAllow...)
			denyList = append(denyList, scrapperDeny...)
		}
	}

	// Add static filter lists.
	allowList = append(allowList, m.staticAllowList...)
	denyList = append(denyList, m.staticDenyList...)

	m.allowList = matchersToMap(allowList)
	m.denyList = matchersToMap(denyList)

	return warnings.MaybeUnwrap()
}

func (m *metricFilter) rebuildThresholdsMetrics(
	thresholdMetricNames []string,
	allowedMetrics map[string]struct{},
) {
	for _, metric := range thresholdMetricNames {
		// Check if the base metric is allowed.
		baseMetric := map[string]string{types.LabelName: metric}
		if m.isAllowedAndNotDeniedNoLock(baseMetric) {
			allowedMetrics[metric+"_status"] = struct{}{}
		} else {
			logger.V(1).Printf("Denied status metric for %s", metric)
		}
	}
}

// Return the allowed and denied metrics for a container using the container
// labels "glouton.allow_metrics" and "glouton.deny_metrics".
func rebuildDynamicScrapperLists(
	containerLabels map[string]string,
	extraLabels map[string]string,
) ([]matcher.Matchers, []matcher.Matchers, error) {
	allowList := []string{}
	denyList := []string{}

	if allow, ok := containerLabels["glouton.allow_metrics"]; ok && allow != "" {
		allowList = strings.Split(allow, ",")
	}

	if deny, ok := containerLabels["glouton.deny_metrics"]; ok && deny != "" {
		denyList = strings.Split(deny, ",")
	}

	scrapeInstance := extraLabels[types.LabelMetaScrapeInstance]
	scrapeJob := extraLabels[types.LabelMetaScrapeJob]
	allowMatchers := make([]matcher.Matchers, 0, len(allowList))
	denyMatchers := make([]matcher.Matchers, 0, len(denyList))

	for _, metric := range allowList {
		matchers, err := addMetaLabels(metric, scrapeInstance, scrapeJob)
		if err != nil {
			return nil, nil, err
		}

		allowMatchers = append(allowMatchers, matchers)
	}

	for _, metric := range denyList {
		matchers, err := addMetaLabels(metric, scrapeInstance, scrapeJob)
		if err != nil {
			return nil, nil, err
		}

		denyMatchers = append(denyMatchers, matchers)
	}

	return allowMatchers, denyMatchers, nil
}

func addMetaLabels(metric string, scrapeInstance string, scrapeJob string) (matcher.Matchers, error) {
	m, err := matcher.NormalizeMetric(metric)
	if err != nil {
		return nil, err
	}

	err = m.Add(types.LabelScrapeInstance, scrapeInstance, labels.MatchEqual)
	if err != nil {
		return nil, err
	}

	err = m.Add(types.LabelScrapeJob, scrapeJob, labels.MatchEqual)
	if err != nil {
		return nil, err
	}

	return m, nil
}
