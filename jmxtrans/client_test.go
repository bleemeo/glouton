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

//nolint:scopelint
package jmxtrans

import (
	"context"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
)

type fakeStore struct {
	Points []types.MetricPoint
}

func (s *fakeStore) EmitPoint(_ context.Context, point types.MetricPoint) {
	s.Points = append(s.Points, point)
}

type fakeConfig struct {
	Services map[string]discovery.Service
	Metrics  map[serviceKey][]config.JmxMetric
}

func (c fakeConfig) GetService(sha256Service string) (discovery.Service, bool) {
	r, ok := c.Services[sha256Service]

	return r, ok
}

func (c fakeConfig) GetMetrics(sha256Service string, sha256Bean string, attr string) ([]config.JmxMetric, bool) {
	key := serviceKey{
		sha256Service: sha256Service,
		sha256Bean:    sha256Bean,
		Attr:          attr,
	}
	result := c.Metrics[key]
	usedInRatio := false

	for _, divisorCandidate := range result {
		for _, metrics := range c.Metrics {
			for _, m := range metrics {
				if m.Ratio == divisorCandidate.Name {
					usedInRatio = true
				}
			}
		}
	}

	return result, usedInRatio
}

type serviceKey struct {
	sha256Service string
	sha256Bean    string
	Attr          string
}

func Test_jmxtransClient_processLine(t *testing.T) { //nolint:maintidx
	logger.SetLevel(2)

	tests := []struct {
		name   string
		config configInterface
		lines  []string
		want   []types.MetricPoint
	}{
		{
			name:   "empty",
			config: fakeConfig{},
		},
		{
			name: "simple",
			config: fakeConfig{
				Services: map[string]discovery.Service{
					"sha256-of-service": {
						Name: "cassandra",
					},
				},
				Metrics: map[serviceKey][]config.JmxMetric{
					{"sha256-of-service", "sha256-bean", "attr"}: {
						{
							Name: "metric_name",
						},
					},
				},
			},
			lines: []string{
				"jmxtrans.sha256-of-service.sha256-bean.attr 42.0 1585818816",
			},
			want: []types.MetricPoint{
				{
					Labels:      map[string]string{types.LabelName: "cassandra_metric_name"},
					Point:       types.Point{Time: time.Unix(1585818816, 0), Value: 42.0},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
			},
		},
		{
			name: "simple-item-container",
			config: fakeConfig{
				Services: map[string]discovery.Service{
					"sha256-of-service": {
						Name:          "cassandra",
						Instance:      "squirreldb-cassandra",
						ContainerName: "squirreldb-cassandra",
					},
				},
				Metrics: map[serviceKey][]config.JmxMetric{
					{"sha256-of-service", "sha256-bean", "attr"}: {
						{
							Name: "metric_name",
						},
					},
				},
			},
			lines: []string{
				"jmxtrans.sha256-of-service.sha256-bean.attr 42.0 1585818816",
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{types.LabelName: "cassandra_metric_name", types.LabelItem: "squirreldb-cassandra"},
					Point:  types.Point{Time: time.Unix(1585818816, 0), Value: 42.0},
					Annotations: types.MetricAnnotations{
						ServiceName:     "cassandra",
						ServiceInstance: "squirreldb-cassandra",
					},
				},
			},
		},
		{
			name: "simple-path",
			config: fakeConfig{
				Services: map[string]discovery.Service{
					"123": {
						Name: "cassandra",
					},
				},
				Metrics: map[serviceKey][]config.JmxMetric{
					{"123", "456", "HeapMemoryUsage_used"}: {
						{
							Name:      "jvm_heap_used",
							Attribute: "HeapMemoryUsage",
							Path:      "used",
						},
					},
				},
			},
			lines: []string{
				"jmxtrans.123.456.HeapMemoryUsage_used 83.2 1585818816",
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{types.LabelName: "cassandra_jvm_heap_used"},
					Point:  types.Point{Time: time.Unix(1585818816, 0), Value: 83.2},
					Annotations: types.MetricAnnotations{
						ServiceName: "cassandra",
					},
				},
			},
		},
		{
			name: "scale",
			config: fakeConfig{
				Services: map[string]discovery.Service{
					"dace7cb780b17dc43fb36cd64c776219": {
						Name: "cassandra",
					},
				},
				Metrics: map[serviceKey][]config.JmxMetric{
					{"dace7cb780b17dc43fb36cd64c776219", "77b03b685768c2c35418c060add42834", "Value"}: {
						{
							Name:  "bloom_filter_false_ratio",
							Scale: 100,
						},
					},
				},
			},
			lines: []string{
				"jmxtrans.dace7cb780b17dc43fb36cd64c776219.77b03b685768c2c35418c060add42834.Value 0.28109049740723024 1585819278",
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{types.LabelName: "cassandra_bloom_filter_false_ratio"},
					Point:  types.Point{Time: time.Unix(1585819278, 0), Value: 28.109049740723024},
					Annotations: types.MetricAnnotations{
						ServiceName: "cassandra",
					},
				},
			},
		},
		{
			name: "derive",
			config: fakeConfig{
				Services: map[string]discovery.Service{
					"123": {
						Name: "bitbucket",
					},
				},
				Metrics: map[serviceKey][]config.JmxMetric{
					{"123", "456", "Pulls"}: {
						{
							Name:   "pulls",
							Derive: true,
						},
					},
				},
			},
			lines: []string{
				"jmxtrans.123.456.Pulls 42 1585810000",
				"jmxtrans.123.456.Pulls 50 1585810010",
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{types.LabelName: "bitbucket_pulls"},
					Point:  types.Point{Time: time.Unix(1585810010, 0), Value: 0.8},
					Annotations: types.MetricAnnotations{
						ServiceName: "bitbucket",
					},
				},
			},
		},
		{
			name: "sum",
			config: fakeConfig{
				Services: map[string]discovery.Service{
					"123": {
						Name: "jvm",
					},
				},
				Metrics: map[serviceKey][]config.JmxMetric{
					{"123", "456", "CollectionCount"}: {
						{
							Name: "jvm_gc",
							Sum:  true,
						},
					},
				},
			},
			lines: []string{
				"jmxtrans.123.456.G1YoungGeneration.CollectionCount 185 1585828618",
				"jmxtrans.123.456.G1OldGeneration.CollectionCount 2 1585828618",
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{types.LabelName: "jvm_jvm_gc"},
					Point:  types.Point{Time: time.Unix(1585828618, 0), Value: 187},
					Annotations: types.MetricAnnotations{
						ServiceName: "jvm",
					},
				},
			},
		},
		{
			name: "sum-derive",
			config: fakeConfig{
				Services: map[string]discovery.Service{
					"123": {
						Name: "jvm",
					},
				},
				Metrics: map[serviceKey][]config.JmxMetric{
					{"123", "456", "CollectionCount"}: {
						{
							Name:   "jvm_gc",
							Sum:    true,
							Derive: true,
						},
					},
				},
			},
			lines: []string{
				"jmxtrans.123.456.G1YoungGeneration.CollectionCount 185 1585828000",
				"jmxtrans.123.456.G1OldGeneration.CollectionCount 2 1585828000",
				"jmxtrans.123.456.G1YoungGeneration.CollectionCount 190 1585828010",
				"jmxtrans.123.456.G1OldGeneration.CollectionCount 12 1585828010",
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{types.LabelName: "jvm_jvm_gc"},
					Point:  types.Point{Time: time.Unix(1585828010, 0), Value: 1.5},
					Annotations: types.MetricAnnotations{
						ServiceName: "jvm",
					},
				},
			},
		},
		{
			name: "derive-scale",
			config: fakeConfig{
				Services: map[string]discovery.Service{
					"123": {
						Name: "jvm",
					},
				},
				Metrics: map[serviceKey][]config.JmxMetric{
					{"123", "456", "Pulls"}: {
						{
							Name:   "jvm_gc",
							Scale:  10,
							Derive: true,
						},
					},
				},
			},
			lines: []string{
				"jmxtrans.123.456.Pulls 42 1585810000",
				"jmxtrans.123.456.Pulls 50 1585810010",
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{types.LabelName: "jvm_jvm_gc"},
					Point:  types.Point{Time: time.Unix(1585810010, 0), Value: 8},
					Annotations: types.MetricAnnotations{
						ServiceName: "jvm",
					},
				},
			},
		},
		{
			name: "ratio",
			config: fakeConfig{
				Services: map[string]discovery.Service{
					"123": {
						Name: "jira",
					},
				},
				Metrics: map[serviceKey][]config.JmxMetric{
					{"123", "456", "requestCount"}: {
						{
							Name: "requests",
						},
					},
					{"123", "456", "processingTime"}: {
						{
							Name:  "request_time",
							Ratio: "requests",
						},
					},
				},
			},
			lines: []string{
				"jmxtrans.123.456.index.requestCount 25 1585828000",
				"jmxtrans.123.456.create.requestCount 20 1585828000",
				"jmxtrans.123.456.index.processingTime 52.5 1585828000",
				"jmxtrans.123.456.create.processingTime 14.5 1585828000",
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{types.LabelName: "jira_requests", types.LabelItem: "index"},
					Point:  types.Point{Time: time.Unix(1585828000, 0), Value: 25},
					Annotations: types.MetricAnnotations{
						ServiceName: "jira",
					},
				},
				{
					Labels: map[string]string{types.LabelName: "jira_requests", types.LabelItem: "create"},
					Point:  types.Point{Time: time.Unix(1585828000, 0), Value: 20},
					Annotations: types.MetricAnnotations{
						ServiceName: "jira",
					},
				},
				{
					Labels: map[string]string{types.LabelName: "jira_request_time", types.LabelItem: "index"},
					Point:  types.Point{Time: time.Unix(1585828000, 0), Value: 2.1},
					Annotations: types.MetricAnnotations{
						ServiceName: "jira",
					},
				},
				{
					Labels: map[string]string{types.LabelName: "jira_request_time", types.LabelItem: "create"},
					Point:  types.Point{Time: time.Unix(1585828000, 0), Value: 0.725},
					Annotations: types.MetricAnnotations{
						ServiceName: "jira",
					},
				},
			},
		},
		{
			name: "sum-ratio",
			config: fakeConfig{
				Services: map[string]discovery.Service{
					"123": {
						Name: "jira",
					},
				},
				Metrics: map[serviceKey][]config.JmxMetric{
					{"123", "456", "requestCount"}: {
						{
							Name: "requests",
							Sum:  true,
						},
					},
					{"123", "456", "processingTime"}: {
						{
							Name:  "request_time",
							Ratio: "requests",
							Sum:   true,
						},
					},
				},
			},
			lines: []string{
				"jmxtrans.123.456.index.requestCount 25 1585828000",
				"jmxtrans.123.456.create.requestCount 20 1585828000",
				"jmxtrans.123.456.index.processingTime 52.5 1585828000",
				"jmxtrans.123.456.create.processingTime 14.5 1585828000",
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{types.LabelName: "jira_requests"},
					Point:  types.Point{Time: time.Unix(1585828000, 0), Value: 45},
					Annotations: types.MetricAnnotations{
						ServiceName: "jira",
					},
				},
				{
					Labels: map[string]string{types.LabelName: "jira_request_time"},
					Point:  types.Point{Time: time.Unix(1585828000, 0), Value: 1.488888888888889},
					Annotations: types.MetricAnnotations{
						ServiceName: "jira",
					},
				},
			},
		},
		{
			name: "derive-sum-ratio",
			config: fakeConfig{
				Services: map[string]discovery.Service{
					"123": {
						Name: "jira",
					},
				},
				Metrics: map[serviceKey][]config.JmxMetric{
					{"123", "456", "requestCount"}: {
						{
							Name:   "requests",
							Sum:    true,
							Derive: true,
						},
					},
					{"123", "456", "processingTime"}: {
						{
							Name:   "request_time",
							Ratio:  "requests",
							Sum:    true,
							Derive: true,
						},
					},
				},
			},
			lines: []string{
				"jmxtrans.123.456.index.requestCount 25 1585828000",
				"jmxtrans.123.456.create.requestCount 20 1585828000",
				"jmxtrans.123.456.index.processingTime 52.5 1585828000",
				"jmxtrans.123.456.create.processingTime 14.5 1585828000",
				"jmxtrans.123.456.index.requestCount 25 1585828015",
				"jmxtrans.123.456.create.requestCount 40 1585828015",
				"jmxtrans.123.456.index.processingTime 52.5 1585828015",
				"jmxtrans.123.456.create.processingTime 20.5 1585828015",
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{types.LabelName: "jira_requests"},
					Point:  types.Point{Time: time.Unix(1585828015, 0), Value: 1.3333333333333333},
					Annotations: types.MetricAnnotations{
						ServiceName: "jira",
					},
				},
				{
					Labels: map[string]string{types.LabelName: "jira_request_time"},
					Point:  types.Point{Time: time.Unix(1585828015, 0), Value: 0.30000000000000004},
					Annotations: types.MetricAnnotations{
						ServiceName: "jira",
					},
				},
			},
		},
		{
			name: "reused_metric",
			config: fakeConfig{
				Services: map[string]discovery.Service{
					"sha256-of-service": {
						Name: "cassandra",
					},
				},
				Metrics: map[serviceKey][]config.JmxMetric{
					{"sha256-of-service", "sha256-bean", "attr"}: {
						{
							Name: "unmodified",
						},
						{
							Name:  "scale",
							Scale: 2,
						},
						{
							Name:  "scale_once_more",
							Scale: 3,
						},
						{
							Name: "sum",
							Sum:  true,
						},
						{
							Name:   "derive",
							Derive: true,
						},
						{
							Name:   "derive-sum-scale",
							Derive: true,
							Sum:    true,
							Scale:  5,
						},
						{
							Name: "unmodified2",
						},
					},
				},
			},
			lines: []string{
				"jmxtrans.sha256-of-service.sha256-bean.One.attr 1.0 1585818816",
				"jmxtrans.sha256-of-service.sha256-bean.Two.attr 2.0 1585818816",
				"jmxtrans.sha256-of-service.sha256-bean.One.attr 1.5 1585818826",
				"jmxtrans.sha256-of-service.sha256-bean.Two.attr 2.8 1585818826",
			},
			want: []types.MetricPoint{
				// timestamp 1585818816
				{
					Labels:      map[string]string{types.LabelName: "cassandra_unmodified", types.LabelItem: "One"},
					Point:       types.Point{Time: time.Unix(1585818816, 0), Value: 1.0},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
				{
					Labels:      map[string]string{types.LabelName: "cassandra_unmodified", types.LabelItem: "Two"},
					Point:       types.Point{Time: time.Unix(1585818816, 0), Value: 2.0},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
				{
					Labels:      map[string]string{types.LabelName: "cassandra_unmodified2", types.LabelItem: "One"},
					Point:       types.Point{Time: time.Unix(1585818816, 0), Value: 1.0},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
				{
					Labels:      map[string]string{types.LabelName: "cassandra_unmodified2", types.LabelItem: "Two"},
					Point:       types.Point{Time: time.Unix(1585818816, 0), Value: 2.0},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
				{
					Labels:      map[string]string{types.LabelName: "cassandra_scale", types.LabelItem: "One"},
					Point:       types.Point{Time: time.Unix(1585818816, 0), Value: 2.0},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
				{
					Labels:      map[string]string{types.LabelName: "cassandra_scale", types.LabelItem: "Two"},
					Point:       types.Point{Time: time.Unix(1585818816, 0), Value: 4.0},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
				{
					Labels:      map[string]string{types.LabelName: "cassandra_scale_once_more", types.LabelItem: "One"},
					Point:       types.Point{Time: time.Unix(1585818816, 0), Value: 3.0},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
				{
					Labels:      map[string]string{types.LabelName: "cassandra_scale_once_more", types.LabelItem: "Two"},
					Point:       types.Point{Time: time.Unix(1585818816, 0), Value: 6.0},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
				{
					Labels:      map[string]string{types.LabelName: "cassandra_sum"},
					Point:       types.Point{Time: time.Unix(1585818816, 0), Value: 3.0},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},

				// timestamp 1585818826
				{
					Labels:      map[string]string{types.LabelName: "cassandra_unmodified", types.LabelItem: "One"},
					Point:       types.Point{Time: time.Unix(1585818826, 0), Value: 1.5},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
				{
					Labels:      map[string]string{types.LabelName: "cassandra_unmodified", types.LabelItem: "Two"},
					Point:       types.Point{Time: time.Unix(1585818826, 0), Value: 2.8},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
				{
					Labels:      map[string]string{types.LabelName: "cassandra_unmodified2", types.LabelItem: "One"},
					Point:       types.Point{Time: time.Unix(1585818826, 0), Value: 1.5},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
				{
					Labels:      map[string]string{types.LabelName: "cassandra_unmodified2", types.LabelItem: "Two"},
					Point:       types.Point{Time: time.Unix(1585818826, 0), Value: 2.8},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
				{
					Labels:      map[string]string{types.LabelName: "cassandra_scale", types.LabelItem: "One"},
					Point:       types.Point{Time: time.Unix(1585818826, 0), Value: 3.0},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
				{
					Labels:      map[string]string{types.LabelName: "cassandra_scale", types.LabelItem: "Two"},
					Point:       types.Point{Time: time.Unix(1585818826, 0), Value: 5.6},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
				{
					Labels:      map[string]string{types.LabelName: "cassandra_scale_once_more", types.LabelItem: "One"},
					Point:       types.Point{Time: time.Unix(1585818826, 0), Value: 4.5},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
				{
					Labels:      map[string]string{types.LabelName: "cassandra_scale_once_more", types.LabelItem: "Two"},
					Point:       types.Point{Time: time.Unix(1585818826, 0), Value: 8.4},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
				{
					Labels:      map[string]string{types.LabelName: "cassandra_sum"},
					Point:       types.Point{Time: time.Unix(1585818826, 0), Value: 4.3},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},

				// derivated (timestamp 1585818826)
				{
					Labels:      map[string]string{types.LabelName: "cassandra_derive", types.LabelItem: "One"},
					Point:       types.Point{Time: time.Unix(1585818826, 0), Value: 0.05},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
				{
					Labels:      map[string]string{types.LabelName: "cassandra_derive", types.LabelItem: "Two"},
					Point:       types.Point{Time: time.Unix(1585818826, 0), Value: 0.08},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
				{
					Labels:      map[string]string{types.LabelName: "cassandra_derive-sum-scale"},
					Point:       types.Point{Time: time.Unix(1585818826, 0), Value: 0.65},
					Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
				},
			},
		},
		{
			name: "jvm_gc_time",
			config: fakeConfig{
				Services: map[string]discovery.Service{
					"123": {
						Name: "cassandra",
					},
				},
				Metrics: map[serviceKey][]config.JmxMetric{
					{"123", "456", "CollectionTime"}: {
						{
							Name:      "jvm_gc_time",
							Attribute: "CollectionTime",
							Derive:    true,
							Sum:       true,
							TypeNames: []string{"name"},
						},
						{
							Name:      "jvm_gc_utilization",
							Attribute: "CollectionTime",
							Derive:    true,
							Sum:       true,
							TypeNames: []string{"name"},
							Scale:     0.1, // time is in ms/s. Convert in %
						},
					},
				},
			},
			lines: []string{
				"jmxtrans.123.456.G1YoungGeneration.CollectionTime 185 1585828000",
				"jmxtrans.123.456.G1OldGeneration.CollectionTime 2 1585828000",
				"jmxtrans.123.456.G1YoungGeneration.CollectionTime 190 1585828010",
				"jmxtrans.123.456.G1OldGeneration.CollectionTime 12 1585828010",
				"jmxtrans.123.456.G1YoungGeneration.CollectionTime 190 1585828020",
				"jmxtrans.123.456.G1OldGeneration.CollectionTime 12 1585828020",
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{types.LabelName: "cassandra_jvm_gc_time"},
					Point:  types.Point{Time: time.Unix(1585828010, 0), Value: 1.5},
					Annotations: types.MetricAnnotations{
						ServiceName: "cassandra",
					},
				},
				{
					Labels: map[string]string{types.LabelName: "cassandra_jvm_gc_utilization"},
					Point:  types.Point{Time: time.Unix(1585828010, 0), Value: 0.15},
					Annotations: types.MetricAnnotations{
						ServiceName: "cassandra",
					},
				},
				{
					Labels: map[string]string{types.LabelName: "cassandra_jvm_gc_time"},
					Point:  types.Point{Time: time.Unix(1585828020, 0), Value: 0},
					Annotations: types.MetricAnnotations{
						ServiceName: "cassandra",
					},
				},
				{
					Labels: map[string]string{types.LabelName: "cassandra_jvm_gc_utilization"},
					Point:  types.Point{Time: time.Unix(1585828020, 0), Value: 0},
					Annotations: types.MetricAnnotations{
						ServiceName: "cassandra",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := fakeStore{}
			c := &jmxtransClient{
				Config:    tt.config,
				EmitPoint: store.EmitPoint,
			}

			c.init()

			for _, line := range tt.lines {
				c.processLine(t.Context(), line)
			}

			c.flush(t.Context())

			if diff := types.DiffMetricPoints(tt.want, store.Points, true); diff != "" {
				t.Errorf("points mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
