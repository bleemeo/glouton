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

package promexporter

import (
	"net/url"
	"testing"

	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/prometheus/scrapper"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

const (
	fakeJobName      = "jobname"
	fakePodNamespace = "default"
)

func TestListExporters(t *testing.T) { //nolint:maintidx
	mustParse := func(text string) *url.URL {
		u, err := url.Parse(text)
		if err != nil {
			t.Fatal(err)
		}

		return u
	}

	tests := []struct {
		name                 string
		containers           []facts.Container
		want                 []*scrapper.Target
		globalIncludeDefault bool
	}{
		{
			name:       "empty",
			containers: []facts.Container{},
			want:       []*scrapper.Target{},
		},
		{
			name: "docker",
			containers: []facts.Container{
				facts.FakeContainer{
					FakeContainerName:  "my_container",
					FakePrimaryAddress: "sample",
					FakeLabels: map[string]string{
						"prometheus.io/scrape": "true",
					},
				},
			},
			want: []*scrapper.Target{
				{
					URL: mustParse("http://sample:9102/metrics"),
					ContainerLabels: map[string]string{
						"prometheus.io/scrape": "true",
					},
					ExtraLabels: map[string]string{
						types.LabelMetaContainerName:  "my_container",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample:9102",
					},
				},
			},
		},
		{
			name: "k8s",
			containers: []facts.Container{
				facts.FakeContainer{
					FakeContainerName:  "k8s_containername_podname_namespace",
					FakePodName:        "my_pod-1234",
					FakePodNamespace:   "default",
					FakePrimaryAddress: "sample",
					FakeAnnotations: map[string]string{
						"prometheus.io/scrape": "true",
					},
				},
			},
			want: []*scrapper.Target{
				{
					URL: mustParse("http://sample:9102/metrics"),
					ContainerLabels: map[string]string{
						"prometheus.io/scrape": "true",
					},
					ExtraLabels: map[string]string{
						// K8S don't use meta label, because registry don't convert them to normal label unlike container_name label
						types.LabelK8SNamespace:       fakePodNamespace,
						types.LabelK8SPODName:         "my_pod-1234",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample:9102",
					},
				},
			},
		},
		{
			name: "another labels",
			containers: []facts.Container{
				facts.FakeContainer{
					FakeContainerName:  "container",
					FakePrimaryAddress: "sample",
					FakeLabels: map[string]string{
						"glouton.enable": "true",
						"my_label":       "value",
					},
					FakeAnnotations: map[string]string{
						"kubernetes.io/hello": "world",
					},
				},
			},
			want: []*scrapper.Target{},
		},
		{
			name: "two-with-alternate-port",
			containers: []facts.Container{
				facts.FakeContainer{
					FakeContainerName:  "sample1_1",
					FakePrimaryAddress: "sample1",
					FakeLabels: map[string]string{
						"prometheus.io/scrape": "true",
					},
				},
				facts.FakeContainer{
					FakeContainerName:  "k8s_sample2_default",
					FakePodName:        "sample2-1234",
					FakePodNamespace:   "default",
					FakePrimaryAddress: "sample2",
					FakeLabels: map[string]string{
						"prometheus.io/scrape": "true",
						"prometheus.io/port":   "8080",
					},
				},
			},
			want: []*scrapper.Target{
				{
					URL: mustParse("http://sample1:9102/metrics"),
					ContainerLabels: map[string]string{
						"prometheus.io/scrape": "true",
					},
					ExtraLabels: map[string]string{
						types.LabelMetaContainerName:  "sample1_1",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample1:9102",
					},
				},
				{
					URL: mustParse("http://sample2:8080/metrics"),
					ContainerLabels: map[string]string{
						"prometheus.io/scrape": "true",
						"prometheus.io/port":   "8080",
					},
					ExtraLabels: map[string]string{
						types.LabelK8SNamespace:       fakePodNamespace,
						types.LabelK8SPODName:         "sample2-1234",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample2:8080",
					},
				},
			},
		},
		{
			name: "full-configured",
			containers: []facts.Container{
				facts.FakeContainer{
					FakeContainerName:  "testname",
					FakePrimaryAddress: "sample",
					FakeAnnotations: map[string]string{
						"prometheus.io/scrape": "true",
						"prometheus.io/port":   "8080",
						"prometheus.io/path":   "/metrics.txt",
					},
				},
			},
			want: []*scrapper.Target{
				{
					URL: mustParse("http://sample:8080/metrics.txt"),
					ContainerLabels: map[string]string{
						"prometheus.io/scrape": "true",
						"prometheus.io/port":   "8080",
						"prometheus.io/path":   "/metrics.txt",
					},
					ExtraLabels: map[string]string{
						types.LabelMetaContainerName:  "testname",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample:8080",
					},
				},
			},
		},
		{
			name: "path-without-slash",
			containers: []facts.Container{
				facts.FakeContainer{
					FakeContainerName:  "testname",
					FakePrimaryAddress: "sample",
					FakeAnnotations: map[string]string{
						"prometheus.io/scrape": "true",
						"prometheus.io/path":   "metrics.txt",
					},
				},
			},
			want: []*scrapper.Target{
				{
					URL: mustParse("http://sample:9102/metrics.txt"),
					ContainerLabels: map[string]string{
						"prometheus.io/scrape": "true",
						"prometheus.io/path":   "metrics.txt",
					},
					ExtraLabels: map[string]string{
						types.LabelMetaContainerName:  "testname",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample:9102",
					},
				},
			},
		},
		{
			name: "docker-global-metrics",
			containers: []facts.Container{
				facts.FakeContainer{
					FakeContainerName:  "my_container",
					FakePrimaryAddress: "sample",
					FakeLabels: map[string]string{
						"prometheus.io/scrape": "true",
					},
				},
			},
			want: []*scrapper.Target{
				{
					URL: mustParse("http://sample:9102/metrics"),
					ContainerLabels: map[string]string{
						"prometheus.io/scrape": "true",
					},
					ExtraLabels: map[string]string{
						types.LabelMetaContainerName:  "my_container",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample:9102",
					},
				},
			},
		},
		{
			name: "docker-allow-deny-metrics",
			containers: []facts.Container{
				facts.FakeContainer{
					FakeContainerName:  "my_container",
					FakePrimaryAddress: "sample",
					FakeLabels: map[string]string{
						"prometheus.io/scrape":            "true",
						"glouton.allow_metrics":           "cpu_used,mem_used",
						"glouton.deny_metrics":            "up{job=\"prometheus\"}",
						"glouton.include_default_metrics": "no",
					},
				},
			},
			want: []*scrapper.Target{
				{
					URL: mustParse("http://sample:9102/metrics"),
					ContainerLabels: map[string]string{
						"prometheus.io/scrape":            "true",
						"glouton.allow_metrics":           "cpu_used,mem_used",
						"glouton.deny_metrics":            "up{job=\"prometheus\"}",
						"glouton.include_default_metrics": "no",
					},
					ExtraLabels: map[string]string{
						types.LabelMetaContainerName:  "my_container",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample:9102",
					},
				},
			},
		},
		{
			name: "docker-reset-global",
			containers: []facts.Container{
				facts.FakeContainer{
					FakeContainerName:  "my_container",
					FakePrimaryAddress: "sample",
					FakeLabels: map[string]string{
						"prometheus.io/scrape":            "true",
						"glouton.allow_metrics":           "",
						"glouton.deny_metrics":            "",
						"glouton.include_default_metrics": "false",
					},
				},
			},
			want: []*scrapper.Target{
				{
					URL: mustParse("http://sample:9102/metrics"),
					ContainerLabels: map[string]string{
						"prometheus.io/scrape":            "true",
						"glouton.allow_metrics":           "",
						"glouton.deny_metrics":            "",
						"glouton.include_default_metrics": "false",
					},
					ExtraLabels: map[string]string{
						types.LabelMetaContainerName:  "my_container",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample:9102",
					},
				},
			},
		},
		{
			name: "k8s-allow-metrics",
			containers: []facts.Container{
				facts.FakeContainer{
					FakeContainerName:  "k8s_containername_podname_namespace",
					FakePodName:        "my_pod-1234",
					FakePodNamespace:   "default",
					FakePrimaryAddress: "sample",
					FakeAnnotations: map[string]string{
						"prometheus.io/scrape":            "true",
						"glouton.allow_metrics":           "something,else",
						"glouton.include_default_metrics": "1",
					},
				},
			},
			want: []*scrapper.Target{
				{
					URL: mustParse("http://sample:9102/metrics"),
					ContainerLabels: map[string]string{
						"prometheus.io/scrape":            "true",
						"glouton.allow_metrics":           "something,else",
						"glouton.include_default_metrics": "1",
					},
					ExtraLabels: map[string]string{
						types.LabelK8SNamespace:       fakePodNamespace,
						types.LabelK8SPODName:         "my_pod-1234",
						types.LabelMetaScrapeJob:      fakeJobName,
						types.LabelMetaScrapeInstance: "sample:9102",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := DynamicScrapper{
				DynamicJobName: "jobname",
			}
			got := d.listExporters(tt.containers)

			if diff := cmp.Diff(tt.want, got, cmpopts.IgnoreUnexported(scrapper.Target{})); diff != "" {
				t.Errorf("ListExporters() != want: %v", diff)
			}
		})
	}
}
