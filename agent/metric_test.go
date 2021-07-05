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
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

const basicConf = `
metric:
  include_default_metrics: false
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

	cpuMatcher, _ := parser.ParseMetricSelector("{__name__=~\"cpu.*\"}")
	proMatcher, _ := parser.ParseMetricSelector("{__name__=~\"pro.*\"}")

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

	new, err := newMetricFilter(&cfg, types.MetricFormatBleemeo)

	if err != nil {
		t.Error(err)
		return
	}

	got := sortMatchers(listFromMap(new.allowList))
	wanted := sortMatchers(listFromMap(want.allowList))

	res := cmp.Diff(got, wanted, cmpopts.IgnoreUnexported(labels.Matcher{}))
	if res != "" {
		t.Errorf("Generated allow list does not match the expected output:\n%s", res)
	}

	got = sortMatchers(listFromMap(new.denyList))
	wanted = sortMatchers(listFromMap(want.denyList))

	res = cmp.Diff(got, wanted, cmpopts.IgnoreUnexported(labels.Matcher{}), cmpopts.IgnoreUnexported(labels.Matcher{}))
	if res != "" {
		t.Errorf("Generated deny list does not match the expected output:\n%s", res)
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

	wantLen := len(bleemeoDefaultSystemMetrics) + len(commonDefaultSystemMetrics)

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

	err = mf.RebuildDynamicLists(&d, []discovery.Service{}, []string{})
	if err != nil {
		t.Errorf("Unexpected error: %w", err)
	}

	got := sortMatchers(listFromMap(mf.allowList))
	wanted := sortMatchers(listFromMap(allowListWant))

	res := cmp.Diff(got, wanted, cmpopts.IgnoreUnexported(labels.Matcher{}))
	if res != "" {
		t.Errorf("got != expected for allowList: %s", res)
	}

	got = sortMatchers(listFromMap(mf.denyList))
	wanted = sortMatchers(listFromMap(denyListWant))

	res = cmp.Diff(got, wanted, cmpopts.IgnoreUnexported(labels.Matcher{}))
	if res != "" {
		t.Errorf("got != expected for denyList: %s", res)
	}
}

func TestDontDuplicateKeys(t *testing.T) {
	cfg := config.Configuration{}

	err := cfg.LoadByte([]byte(`
metric:
  include_default_metrics: false
  allow_metrics:
    - cpu*
    - cpu*
    - pro
    - pro
`))
	if err != nil {
		t.Error(err)
	}

	mf, _ := newMetricFilter(&cfg, types.MetricFormatBleemeo)

	if len(mf.allowList) != 2 {
		t.Errorf("Unexpected number of matchers: expected 2, got %d", len(mf.allowList))
	}
}

func Test_newMetricFilter(t *testing.T) {
	tests := []struct {
		name                 string
		configAllow          []string
		configDeny           []string
		configIncludeDefault bool
		metricFormat         types.MetricFormat
		metrics              []labels.Labels
		want                 []labels.Labels
	}{
		{
			name: "mix RE and NRE",
			configAllow: []string{
				`{__name__!~"cpu_.*", mountpoint="/home"}`,
				`{__name__=~"cpu_.*"}`,
			},
			configIncludeDefault: false,
			metricFormat:         types.MetricFormatBleemeo,
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
			metricFormat:         types.MetricFormatBleemeo,
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
				`{__name__="cpu_used", mountpoint="/mnt"}`,
				`{__name__="cpu_used", mountpoint="/home"}`,
			},
			configIncludeDefault: false,
			metricFormat:         types.MetricFormatBleemeo,
			metrics: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__":   "cpu_used",
					"mountpoint": "/mnt",
				}),
				labels.FromMap(map[string]string{
					"__name__":   "cpu_used",
					"mountpoint": "/home",
				}),
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
				}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__":   "cpu_used",
					"mountpoint": "/mnt",
				}),
				labels.FromMap(map[string]string{
					"__name__":   "cpu_used",
					"mountpoint": "/home",
				}),
			},
		},
		{
			name: "Merge RE",
			configAllow: []string{
				`{__name__=~"cpu_.*", mountpoint="/mnt"}`,
				`{__name__=~"cpu_.*", mountpoint="/home"}`,
			},
			configIncludeDefault: false,
			metricFormat:         types.MetricFormatBleemeo,
			metrics: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__":   "cpu_used",
					"mountpoint": "/mnt",
				}),
				labels.FromMap(map[string]string{
					"__name__":   "cpu_used",
					"mountpoint": "/home",
				}),
				labels.FromMap(map[string]string{
					"__name__": "cpu_used",
				}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__":   "cpu_used",
					"mountpoint": "/mnt",
				}),
				labels.FromMap(map[string]string{
					"__name__":   "cpu_used",
					"mountpoint": "/home",
				}),
			},
		},
		{
			name: "Merge NRE",
			configAllow: []string{
				`{__name__!~"cpu_.*", mountpoint="/mnt"}`,
				`{__name__!~"cpu_.*", mountpoint="/home"}`,
			},
			configIncludeDefault: false,
			metricFormat:         types.MetricFormatBleemeo,
			metrics: []labels.Labels{
				labels.FromMap(map[string]string{
					"__name__":   "cpu_used",
					"mountpoint": "/mnt",
				}),
				labels.FromMap(map[string]string{
					"__name__":   "cpu_used",
					"mountpoint": "/home",
				}),
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
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &config.Configuration{}

			cfg.Set("metric.allow_metrics", tt.configAllow)
			cfg.Set("metric.deny_metrics", tt.configDeny)
			cfg.Set("metric.include_default_metrics", tt.configIncludeDefault)

			filter, err := newMetricFilter(cfg, tt.metricFormat)
			if err != nil {
				t.Errorf("newMetricFilter() error = %v", err)
				return
			}

			t0 := time.Now()
			points := makePointsFromLabels(tt.metrics, t0)
			want := makePointsFromLabels(tt.want, t0)
			got := filter.FilterPoints(points)

			if diff := cmp.Diff(got, want); diff != "" {
				t.Errorf("FilterPoints(): %s", diff)
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

func listFromMap(m map[labels.Matcher][]matcher.Matchers) []matcher.Matchers {
	res := []matcher.Matchers{}

	for _, val := range m {
		res = append(res, val...)
	}

	return res
}

func sortMatchers(list []matcher.Matchers) []matcher.Matchers {
	nameList := []string{}
	orderedList := []matcher.Matchers{}

	for _, val := range list {
		nameList = append(nameList, val[0].Value)
	}

	sort.Strings(nameList)

	for _, val := range nameList {
		for _, v := range list {
			if v[0].Value == val {
				orderedList = append(orderedList, v)
				break
			}
		}
	}

	return orderedList
}

func generatePoints(nb int, lbls map[string]string) []types.MetricPoint {
	list := make([]types.MetricPoint, nb)

	for i := 0; i < nb; i++ {
		new := types.MetricPoint{
			Labels: lbls,
		}

		list = append(list, new)
	}

	return list
}

//nolint: gochecknoglobals
var goodPoint = map[string]string{
	"__name__":        "node_cpu_seconds_global",
	"label_not_read":  "value_not_read",
	"scrape_instance": "localhost:2113",
	"scrape_job":      "my_application123",
}

//nolint: gochecknoglobals
var badPoint = map[string]string{
	"__name__":        "cpu_used_status",
	"label_not_read":  "value_not_read",
	"scrape_instance": "localhost:2113",
	"scrape_job":      "my_application123",
}

func Benchmark_filters_no_match(b *testing.B) {
	cfg := config.Configuration{}

	err := cfg.LoadByte([]byte(defaultConf))
	if err != nil {
		b.Error(err)
	}

	metricFilter, _ := newMetricFilter(&cfg, types.MetricFormatPrometheus)

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

		for i := 0; i < b.N; i++ {
			b.StopTimer()
			copy(cop, list1)
			b.StartTimer()

			metricFilter.FilterPoints(cop)
		}
	})

	b.Run("Benchmark_filters_no_match_10", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list10))

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			b.StopTimer()
			copy(cop, list10)
			b.StartTimer()

			metricFilter.FilterPoints(cop)
		}
	})

	b.Run("Benchmark_filters_no_match_100", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list100))

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			b.StopTimer()
			copy(cop, list100)
			b.StartTimer()

			metricFilter.FilterPoints(cop)
		}
	})
}

func Benchmark_filters_one_match_first(b *testing.B) {
	cfg := config.Configuration{}

	err := cfg.LoadByte([]byte(defaultConf))
	if err != nil {
		b.Error(err)
	}

	metricFilter, _ := newMetricFilter(&cfg, types.MetricFormatPrometheus)

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

		for i := 0; i < b.N; i++ {
			b.StopTimer()
			copy(cop, list1)
			b.StartTimer()

			metricFilter.FilterPoints(cop)
		}
	})

	b.Run("Benchmark_filters_one_match_first_10", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list10))

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			b.StopTimer()
			copy(cop, list10)
			b.StartTimer()

			metricFilter.FilterPoints(cop)
		}
	})

	b.Run("Benchmark_filters_one_match_first_100", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list100))

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			b.StopTimer()
			copy(cop, list100)
			b.StartTimer()

			metricFilter.FilterPoints(cop)
		}
	})
}

func Benchmark_filters_one_match_middle(b *testing.B) {
	cfg := config.Configuration{}

	err := cfg.LoadByte([]byte(defaultConf))
	if err != nil {
		b.Error(err)
	}

	metricFilter, _ := newMetricFilter(&cfg, types.MetricFormatPrometheus)

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

		for i := 0; i < b.N; i++ {
			b.StopTimer()
			copy(cop, list10)
			b.StartTimer()

			metricFilter.FilterPoints(cop)
		}
	})

	b.Run("Benchmark_filters_one_match_middle_100", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list100))

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			b.StopTimer()
			copy(cop, list100)
			b.StartTimer()

			metricFilter.FilterPoints(cop)
		}
	})
}

func Benchmark_filters_one_match_last(b *testing.B) {
	cfg := config.Configuration{}

	err := cfg.LoadByte([]byte(defaultConf))
	if err != nil {
		b.Error(err)
	}

	metricFilter, _ := newMetricFilter(&cfg, types.MetricFormatPrometheus)

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

		for i := 0; i < b.N; i++ {
			b.StopTimer()
			copy(cop, list10)
			b.StartTimer()

			metricFilter.FilterPoints(cop)
		}
	})

	b.Run("Benchmark_filters_one_match_last_100", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list100))

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			b.StopTimer()
			copy(cop, list100)
			b.StartTimer()

			metricFilter.FilterPoints(cop)
		}
	})
}

func Benchmark_filters_all(b *testing.B) {
	cfg := config.Configuration{}

	err := cfg.LoadByte([]byte(defaultConf))
	if err != nil {
		b.Error(err)
	}

	metricFilter, _ := newMetricFilter(&cfg, types.MetricFormatPrometheus)

	list100 := generatePoints(100, goodPoint)

	list10 := generatePoints(10, goodPoint)

	b.ResetTimer()

	b.Run("Benchmark_filters_all_10", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list10))

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			b.StopTimer()
			copy(cop, list10)
			b.StartTimer()

			metricFilter.FilterPoints(cop)
		}
	})

	b.Run("Benchmark_filters_all_100", func(b *testing.B) {
		cop := make([]types.MetricPoint, len(list100))

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			b.StopTimer()
			copy(cop, list100)
			b.StartTimer()

			metricFilter.FilterPoints(cop)
		}
	})
}
