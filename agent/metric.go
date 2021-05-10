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
	"errors"
	"fmt"
	"glouton/config"
	"glouton/discovery/promexporter"
	"glouton/facts/container-runtime/merge"
	"glouton/logger"
	"glouton/prometheus/matcher"
	"glouton/types"
	"strings"
	"sync"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/pkg/labels"
)

var errCantCast = errors.New("could not convert field to correct type")

//nolint:gochecknoglobals
var defaultMetrics []string = []string{
	// Operating system metrics
	"agent_gather_time",
	"agent_status",
	"cpu_idle",
	"cpu_interrup",
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
	"io_merge_reads",
	"io_merged_writes",
	"io_read_bytes",
	"io_reads",
	"io_time",
	"io_utilization",
	"io_write_bytes",
	"io_write_time",
	"io_wites",
	"mem_available",
	"mem_available_perc",
	"mem_buffered",
	"mem_cached",
	"mem_free",
	"mem_total",
	"mem_used",
	"mem_used_perc",
	"mem_used,perc_status",
	"net_bites_recv",
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
	"system_pending_updates",
	"system_pending_security_updates",
	"uptime",
	"users_logged",
	// Services
	"apache_*",
	"bitbucket_*",
	"cassandra_*",
	"confluence*",
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
	// Java
	"*_jvm_gc",
	"*_jvm_gc_time",
	"*_jvm_gc_utilization",
	"*_jvm_heap_used",
	"*_jvm_non_heap_used",
}

//MetricFilter is a thread-safe holder of an allow / deny metrics list.
type MetricFilter struct {

	// staticList contains the matchers generated from static source (config file).
	// They won't change at runtime, and don't need to be rebuilt
	staticAllowList []matcher.Matchers
	staticDenyList  []matcher.Matchers

	// these list are the actual list used while filtering.
	allowList []matcher.Matchers
	denyList  []matcher.Matchers
	l         sync.Mutex
	config    *config.Configuration
}

func buildMatcherList(config *config.Configuration, listType string) ([]matcher.Matchers, error) {
	metricListType := listType + "_metrics"
	metricList := []matcher.Matchers{}
	globalList := config.StringList("metric." + metricListType)

	for _, str := range globalList {
		new, err := matcher.NormalizeMetric(str)
		if err != nil {
			return nil, err
		}

		metricList = append(metricList, new)
	}

	metricList = addScrappersList(config, metricList, listType)

	return metricList, nil
}

func addScrappersList(config *config.Configuration, metricList []matcher.Matchers,
	metricListType string) []matcher.Matchers {
	promTargets, _ := config.Get("metric.prometheus.targets")
	targetList := prometheusConfigToURLs(promTargets, []string{}, []string{})

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

func (m *MetricFilter) buildList(config *config.Configuration) error {
	var err error

	m.l.Lock()
	defer m.l.Unlock()

	m.staticAllowList, err = buildMatcherList(config, "allow")
	if err != nil {
		return err
	}

	if len(m.staticAllowList) > 0 {
		logger.V(1).Println("Your allow list may not be compatible with your plan. Please your plan allowed metrics if you encounter any problem.")
	}

	m.staticDenyList, err = buildMatcherList(config, "deny")
	if err != nil {
		return err
	}

	includeDefault := config.Bool("metric.include_default_metrics")
	if includeDefault {
		for _, val := range defaultMetrics {
			new, err := matcher.NormalizeMetric(val)
			if err != nil {
				return err
			}

			m.staticAllowList = append(m.staticAllowList, new)
		}
	}

	jmxMetrics, err := getJmxMetrics(config)
	if err != nil {
		return err
	}

	if len(jmxMetrics) > 0 {
		for _, val := range jmxMetrics {
			new, err := matcher.NormalizeMetric(val)
			if err != nil {
				return err
			}

			m.staticAllowList = append(m.staticAllowList, new)
		}
	}

	m.allowList = m.staticAllowList
	m.denyList = m.staticDenyList

	return err
}

func getJmxMetrics(cfg *config.Configuration) ([]string, error) {
	res := []string{}
	serviceListRaw, found := cfg.Get("service")

	if !found {
		return []string{}, nil
	}

	serviceList, ok := serviceListRaw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("%w: service", errCantCast)
	}

	for _, service := range serviceList {
		srv, ok := service.(map[string]interface{})
		if !ok {
			continue
		}

		if srv["id"] == nil || srv["jmx_metrics"] == nil {
			continue
		}

		nameList, ok := srv["jmx_metrics"].([]interface{})
		if !ok {
			continue
		}

		for _, val := range nameList {
			metric, ok := val.(map[string]interface{})
			if !ok {
				continue
			}

			name := metric["name"].(string)

			res = append(res, srv["id"].(string)+"_"+name)
		}
	}

	return res, nil
}

func NewMetricFilter(config *config.Configuration) (*MetricFilter, error) {
	new := MetricFilter{}
	err := new.buildList(config)

	new.config = config

	return &new, err
}

func (m *MetricFilter) FilterPoints(points []types.MetricPoint) []types.MetricPoint {
	i := 0

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

	return points
}

func (m *MetricFilter) filterFamily(f *dto.MetricFamily) {
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

func (m *MetricFilter) FilterFamilies(f []*dto.MetricFamily) []*dto.MetricFamily {
	i := 0

	for _, family := range f {
		m.filterFamily(family)

		if len(family.Metric) != 0 {
			f[i] = family
		}
	}

	f = f[:i]

	return f
}

func (m *MetricFilter) RebuildDynamicLists(scrapper *promexporter.DynamicScrapper) error {
	allowList := []matcher.Matchers{}
	denyList := []matcher.Matchers{}
	errors := merge.MultiError{}
	registeredLabels := scrapper.GetRegisteredLabels()
	containersLabels := scrapper.GetContainersLabels()

	for key, val := range registeredLabels {
		allowMatchers, denyMatchers, err := addNewSource(containersLabels[key], val)
		if err != nil {
			errors = append(errors, err)
			continue
		}

		allowList = append(allowList, allowMatchers...)
		denyList = append(denyList, denyMatchers...)
	}

	m.l.Lock()
	defer m.l.Unlock()

	m.allowList = m.staticAllowList
	m.allowList = append(m.allowList, allowList...)
	m.denyList = m.staticDenyList
	m.denyList = append(m.denyList, denyList...)

	if len(errors) == 0 {
		return nil
	}

	return errors
}

func addNewSource(cLabels map[string]string, extraLabels map[string]string) ([]matcher.Matchers, []matcher.Matchers, error) {
	allowList := []string{}
	denyList := []string{}

	if allow, ok := cLabels["glouton.allow_metrics"]; ok && allow != "" {
		allowList = strings.Split(allow, ",")
	}

	if deny, ok := cLabels["glouton.deny_metrics"]; ok && deny != "" {
		denyList = strings.Split(deny, ",")
	}

	logger.V(0).Println(allowList)
	logger.V(0).Println(denyList)
	allowMatchers, denyMatchers, err := newMatcherSource(allowList, denyList,
		extraLabels[types.LabelMetaScrapeInstance], extraLabels[types.LabelMetaScrapeJob])

	if err != nil {
		return nil, nil, err
	}

	return allowMatchers, denyMatchers, nil
}

func addMetaLabels(metric string, scrapeInstance string, scrapeJob string) (string, error) {
	m, err := matcher.NormalizeMetric(metric)
	if err != nil {
		return "", err
	}

	err = m.Add(types.LabelScrapeInstance, scrapeInstance, labels.MatchEqual)
	if err != nil {
		return "", err
	}

	err = m.Add(types.LabelScrapeJob, scrapeJob, labels.MatchEqual)
	if err != nil {
		return "", err
	}

	return m.String(), nil
}

func newMatcherSource(allowList []string, denyList []string, scrapeInstance string, scrapeJob string) ([]matcher.Matchers, []matcher.Matchers, error) {
	allowMatchers := []matcher.Matchers{}
	denyMatchers := []matcher.Matchers{}

	for _, val := range allowList {
		allowVal, err := addMetaLabels(val, scrapeInstance, scrapeJob)
		if err != nil {
			return nil, nil, err
		}

		res, err := matcher.NormalizeMetric(allowVal)
		if err != nil {
			return nil, nil, err
		}

		allowMatchers = append(allowMatchers, res)
	}

	for _, val := range denyList {
		denyVal, err := addMetaLabels(val, scrapeInstance, scrapeJob)
		if err != nil {
			return nil, nil, err
		}

		res, err := matcher.NormalizeMetric(denyVal)
		if err != nil {
			return nil, nil, err
		}

		denyMatchers = append(denyMatchers, res)
	}

	return allowMatchers, denyMatchers, nil
}
