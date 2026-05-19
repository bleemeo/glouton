// Copyright 2015-2026 Bleemeo
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

package matcher

import (
	"testing"

	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/model/labels"
)

const (
	testCPUPercent   = "cpu_percent"
	testInstance1    = "instance_4"
	testInstance3    = "instance_3"
	testInstance2    = "instance_2"
	testJob1         = "job_4"
	testJob2         = "job_2"
	testJob3         = "job_3"
	testJobNotWanted = "job_not_wanted"
	testShouldFail   = "should_fail"
	testCPU2         = "cpu2"
	testCPU          = "cpu"
	testLabelTest    = "test"
)

func Test_NormalizeMetric(t *testing.T) {
	tests := []struct {
		name            string
		allowListString string
		scrapperName    string
		want            Matchers
	}{
		{
			name:            "basic metric",
			allowListString: testCPUPercent,
			want: Matchers{
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelName,
					Value: testCPUPercent,
				},
			},
		},
		{
			name:            "basic glob metric",
			allowListString: "cpu_*",
			want: Matchers{
				&labels.Matcher{
					Type:  labels.MatchRegexp,
					Name:  types.LabelName,
					Value: "cpu_.*",
				},
			},
		},
		{
			name:            "multiple glob metric",
			allowListString: "prom*_cpu_*",
			want: Matchers{
				&labels.Matcher{
					Type:  labels.MatchRegexp,
					Name:  types.LabelName,
					Value: "prom.*_cpu_.*",
				},
			},
		},
		{
			name:            "complete PromQL Matcher string",
			allowListString: "prom_cpu{test=\"Hello\",scrape_instance=\"instance_4\",scrape_job=\"job_4\"}",
			want: Matchers{
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  testLabelTest,
					Value: "Hello",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: testInstance1,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: testJob1,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelName,
					Value: "prom_cpu",
				},
			},
		},
		{
			name:            "metric contains a point but is not a regex",
			allowListString: "my.metric",
			want: Matchers{
				&labels.Matcher{
					Type: labels.MatchEqual,
					Name: types.LabelName,
					// The "." is transformed in "_", because "." is invalid for
					// the name of a metric.
					Value: "my_metric",
				},
			},
		},
		{
			name:            "metric contains a regex and is a glob",
			allowListString: "curr*.$_customer*",
			want: Matchers{
				&labels.Matcher{
					Type: labels.MatchRegexp,
					Name: types.LabelName,
					// The "." is transformed in "_", because "." is invalid for
					// the name of a metric.
					// Note: "$" is also invalid, but only some char are transformed.
					Value: "curr.*_\\$_customer.*",
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeMetric(test.allowListString)
			if err != nil {
				t.Errorf("Invalid result Got error => %v ", err)
			}

			res := cmp.Diff(got, test.want, cmpopts.IgnoreUnexported(labels.Matcher{}))
			if res != "" {
				t.Errorf("got() != expected(): =%s", res)
			}
		})
	}
}

func Test_Fail_NormalizeMetrics(t *testing.T) {
	metric := "this*_should$_fail{scrape_instance=\"error\"}"

	if _, err := NormalizeMetric(metric); err == nil {
		t.Errorf("Invalid case not treated as error: expected metric %s to fail", metric)
	}
}

func Test_Add_Same_Type(t *testing.T) {
	m, _ := NormalizeMetric(testCPU)

	err := m.Add(types.LabelName, testCPU2, labels.MatchEqual)
	if err != nil {
		t.Errorf("An error occurred: %v", err)
	}

	last := m[len(m)-1]

	if last.Name != types.LabelName || last.Type != labels.MatchEqual || last.Value != testCPU2 {
		t.Errorf("Invalid value of matcher field: expected {%s %v %s}, got {%s %s %s}",
			types.LabelName, labels.MatchEqual, testCPU2,
			last.Name, last.Type, last.Value)
	}
}

func Test_Add_Different_Type(t *testing.T) {
	m, _ := NormalizeMetric(testCPU)
	before := len(m)

	err := m.Add(types.LabelName, testCPU2, labels.MatchRegexp)
	if err != nil {
		t.Errorf("An error occurred: %v", err)
	}

	if before == len(m) {
		t.Errorf("Add function did not add properly: expected size %d, got %d", before, len(m))
	}
}

func Test_Add_Error(t *testing.T) {
	metric := testCPU
	m, _ := NormalizeMetric(metric)

	err := m.Add(testLabelTest, metric+"[", labels.MatchRegexp)
	if err == nil {
		t.Errorf("An error was not caught: expected a regex compile error on metric %s", metric)
	}
}

func Test_Matches_Basic_Point(t *testing.T) {
	tests := []struct {
		name     string
		point    types.MetricPoint
		matchers Matchers
		want     bool
	}{
		{
			name: "basic metric",
			point: types.MetricPoint{
				Labels: map[string]string{
					types.LabelName:           testCPUPercent,
					types.LabelScrapeInstance: testInstance1,
					types.LabelScrapeJob:      testJob1,
				},
			},
			matchers: Matchers{
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelName,
					Value: testCPUPercent,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: testInstance1,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: testJob1,
				},
			},
			want: true,
		},
		{
			name: "basic metric fail",
			point: types.MetricPoint{
				Labels: map[string]string{
					types.LabelName:           testShouldFail,
					types.LabelScrapeInstance: testInstance3,
					types.LabelScrapeJob:      testJob3,
				},
			},
			matchers: Matchers{
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelName,
					Value: testCPUPercent,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: testInstance3,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: testJob3,
				},
			},
			want: false,
		},
		{
			name: "basic metric fail missing label",
			point: types.MetricPoint{
				Labels: map[string]string{
					types.LabelName:           testCPUPercent,
					types.LabelScrapeInstance: testInstance3,
					// missing scrape job
				},
			},
			matchers: Matchers{
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelName,
					Value: testCPUPercent,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: testInstance3,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: testJob3,
				},
			},
			want: false,
		},
		{
			name: "basic metric regex",
			point: types.MetricPoint{
				Labels: map[string]string{
					types.LabelName:           "cpu_percent_whatever",
					types.LabelScrapeInstance: testInstance2,
					types.LabelScrapeJob:      testJob2,
				},
			},
			matchers: Matchers{
				labels.MustNewMatcher(labels.MatchRegexp, types.LabelName, "cpu_percent.*"),
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: testInstance2,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: testJob2,
				},
			},
			want: true,
		},
		{
			name: "basic metric regex fail",
			point: types.MetricPoint{
				Labels: map[string]string{
					types.LabelName:           "cpu_percent_whatever_should_fail",
					types.LabelScrapeInstance: testInstance1,
					types.LabelScrapeJob:      testJob1,
				},
			},
			matchers: Matchers{
				labels.MustNewMatcher(labels.MatchRegexp, types.LabelName, "cpu_percent.*_whatever"),
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: testInstance1,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: testJob1,
				},
			},
			want: false,
		},
		{
			name: "basic metric regex fail missing label",
			point: types.MetricPoint{
				Labels: map[string]string{
					types.LabelName:           "cpu_percent_whatever",
					types.LabelScrapeInstance: testInstance2,
				},
			},
			matchers: Matchers{
				labels.MustNewMatcher(labels.MatchRegexp, types.LabelName, "cpu_percent.*"),
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: testInstance2,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: testJob2,
				},
			},
			want: false,
		},
		{
			name: "basic metric label that matches not equal",
			point: types.MetricPoint{
				Labels: map[string]string{
					types.LabelName:           testCPUPercent,
					types.LabelScrapeInstance: testInstance3,
					types.LabelScrapeJob:      testJobNotWanted,
				},
			},
			matchers: Matchers{
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelName,
					Value: testCPUPercent,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: testInstance3,
				},
				&labels.Matcher{
					Type:  labels.MatchNotEqual,
					Name:  types.LabelScrapeJob,
					Value: testJobNotWanted,
				},
			},
			want: false,
		},
		{
			name: "basic metric missing label that works",
			point: types.MetricPoint{
				Labels: map[string]string{
					types.LabelName:           testCPUPercent,
					types.LabelScrapeInstance: testInstance1,
					// No scrape job, but the matcher is : scrape_job != testJobNotWanted
				},
			},
			matchers: Matchers{
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelName,
					Value: testCPUPercent,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: testInstance1,
				},
				&labels.Matcher{
					Type:  labels.MatchNotEqual,
					Name:  types.LabelScrapeJob,
					Value: testJobNotWanted,
				},
			},
			want: true,
		},
		{
			name: "should fail",
			point: types.MetricPoint{
				Labels: map[string]string{
					types.LabelScrapeInstance: testShouldFail,
					types.LabelScrapeJob:      testShouldFail,
				},
			},
			matchers: Matchers{
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelName,
					Value: testCPUPercent,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: testInstance1,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: testJob1,
				},
			},
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.matchers.Matches(test.point.Labels)

			if got != test.want {
				t.Errorf("Incorrect result expected %v, got %v", test.want, got)
			}
		})
	}
}

func Test_Matches_Basic_Family(t *testing.T) {
	fn := []string{testCPUPercent, testShouldFail, "cpu_whatever_should_fail"}
	lbln := []string{types.LabelScrapeInstance, types.LabelScrapeJob}
	lblv := []string{testInstance1, testJob1, testInstance2, testJob2}
	tests := []struct {
		name       string
		metricName string
		metric     *dto.Metric
		matchers   Matchers
		want       bool
	}{
		{
			name:       "basic metric glob",
			metricName: fn[0],
			metric: &dto.Metric{
				Label: []*dto.LabelPair{
					{
						Name:  &lbln[0],
						Value: &lblv[0],
					},
					{
						Name:  &lbln[1],
						Value: &lblv[1],
					},
				},
			},
			matchers: Matchers{
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelName,
					Value: testCPUPercent,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: testInstance1,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: testJob1,
				},
			},
			want: true,
		},
		{
			name:       "basic metric regex",
			metricName: fn[0],
			metric: &dto.Metric{
				Label: []*dto.LabelPair{
					{
						Name:  &lbln[0],
						Value: &lblv[0],
					},
					{
						Name:  &lbln[1],
						Value: &lblv[1],
					},
				},
			},
			matchers: Matchers{
				labels.MustNewMatcher(labels.MatchRegexp, types.LabelName, "cpu.*"),
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: testInstance1,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: testJob1,
				},
			},
			want: true,
		},
		{
			name:       "basic metric glob fail",
			metricName: fn[1],
			metric: &dto.Metric{
				Label: []*dto.LabelPair{
					{
						Name:  &lbln[0],
						Value: &lblv[0],
					},
					{
						Name:  &lbln[1],
						Value: &lblv[1],
					},
				},
			},
			matchers: Matchers{
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelName,
					Value: testCPUPercent,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: testInstance1,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: testJob1,
				},
			},
			want: false,
		},
		{
			name:       "basic metric glob fail missing label",
			metricName: fn[0],
			metric: &dto.Metric{
				Label: []*dto.LabelPair{
					{
						Name:  &lbln[0],
						Value: &lblv[0],
					}, // missing scrape job label
				},
			},
			matchers: Matchers{
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelName,
					Value: testCPUPercent,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: testInstance1,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: testJob1,
				},
			},
			want: false,
		},
		{
			name:       "basic metric regex fail",
			metricName: fn[2],
			metric: &dto.Metric{
				Label: []*dto.LabelPair{
					{
						Name:  &lbln[0],
						Value: &lblv[0],
					},
					{
						Name:  &lbln[1],
						Value: &lblv[1],
					},
				},
			},
			matchers: Matchers{
				labels.MustNewMatcher(labels.MatchRegexp, types.LabelName, "cpu.*whatever"),
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: testInstance1,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: testJob1,
				},
			},
			want: false,
		},
		{
			name:       "metric regex with unchecked labels",
			metricName: fn[0],
			metric: &dto.Metric{
				Label: []*dto.LabelPair{
					{
						Name:  &lbln[0],
						Value: &lblv[0],
					},
					{
						Name:  &lbln[1],
						Value: &lblv[1],
					},
				},
			},
			matchers: Matchers{
				labels.MustNewMatcher(labels.MatchRegexp, types.LabelName, "cpu.*"),
			},
			want: true,
		},
		{
			name:       "metric regex with different labels",
			metricName: fn[2],
			metric: &dto.Metric{
				Label: []*dto.LabelPair{
					{
						Name:  &lbln[0],
						Value: &lblv[2],
					},
					{
						Name:  &lbln[1],
						Value: &lblv[3],
					},
				},
			},
			matchers: Matchers{
				labels.MustNewMatcher(labels.MatchRegexp, types.LabelName, "cpu.*whatever"),
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: testInstance1,
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: testJob1,
				},
			},
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.matchers.Matches(model.DTO2Labels(test.metricName, test.metric.GetLabel()))

			if got != test.want {
				t.Errorf("An error occurred: expected %v, got %v", test.want, got)
			}
		})
	}
}

func Test_String(t *testing.T) {
	m, _ := NormalizeMetric(testCPU)

	_ = m.Add(testLabelTest, testLabelTest, labels.MatchEqual)

	want := "{__name__=\"cpu\",test=\"test\"}"

	if got := m.String(); got != want {
		t.Errorf("Invalid value for string result: got %s want %s", got, want)
	}
}
