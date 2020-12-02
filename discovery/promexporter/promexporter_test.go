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

// nolint: scopelint
package promexporter

import (
	"glouton/prometheus/scrapper"
	"glouton/types"
	"net/url"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

const fakeJobName = "jobname"
const fakePodNamespace = "default"

type mockContainers struct {
	labels         map[string]string
	annotations    map[string]string
	primaryAddress string
	name           string
	podName        string
}

func (c mockContainers) Labels() map[string]string {
	return c.labels
}

func (c mockContainers) Annotations() map[string]string {
	return c.annotations
}

func (c mockContainers) PrimaryAddress() string {
	return c.primaryAddress
}

func (c mockContainers) Name() string {
	return c.name
}

func (c mockContainers) PodNamespaceName() (string, string) {
	if c.podName == "" {
		return "", ""
	}

	return fakePodNamespace, c.podName
}

func TestListExporters(t *testing.T) {
	mustParse := func(text string) *url.URL {
		u, err := url.Parse(text)
		if err != nil {
			t.Fatal(err)
		}

		return u
	}

	tests := []struct {
		name                 string
		containers           []Container
		want                 []*scrapper.Target
		globalAllow          []string
		globalDeny           []string
		globalIncludeDefault bool
	}{
		{
			name:       "empty",
			containers: []Container{},
			want:       []*scrapper.Target{},
		},
		{
			name: "docker",
			containers: []Container{
				mockContainers{
					name:           "my_container",
					primaryAddress: "sample",
					labels: map[string]string{
						"prometheus.io/scrape": "true",
					},
				},
			},
			want: []*scrapper.Target{
				{
					URL: mustParse("http://sample:9102/metrics"),
					ExtraLabels: map[string]string{
						types.LabelContainerName:      "my_container",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample:9102",
					},
				},
			},
		},
		{
			name: "k8s",
			containers: []Container{
				mockContainers{
					name:           "k8s_containername_podname_namespace",
					podName:        "my_pod-1234",
					primaryAddress: "sample",
					annotations: map[string]string{
						"prometheus.io/scrape": "true",
					},
				},
			},
			want: []*scrapper.Target{
				{
					URL: mustParse("http://sample:9102/metrics"),
					ExtraLabels: map[string]string{
						"kubernetes.pod.namespace":    fakePodNamespace,
						"kubernetes.pod.name":         "my_pod-1234",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample:9102",
					},
				},
			},
		},
		{
			name: "another labels",
			containers: []Container{
				mockContainers{
					name:           "container",
					primaryAddress: "sample",
					labels: map[string]string{
						"glouton.enable": "true",
						"my_label":       "value",
					},
					annotations: map[string]string{
						"kubernetes.io/hello": "world",
					},
				},
			},
			want: []*scrapper.Target{},
		},
		{
			name: "two-with-alternate-port",
			containers: []Container{
				mockContainers{
					name:           "sample1_1",
					primaryAddress: "sample1",
					labels: map[string]string{
						"prometheus.io/scrape": "true",
					},
				},
				mockContainers{
					name:           "k8s_sample2_default",
					podName:        "sample2-1234",
					primaryAddress: "sample2",
					labels: map[string]string{
						"prometheus.io/scrape": "true",
						"prometheus.io/port":   "8080",
					},
				},
			},
			want: []*scrapper.Target{
				{
					URL: mustParse("http://sample1:9102/metrics"),
					ExtraLabels: map[string]string{
						types.LabelContainerName:      "sample1_1",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample1:9102",
					},
				},
				{
					URL: mustParse("http://sample2:8080/metrics"),
					ExtraLabels: map[string]string{
						"kubernetes.pod.namespace":    fakePodNamespace,
						"kubernetes.pod.name":         "sample2-1234",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample2:8080",
					},
				},
			},
		},
		{
			name: "full-configured",
			containers: []Container{
				mockContainers{
					name:           "testname",
					primaryAddress: "sample",
					annotations: map[string]string{
						"prometheus.io/scrape": "true",
						"prometheus.io/port":   "8080",
						"prometheus.io/path":   "/metrics.txt",
					},
				},
			},
			want: []*scrapper.Target{
				{
					URL: mustParse("http://sample:8080/metrics.txt"),
					ExtraLabels: map[string]string{
						types.LabelContainerName:      "testname",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample:8080",
					},
				},
			},
		},
		{
			name: "path-without-slash",
			containers: []Container{
				mockContainers{
					name:           "testname",
					primaryAddress: "sample",
					annotations: map[string]string{
						"prometheus.io/scrape": "true",
						"prometheus.io/path":   "metrics.txt",
					},
				},
			},
			want: []*scrapper.Target{
				{
					URL: mustParse("http://sample:9102/metrics.txt"),
					ExtraLabels: map[string]string{
						types.LabelContainerName:      "testname",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample:9102",
					},
				},
			},
		},
		{
			name: "docker-global-metrics",
			containers: []Container{
				mockContainers{
					name:           "my_container",
					primaryAddress: "sample",
					labels: map[string]string{
						"prometheus.io/scrape": "true",
					},
				},
			},
			globalAllow:          []string{"global", "allow*"},
			globalDeny:           []string{"nope"},
			globalIncludeDefault: true,
			want: []*scrapper.Target{
				{
					URL: mustParse("http://sample:9102/metrics"),
					ExtraLabels: map[string]string{
						types.LabelContainerName:      "my_container",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample:9102",
					},
					AllowList:      []string{"global", "allow*"},
					DenyList:       []string{"nope"},
					IncludeDefault: true,
				},
			},
		},
		{
			name: "docker-allow-deny-metrics",
			containers: []Container{
				mockContainers{
					name:           "my_container",
					primaryAddress: "sample",
					labels: map[string]string{
						"prometheus.io/scrape":            "true",
						"glouton.allow_metrics":           "cpu_used,mem_used",
						"glouton.deny_metrics":            "up{job=\"prometheus\"}",
						"glouton.include_default_metrics": "no",
					},
				},
			},
			globalAllow:          []string{"global", "allow*"},
			globalDeny:           []string{"nope"},
			globalIncludeDefault: true,
			want: []*scrapper.Target{
				{
					URL: mustParse("http://sample:9102/metrics"),
					ExtraLabels: map[string]string{
						types.LabelContainerName:      "my_container",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample:9102",
					},
					AllowList:      []string{"cpu_used", "mem_used"},
					DenyList:       []string{"up{job=\"prometheus\"}"},
					IncludeDefault: false,
				},
			},
		},
		{
			name: "docker-reset-global",
			containers: []Container{
				mockContainers{
					name:           "my_container",
					primaryAddress: "sample",
					labels: map[string]string{
						"prometheus.io/scrape":            "true",
						"glouton.allow_metrics":           "",
						"glouton.deny_metrics":            "",
						"glouton.include_default_metrics": "false",
					},
				},
			},
			globalAllow:          []string{"global", "allow*"},
			globalDeny:           []string{"nope"},
			globalIncludeDefault: true,
			want: []*scrapper.Target{
				{
					URL: mustParse("http://sample:9102/metrics"),
					ExtraLabels: map[string]string{
						types.LabelContainerName:      "my_container",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample:9102",
					},
					AllowList:      []string{},
					DenyList:       []string{},
					IncludeDefault: false,
				},
			},
		},
		{
			name: "k8s-allow-metrics",
			containers: []Container{
				mockContainers{
					name:           "k8s_containername_podname_namespace",
					podName:        "my_pod-1234",
					primaryAddress: "sample",
					annotations: map[string]string{
						"prometheus.io/scrape":            "true",
						"glouton.allow_metrics":           "something,else",
						"glouton.include_default_metrics": "1",
					},
				},
			},
			globalAllow:          []string{"global", "allow*"},
			globalDeny:           []string{"nope"},
			globalIncludeDefault: false,
			want: []*scrapper.Target{
				{
					URL: mustParse("http://sample:9102/metrics"),
					ExtraLabels: map[string]string{
						"kubernetes.pod.namespace":    fakePodNamespace,
						"kubernetes.pod.name":         "my_pod-1234",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample:9102",
					},
					AllowList:      []string{"something", "else"},
					DenyList:       []string{"nope"},
					IncludeDefault: true,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := DynamicScrapper{
				DynamicJobName:              "jobname",
				GlobalAllowMetrics:          tt.globalAllow,
				GlobalDenyMetrics:           tt.globalDeny,
				GlobalIncludeDefaultMetrics: tt.globalIncludeDefault,
			}
			got := d.listExporters(tt.containers)

			if diff := cmp.Diff(tt.want, got, cmpopts.IgnoreUnexported(scrapper.Target{})); diff != "" {
				t.Errorf("ListExporters() != want: %v", diff)
			}
		})
	}
}
