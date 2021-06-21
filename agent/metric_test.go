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
	"fmt"
	"glouton/config"
	"glouton/discovery"
	"glouton/prometheus/matcher"
	"glouton/types"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/pkg/labels"
)

const basicConf = `
metric:
  allow_metrics:
    - cpu*
    - pro*
  deny_metrics:
    - process_cpu_seconds_total{scrape_job="my_application123"}
    - whatever

  prometheus:
    targets:
      - url: "http://localhost:2113/metrics"
        name: "my_application123"
        allow_metrics:
          - process_cpu_seconds_total

service:
  - id: myapplication
    jmx_port: 1234
    jmx_metrics:
    - name: heap_size_mb
      mbean: java.lang:type=Memory
      attribute: HeapMemoryUsage
      path: used
      scale: 0.000000954  # 1 / 1024 / 1024
    - name: request
      mbean: com.bleemeo.myapplication:type=ClientRequest
      attribute: Count
      derive: True
  - id: "apache"
    address: "127.0.0.1"
    port: 80
    http_path: "/"
    http_host: "127.0.0.1:80"
`

const defaultConf = `
metric:
  include_default_metrics: true
`

func Test_Basic_Build(t *testing.T) {
	cfg := config.Configuration{}

	err := cfg.LoadByte([]byte(basicConf))
	if err != nil {
		t.Error(err)
		return
	}

	want := metricFilter{
		allowList: map[labels.Matcher][]matcher.Matchers{
			{
				Name:  types.LabelName,
				Type:  labels.MatchRegexp,
				Value: "cpu.*",
			}: {
				{
					&labels.Matcher{
						Name:  types.LabelName,
						Type:  labels.MatchRegexp,
						Value: "cpu.*",
					},
				},
			},
			{
				Name:  types.LabelName,
				Type:  labels.MatchRegexp,
				Value: "pro.*",
			}: {
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

	new, err := newMetricFilter(&cfg, types.MetricFormatBleemeo)

	if err != nil {
		t.Error(err)
		return
	}

	fmt.Println("Before")
	for key := range new.allowList {
		fmt.Printf("%v %s\n", key, key.GetRegexString())
	}
	fmt.Println("want")
	for key := range new.allowList {
		fmt.Printf("%v %s\n", key, key.GetRegexString())
	}
	fmt.Println("end")

	new.allowList = sortMatchers(new.allowList)
	want.allowList = sortMatchers(want.allowList)

	fmt.Println("After")
	for key := range new.allowList {
		fmt.Printf("%v %s\n", key, key.GetRegexString())
	}

	fmt.Println("want")
	for key := range new.allowList {
		fmt.Printf("%v %s\n", key, key.GetRegexString())
	}
	fmt.Println("end")

	res := cmp.Diff(new.allowList, want.allowList, cmpopts.IgnoreUnexported(labels.Matcher{}))
	if res != "" {
		t.Errorf("Generated allow list does not match the expected output: %s", res)
	}

	res = cmp.Diff(new.denyList, want.denyList, cmpopts.IgnoreUnexported(labels.Matcher{}))
	if res != "" {
		t.Errorf("Generated deny list does not match the expected output: %s", res)
	}
}

func Test_basic_build_default(t *testing.T) {
	cfg := config.Configuration{}

	err := cfg.LoadByte([]byte(defaultConf))
	if err != nil {
		t.Error(err)
	}

	filter, err := newMetricFilter(&cfg, types.MetricFormatBleemeo)

	if err != nil {
		t.Error(err)
	}

	wantLen := len(bleemeoDefaultSystemMetrics) + len(commonDefaultSystemMetrics) + len(defaultServiceMetrics)

	if len(filter.allowList) != wantLen {
		t.Errorf("Unexpected number of matcher: expected %d, got %d", wantLen, len(filter.allowList))
	}
}

func Test_Basic_FilterPoints(t *testing.T) {
	cfg := config.Configuration{}

	err := cfg.LoadByte([]byte(basicConf))
	if err != nil {
		t.Error(err)
		return
	}

	new, err := newMetricFilter(&cfg, types.MetricFormatBleemeo)

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

	newPoints := new.FilterPoints(points)

	if len(newPoints) != len(want) {
		for _, val := range newPoints {
			fmt.Println(val.Labels)
		}

		t.Errorf("Invalid length of result: expected %d, got %d", len(want), len(newPoints))

		return
	}

	for idx, p := range newPoints {
		for key, val := range p.Labels {
			if val != want[idx].Labels[key] {
				t.Errorf("Invalid value of label %s: expected %s, got %s", key, want[idx].Labels[key], val)
			}
		}
	}
}

func Test_Basic_FilterFamilies(t *testing.T) {
	cfg := config.Configuration{}

	err := cfg.LoadByte([]byte(basicConf))
	if err != nil {
		t.Error(err)
		return
	}

	new, err := newMetricFilter(&cfg, types.MetricFormatBleemeo)

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

	got := new.FilterFamilies(fm)

	fmt.Println(got)

	res := cmp.Diff(got, want,
		cmpopts.IgnoreUnexported(dto.MetricFamily{}), cmpopts.IgnoreUnexported(dto.Metric{}),
		cmpopts.IgnoreUnexported(dto.LabelPair{}))

	if res != "" {
		t.Errorf("got() != expected(): %s", res)
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
	cfg := config.Configuration{}

	err := cfg.LoadByte([]byte(basicConf))
	if err != nil {
		t.Error(err)
	}

	mf, _ := newMetricFilter(&cfg, types.MetricFormatBleemeo)

	d := fakeScrapper{
		name: "jobname",
		registeredLabels: map[string]map[string]string{
			"containerURL": {
				types.LabelMetaScrapeInstance: "containerURL",
				types.LabelMetaScrapeJob:      "discovered-exporters",
				"test":                        "salut",
			},
		},
		containersLabels: map[string]map[string]string{
			"containerURL": {
				"prometheus.io/scrape":  "true",
				"glouton.allow_metrics": "something,else",
				"glouton.deny_metrics":  "other",
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

	new, _ := matcher.NormalizeMetric("{__name__=\"something\",scrape_instance=\"containerURL\",scrape_job=\"discovered-exporters\"}")
	new2, _ := matcher.NormalizeMetric("{__name__=\"else\",scrape_instance=\"containerURL\",scrape_job=\"discovered-exporters\"}")
	new3, _ := matcher.NormalizeMetric("{__name__=\"other\",scrape_instance=\"containerURL\",scrape_job=\"discovered-exporters\"}")

	allowListWant[*new.Get(types.LabelName)] = append(allowListWant[*new.Get(types.LabelName)], new)
	allowListWant[*new2.Get(types.LabelName)] = append(allowListWant[*new2.Get(types.LabelName)], new2)
	denyListWant[*new3.Get(types.LabelName)] = append(denyListWant[*new3.Get(types.LabelName)], new3)

	allowListWant = sortMatchers(allowListWant)
	denyListWant = sortMatchers(denyListWant)

	err = mf.RebuildDynamicLists(&d, []discovery.Service{}, []string{})
	if err != nil {
		t.Errorf("Unexpected error: %w", err)
	}

	mf.allowList = sortMatchers(mf.allowList)
	mf.denyList = sortMatchers(mf.denyList)

	res := cmp.Diff(mf.allowList, allowListWant, cmpopts.IgnoreUnexported(labels.Matcher{}))
	if res != "" {
		t.Errorf("got != expected for allowList: %s", res)
	}

	res = cmp.Diff(mf.denyList, denyListWant, cmpopts.IgnoreUnexported(labels.Matcher{}))
	if res != "" {
		t.Errorf("got != expected for denyList: %s", res)
	}
}

func sortMatchers(list map[labels.Matcher][]matcher.Matchers) map[labels.Matcher][]matcher.Matchers {
	keyList := []string{}
	orderedKey := make(map[labels.Matcher][]matcher.Matchers)

	for key := range list {
		keyList = append(keyList, key.Value)
	}

	for _, keyVal := range keyList {
		for k, l := range list {
			if k.Value != keyVal {
				continue
			}

			orderedList := []matcher.Matchers{}
			nameList := []string{}

			for _, val := range l {
				nameList = append(nameList, val[0].Value)
			}

			sort.Strings(nameList)

			for _, val := range nameList {
				for _, v := range l {
					if v[0].Value == val {
						orderedList = append(orderedList, v)
						break
					}
				}
			}

			orderedKey[k] = orderedList
		}
	}

	return orderedKey
}

func Benchmark_filters_no_match(b *testing.B) {
	cfg := config.Configuration{}

	err := cfg.LoadByte([]byte(defaultConf))
	if err != nil {
		b.Error(err)
	}

	metricFilter, _ := newMetricFilter(&cfg, types.MetricFormatPrometheus)

	list := []types.MetricPoint{}

	for i := 0; i < b.N; i++ {
		new := types.MetricPoint{
			Labels: map[string]string{
				"__name__":        "cpu_used_status",
				"label_not_read":  "value_not_read",
				"scrape_instance": "localhost:2113",
				"scrape_job":      "my_application123",
			},
		}

		list = append(list, new)
	}

	cop := []types.MetricPoint{}

	copy(cop, list)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		metricFilter.FilterPoints(list)

		b.StopTimer()
		copy(cop, list)
		b.StartTimer()
	}
}

func Benchmark_filters_one_match_first(b *testing.B) {
	cfg := config.Configuration{}

	err := cfg.LoadByte([]byte(defaultConf))
	if err != nil {
		b.Error(err)
	}

	metricFilter, _ := newMetricFilter(&cfg, types.MetricFormatPrometheus)

	list := []types.MetricPoint{
		{
			Labels: map[string]string{
				"__name__":        "node_cpu_seconds_global",
				"label_not_read":  "value_not_read",
				"scrape_instance": "localhost:2113",
				"scrape_job":      "my_application123",
			},
		},
	}

	for i := 0; i < b.N; i++ {
		new := types.MetricPoint{
			Labels: map[string]string{
				"__name__":        "cpu_used_status",
				"label_not_read":  "value_not_read",
				"scrape_instance": "localhost:2113",
				"scrape_job":      "my_application123",
			},
		}

		list = append(list, new)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		metricFilter.FilterPoints(list)
	}
}

func Benchmark_filters_one_match_middle(b *testing.B) {
	cfg := config.Configuration{}

	err := cfg.LoadByte([]byte(defaultConf))
	if err != nil {
		b.Error(err)
	}

	metricFilter, _ := newMetricFilter(&cfg, types.MetricFormatPrometheus)

	list := []types.MetricPoint{}

	i := 0

	for ; i < b.N/2; i++ {
		new := types.MetricPoint{
			Labels: map[string]string{
				"__name__":        "cpu_used_status",
				"label_not_read":  "value_not_read",
				"scrape_instance": "localhost:2113",
				"scrape_job":      "my_application123",
			},
		}

		list = append(list, new)
	}

	list = append(list, types.MetricPoint{
		Labels: map[string]string{
			"__name__":        "node_cpu_seconds_global",
			"label_not_read":  "value_not_read",
			"scrape_instance": "localhost:2113",
			"scrape_job":      "my_application123",
		},
	})

	for ; i < b.N; i++ {
		new := types.MetricPoint{
			Labels: map[string]string{
				"__name__":        "cpu_used_status",
				"label_not_read":  "value_not_read",
				"scrape_instance": "localhost:2113",
				"scrape_job":      "my_application123",
			},
		}

		list = append(list, new)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		metricFilter.FilterPoints(list)
	}
}

func Benchmark_filters_one_match_last(b *testing.B) {
	cfg := config.Configuration{}

	err := cfg.LoadByte([]byte(defaultConf))
	if err != nil {
		b.Error(err)
	}

	metricFilter, _ := newMetricFilter(&cfg, types.MetricFormatPrometheus)

	list := []types.MetricPoint{}

	for i := 0; i < b.N; i++ {
		new := types.MetricPoint{
			Labels: map[string]string{
				"__name__":        "cpu_used_status",
				"label_not_read":  "value_not_read",
				"scrape_instance": "localhost:2113",
				"scrape_job":      "my_application123",
			},
		}

		list = append(list, new)
	}

	list = append(list, types.MetricPoint{
		Labels: map[string]string{
			"__name__":        "node_cpu_seconds_global",
			"label_not_read":  "value_not_read",
			"scrape_instance": "localhost:2113",
			"scrape_job":      "my_application123",
		},
	})

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		metricFilter.FilterPoints(list)
	}
}

func Benchmark_filters_all(b *testing.B) {
	cfg := config.Configuration{}

	err := cfg.LoadByte([]byte(defaultConf))
	if err != nil {
		b.Error(err)
	}

	metricFilter, _ := newMetricFilter(&cfg, types.MetricFormatPrometheus)

	list := []types.MetricPoint{}

	for i := 0; i < b.N; i++ {
		new := types.MetricPoint{
			Labels: map[string]string{
				"__name__":        "node_cpu_seconds_global",
				"label_not_read":  "value_not_read",
				"scrape_instance": "localhost:2113",
				"scrape_job":      "my_application123",
			},
		}

		list = append(list, new)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		metricFilter.FilterPoints(list)
	}
}
