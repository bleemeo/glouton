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

//nolint:scopelint
package jmxtrans

import (
	"context"
	"fmt"
	"glouton/config"
	"glouton/discovery"
	"glouton/logger"
	"glouton/types"
	"reflect"
	"testing"
	"time"
)

type fakeStore struct {
	Points []types.MetricPoint
	ByName map[string]types.Point
}

func (s *fakeStore) EmitPoint(_ context.Context, point types.MetricPoint) {
	if s.ByName == nil {
		s.ByName = make(map[string]types.Point)
	}

	s.Points = append(s.Points, point)
	s.ByName[fmt.Sprintf("%s:%s", point.Labels["__name__"], point.Annotations.BleemeoItem)] = point.Point
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
		want   map[string]types.Point
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
			want: map[string]types.Point{
				"cassandra_metric_name:": {Time: time.Unix(1585818816, 0), Value: 42.0},
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
			want: map[string]types.Point{
				"cassandra_metric_name:squirreldb-cassandra": {Time: time.Unix(1585818816, 0), Value: 42.0},
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
			want: map[string]types.Point{
				"cassandra_jvm_heap_used:": {Time: time.Unix(1585818816, 0), Value: 83.2},
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
			want: map[string]types.Point{
				"cassandra_bloom_filter_false_ratio:": {Time: time.Unix(1585819278, 0), Value: 28.109049740723024},
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
			want: map[string]types.Point{
				"bitbucket_pulls:": {Time: time.Unix(1585810010, 0), Value: 0.8},
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
			want: map[string]types.Point{
				"jvm_jvm_gc:": {Time: time.Unix(1585828618, 0), Value: 187},
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
			want: map[string]types.Point{
				"jvm_jvm_gc:": {Time: time.Unix(1585828010, 0), Value: 1.5},
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
			want: map[string]types.Point{
				"jira_requests:index":      {Time: time.Unix(1585828000, 0), Value: 25},
				"jira_requests:create":     {Time: time.Unix(1585828000, 0), Value: 20},
				"jira_request_time:index":  {Time: time.Unix(1585828000, 0), Value: 2.1},
				"jira_request_time:create": {Time: time.Unix(1585828000, 0), Value: 0.725},
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
			want: map[string]types.Point{
				"jira_requests:":     {Time: time.Unix(1585828000, 0), Value: 45},
				"jira_request_time:": {Time: time.Unix(1585828000, 0), Value: 1.488888888888889},
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
			want: map[string]types.Point{
				"jira_requests:":     {Time: time.Unix(1585828015, 0), Value: 1.3333333333333333},
				"jira_request_time:": {Time: time.Unix(1585828015, 0), Value: 0.30000000000000004},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := fakeStore{}
			c := &jmxtransClient{
				Config:    tt.config,
				EmitPoint: store.EmitPoint,
			}

			c.init()

			for _, line := range tt.lines {
				c.processLine(context.Background(), line)
			}

			c.flush(context.Background())

			if !reflect.DeepEqual(store.ByName, tt.want) {
				t.Errorf("store contains = %v, want %v", store.ByName, tt.want)
			}
		})
	}
}
