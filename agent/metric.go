// Copyright 2015-2021 Bleemeo
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
	"archive/zip"
	"fmt"
	"glouton/config"
	"glouton/discovery"
	"glouton/facts/container-runtime/merge"
	"glouton/jmxtrans"
	"glouton/logger"
	"glouton/prometheus/matcher"
	"glouton/types"
	"runtime"
	"strings"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/pkg/labels"
)

//nolint:gochecknoglobals
var commonDefaultSystemMetrics []string = []string{
	"agent_status",
	"system_pending_updates",
	"system_pending_security_updates",
	"time_drift",
	"agent_config_warning",

	// services metrics that are not classied as a service in common.serviceType

	// Kubernetes
	"kubernetes_ca_day_left",
	"kubernetes_certificate_day_left",

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
	"process_cpu_seconds_total",
	"process_resident_memory_bytes",

	// Probes
	"probe_duration_seconds",
	"probe_http_status_code",
	"probe_success",
	"probe_ssl_earliest_cert_expiry",
}

//nolint:gochecknoglobals
var promLinuxDefaultSystemMetrics []string = []string{
	"glouton_gatherer_execution_seconds_count",
	"glouton_gatherer_execution_seconds_sum",
	"node_load1",
	"node_cpu_seconds_global",
	"node_memory_MemTotal_bytes",
	"node_memory_MemAvailable_bytes",
	"node_memory_MemFree_bytes",
	"node_memory_Buffers_bytes",
	"node_memory_Cached_bytes",
	"node_memory_SwapFree_bytes",
	"node_memory_SwapTotal_bytes",
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
	"node_uname_info",
}

//nolint:gochecknoglobals
var promWindowsDefaultSystemMetrics []string = []string{
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

//nolint:gochecknoglobals
var bleemeoDefaultSystemMetrics []string = []string{
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
	"io_read_time",
	"io_time",
	"io_utilization",
	"io_write_bytes",
	"io_write_merged",
	"io_writes",
	"io_write_time",
	"mem_available",
	"mem_available_perc",
	"mem_buffered",
	"mem_cached",
	"mem_free",
	"mem_total",
	"mem_used",
	"mem_used_perc",
	"mem_used,perc_status",
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
	"swap_free",
	"swap_in",
	"swap_out",
	"swap_total",
	"swap_used",
	"swap_used_perc",
	"swap_used_perc_status",
	"system_load1",
	"system_load5",
	"system_load15",
	"uptime",
	"users_logged",
}

//nolint:gochecknoglobals
var defaultServiceMetrics map[discovery.ServiceName][]string = map[discovery.ServiceName][]string{
	discovery.ApacheService: {
		"apache_busy_workers",
		"apache_busy_workers_perc",
		"apache_bytes",
		"apache_connections",
		"apache_idle_workers",
		"apache_max_workers",
		"apache_requests",
		"apache_status",
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
		"bitbucket_jvm_gc_time",
		"bitbucket_jvm_gc_utilization",
		"bitbucket_jvm_heap_used",
		"bitbucket_jvm_non_heap_used",
		"bitbucket_pulls",
		"bitbucket_pushes",
		"bitbucket_queued_events",
		"bitbucket_queued_scm_clients",
		"bitbucket_queued_scm_commands",
		"bitbucket_queued_request_time",
		"bitbucket_queued_requests",
		"bitbucket_queued_ssh_connections",
		"bitbucket_status",
		"bitbucket_tasks",
	},

	discovery.CassandraService: {
		"cassandra_bloom_filter_false_ratio",
		"cassandra_jvm_gc",
		"cassandra_jvm_gc_time",
		"cassandra_jvm_gc_utilization",
		"cassandra_jvm_heap_used",
		"cassandra_jvm_non_heap_used",
		"cassandra_read_requests",
		"cassandra_read_time",
		"cassandra_sstable",
		"cassandra_status",
		"cassandra_write_requests",
		"cassandra_write_time",
		"cassandra_bloom_filter_false_ratio",
		"cassandra_read_requests",
		"cassandra_read_time",
		"cassandra_sstable",
		"cassandra_write_requests",
		"cassandra_write_time",
	},

	discovery.ConfluenceService: {
		"confluence_db_query_time",
		"confluence_jvm_gc",
		"confluence_jvm_gc_time",
		"confluence_jvm_gc_utilization",
		"confluence_jvm_heap_used",
		"confluence_jvm_non_heap_used",
		"confluence_last_index_time",
		"confluence_queued_error_mails",
		"confluence_queued_index_tasks",
		"confluence_queued_mails",
		"confluence_queued_request_time",
		"confluence_queued_requests",
		"confluence_status",
	},

	discovery.BindService: {
		"bind_status",
	},

	discovery.DovecoteService: {
		"dovecot_status",
	},

	discovery.EjabberService: {
		"ejabberd_status",
	},

	discovery.ElasticSearchService: {
		"elasticsearch_status",
		"elasticsearch_docs_count",
		"elasticsearch_jvm_gc",
		"elasticsearch_jvm_gc_time",
		"elasticsearch_jvm_gc_utilization",
		"elasticsearch_jvm_heap_used",
		"elasticsearch_jvm_non_heap_used",
		"elasticsearch_size",
		"elasticsearch_search",
		"elasticsearch_search_time",
	},

	discovery.EximService: {
		"exim_status",
		"exim_queue_size",
	},

	discovery.HAProxyService: {
		"haproxy_status",
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

	discovery.InfluxDBService: {
		"influxdb_status",
	},

	discovery.JIRAService: {
		"jira_jvm_gc",
		"jira_jvm_gc_time",
		"jira_jvm_gc_utilization",
		"jira_jvm_heap_used",
		"jira_jvm_non_heap_used",
		"jira_queued_request_time",
		"jira_queued_requests",
		"jira_status",
	},

	discovery.MemcachedService: {
		"memcached_status",
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
		"mongodb_status",
		"mongodb_open_connections",
		"mongodb_net_in_bytes",
		"mongodb_net_out_bytes",
		"mongodb_queued_reads",
		"mongodb_queued_writes",
		"mongodb_active_reads",
		"mongodb_active_writes",
		"mongodb_queries",
	},

	discovery.MosquittoService: {
		"mosquitto_status", //nolint: misspell
	},

	discovery.MySQLService: {
		"mysql_status",
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

	discovery.NginxService: {
		"nginx_status",
		"nginx_requests",
		"nginx_connections_accepted",
		"nginx_connections_handled",
		"nginx_connections_active",
		"nginx_connections_waiting",
		"nginx_connections_reading",
		"nginx_connections_writing",
	},

	discovery.NTPService: {
		"ntp_status",
	},

	discovery.OpenLDAPService: {
		"openldap_status",
	},

	discovery.OpenVPNService: {
		"openvpn_status",
	},

	discovery.PHPFPMService: {
		"phpfpm_status",
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
		"postfix_status",
		"postfix_queue_size",
	},

	discovery.PostgreSQLService: {
		"postgresql_status",
		"postgresql_blk_read_time",
		"postgresql_blk_write_time",
		"postgresql_blks_hit",
		"postgresql_blks_read",
		"postgresql_commit",
		"postgresql_rollback",
		"postgresql_temp_bytes",
		"postgresql_temp_files",
		"postgresql_tup_deleted",
		"postgresql_tup_fetched",
		"postgresql_tup_inserted",
		"postgresql_tup_returned",
		"postgresql_tup_updated",
	},

	discovery.RabbitMQService: {
		"rabbitmq_status",
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
		"redis_status",
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

	discovery.SaltMasterService: {
		"salt_status",
	},

	discovery.SquidService: {
		"squid3_status",
	},

	discovery.UWSGIService: {
		"uwsgi_status",
	},

	discovery.VarnishService: {
		"varnish_status",
	},

	discovery.ZookeeperService: {
		"zookeeper_status",
		"zookeeper_connections",
		"zookeeper_packets_received",
		"zookeeper_packets_sent",
		"zookeeper_ephemerals_count",
		"zookeeper_watch_count",
		"zookeeper_znode_count",
		"zookeeper_jvm_gc",
		"zookeeper_jvm_gc_time",
		"zookeeper_jvm_gc_utilization",
		"zookeeper_jvm_heap_used",
		"zookeeper_jvm_non_heap_used",
	},
}

const (
	filterLogDuration = 50 * time.Millisecond
)

//metricFilter is a thread-safe holder of an allow / deny metrics list.
type metricFilter struct {

	// staticList contains the matchers generated from static source (config file).
	// They won't change at runtime, and don't need to be rebuilt
	staticAllowList map[labels.Matcher][]matcher.Matchers
	staticDenyList  map[labels.Matcher][]matcher.Matchers

	// these list are the actual list used while filtering.
	allowList map[labels.Matcher][]matcher.Matchers
	denyList  map[labels.Matcher][]matcher.Matchers

	includeDefaultMetrics bool

	l sync.Mutex
}

func buildMatcherList(config *config.Configuration, listType string) map[labels.Matcher][]matcher.Matchers {
	metricListType := listType + "_metrics"
	metricList := make(map[labels.Matcher][]matcher.Matchers)
	globalList := config.StringList("metric." + metricListType)

	for _, str := range globalList {
		new, err := matcher.NormalizeMetric(str)
		if err != nil {
			logger.V(2).Printf("An error occurred while normalizing metric: %w", err)
			continue
		}

		addToList(metricList, new)
	}

	addScrappersList(config, metricList, listType)

	return metricList
}

func addToList(metricList map[labels.Matcher][]matcher.Matchers, new matcher.Matchers) {
	newName := new.Get(types.LabelName)

	if newName.Type == labels.MatchEqual || newName.Type == labels.MatchNotEqual {
		if metricList[*newName] == nil {
			metricList[*newName] = []matcher.Matchers{new}
		} else {
			metricList[*newName] = append(metricList[*newName], new)
		}

		return
	}

	for key := range metricList {
		if key.Type != newName.Type {
			continue
		}

		if key.Value == newName.Value {
			metricList[key] = append(metricList[key], new)

			return
		}
	}

	metricList[*newName] = []matcher.Matchers{new}
}

func addScrappersList(config *config.Configuration, metricList map[labels.Matcher][]matcher.Matchers,
	metricListType string) {
	promTargets, _ := config.Get("metric.prometheus.targets")
	targetList := prometheusConfigToURLs(promTargets)

	for _, t := range targetList {
		var list []string

		if metricListType == "allow" {
			list = t.AllowList
		} else {
			list = t.DenyList
		}

		for _, val := range list {
			new, err := matcher.NormalizeMetric(val)
			if err != nil {
				logger.V(2).Printf("Could not normalize metric %s with instance %s and job %s", val, t.ExtraLabels[types.LabelMetaScrapeInstance], t.ExtraLabels[types.LabelMetaScrapeJob])
				continue
			}

			if new.Get(types.LabelScrapeInstance) == nil {
				err := new.Add(types.LabelScrapeInstance, t.ExtraLabels[types.LabelMetaScrapeInstance], labels.MatchEqual)
				if err != nil {
					logger.V(2).Printf("Could not add label %s to metric %s", types.LabelScrapeInstance, val)
					continue
				}
			}

			if new.Get(types.LabelScrapeJob) == nil {
				err := new.Add(types.LabelScrapeJob, t.ExtraLabels[types.LabelMetaScrapeJob], labels.MatchEqual)
				if err != nil {
					logger.V(2).Printf("Could not add label %s to metric %s", types.LabelScrapeJob, val)
					continue
				}
			}

			addToList(metricList, new)
		}
	}
}

func getDefaultMetrics(format types.MetricFormat) []string {
	res := commonDefaultSystemMetrics

	switch {
	case format == types.MetricFormatBleemeo:
		res = append(res, bleemeoDefaultSystemMetrics...)
	case runtime.GOOS == "windows":
		res = append(res, promWindowsDefaultSystemMetrics...)
	default:
		res = append(res, promLinuxDefaultSystemMetrics...)
	}

	return res
}

func (m *metricFilter) DiagnosticZip(zipFile *zip.Writer) error {
	file, err := zipFile.Create("metrics-filter.txt")
	if err != nil {
		return err
	}

	m.l.Lock()
	defer m.l.Unlock()

	fmt.Fprintf(file, "# Allow list (%d entry)\n", len(m.allowList))

	for _, r := range m.allowList {
		for _, val := range r {
			fmt.Fprintf(file, "%s\n", val.String())
		}
	}

	fmt.Fprintf(file, "\n# Deny list (%d entry)\n", len(m.denyList))

	for _, r := range m.denyList {
		for _, val := range r {
			fmt.Fprintf(file, "%s\n", val.String())
		}
	}

	return nil
}

func (m *metricFilter) buildList(config *config.Configuration, format types.MetricFormat) error {
	m.l.Lock()
	defer m.l.Unlock()

	m.staticAllowList = buildMatcherList(config, "allow")

	if len(m.staticAllowList) > 0 {
		logger.V(1).Println("Your allow list may not be compatible with your plan. Please check your allowed metrics for your plan if you encounter any problem.")
	}

	m.staticDenyList = buildMatcherList(config, "deny")

	_, found := config.Get("metric.include_default_metrics")

	if !found {
		m.includeDefaultMetrics = true
	} else {
		m.includeDefaultMetrics = config.Bool("metric.include_default_metrics")
	}

	if m.includeDefaultMetrics {
		defaultMetricsList := getDefaultMetrics(format)
		for _, val := range defaultMetricsList {
			new, err := matcher.NormalizeMetric(val)
			if err != nil {
				return err
			}

			addToList(m.staticAllowList, new)
		}
	}

	m.allowList = map[labels.Matcher][]matcher.Matchers{}

	for key, val := range m.staticAllowList {
		m.allowList[key] = make([]matcher.Matchers, len(val))

		for i, matchers := range val {
			m.allowList[key][i] = matchers
		}
	}

	m.denyList = map[labels.Matcher][]matcher.Matchers{}

	for key, val := range m.staticDenyList {
		m.denyList[key] = make([]matcher.Matchers, len(val))

		for i, matchers := range val {
			m.denyList[key][i] = matchers
		}
	}

	return nil
}

func newMetricFilter(config *config.Configuration, metricFormat types.MetricFormat) (*metricFilter, error) {
	new := metricFilter{}
	err := new.buildList(config, metricFormat)

	return &new, err
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

func (m *metricFilter) FilterPoints(points []types.MetricPoint) []types.MetricPoint {
	i := 0

	m.l.Lock()
	defer m.l.Unlock()

	start := time.Now()

	if len(m.denyList) != 0 {
		for _, point := range points {
			didMatch := false

			for key, denyVals := range m.denyList {
				if !key.Matches(point.Labels[types.LabelName]) {
					continue
				}

				for _, denyVal := range denyVals {
					matched := denyVal.MatchesPoint(point)
					if matched {
						didMatch = true

						break
					}
				}

				if didMatch {
					break
				}
			}

			if !didMatch {
				points[i] = point
				i++
			}
		}

		points = points[:i]
		i = 0
	}

	for _, point := range points {
		didMatch := false

		for key, allowVals := range m.allowList {
			if !key.Matches(point.Labels[types.LabelName]) {
				continue
			}

			for _, allowVal := range allowVals {
				if allowVal.MatchesPoint(point) {
					points[i] = point
					didMatch = true

					i++

					break
				}
			}

			if didMatch {
				break
			}
		}
	}

	checkMaxDuration(start, points, i)

	points = points[:i]

	return points
}

func checkMaxDuration(start time.Time, points []types.MetricPoint, i int) {
	duration := time.Since(start)

	if duration > filterLogDuration {
		logger.V(2).Printf("filtering points took %v with %d points in and %d points out", duration, len(points), i)
	}
}

func (m *metricFilter) filterMetrics(mt []types.Metric) []types.Metric {
	i := 0

	m.l.Lock()
	defer m.l.Unlock()

	if len(m.denyList) > 0 {
		for _, metric := range mt {
			didMatch := false

			for key, denyVals := range m.denyList {
				if !key.Matches(metric.Labels()[types.LabelName]) {
					continue
				}

				for _, denyVal := range denyVals {
					if denyVal.MatchesLabels(metric.Labels()) {
						didMatch = true
						break
					}
				}
			}

			if !didMatch {
				mt[i] = metric
				i++
			}
		}

		mt = mt[:i]
		i = 0
	}

	for _, metric := range mt {
		didMatch := false

		for key, allowVals := range m.allowList {
			if !key.Matches(metric.Labels()[types.LabelName]) {
				continue
			}

			for _, allowVal := range allowVals {
				if allowVal.MatchesLabels(metric.Labels()) {
					mt[i] = metric
					didMatch = true
					i++

					break
				}
			}

			if didMatch {
				break
			}
		}
	}

	mt = mt[:i]

	return mt
}

func (m *metricFilter) filterFamily(f *dto.MetricFamily) {
	i := 0
	denyVals := getMatchersList(m.denyList, *f.Name)

	if len(denyVals) > 0 {
		for _, metric := range f.Metric {
			didMatch := false

			for _, denyVal := range denyVals {
				if denyVal.MatchesMetric(*f.Name, metric) {
					didMatch = true
					break
				}
			}

			if !didMatch {
				f.Metric[i] = metric
				i++
			}
		}

		f.Metric = f.Metric[:i]
		i = 0
	}

	allowVals := getMatchersList(m.allowList, *f.Name)

	for _, metric := range f.Metric {
		for _, allowVal := range allowVals {
			if allowVal.MatchesMetric(*f.Name, metric) {
				f.Metric[i] = metric
				i++

				break
			}
		}
	}

	f.Metric = f.Metric[:i]
}

func (m *metricFilter) FilterFamilies(f []*dto.MetricFamily) []*dto.MetricFamily {
	i := 0

	m.l.Lock()
	defer m.l.Unlock()

	start := time.Now()
	pointsIn := 0
	pointsOut := 0

	for _, family := range f {
		pointsIn += len(family.Metric)

		m.filterFamily(family)

		pointsOut += len(family.Metric)

		if len(family.Metric) != 0 {
			f[i] = family
			i++
		}
	}

	duration := time.Since(start)
	if duration > filterLogDuration {
		logger.V(2).Printf("filtering family took %v with %d points in and %d points out", duration, pointsIn, pointsOut)
	}

	f = f[:i]

	return f
}

type dynamicScrapper interface {
	GetRegisteredLabels() map[string]map[string]string
	GetContainersLabels() map[string]map[string]string
}

func (m *metricFilter) rebuildServicesMetrics(allowList map[string]matcher.Matchers, services []discovery.Service, errors merge.MultiError) (map[string]matcher.Matchers, merge.MultiError) {
	for _, service := range services {
		if !service.Active {
			continue
		}

		newMetrics := jmxtrans.GetJMXMetrics(service)

		if len(newMetrics) != 0 {
			for _, val := range newMetrics {
				metricName := service.Name + "_" + val.Name

				new, err := matcher.NormalizeMetric(metricName)
				if err != nil {
					errors = append(errors, err)
					continue
				}

				allowList[metricName] = new
			}
		}

		new, err := matcher.NormalizeMetric(service.Name + "_status")
		if err != nil {
			errors = append(errors, err)
			continue
		}

		allowList[service.Name+"_status"] = new
	}

	if m.includeDefaultMetrics {
		err := m.rebuildDefaultMetrics(services, allowList)
		if err != nil {
			errors = append(errors, err)
		}
	}

	return allowList, errors
}

func (m *metricFilter) rebuildDefaultMetrics(services []discovery.Service, list map[string]matcher.Matchers) error {
	for _, val := range services {
		defaults := defaultServiceMetrics[val.ServiceType]

		for _, val := range defaults {
			new, err := matcher.NormalizeMetric(val)
			if err != nil {
				return err
			}

			list[val] = new
		}
	}

	return nil
}

func (m *metricFilter) RebuildDynamicLists(scrapper dynamicScrapper, services []discovery.Service, thresholdMetricNames []string) error {
	allowList := make(map[string]matcher.Matchers)
	denyList := make(map[string]matcher.Matchers)
	errors := merge.MultiError{}

	m.l.Lock()
	defer m.l.Unlock()

	var registeredLabels map[string]map[string]string

	var containersLabels map[string]map[string]string

	if scrapper != nil {
		registeredLabels = scrapper.GetRegisteredLabels()
		containersLabels = scrapper.GetContainersLabels()

		for key, val := range registeredLabels {
			allowMatchers, denyMatchers, err := addNewSource(containersLabels[key], val)
			if err != nil {
				errors = append(errors, err)
				continue
			}

			for key, val := range allowMatchers {
				allowList[key] = val
			}

			for key, val := range denyMatchers {
				denyList[key] = val
			}
		}
	}

	allowList, errors = m.rebuildServicesMetrics(allowList, services, errors)

	for _, val := range thresholdMetricNames {
		new, err := matcher.NormalizeMetric(val + "_status")
		if err != nil {
			errors = append(errors, err)
			continue
		}

		allowList[val+"_status"] = new
	}

	m.allowList = map[labels.Matcher][]matcher.Matchers{}

	for key, val := range m.staticAllowList {
		m.allowList[key] = make([]matcher.Matchers, len(val))

		for i, matchers := range val {
			m.allowList[key][i] = matchers
		}
	}

	for _, val := range allowList {
		addToList(m.allowList, val)
	}

	m.denyList = map[labels.Matcher][]matcher.Matchers{}

	for key, val := range m.staticDenyList {
		m.denyList[key] = make([]matcher.Matchers, len(val))

		for i, matchers := range val {
			m.denyList[key][i] = matchers
		}
	}

	for _, val := range denyList {
		addToList(m.denyList, val)
	}

	if len(errors) == 0 {
		return nil
	}

	return errors
}

func addNewSource(cLabels map[string]string, extraLabels map[string]string) (map[string]matcher.Matchers, map[string]matcher.Matchers, error) {
	allowList := []string{}
	denyList := []string{}

	if allow, ok := cLabels["glouton.allow_metrics"]; ok && allow != "" {
		allowList = strings.Split(allow, ",")
	}

	if deny, ok := cLabels["glouton.deny_metrics"]; ok && deny != "" {
		denyList = strings.Split(deny, ",")
	}

	allowMatchers, denyMatchers, err := newMatcherSource(allowList, denyList,
		extraLabels[types.LabelMetaScrapeInstance], extraLabels[types.LabelMetaScrapeJob])

	if err != nil {
		return nil, nil, err
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

func newMatcherSource(allowList []string, denyList []string, scrapeInstance string, scrapeJob string) (map[string]matcher.Matchers, map[string]matcher.Matchers, error) {
	allowMatchers := make(map[string]matcher.Matchers)
	denyMatchers := make(map[string]matcher.Matchers)

	for _, val := range allowList {
		allowVal, err := addMetaLabels(val, scrapeInstance, scrapeJob)
		if err != nil {
			return nil, nil, err
		}

		allowMatchers[val] = allowVal
	}

	for _, val := range denyList {
		denyVal, err := addMetaLabels(val, scrapeInstance, scrapeJob)
		if err != nil {
			return nil, nil, err
		}

		denyMatchers[val] = denyVal
	}

	return allowMatchers, denyMatchers, nil
}
