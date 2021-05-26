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
		allowList: []matcher.Matchers{
			{
				&labels.Matcher{
					Name:  types.LabelName,
					Type:  labels.MatchRegexp,
					Value: "cpu.*",
				},
			},
			{
				&labels.Matcher{
					Name:  types.LabelName,
					Type:  labels.MatchRegexp,
					Value: "pro.*",
				},
			},
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
		denyList: []matcher.Matchers{
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
			{
				&labels.Matcher{
					Name:  types.LabelName,
					Type:  labels.MatchEqual,
					Value: "whatever",
				},
			},
		},
	}

	new, err := newMetricFilter(&cfg, types.MetricFormatBleemeo)

	if err != nil {
		t.Error(err)
		return
	}

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

	allowListWant := make([]matcher.Matchers, len(mf.allowList))
	denyListWant := make([]matcher.Matchers, len(mf.denyList))

	copy(allowListWant, mf.allowList)
	copy(denyListWant, mf.denyList)

	new, _ := matcher.NormalizeMetric("{__name__=\"something\",scrape_instance=\"containerURL\",scrape_job=\"discovered-exporters\"}")
	new2, _ := matcher.NormalizeMetric("{__name__=\"else\",scrape_instance=\"containerURL\",scrape_job=\"discovered-exporters\"}")
	new3, _ := matcher.NormalizeMetric("{__name__=\"other\",scrape_instance=\"containerURL\",scrape_job=\"discovered-exporters\"}")

	allowListWant = append(allowListWant, new2)
	allowListWant = append(allowListWant, new)
	denyListWant = append(denyListWant, new3)

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
