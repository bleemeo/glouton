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
	"glouton/config"
	"glouton/discovery"
	"glouton/facts/container-runtime/merge"
	"glouton/jmxtrans"
	"glouton/logger"
	"glouton/prometheus/matcher"
	"glouton/types"
	"strings"
	"sync"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/pkg/labels"
)

//nolint:gochecknoglobals
var commonDefaultSystemMetrics []string = []string{
	"agent_status",
	"system_pending_updates",
	"system_pending_security_updates",
	"time_drift",
}

//nolint:gochecknoglobals
var promDefaultSystemMetrics []string = []string{
	"glouton_gatherer_execution_seconds_count",
	"glouton_gatherer_execution_seconds_sum",
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
	"node_disk_write_bytes_total",
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
var defaultServiceMetrics []string = []string{
	// Services
	"apache_*",
	"bitbucket_*",
	"cassandra_*",
	"confluence_*",
	"bind_status",
	"dovecot_status",
	"ejabberd_status",
	"elasticsearch_*",
	"exim_status",
	"exim_queue_size",
	"haproxy_*",
	"influxdb_status",
	"jira_*",
	"memcached_*",
	"mongodb_*",
	"mosquitto_status", //nolint: misspell
	"mysql_*",
	"nginx_*",
	"ntp_status",
	"openldap_status",
	"openvpn_status",
	"phpfpm_*",
	"postfix_status",
	"postfix_queue_size",
	"postgresql_*",
	"rabbitmq_*",
	"redis_*",
	"salt_status",
	"squid3_status",
	"uwsgi_status",
	"varnish_status",
	"zookeeper_*",
	// Key Processes
	"process_context_switch",
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
}

//metricFilter is a thread-safe holder of an allow / deny metrics list.
type metricFilter struct {

	// staticList contains the matchers generated from static source (config file).
	// They won't change at runtime, and don't need to be rebuilt
	staticAllowList []matcher.Matchers
	staticDenyList  []matcher.Matchers

	// these list are the actual list used while filtering.
	allowList []matcher.Matchers
	denyList  []matcher.Matchers

	l sync.Mutex
}

func buildMatcherList(config *config.Configuration, listType string) []matcher.Matchers {
	metricListType := listType + "_metrics"
	metricList := []matcher.Matchers{}
	globalList := config.StringList("metric." + metricListType)

	for _, str := range globalList {
		new, err := matcher.NormalizeMetric(str)
		if err != nil {
			logger.V(2).Printf("An error occurred while normalizing metric: %w", err)
			continue
		}

		metricList = append(metricList, new)
	}

	metricList = addScrappersList(config, metricList, listType)

	return metricList
}

func addScrappersList(config *config.Configuration, metricList []matcher.Matchers,
	metricListType string) []matcher.Matchers {
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

			metricList = append(metricList, new)
		}
	}

	return metricList
}

func getDefaultMetrics(format types.MetricFormat) []string {
	res := defaultServiceMetrics

	res = append(res, commonDefaultSystemMetrics...)
	if format == types.MetricFormatBleemeo {
		res = append(res, bleemeoDefaultSystemMetrics...)
	} else {
		res = append(res, promDefaultSystemMetrics...)
	}

	return res
}

func (m *metricFilter) buildList(config *config.Configuration, format types.MetricFormat) error {
	m.l.Lock()
	defer m.l.Unlock()

	m.staticAllowList = buildMatcherList(config, "allow")

	if len(m.staticAllowList) > 0 {
		logger.V(1).Println("Your allow list may not be compatible with your plan. Please your plan allowed metrics if you encounter any problem.")
	}

	m.staticDenyList = buildMatcherList(config, "deny")

	includeDefault := config.Bool("metric.include_default_metrics")
	if includeDefault {
		defaultMetricsList := getDefaultMetrics(format)
		for _, val := range defaultMetricsList {
			new, err := matcher.NormalizeMetric(val)
			if err != nil {
				return err
			}

			m.staticAllowList = append(m.staticAllowList, new)
		}
	}

	m.allowList = m.staticAllowList
	m.denyList = m.staticDenyList

	return nil
}

func newMetricFilter(config *config.Configuration, metricFormat types.MetricFormat) (*metricFilter, error) {
	new := metricFilter{}
	err := new.buildList(config, metricFormat)

	return &new, err
}

func (m *metricFilter) FilterPoints(points []types.MetricPoint) []types.MetricPoint {
	i := 0

	logger.V(2).Printf("Starting Point filtering. Number of points to filter: %d", len(points))

	m.l.Lock()
	defer m.l.Unlock()

	if len(m.denyList) != 0 {
		for _, point := range points {
			didMatch := false

			for _, denyVal := range m.denyList {
				matched := denyVal.MatchesPoint(point)
				if matched {
					didMatch = true

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
		for _, allowVal := range m.allowList {
			matched := allowVal.MatchesPoint(point)
			if matched {
				points[i] = point
				i++

				break
			}
		}
	}

	points = points[:i]

	logger.V(2).Printf("Finished Point filtering. Number of points after filter: %d", len(points))

	return points
}

func (m *metricFilter) filterFamily(f *dto.MetricFamily) {
	i := 0

	if len(m.denyList) > 0 {
		for _, metric := range f.Metric {
			didMatch := false

			for _, denyVal := range m.denyList {
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

	for _, metric := range f.Metric {
		for _, allowVal := range m.allowList {
			if allowVal.MatchesMetric(*f.Name, metric) {
				f.Metric[i] = metric
				i++

				break
			}
		}
	}
}

func (m *metricFilter) FilterFamilies(f []*dto.MetricFamily) []*dto.MetricFamily {
	i := 0

	logger.V(2).Printf("Starting Families filtering. Number of families to filter: %d", len(f))

	m.l.Lock()
	defer m.l.Unlock()

	for _, family := range f {
		m.filterFamily(family)

		if len(family.Metric) != 0 {
			f[i] = family
			i++
		}
	}

	f = f[:i]

	logger.V(2).Printf("Families filtering finished. Number of families after filter: %d", len(f))

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
				metricName := service.Name + "." + val.Name

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

	return allowList, errors
}

func (m *metricFilter) RebuildDynamicLists(scrapper dynamicScrapper, services []discovery.Service, thresholdMetricNames []string) error {
	allowList := make(map[string]matcher.Matchers)
	denyList := make(map[string]matcher.Matchers)
	errors := merge.MultiError{}

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

	m.l.Lock()
	defer m.l.Unlock()

	m.allowList = m.staticAllowList

	for _, val := range allowList {
		m.allowList = append(m.allowList, val)
	}

	m.denyList = m.staticDenyList

	for _, val := range denyList {
		m.denyList = append(m.denyList, val)
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
