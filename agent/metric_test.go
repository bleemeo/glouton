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
	"bytes"
	"math/rand"
	"os"
	"reflect"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/prometheus/matcher"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/model/labels"
	promParser "github.com/prometheus/prometheus/promql/parser"
)

//nolint:gochecknoglobals
var (
	basicConf = config.Config{
		Metric: config.Metric{
			IncludeDefaultMetrics: false,
			AllowMetrics:          []string{"cpu*", "pro*"},
			DenyMetrics:           []string{`process_cpu_seconds_total{scrape_job="my_application123"}`, "whatever"},
			Prometheus: config.Prometheus{
				Targets: []config.PrometheusTarget{
					{
						URL:          "http://localhost:2113/metrics",
						Name:         "my_application123",
						AllowMetrics: []string{"process_cpu_seconds_total"},
					},
				},
			},
		},
		Services: []config.Service{
			{
				Type:    "myapplication",
				JMXPort: 1234,
				JMXMetrics: []config.JmxMetric{
					{
						Name:      "heap_size_mb",
						MBean:     "java.lang:type=Memory",
						Attribute: "HeapMemoryUsage",
						Path:      "used",
						Scale:     0.000000954, // 1 / 1024 / 1024
					},
					{
						Name:      "request",
						MBean:     "com.bleemeo.myapplication:type=ClientRequest",
						Attribute: "Count",
						Derive:    true,
					},
				},
			},
			{
				Type:     "apache",
				Address:  "127.0.0.1",
				Port:     80,
				HTTPPath: "/",
				HTTPHost: "127.0.0.1:80",
			},
		},
	}

	defaultConf = config.Config{
		Metric: config.Metric{
			IncludeDefaultMetrics: true,
		},
	}
)

func Test_Basic_Build(t *testing.T) {
	cpuMatcher, _ := promParser.ParseMetricSelector("{__name__=~\"cpu.*\"}")
	proMatcher, _ := promParser.ParseMetricSelector("{__name__=~\"pro.*\"}")

	want := metricFilter{
		allowList: map[labels.Matcher][]matcher.Matchers{
			*cpuMatcher[0]: {
				{
					&labels.Matcher{
						Name:  types.LabelName,
						Type:  labels.MatchRegexp,
						Value: "cpu.*",
					},
				},
			},
			*proMatcher[0]: {
				{
					&labels.Matcher{
						Name:  types.LabelName,
						Type:  labels.MatchRegexp,
						Value: "pro.*",
					},
				},
			},
			{
				Name:  types.LabelName,
				Type:  labels.MatchEqual,
				Value: "process_cpu_seconds_total",
			}: {
				{
					&labels.Matcher{
						Name:  types.LabelName,
						Type:  labels.MatchEqual,
						Value: "process_cpu_seconds_total",
					},
					&labels.Matcher{
						Name:  types.LabelScrapeInstance,
						Type:  labels.MatchEqual,
						Value: "localhost:2113",
					},
					&labels.Matcher{
						Name:  types.LabelScrapeJob,
						Type:  labels.MatchEqual,
						Value: "my_application123",
					},
				},
			},
		},
		denyList: map[labels.Matcher][]matcher.Matchers{
			{
				Name:  types.LabelName,
				Type:  labels.MatchEqual,
				Value: "process_cpu_seconds_total",
			}: {
				{
					&labels.Matcher{
						Name:  types.LabelScrapeJob,
						Type:  labels.MatchEqual,
						Value: "my_application123",
					},
					&labels.Matcher{
						Name:  types.LabelName,
						Type:  labels.MatchEqual,
						Value: "process_cpu_seconds_total",
					},
				},
			},
			{
				Name:  types.LabelName,
				Type:  labels.MatchEqual,
				Value: "whatever",
			}: {
				{
					&labels.Matcher{
						Name:  types.LabelName,
						Type:  labels.MatchEqual,
						Value: "whatever",
					},
				},
			},
		},
	}

	filter, err := newMetricFilter(basicConf.Metric, false, true, true)
	if err != nil {
		t.Error(err)

		return
	}

	got := sortMatchers(listFromMap(filter.allowList))
	wanted := sortMatchers(listFromMap(want.allowList))

	res := cmp.Diff(got, wanted, cmpopts.IgnoreUnexported(labels.Matcher{}))
	if res != "" {
		t.Errorf("Generated allow list does not match the expected output:\n%s", res)
	}

	got = sortMatchers(listFromMap(filter.denyList))
	wanted = sortMatchers(listFromMap(want.denyList))

	res = cmp.Diff(got, wanted, cmpopts.IgnoreUnexported(labels.Matcher{}), cmpopts.IgnoreUnexported(labels.Matcher{}))
	if res != "" {
		t.Errorf("Generated deny list does not match the expected output:\n%s", res)
	}
}

func Test_basic_build_default(t *testing.T) {
	filter, err := newMetricFilter(defaultConf.Metric, true, true, true)
	if err != nil {
		t.Error(err)
	}

	allowedPoints := []types.MetricPoint{
		{
			Labels: map[string]string{
				"__name__":        "process_cpu_seconds_total",
				"label_not_read":  "value_not_read",
				"scrape_instance": "localhost:2113",
				"scrape_job":      "my_application123",
			},
		},
		{
			Labels: map[string]string{
				"__name__": "cpu_used",
			},
		},
		{
			Labels: map[string]string{
				"__name__": "agent_status",
			},
		},
		{
			Labels: map[string]string{
				"__name__": "process_context_switch",
			},
		},
		{
			Labels: map[string]string{
				"__name__": "swap_free",
			},
		},
		{
			Labels: map[string]string{
				"__name__":    "sysUpTime",
				"snmp_target": "printer.local",
			},
		},
		{
			Labels: map[string]string{
				"__name__": "smart_device_health_status",
			},
		},
		{
			Labels: map[string]string{
				"__name__": "sensor_temperature",
				"sensor":   "coretemp_package_id_0",
			},
		},
		{
			Labels: map[string]string{
				"__name__": "sensor_temperature",
				"sensor":   "coretemp_package_id_0",
			},
		},
		{
			Labels: map[string]string{
				"__name__": "sensor_temperature",
				"sensor":   "k10temp_tctl",
			},
		},
	}

	deniedPoints := []types.MetricPoint{
		{
			Labels: map[string]string{
				"__name__": "memcached_command_flush",
				"item":     "redis-memcached-1",
			},
		},
		{
			Labels: map[string]string{
				"__name__": "node_load1",
			},
		},
		{
			Labels: map[string]string{
				"__name__": "sensor_temperature",
				"sensor":   "coretemp_core_0",
			},
		},
		{
			Labels: map[string]string{
				"__name__": "sensor_temperature",
				"sensor":   "k10temp_tccd1",
			},
		},
		// Service metrics are only allowed when the service is discovered. This
		// test the default filter without any discovered services.
		{
			Labels: map[string]string{
				"__name__": "apache_requests",
			},
		},
	}

	sendPoints := make([]types.MetricPoint, len(allowedPoints)+len(deniedPoints))
	copy(sendPoints[:len(allowedPoints)], allowedPoints)
	copy(sendPoints[len(allowedPoints):len(allowedPoints)+len(deniedPoints)], deniedPoints)

	// Shuffle array to mix allowed & denied points
	rnd := rand.New(rand.NewSource(42)) //nolint: gosec
	rnd.Shuffle(len(sendPoints), func(i, j int) {
		sendPoints[i], sendPoints[j] = sendPoints[j], sendPoints[i]
	})

	gotPoints := filter.FilterPoints(sendPoints, false)

	if diff := types.DiffMetricPoints(allowedPoints, gotPoints, false); diff != "" {
		t.Errorf("FilterPoints mismatch (-want +got)\n%s", diff)
	}
}

func Test_Basic_FilterPoints(t *testing.T) {
	filter, err := newMetricFilter(basicConf.Metric, false, true, true)
	if err != nil {
		t.Error(err)

		return
	}

	// these points will be filtered out by the filter
	points := []types.MetricPoint{
		{
			Labels: map[string]string{
				"__name__":        "process_cpu_seconds_total",
				"label_not_read":  "value_not_read",
				"scrape_instance": "localhost:2113",
				"scrape_job":      "my_application123",
			},
		},
		{
			Labels: map[string]string{
				"__name__":        "whatever",
				"scrape_instance": "should_not_be_checked:8080",
				"scrape_job":      "should_not_be_checked",
			},
		},
	}

	want := []types.MetricPoint{
		{
			Labels: map[string]string{
				"__name__":            "cpu_process_1",
				"label_not_impacting": "value_not_impacting",
			},
		},
		{
			Labels: map[string]string{
				"__name__":        "process_cpu_seconds_total",
				"scrape_instance": "localhost:2112",
				"scrape_job":      "my_application122",
			},
		},
		{
			Labels: map[string]string{
				"__name__": "cpu_process_2",
			},
		},
		{
			Labels: map[string]string{
				"__name__": "cpu_process_1",
			},
		},
	}

	points = append(points, want...)

	newPoints := filter.FilterPoints(points, false)

	if diff := cmp.Diff(want, newPoints); diff != "" {
		t.Errorf("FilterPoints mismatch (-want +got)\n%s", diff)
	}
}

func Test_Basic_FilterFamilies(t *testing.T) {
	filter, err := newMetricFilter(basicConf.Metric, false, true, true)
	if err != nil {
		t.Error(err)

		return
	}

	metricNames := []string{"cpu_seconds", "process_cpu_seconds_total", "whatever"}
	lblsNames := []string{"scrape_instance", "scrape_job", "does_not_impact"}
	lblsValues := []string{"localhost:8015", "my_application123", "my_application456", "does_not_impact"}

	fm := []*dto.MetricFamily{
		{ // should not be filtered out
			Name: &metricNames[0],
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{
							Name:  &lblsNames[0],
							Value: &lblsValues[0],
						},
					},
				},
				{
					Label: []*dto.LabelPair{
						{
							Name:  &lblsNames[0],
							Value: &lblsValues[0],
						},
						{
							Name:  &lblsNames[2],
							Value: &lblsValues[3],
						},
					},
				},
			},
		},
		{ // should be half filtered out, so the family should NOT be removed
			Name: &metricNames[1],
			Metric: []*dto.Metric{
				{ // should be filtered out
					Label: []*dto.LabelPair{
						{
							Name:  &lblsNames[0],
							Value: &lblsValues[0],
						},
						{
							Name:  &lblsNames[1],
							Value: &lblsValues[1],
						},
					},
				},
				{ // should be filtered out, even with the extra label
					Label: []*dto.LabelPair{
						{
							Name:  &lblsNames[0],
							Value: &lblsValues[0],
						},
						{
							Name:  &lblsNames[1],
							Value: &lblsValues[1],
						},
						{
							Name:  &lblsNames[2],
							Value: &lblsValues[3],
						},
					},
				},
				{ // should NOT be filtered out
					Label: []*dto.LabelPair{
						{
							Name:  &lblsNames[0],
							Value: &lblsValues[0],
						},
						{
							Name:  &lblsNames[1],
							Value: &lblsValues[2],
						},
					},
				},
			},
		},
		{ // should be entirely filtered out, so the family should be removed
			Name: &metricNames[2],
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{
							Name:  &lblsNames[0],
							Value: &lblsValues[0],
						},
					},
				},
			},
		},
	}

	want := []*dto.MetricFamily{
		{
			Name: &metricNames[0],
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{
							Name:  &lblsNames[0],
							Value: &lblsValues[0],
						},
					},
				},
				{
					Label: []*dto.LabelPair{
						{
							Name:  &lblsNames[0],
							Value: &lblsValues[0],
						},
						{
							Name:  &lblsNames[2],
							Value: &lblsValues[3],
						},
					},
				},
			},
		},
		{
			Name: &metricNames[1],
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{
							Name:  &lblsNames[0],
							Value: &lblsValues[0],
						},
						{
							Name:  &lblsNames[1],
							Value: &lblsValues[2],
						},
					},
				},
			},
		},
	}

	got := filter.FilterFamilies(fm, false)

	res := cmp.Diff(got, want,
		cmpopts.IgnoreUnexported(dto.MetricFamily{}), cmpopts.IgnoreUnexported(dto.Metric{}),
		cmpopts.IgnoreUnexported(dto.LabelPair{}))

	if res != "" {
		t.Errorf("got() != expected():\n%s", res)
	}
}

type fakeScrapper struct {
	name             string
	registeredLabels map[string]map[string]string
	containersLabels map[string]map[string]string
}

func (f *fakeScrapper) GetContainersLabels() map[string]map[string]string {
	return f.containersLabels
}

func (f *fakeScrapper) GetRegisteredLabels() map[string]map[string]string {
	return f.registeredLabels
}

func Test_RebuildDynamicList(t *testing.T) {
	mf, _ := newMetricFilter(basicConf.Metric, false, true, true)

	d := fakeScrapper{
		name: "jobname",
		registeredLabels: map[string]map[string]string{
			"containerURL": {
				types.LabelMetaScrapeInstance: "containerURL",
				types.LabelMetaScrapeJob:      "discovered-exporters",
				"test":                        "salut",
			},
			"container2URL": {
				types.LabelMetaScrapeInstance: "container2URL",
				types.LabelMetaScrapeJob:      "discovered-exporters",
			},
		},
		containersLabels: map[string]map[string]string{
			"containerURL": {
				"prometheus.io/scrape":  "true",
				"glouton.allow_metrics": "something,else,same_name",
				"glouton.deny_metrics":  "other,same_name_2",
			},
			"container2URL": {
				"prometheus.io/scrape":  "true",
				"glouton.allow_metrics": "same_name",
				"glouton.deny_metrics":  "same_name_2",
			},
		},
	}

	allowListWant := make(map[labels.Matcher][]matcher.Matchers)
	denyListWant := make(map[labels.Matcher][]matcher.Matchers)

	for key, val := range mf.allowList {
		allowListWant[key] = val
	}

	for key, val := range mf.denyList {
		denyListWant[key] = val
	}

	m, _ := matcher.NormalizeMetric("{__name__=\"something\",scrape_instance=\"containerURL\",scrape_job=\"discovered-exporters\"}")
	m2, _ := matcher.NormalizeMetric("{__name__=\"else\",scrape_instance=\"containerURL\",scrape_job=\"discovered-exporters\"}")
	m3, _ := matcher.NormalizeMetric("{__name__=\"same_name\",scrape_instance=\"containerURL\",scrape_job=\"discovered-exporters\"}")
	m4, _ := matcher.NormalizeMetric("{__name__=\"same_name\",scrape_instance=\"container2URL\",scrape_job=\"discovered-exporters\"}")
	m5, _ := matcher.NormalizeMetric("{__name__=\"other\",scrape_instance=\"containerURL\",scrape_job=\"discovered-exporters\"}")
	m6, _ := matcher.NormalizeMetric("{__name__=\"same_name_2\",scrape_instance=\"containerURL\",scrape_job=\"discovered-exporters\"}")
	m7, _ := matcher.NormalizeMetric("{__name__=\"same_name_2\",scrape_instance=\"container2URL\",scrape_job=\"discovered-exporters\"}")

	allowListWant[*m.Get(types.LabelName)] = append(allowListWant[*m.Get(types.LabelName)], m)
	allowListWant[*m2.Get(types.LabelName)] = append(allowListWant[*m2.Get(types.LabelName)], m2)
	allowListWant[*m3.Get(types.LabelName)] = append(allowListWant[*m3.Get(types.LabelName)], m3, m4)
	denyListWant[*m5.Get(types.LabelName)] = append(denyListWant[*m5.Get(types.LabelName)], m5)
	denyListWant[*m6.Get(types.LabelName)] = append(denyListWant[*m6.Get(types.LabelName)], m6, m7)

	err := mf.RebuildDynamicLists(&d, []discovery.Service{}, []string{}, []string{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	got := sortMatchers(listFromMap(mf.allowList))
	wanted := sortMatchers(listFromMap(allowListWant))

	res := cmp.Diff(got, wanted, cmpopts.IgnoreUnexported(labels.Matcher{}))
	if res != "" {
		t.Fatalf("got != expected for allowList: %s", res)
	}

	got = sortMatchers(listFromMap(mf.denyList))
	wanted = sortMatchers(listFromMap(denyListWant))

	res = cmp.Diff(got, wanted, cmpopts.IgnoreUnexported(labels.Matcher{}))
	if res != "" {
		t.Fatalf("got != expected for denyList: %s", res)
	}

	// Rebuild is done twice to make sure the build is effectively cleared and rebuild
	err = mf.RebuildDynamicLists(&d, []discovery.Service{}, []string{}, []string{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	got = sortMatchers(listFromMap(mf.allowList))
	wanted = sortMatchers(listFromMap(allowListWant))

	res = cmp.Diff(got, wanted, cmpopts.IgnoreUnexported(labels.Matcher{}))
	if res != "" {
		t.Fatalf("got != expected for second allowList: %s", res)
	}

	got = sortMatchers(listFromMap(mf.denyList))
	wanted = sortMatchers(listFromMap(denyListWant))

	res = cmp.Diff(got, wanted, cmpopts.IgnoreUnexported(labels.Matcher{}))
	if res != "" {
		t.Fatalf("got != expected for second denyList: %s", res)
	}
}

func TestDontDuplicateKeys(t *testing.T) {
	metricCfg := config.Metric{
		IncludeDefaultMetrics: false,
		AllowMetrics: []string{
			"cpu*",
			"cpu*",
			"pro",
			"pro",
		},
	}

	mf, _ := newMetricFilter(metricCfg, false, true, true)

	if len(mf.allowList) != 2 {
		t.Errorf("Unexpected number of matchers: expected 2, got %d", len(mf.allowList))
	}
}

func Test_newMetricFilter(t *testing.T) { //nolint:maintidx
	tests := []struct {
		name                 string
		configAllow          []string
		configDeny           []string
		configIncludeDefault bool
		metrics              []labels.Labels
		rulesMatchers        []matcher.Matchers
		allowNeededByRules   bool
		want                 []labels.Labels
	}{
		{
			name: "mix RE and NRE",
			configAllow: []string{
				`{__name__!~"cpu_.*", item="/home"}`,
				`{__name__=~"cpu_.*"}`,
			},
			configIncludeDefault: false,
			metrics: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
				}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
				}),
			},
		},
		{
			name: "don't duplicate metrics",
			configAllow: []string{
				`{__name__=~"cpu_.*"}`,
			},
			configIncludeDefault: true,
			metrics: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
				}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
				}),
			},
		},
		{
			name: "Merge EQ",
			configAllow: []string{
				`{__name__="cpu_used", item="/mnt"}`,
				`{__name__="cpu_used", item="/home"}`,
			},
			configIncludeDefault: false,
			metrics: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
					"item":     "/mnt",
				}),
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
					"item":     "/home",
				}),
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
				}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
					"item":     "/mnt",
				}),
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
					"item":     "/home",
				}),
			},
		},
		{
			name: "Merge RE",
			configAllow: []string{
				`{__name__=~"cpu_.*", item="/mnt"}`,
				`{__name__=~"cpu_.*", item="/home"}`,
			},
			configIncludeDefault: false,
			metrics: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
					"item":     "/mnt",
				}),
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
					"item":     "/home",
				}),
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
					"item":     "/test",
				}),
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
				}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
					"item":     "/mnt",
				}),
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
					"item":     "/home",
				}),
			},
		},
		{
			name: "Merge NRE",
			configAllow: []string{
				`{__name__!~"cpu_.*", item="/mnt"}`,
				`{__name__!~"cpu_.*", item="/home"}`,
			},
			configIncludeDefault: false,
			metrics: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
					"item":     "/mnt",
				}),
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
					"item":     "/home",
				}),
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
				}),
				labels.FromMap(map[string]string{
					"__name__": "memory_used",
					"item":     "/home",
				}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__": "memory_used",
					"item":     "/home",
				}),
			},
		},
		{
			name: "Merge NEQ",
			configAllow: []string{
				`{__name__!="cpu_used", item="/mnt"}`,
				`{__name__!="cpu_used", item="/home"}`,
			},
			configIncludeDefault: false,
			metrics: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
					"item":     "/mnt",
				}),
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
					"item":     "/home",
				}),
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
				}),
				labels.FromMap(map[string]string{
					"__name__": "memory_used",
					"item":     "/home",
				}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__": "memory_used",
					"item":     "/home",
				}),
			},
		},
		{
			name: "Mix all filters",
			configAllow: []string{
				"cpu_*",
				"process_used",
				`node_cpu_seconds_global{mode=~"nice|user|system"}`,
				`{__name__!="cpu_used", item="/home"}`,
			},
			configDeny: []string{
				`{__name__=~"node_cpu_seconds_.*",mode="system"}`,
				"process_count",
			},
			configIncludeDefault: false,
			metrics: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
					"item":     "/mnt",
				}),
				labels.FromMap(map[string]string{
					"__name__": "memory_used",
				}),
				labels.FromMap(map[string]string{
					"__name__": "node_cpu_seconds_global",
					"mode":     "system",
				}),
				labels.FromMap(map[string]string{
					"__name__": "node_cpu_seconds_global",
					"mode":     "user",
				}),
				labels.FromMap(map[string]string{
					"__name__": "node_cpu_seconds_global",
					"mode":     "wait",
				}),
				labels.FromMap(map[string]string{
					"__name__": "memory_used",
					"item":     "/home",
				}),
				labels.FromMap(map[string]string{
					"__name__": "process_count",
					"item":     "/mnt",
				}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
					"item":     "/mnt",
				}),
				labels.FromMap(map[string]string{
					"__name__": "node_cpu_seconds_global",
					"mode":     "user",
				}),
				labels.FromMap(map[string]string{
					"__name__": "memory_used",
					"item":     "/home",
				}),
			},
		},
		{
			name: "No allowNeededByRules",
			configAllow: []string{
				`cpu_used`,
				`disk_used{item="/home"}`,
				`io_reads`,
				`io_writes`,
			},
			configDeny: []string{
				`io_writes`,
				`io_reads{item="/dev/sda"}`,
			},
			configIncludeDefault: false,
			rulesMatchers: []matcher.Matchers{
				{
					labels.MustNewMatcher(labels.MatchEqual, types.LabelName, "io_writes"),
				},
				{
					labels.MustNewMatcher(labels.MatchEqual, types.LabelName, "io_reads"),
				},
				{
					labels.MustNewMatcher(labels.MatchEqual, types.LabelName, "mem_used"),
				},
				{
					labels.MustNewMatcher(labels.MatchEqual, types.LabelName, "io_times"),
					labels.MustNewMatcher(labels.MatchEqual, types.LabelItem, "/dev/sdb"),
				},
			},
			metrics: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
				}),
				labels.FromMap(map[string]string{
					"__name__": "mem_used",
				}),
				labels.FromMap(map[string]string{
					"__name__": "disk_used",
					"item":     "/home",
				}),
				labels.FromMap(map[string]string{
					"__name__": "disk_used",
					"item":     "/srv",
				}),
				labels.FromMap(map[string]string{
					"__name__": "io_reads",
					"item":     "/dev/sda",
				}),
				labels.FromMap(map[string]string{
					"__name__": "io_reads",
					"item":     "/dev/sdb",
				}),
				labels.FromMap(map[string]string{
					"__name__": "io_writes",
					"item":     "/dev/sda",
				}),
				labels.FromMap(map[string]string{
					"__name__": "io_writes",
					"item":     "/dev/sdb",
				}),
				labels.FromMap(map[string]string{
					"__name__": "io_times",
					"item":     "/dev/sda",
				}),
				labels.FromMap(map[string]string{
					"__name__": "io_times",
					"item":     "/dev/sdb",
				}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
				}),
				labels.FromMap(map[string]string{
					"__name__": "disk_used",
					"item":     "/home",
				}),
				labels.FromMap(map[string]string{
					"__name__": "io_reads",
					"item":     "/dev/sdb",
				}),
			},
		},
		{
			name: "With allowNeededByRules",
			configAllow: []string{
				`cpu_used`,
				`disk_used{item="/home"}`,
				`io_reads`,
				`io_writes`,
			},
			configDeny: []string{
				`io_writes`,
				`io_reads{item="/dev/sda"}`,
			},
			configIncludeDefault: false,
			allowNeededByRules:   true,
			rulesMatchers: []matcher.Matchers{
				{
					labels.MustNewMatcher(labels.MatchEqual, types.LabelName, "io_writes"),
				},
				{
					labels.MustNewMatcher(labels.MatchEqual, types.LabelName, "io_reads"),
				},
				{
					labels.MustNewMatcher(labels.MatchEqual, types.LabelName, "mem_used"),
				},
				{
					labels.MustNewMatcher(labels.MatchEqual, types.LabelName, "io_times"),
					labels.MustNewMatcher(labels.MatchEqual, types.LabelItem, "/dev/sdb"),
				},
			},
			metrics: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
				}),
				labels.FromMap(map[string]string{
					"__name__": "mem_used",
				}),
				labels.FromMap(map[string]string{
					"__name__": "disk_used",
					"item":     "/home",
				}),
				labels.FromMap(map[string]string{
					"__name__": "disk_used",
					"item":     "/srv",
				}),
				labels.FromMap(map[string]string{
					"__name__": "io_reads",
					"item":     "/dev/sda",
				}),
				labels.FromMap(map[string]string{
					"__name__": "io_reads",
					"item":     "/dev/sdb",
				}),
				labels.FromMap(map[string]string{
					"__name__": "io_writes",
					"item":     "/dev/sda",
				}),
				labels.FromMap(map[string]string{
					"__name__": "io_writes",
					"item":     "/dev/sdb",
				}),
				labels.FromMap(map[string]string{
					"__name__": "io_times",
					"item":     "/dev/sda",
				}),
				labels.FromMap(map[string]string{
					"__name__": "io_times",
					"item":     "/dev/sdb",
				}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
				}),
				labels.FromMap(map[string]string{
					"__name__": "mem_used",
				}),
				labels.FromMap(map[string]string{
					"__name__": "disk_used",
					"item":     "/home",
				}),
				labels.FromMap(map[string]string{
					"__name__": "io_reads",
					"item":     "/dev/sda",
				}),
				labels.FromMap(map[string]string{
					"__name__": "io_reads",
					"item":     "/dev/sdb",
				}),
				labels.FromMap(map[string]string{
					"__name__": "io_writes",
					"item":     "/dev/sda",
				}),
				labels.FromMap(map[string]string{
					"__name__": "io_writes",
					"item":     "/dev/sdb",
				}),
				labels.FromMap(map[string]string{
					"__name__": "io_times",
					"item":     "/dev/sdb",
				}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			metricCfg := config.Metric{
				AllowMetrics:          tt.configAllow,
				DenyMetrics:           tt.configDeny,
				IncludeDefaultMetrics: tt.configIncludeDefault,
			}

			filter, err := newMetricFilter(metricCfg, false, true, true)
			if err != nil {
				t.Errorf("newMetricFilter() error = %v", err)

				return
			}

			filter.UpdateRulesMatchers(tt.rulesMatchers)

			t0 := time.Now()
			metrics := makeMetricsFromLabels(tt.metrics)
			points := makePointsFromLabels(tt.metrics, t0)
			families := makeFamiliesFromLabels(tt.metrics)
			wantMetrics := makeMetricsFromLabels(tt.want)
			wantPoints := makePointsFromLabels(tt.want, t0)
			wantFamilies := makeFamiliesFromLabels(tt.want)
			gotMetrics := filter.filterMetrics(metrics)
			gotPoints := filter.FilterPoints(points, tt.allowNeededByRules)
			gotFamilies := filter.FilterFamilies(families, tt.allowNeededByRules)

			if !tt.allowNeededByRules {
				// filterMetrics only support allowNeededByRules == false, so only test result
				// in that case.
				if !reflect.DeepEqual(wantMetrics, gotMetrics) {
					t.Errorf("FilterMetrics(): Expected :\n%v\ngot:\n%v", wantMetrics, gotMetrics)
				}
			}

			if diff := cmp.Diff(wantPoints, gotPoints); diff != "" {
				t.Errorf("FilterPoints mismatch (-want +got):\n%s", diff)
			}

			if diff := types.DiffMetricFamilies(wantFamilies, gotFamilies, false, false); diff != "" {
				t.Errorf("FilterFamilies mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func makePointsFromLabels(input []labels.Labels, t0 time.Time) []types.MetricPoint {
	points := make([]types.MetricPoint, 0, len(input))

	for _, lbls := range input {
		points = append(points, types.MetricPoint{
			Point: types.Point{
				Time:  t0,
				Value: 42,
			},
			Labels: lbls.Map(),
		})
	}

	return points
}

func makeFamiliesFromLabels(input []labels.Labels) []*dto.MetricFamily {
	points := makePointsFromLabels(input, time.Time{})

	return model.MetricPointsToFamilies(points)
}

func makeMetricsFromLabels(input []labels.Labels) []types.Metric {
	res := make([]types.Metric, 0, len(input))

	for _, lbls := range input {
		metric := fakeMetric{labels: lbls.Map()}

		res = append(res, metric)
	}

	return res
}

type fakeMetric struct {
	labels map[string]string
}

// Labels returns all label of the metric.
func (m fakeMetric) Labels() map[string]string {
	labels := make(map[string]string)

	for k, v := range m.labels {
		labels[k] = v
	}

	return labels
}

// Annotations returns all annotations of the metric.
func (m fakeMetric) Annotations() types.MetricAnnotations {
	return types.MetricAnnotations{}
}

// Points returns points between the two given time range (boundary are included).
func (m fakeMetric) Points(_, _ time.Time) (result []types.Point, err error) {
	return nil, nil
}

// LastPointReceivedAt returns points between the two given time range (boundary are included).
func (m fakeMetric) LastPointReceivedAt() time.Time {
	return time.Time{}
}

func listFromMap(m map[labels.Matcher][]matcher.Matchers) []matcher.Matchers {
	res := []matcher.Matchers{}

	for _, val := range m {
		res = append(res, val...)
	}

	return res
}

func sortMatchers(matchers []matcher.Matchers) []matcher.Matchers {
	sort.Slice(matchers, func(i, j int) bool {
		return matchers[i].String() < matchers[j].String()
	})

	return matchers
}

func generatePoints(nb int, lbls map[string]string) []types.MetricPoint {
	list := make([]types.MetricPoint, nb)

	for i := range nb {
		point := types.MetricPoint{
			Labels: lbls,
		}

		list[i] = point
	}

	return list
}

//nolint:gochecknoglobals
var goodPoint = map[string]string{
	"__name__":        "node_cpu_seconds_global",
	"label_not_read":  "value_not_read",
	"scrape_instance": "localhost:2113",
	"scrape_job":      "my_application123",
}

//nolint:gochecknoglobals
var badPoint = map[string]string{
	"__name__":        "cpu_used_status",
	"label_not_read":  "value_not_read",
	"scrape_instance": "localhost:2113",
	"scrape_job":      "my_application123",
}

func Benchmark_filters_no_match(b *testing.B) {
	metricFilter, _ := newMetricFilter(basicConf.Metric, false, true, false)

	list100 := generatePoints(100, badPoint)

	list10 := generatePoints(10, badPoint)

	list1 := []types.MetricPoint{
		{
			Labels: badPoint,
		},
	}

	b.ResetTimer()

	b.Run("Benchmark_filters_no_match_1", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list1))

		b.ResetTimer()

		for b.Loop() {
			copy(cop, list1)

			metricFilter.FilterPoints(cop, false)
		}
	})

	b.Run("Benchmark_filters_no_match_10", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list10))

		b.ResetTimer()

		for b.Loop() {
			copy(cop, list10)

			metricFilter.FilterPoints(cop, false)
		}
	})

	b.Run("Benchmark_filters_no_match_100", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list100))

		b.ResetTimer()

		for b.Loop() {
			copy(cop, list100)

			metricFilter.FilterPoints(cop, false)
		}
	})
}

func Benchmark_filters_one_match_first(b *testing.B) {
	metricFilter, _ := newMetricFilter(basicConf.Metric, false, true, false)

	list100 := []types.MetricPoint{
		{
			Labels: goodPoint,
		},
	}

	list100 = append(list100, generatePoints(99, badPoint)...)

	list10 := []types.MetricPoint{
		{
			Labels: goodPoint,
		},
	}

	list10 = append(list10, generatePoints(9, badPoint)...)

	list1 := []types.MetricPoint{
		{
			Labels: goodPoint,
		},
	}

	b.ResetTimer()

	b.Run("Benchmark_filters_one_match_first_1", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list1))

		b.ResetTimer()

		for b.Loop() {
			copy(cop, list1)

			metricFilter.FilterPoints(cop, false)
		}
	})

	b.Run("Benchmark_filters_one_match_first_10", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list10))

		b.ResetTimer()

		for b.Loop() {
			copy(cop, list10)

			metricFilter.FilterPoints(cop, false)
		}
	})

	b.Run("Benchmark_filters_one_match_first_100", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list100))

		b.ResetTimer()

		for b.Loop() {
			copy(cop, list100)

			metricFilter.FilterPoints(cop, false)
		}
	})
}

func Benchmark_filters_one_match_middle(b *testing.B) {
	metricFilter, _ := newMetricFilter(basicConf.Metric, false, true, false)

	list100 := generatePoints(49, badPoint)

	list100 = append(list100, types.MetricPoint{
		Labels: goodPoint,
	})

	list100 = append(list100, generatePoints(50, badPoint)...)

	list10 := generatePoints(4, badPoint)

	list10 = append(list10, types.MetricPoint{
		Labels: goodPoint,
	})

	list10 = append(list10, generatePoints(5, badPoint)...)

	b.ResetTimer()

	b.Run("Benchmark_filters_one_match_middle_10", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list10))

		b.ResetTimer()

		for b.Loop() {
			copy(cop, list10)

			metricFilter.FilterPoints(cop, false)
		}
	})

	b.Run("Benchmark_filters_one_match_middle_100", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list100))

		b.ResetTimer()

		for b.Loop() {
			copy(cop, list100)

			metricFilter.FilterPoints(cop, false)
		}
	})
}

func Benchmark_filters_one_match_last(b *testing.B) {
	metricFilter, _ := newMetricFilter(basicConf.Metric, false, true, false)

	list100 := generatePoints(99, badPoint)

	list100 = append(list100, types.MetricPoint{
		Labels: goodPoint,
	})

	list10 := generatePoints(9, badPoint)

	list10 = append(list10, types.MetricPoint{
		Labels: goodPoint,
	})

	b.ResetTimer()

	b.Run("Benchmark_filters_one_match_last_10", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list10))

		b.ResetTimer()

		for b.Loop() {
			copy(cop, list10)

			metricFilter.FilterPoints(cop, false)
		}
	})

	b.Run("Benchmark_filters_one_match_last_100", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list100))

		b.ResetTimer()

		for b.Loop() {
			copy(cop, list100)

			metricFilter.FilterPoints(cop, false)
		}
	})
}

func Benchmark_filters_all(b *testing.B) {
	metricFilter, _ := newMetricFilter(basicConf.Metric, false, true, false)

	list100 := generatePoints(100, goodPoint)

	list10 := generatePoints(10, goodPoint)

	b.ResetTimer()

	b.Run("Benchmark_filters_all_10", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list10))

		b.ResetTimer()

		for b.Loop() {
			copy(cop, list10)

			metricFilter.FilterPoints(cop, false)
		}
	})

	b.Run("Benchmark_filters_all_100", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list100))

		b.ResetTimer()

		for b.Loop() {
			copy(cop, list100)

			metricFilter.FilterPoints(cop, false)
		}
	})
}

func Test_RebuildDefaultMetrics(t *testing.T) {
	metricCfg := config.Metric{
		IncludeDefaultMetrics: true,
	}

	metricFilter, _ := newMetricFilter(metricCfg, false, true, false)

	services := []discovery.Service{
		{
			Active:      true,
			ServiceType: discovery.PostfixService,
		},
		{
			Active:      true,
			ServiceType: discovery.BindService,
		},
	}

	metricsMap := make(map[string]struct{})

	metricFilter.rebuildServicesMetrics(services, metricsMap)

	metricsNames := make([]string, 0, len(metricsMap))
	for k := range metricsMap {
		metricsNames = append(metricsNames, k)
	}

	want := []string{"postfix_queue_size"}

	res := cmp.Diff(metricsNames, want, cmpopts.IgnoreUnexported(labels.Matcher{}))

	if res != "" {
		t.Errorf("rebuildDefaultMetrics():\n%s", res)
	}
}

func Test_MergeMetricFilters(t *testing.T) {
	t.Parallel()

	mustMatchers := func(s string) matcher.Matchers {
		matchers, err := matcher.NormalizeMetric(s)
		if err != nil {
			t.Fatalf("Can't normalize metric %q: %v", s, err)
		}

		return matchers
	}

	metricCfg := config.Metric{
		IncludeDefaultMetrics: false,
		AllowMetrics:          []string{"m1", "m2"},
		DenyMetrics:           []string{"no1"},
	}
	mf1, _ := newMetricFilter(metricCfg, false, false, false)

	metricCfg.AllowMetrics = []string{"m3", "m4"}
	metricCfg.DenyMetrics = []string{"no1", "no2"}
	mf2, _ := newMetricFilter(metricCfg, false, false, false)

	mergedFilter := mergeMetricFilters(mf1, mf2)

	expectedFilter := &metricFilter{
		includeDefaultMetrics: false,
		staticAllowList:       []matcher.Matchers{mustMatchers("m1"), mustMatchers("m2"), mustMatchers("m3"), mustMatchers("m4")},
		staticDenyList:        []matcher.Matchers{mustMatchers("no1")},
		allowList: map[labels.Matcher][]matcher.Matchers{
			*labels.MustNewMatcher(labels.MatchEqual, types.LabelName, "m1"): {mustMatchers("m1")},
			*labels.MustNewMatcher(labels.MatchEqual, types.LabelName, "m2"): {mustMatchers("m2")},
			*labels.MustNewMatcher(labels.MatchEqual, types.LabelName, "m3"): {mustMatchers("m3")},
			*labels.MustNewMatcher(labels.MatchEqual, types.LabelName, "m4"): {mustMatchers("m4")},
		},
		denyList: map[labels.Matcher][]matcher.Matchers{
			*labels.MustNewMatcher(labels.MatchEqual, types.LabelName, "no1"): {mustMatchers("no1")},
		},
	}

	cmpOpts := cmp.Options{cmp.AllowUnexported(metricFilter{}), cmpopts.IgnoreTypes(sync.Mutex{}), cmpopts.IgnoreFields(labels.Matcher{}, "re")}
	if diff := cmp.Diff(expectedFilter, mergedFilter, cmpOpts); diff != "" {
		t.Fatalf("Unexpected filter merge result: (-want +got):\n%s", diff)
	}
}

// Test_MergeMetricFiltersMutation ensure that it's possible to modify in place
// the merge filter. This is required because filter get updated by RebuildDynamicLists.
func Test_MergeMetricFiltersMutation(t *testing.T) {
	t.Parallel()

	mustMatchers := func(s string) matcher.Matchers {
		matchers, err := matcher.NormalizeMetric(s)
		if err != nil {
			t.Fatalf("Can't normalize metric %q: %v", s, err)
		}

		return matchers
	}

	metricCfg := config.Metric{
		IncludeDefaultMetrics: false,
		AllowMetrics:          []string{"m1", "m2"},
		DenyMetrics:           []string{"no1"},
	}
	mf1, _ := newMetricFilter(metricCfg, false, false, false)

	metricCfg.AllowMetrics = []string{"m3", "m4"}
	metricCfg.DenyMetrics = []string{"no1", "no2"}
	mf2, _ := newMetricFilter(metricCfg, false, false, false)

	mergedFilter := mergeMetricFilters(mf1, mf2)
	shallowCopy := mergedFilter

	metricCfg2 := config.Metric{
		IncludeDefaultMetrics: false,
		AllowMetrics:          []string{"m1", "m2"},
		DenyMetrics:           []string{"m4"},
	}
	mf1b, _ := newMetricFilter(metricCfg2, false, false, false)

	mergedFilter.mergeInPlace(mf1b, mf2)

	expectedFilter := &metricFilter{
		includeDefaultMetrics: false,
		staticAllowList:       []matcher.Matchers{mustMatchers("m1"), mustMatchers("m2"), mustMatchers("m3"), mustMatchers("m4")},
		staticDenyList:        nil,
		allowList: map[labels.Matcher][]matcher.Matchers{
			*labels.MustNewMatcher(labels.MatchEqual, types.LabelName, "m1"): {mustMatchers("m1")},
			*labels.MustNewMatcher(labels.MatchEqual, types.LabelName, "m2"): {mustMatchers("m2")},
			*labels.MustNewMatcher(labels.MatchEqual, types.LabelName, "m3"): {mustMatchers("m3")},
			*labels.MustNewMatcher(labels.MatchEqual, types.LabelName, "m4"): {mustMatchers("m4")},
		},
		denyList: map[labels.Matcher][]matcher.Matchers{},
	}

	cmpOpts := cmp.Options{cmp.AllowUnexported(metricFilter{}), cmpopts.IgnoreTypes(sync.Mutex{}), cmpopts.IgnoreFields(labels.Matcher{}, "re")}
	if diff := cmp.Diff(expectedFilter, shallowCopy, cmpOpts); diff != "" {
		t.Fatalf("Unexpected filter merge result: (-want +got):\n%s", diff)
	}
}

func Benchmark_MultipleFilters(b *testing.B) {
	rawData, err := os.ReadFile("../prometheus/scrapper/testdata/large.txt")
	if err != nil {
		b.Fatal("Failed to read data file:", err)
	}

	lines := bytes.Split(rawData, []byte("\n"))
	metrics := make([]labels.Labels, 0, len(lines))

	for _, line := range lines {
		if len(line) == 0 {
			continue
		}

		spaceIdx := bytes.Index(line, []byte(" "))
		metric := string(line[:spaceIdx])

		lbls, err := promParser.ParseMetric(metric)
		if err != nil {
			b.Fatalf("Failed to parse %q: %v", metric, err)
		}

		metrics = append(metrics, lbls)
	}

	metrics = slices.Repeat(metrics, 10)

	b.Logf("Successfully loaded %d metrics", len(metrics))

	metricCfg := config.Metric{
		IncludeDefaultMetrics: true,
		AllowMetrics:          []string{"lotus_chain_basefee", "lotus_chain_height", "lotus_miner_deadline_active_partition_sector"},
		DenyMetrics:           []string{"lotus_miner_deadline_active_partition_sector"},
	}

	mf1, warns := newMetricFilter(metricCfg, false, true, false)
	if warns != nil {
		b.Fatal("Unexpected warns when building filter n°1:", warns)
	}

	metricCfg.AllowMetrics = []string{"lotus_miner_deadline_active_partition_sector"}

	mf2, warns := newMetricFilter(metricCfg, true, true, true)
	if warns != nil {
		b.Fatal("Unexpected warns when building filter n°2:", warns)
	}

	mergedMF := mergeMetricFilters(mf1, mf2)

	b.ResetTimer()

	b.Run("or", func(b *testing.B) {
		for b.Loop() {
			var allowed int

			for _, lbls := range metrics {
				if mf1.IsMetricAllowed(lbls, false) || mf2.IsMetricAllowed(lbls, false) {
					allowed++
				}
			}
		}
	})

	b.Run("merge", func(b *testing.B) {
		for b.Loop() {
			var allowed int

			for _, lbls := range metrics {
				if mergedMF.IsMetricAllowed(lbls, false) {
					allowed++
				}
			}
		}
	})
}
