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

package syncmetrics

import (
	"reflect"
	"testing"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	gloutonTypes "github.com/bleemeo/glouton/types"
)

func TestPrioritizeAndFilterMetrics(t *testing.T) {
	inputNames := []struct {
		Name         string
		HighPriority bool
	}{
		{MetricCPUUsed, true},
		{"service_status", false},
		{"io_utilization", true},
		{"nginx_requests", false},
		{"mem_used", true},
		{"mem_used_perc", true},
	}
	isHighPriority := make(map[string]bool)
	countHighPriority := 0
	metrics := make([]gloutonTypes.Metric, len(inputNames))
	metrics2 := make([]gloutonTypes.Metric, len(inputNames))

	for i, n := range inputNames {
		metrics[i] = fakeMetric{label: n.Name}
		metrics2[i] = fakeMetric{label: n.Name}

		if n.HighPriority {
			countHighPriority++

			isHighPriority[n.Name] = true
		}
	}

	metrics = prioritizeAndFilterMetrics(metrics, false)
	metrics2 = prioritizeAndFilterMetrics(metrics2, true)

	for i, m := range metrics {
		if !isHighPriority[m.Labels()[gloutonTypes.LabelName]] && i < countHighPriority {
			t.Errorf("Found metrics %#v at index %d, want after %d", m.Labels()[gloutonTypes.LabelName], i, countHighPriority)
		}

		if isHighPriority[m.Labels()[gloutonTypes.LabelName]] && i >= countHighPriority {
			t.Errorf("Found metrics %#v at index %d, want before %d", m.Labels()[gloutonTypes.LabelName], i, countHighPriority)
		}
	}

	for i, m := range metrics2 {
		if !isHighPriority[m.Labels()[gloutonTypes.LabelName]] {
			t.Errorf("Found metrics %#v at index %d, but it's not prioritary", m.Labels()[gloutonTypes.LabelName], i)
		}
	}
}

func TestPrioritizeAndFilterMetrics2(t *testing.T) {
	type order struct {
		LabelBefore string
		LabelAfter  string
	}

	cases := []struct {
		name   string
		inputs []string
		order  []order
	}{
		{
			name: "without item are sorted first",
			inputs: []string{
				`__name__="cpu_used"`,
				`__name__="net_bits_recv",item="eth0"`,
				`__name__="net_bits_sent",item="eth0"`,
				`__name__="mem_used"`,
			},
			order: []order{
				{LabelBefore: `__name__="cpu_used"`, LabelAfter: `__name__="net_bits_recv",item="eth0"`},
				{LabelBefore: `__name__="cpu_used"`, LabelAfter: `__name__="net_bits_sent",item="eth0"`},
				{LabelBefore: `__name__="mem_used"`, LabelAfter: `__name__="net_bits_recv",item="eth0"`},
				{LabelBefore: `__name__="mem_used"`, LabelAfter: `__name__="net_bits_sent",item="eth0"`},
			},
		},
		{
			name: "network and fs are after",
			inputs: []string{
				`__name__="io_reads",item="the_item"`,
				`__name__="net_bits_recv",item="the_item"`,
				`__name__="io_writes",item="the_item"`,
				`__name__="cpu_used"`,
				`__name__="disk_used_perc",item="the_item"`,
				`__name__="io_utilization",item="the_item"`,
			},
			order: []order{
				{LabelBefore: `__name__="io_reads",item="the_item"`, LabelAfter: `__name__="net_bits_recv",item="the_item"`},
				{LabelBefore: `__name__="io_writes",item="the_item"`, LabelAfter: `__name__="net_bits_recv",item="the_item"`},
				{LabelBefore: `__name__="io_utilization",item="the_item"`, LabelAfter: `__name__="net_bits_recv",item="the_item"`},
				{LabelBefore: `__name__="cpu_used"`, LabelAfter: `__name__="net_bits_recv",item="the_item"`},
				{LabelBefore: `__name__="io_reads",item="the_item"`, LabelAfter: `__name__="disk_used_perc",item="the_item"`},
				{LabelBefore: `__name__="io_writes",item="the_item"`, LabelAfter: `__name__="disk_used_perc",item="the_item"`},
				{LabelBefore: `__name__="io_utilization",item="the_item"`, LabelAfter: `__name__="disk_used_perc",item="the_item"`},
				{LabelBefore: `__name__="cpu_used"`, LabelAfter: `__name__="disk_used_perc",item="the_item"`},
			},
		},
		{
			name: "custom are always after",
			inputs: []string{
				`__name__="custom_metric"`,
				`__name__="net_bits_recv",item="eth0"`,
				`__name__="net_bits_recv",item="the_item"`,
				`__name__="cpu_used"`,
				`__name__="custom_metric_item",item="the_item"`,
				`__name__="io_reads",item="the_item"`,
				`__name__="custom_metric2"`,
			},
			order: []order{
				{LabelBefore: `__name__="cpu_used"`, LabelAfter: `__name__="custom_metric_item",item="the_item"`},
				{LabelBefore: `__name__="cpu_used"`, LabelAfter: `__name__="custom_metric2"`},
				{LabelBefore: `__name__="cpu_used"`, LabelAfter: `__name__="custom_metric"`},
				{LabelBefore: `__name__="net_bits_recv",item="eth0"`, LabelAfter: `__name__="custom_metric_item",item="the_item"`},
				{LabelBefore: `__name__="net_bits_recv",item="eth0"`, LabelAfter: `__name__="custom_metric2"`},
				{LabelBefore: `__name__="net_bits_recv",item="eth0"`, LabelAfter: `__name__="custom_metric"`},
				{LabelBefore: `__name__="net_bits_recv",item="the_item"`, LabelAfter: `__name__="custom_metric_item",item="the_item"`},
				{LabelBefore: `__name__="net_bits_recv",item="the_item"`, LabelAfter: `__name__="custom_metric2"`},
				{LabelBefore: `__name__="net_bits_recv",item="the_item"`, LabelAfter: `__name__="custom_metric"`},
				{LabelBefore: `__name__="io_reads",item="the_item"`, LabelAfter: `__name__="custom_metric_item",item="the_item"`},
				{LabelBefore: `__name__="io_reads",item="the_item"`, LabelAfter: `__name__="custom_metric2"`},
				{LabelBefore: `__name__="io_reads",item="the_item"`, LabelAfter: `__name__="custom_metric"`},
			},
		},
		{
			name: "network item are sorted",
			inputs: []string{
				`__name__="net_bits_sent",item="eno1"`,
				`__name__="net_bits_recv",item="the_item"`,
				`__name__="net_bits_recv",item="eth0"`,
				`__name__="net_bits_recv",item="br-1234"`,
				`__name__="net_bits_recv",item="eno1"`,
				`__name__="net_bits_sent",item="eth0"`,
				`__name__="net_bits_recv",item="eth1"`,
				`__name__="net_bits_recv",item="br-1"`,
				`__name__="net_bits_recv",item="br-0"`,
			},
			order: []order{
				{LabelBefore: `__name__="net_bits_recv",item="eth0"`, LabelAfter: `__name__="net_bits_recv",item="the_item"`},
				{LabelBefore: `__name__="net_bits_recv",item="eth0"`, LabelAfter: `__name__="net_bits_recv",item="br-0"`},
				{LabelBefore: `__name__="net_bits_recv",item="eth0"`, LabelAfter: `__name__="net_bits_recv",item="br-1234"`},
				{LabelBefore: `__name__="net_bits_recv",item="eth0"`, LabelAfter: `__name__="net_bits_recv",item="eth1"`},
				{LabelBefore: `__name__="net_bits_sent",item="eth0"`, LabelAfter: `__name__="net_bits_recv",item="the_item"`},
				{LabelBefore: `__name__="net_bits_sent",item="eth0"`, LabelAfter: `__name__="net_bits_recv",item="br-0"`},
				{LabelBefore: `__name__="net_bits_sent",item="eth0"`, LabelAfter: `__name__="net_bits_recv",item="br-1234"`},
				{LabelBefore: `__name__="net_bits_sent",item="eth0"`, LabelAfter: `__name__="net_bits_recv",item="eth1"`},
				{LabelBefore: `__name__="net_bits_recv",item="eth1"`, LabelAfter: `__name__="net_bits_recv",item="the_item"`},
				{LabelBefore: `__name__="net_bits_recv",item="eth1"`, LabelAfter: `__name__="net_bits_recv",item="br-0"`},
				{LabelBefore: `__name__="net_bits_recv",item="eth1"`, LabelAfter: `__name__="net_bits_recv",item="br-1234"`},
				{LabelBefore: `__name__="net_bits_recv",item="eno1"`, LabelAfter: `__name__="net_bits_recv",item="the_item"`},
				{LabelBefore: `__name__="net_bits_recv",item="eno1"`, LabelAfter: `__name__="net_bits_recv",item="br-0"`},
				{LabelBefore: `__name__="net_bits_recv",item="eno1"`, LabelAfter: `__name__="net_bits_recv",item="br-1234"`},
				{LabelBefore: `__name__="net_bits_sent",item="eno1"`, LabelAfter: `__name__="net_bits_recv",item="the_item"`},
				{LabelBefore: `__name__="net_bits_sent",item="eno1"`, LabelAfter: `__name__="net_bits_recv",item="br-0"`},
				{LabelBefore: `__name__="net_bits_sent",item="eno1"`, LabelAfter: `__name__="net_bits_recv",item="br-1234"`},
				{LabelBefore: `__name__="net_bits_recv",item="br-0"`, LabelAfter: `__name__="net_bits_recv",item="br-1"`},
			},
		},
		{
			name: "container are sorted",
			inputs: []string{
				`__name__="container_net_bits_sent",item="my_redis"`,
				`__name__="container_mem_used",item="my_memcached"`,
				`__name__="container_cpu_used",item="my_rabbitmq"`,
				`__name__="container_cpu_used",item="my_redis"`,
				`__name__="container_cpu_used",item="my_memcached"`,
				`__name__="container_mem_used",item="my_redis"`,
				`__name__="container_mem_used",item="my_rabbitmq"`,
				`__name__="container_net_bits_sent",item="my_memcached"`,
				`__name__="container_net_bits_sent",item="my_rabbitmq"`,
			},
			order: []order{
				// What we only want is the item are together. Currently item are sorted in lexical order
				{LabelBefore: `__name__="container_net_bits_sent",item="my_memcached"`, LabelAfter: `__name__="container_net_bits_sent",item="my_redis"`},
				{LabelBefore: `__name__="container_net_bits_sent",item="my_memcached"`, LabelAfter: `__name__="container_net_bits_sent",item="my_rabbitmq"`},
				{LabelBefore: `__name__="container_net_bits_sent",item="my_memcached"`, LabelAfter: `__name__="container_mem_used",item="my_redis"`},
				{LabelBefore: `__name__="container_net_bits_sent",item="my_memcached"`, LabelAfter: `__name__="container_mem_used",item="my_rabbitmq"`},
				{LabelBefore: `__name__="container_net_bits_sent",item="my_memcached"`, LabelAfter: `__name__="container_cpu_used",item="my_redis"`},
				{LabelBefore: `__name__="container_net_bits_sent",item="my_memcached"`, LabelAfter: `__name__="container_cpu_used",item="my_rabbitmq"`},

				{LabelBefore: `__name__="container_cpu_used",item="my_memcached"`, LabelAfter: `__name__="container_net_bits_sent",item="my_redis"`},
				{LabelBefore: `__name__="container_cpu_used",item="my_memcached"`, LabelAfter: `__name__="container_net_bits_sent",item="my_rabbitmq"`},
				{LabelBefore: `__name__="container_cpu_used",item="my_memcached"`, LabelAfter: `__name__="container_mem_used",item="my_redis"`},
				{LabelBefore: `__name__="container_cpu_used",item="my_memcached"`, LabelAfter: `__name__="container_mem_used",item="my_rabbitmq"`},
				{LabelBefore: `__name__="container_cpu_used",item="my_memcached"`, LabelAfter: `__name__="container_cpu_used",item="my_redis"`},
				{LabelBefore: `__name__="container_cpu_used",item="my_memcached"`, LabelAfter: `__name__="container_cpu_used",item="my_rabbitmq"`},

				{LabelBefore: `__name__="container_mem_used",item="my_memcached"`, LabelAfter: `__name__="container_net_bits_sent",item="my_redis"`},
				{LabelBefore: `__name__="container_mem_used",item="my_memcached"`, LabelAfter: `__name__="container_net_bits_sent",item="my_rabbitmq"`},
				{LabelBefore: `__name__="container_mem_used",item="my_memcached"`, LabelAfter: `__name__="container_mem_used",item="my_redis"`},
				{LabelBefore: `__name__="container_mem_used",item="my_memcached"`, LabelAfter: `__name__="container_mem_used",item="my_rabbitmq"`},
				{LabelBefore: `__name__="container_mem_used",item="my_memcached"`, LabelAfter: `__name__="container_cpu_used",item="my_redis"`},
				{LabelBefore: `__name__="container_mem_used",item="my_memcached"`, LabelAfter: `__name__="container_cpu_used",item="my_rabbitmq"`},
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			metrics := make([]gloutonTypes.Metric, 0, len(tt.inputs))

			for _, lbls := range tt.inputs {
				metrics = append(metrics, labelsMetric{lbls: gloutonTypes.TextToLabels(lbls)})
			}

			result := prioritizeAndFilterMetrics(metrics, false)

			for _, ord := range tt.order {
				firstIdx := -1
				secondIdx := -1

				for i, m := range result {
					if reflect.DeepEqual(m.Labels(), gloutonTypes.TextToLabels(ord.LabelBefore)) {
						if firstIdx == -1 {
							firstIdx = i
						} else {
							t.Errorf("metric labels %s present at index %d and %d", ord.LabelBefore, firstIdx, i)
						}
					}

					if reflect.DeepEqual(m.Labels(), gloutonTypes.TextToLabels(ord.LabelAfter)) {
						if secondIdx == -1 {
							secondIdx = i
						} else {
							t.Errorf("metric labels %s present at index %d and %d", ord.LabelAfter, secondIdx, i)
						}
					}
				}

				switch {
				case firstIdx == -1:
					t.Errorf("metric %s is not present", ord.LabelBefore)
				case secondIdx == -1:
					t.Errorf("metric %s is not present", ord.LabelAfter)
				case firstIdx >= secondIdx:
					t.Errorf("metric %s is after metric %s (%d >= %d)", ord.LabelBefore, ord.LabelAfter, firstIdx, secondIdx)
				}
			}
		})
	}
}

func Test_metricComparator_IsSignificantItem(t *testing.T) {
	tests := []struct {
		item string
		want bool
	}{
		{
			item: "/",
			want: true,
		},
		{
			item: "/home",
			want: true,
		},
		{
			item: "/home",
			want: true,
		},
		{
			item: "/srv",
			want: true,
		},
		{
			item: "/var",
			want: true,
		},
		{
			item: "eth0",
			want: true,
		},
		{
			item: "eth1",
			want: true,
		},
		{
			item: "ens18",
			want: true,
		},
		{
			item: "ens1",
			want: true,
		},
		{
			item: "eno1",
			want: true,
		},
		{
			item: "eno5",
			want: true,
		},
		{
			item: "enp7s0",
			want: true,
		},
		{
			item: "ens1f1",
			want: true,
		},

		{
			item: "/home/user",
			want: false,
		},
		{
			item: "enp7s0.4010",
			want: false,
		},
		{
			item: "eth99",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.item, func(t *testing.T) {
			t.Parallel()

			m := newComparator()
			if got := m.IsSignificantItem(tt.item); got != tt.want {
				t.Errorf("metricComparator.IsSignificantItem() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_metricComparator_SkipInOnlyEssential(t *testing.T) {
	tests := []struct {
		name                string
		metric              string
		keepInOnlyEssential bool
	}{
		{
			name:                "default dashboard metrics are essential 1",
			metric:              `__name__="cpu_used"`,
			keepInOnlyEssential: true,
		},
		{
			name:                "default dashboard metrics are essential 2",
			metric:              `__name__="io_reads",item="nvme0"`,
			keepInOnlyEssential: true,
		},
		{
			name:                "default dashboard metrics are essential 3",
			metric:              `__name__="net_bits_recv",item="eth0"`,
			keepInOnlyEssential: true,
		},
		{
			name:                "high cardinality aren't essential",
			metric:              `__name__="net_bits_recv",item="the_item"`,
			keepInOnlyEssential: false,
		},
		{
			name:                "high cardinality aren't essential",
			metric:              `__name__="net_bits_recv",item="br-2a4d1a465acd"`,
			keepInOnlyEssential: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newComparator()
			metric := gloutonTypes.TextToLabels(tt.metric)

			if got := m.KeepInOnlyEssential(metric); got != tt.keepInOnlyEssential {
				t.Errorf("metricComparator.KeepInOnlyEssential() = %v, want %v", got, tt.keepInOnlyEssential)
			}
		})
	}
}

func Test_metricComparator_importanceWeight(t *testing.T) {
	tests := []struct {
		name         string
		metricBefore string
		metricAfter  string
	}{
		{
			name:         "metric of system dashboard are first 1",
			metricBefore: `__name__="cpu_used"`,
			metricAfter:  `__name__="custom_metric"`,
		},
		{
			name:         "metric of system dashboard are first 2",
			metricBefore: `__name__="net_bits_recv",item="eth0"`,
			metricAfter:  `__name__="custom_metric"`,
		},
		{
			name:         "metric of system dashboard are first 3",
			metricBefore: `__name__="net_bits_recv",item="the_item"`,
			metricAfter:  `__name__="custom_metric"`,
		},
		{
			name:         "metric of system dashboard are first 4",
			metricBefore: `__name__="io_reads",item="nvme0"`,
			metricAfter:  `__name__="custom_metric"`,
		},
		{
			name:         "high cardinality after important",
			metricBefore: `__name__="system_pending_security_updates"`,
			metricAfter:  `__name__="disk_used_perc",item="/random-value"`,
		},
		{
			name:         "good item before important",
			metricBefore: `__name__="disk_used_perc",item="/home"`,
			metricAfter:  `__name__="system_pending_security_updates"`,
		},
		{
			name:         "high cardinality after status",
			metricBefore: `__name__="service_status"`,
			metricAfter:  `__name__="net_bits_recv",item="tap150"`,
		},
		{
			name:         "good item before status",
			metricBefore: `__name__="net_bits_recv",item="eth0"`,
			metricAfter:  `__name__="service_status"`,
		},
		{
			name:         "high cardinality before custom",
			metricBefore: `__name__="net_bits_recv",item="tap150"`,
			metricAfter:  `__name__="custom_metric"`,
		},
		{
			name:         "essential without item first",
			metricBefore: `__name__="cpu_used"`,
			metricAfter:  `__name__="cpu_used",item="value"`,
		},
		{
			name:         "essential without item first 2",
			metricBefore: `__name__="cpu_used"`,
			metricAfter:  `__name__="io_reads",item="/dev/sda"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newComparator()

			metricA := gloutonTypes.TextToLabels(tt.metricBefore)
			metricB := gloutonTypes.TextToLabels(tt.metricAfter)

			weightA := m.importanceWeight(metricA)
			weightB := m.importanceWeight(metricB)

			if weightA >= weightB {
				t.Errorf("weightA = %d, want less than %d", weightA, weightB)
			}
		})
	}
}

func Test_httpResponseToMetricFailureKind(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bleemeoTypes.FailureKind
	}{
		{
			name:    "random content",
			content: "any random content",
			want:    bleemeoTypes.FailureUnknown,
		},
		{
			name:    "not whitelisted",
			content: `{"label":["This metric is not whitelisted for this agent"]}`,
			want:    bleemeoTypes.FailureAllowList,
		},
		{
			name:    "not allowed",
			content: `{"label":["This metric is not in allow-list for this agent"]}`,
			want:    bleemeoTypes.FailureAllowList,
		},
		{
			name:    "too many custom metrics",
			content: `{"label":["Too many non standard metrics"]}`,
			want:    bleemeoTypes.FailureTooManyCustomMetrics,
		},
		{
			name:    "too many metrics",
			content: `{"label":["Too many metrics"]}`,
			want:    bleemeoTypes.FailureTooManyStandardMetrics,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := httpResponseToMetricFailureKind(tt.content); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("httpResponseToMetricFailureKind() = %v, want %v", got, tt.want)
			}
		})
	}
}
