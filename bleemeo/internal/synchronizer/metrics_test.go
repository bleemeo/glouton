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

//nolint:goconst
package synchronizer

import (
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/syncservices"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/prometheus/exporter/snmp"
	"github.com/bleemeo/glouton/prometheus/model"
	gloutonTypes "github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/prometheus/model/labels"
)

func sortList(list []bleemeoTypes.Metric) []bleemeoTypes.Metric {
	newList := make([]bleemeoTypes.Metric, 0, len(list))
	orderedNames := make([]string, 0, len(list))

	for _, val := range list {
		orderedNames = append(orderedNames, val.LabelsText)
	}

	sort.Strings(orderedNames)

	for _, name := range orderedNames {
		for _, val := range list {
			if val.LabelsText == name {
				newList = append(newList, val)

				break
			}
		}
	}

	return newList
}

func TestPrioritizeAndFilterMetrics(t *testing.T) {
	inputNames := []struct {
		Name         string
		HighPriority bool
	}{
		{"cpu_used", true},
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
		metrics[i] = mockMetric{Name: n.Name}
		metrics2[i] = mockMetric{Name: n.Name}

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
				metrics = append(metrics, mockMetric{labels: gloutonTypes.TextToLabels(lbls)})
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

// TestMetricSimpleSync test "normal" scenario:
// Agent start and register metrics
// Some metrics disappear => mark inactive
// Some re-appear and some new => mark active & register.
func TestMetricSimpleSync(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)
	helper.initSynchronizer(t)
	helper.AddTime(time.Minute)

	if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	// list metrics and register agent_status
	helper.wrapperClientMock.AssertCallsPerResource(t, mockAPIResourceMetric, 2)

	idAgentMain, _ := helper.state.BleemeoCredentials()

	metrics := helper.MetricsFromAPI()
	want := []bleemeoapi.MetricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:         "1",
				AgentID:    helper.s.agentID,
				LabelsText: "",
			},
			Name: agentStatusName,
		},
	}

	if diff := cmp.Diff(want, metrics); diff != "" {
		t.Errorf("metrics mismatch (-want +got):\n%s", diff)
	}

	helper.AddTime(5 * time.Minute)
	helper.pushPoints(t, []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "cpu_system"},
			labels.Label{Name: gloutonTypes.LabelInstanceUUID, Value: idAgentMain},
		),
	})

	if err := helper.runOnceWithResult(t).CheckMethodWithoutFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	// We do 3 requests: list metrics, list inactive metrics and register new metric.
	helper.wrapperClientMock.AssertCallsPerResource(t, mockAPIResourceMetric, 3)

	metrics = helper.MetricsFromAPI()
	want = []bleemeoapi.MetricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:         "1",
				AgentID:    idAgentMain,
				LabelsText: "",
			},
			Name: agentStatusName,
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:         "2",
				AgentID:    idAgentMain,
				LabelsText: "",
			},
			Name: "cpu_system",
		},
	}

	if diff := cmp.Diff(want, metrics); diff != "" {
		t.Errorf("metrics mismatch (-want +got):\n%s", diff)
	}

	helper.AddTime(5 * time.Minute)

	// Register 1000 metrics
	for n := range 1000 {
		helper.pushPoints(t, []labels.Labels{
			labels.New(
				labels.Label{Name: gloutonTypes.LabelName, Value: "metric"},
				labels.Label{Name: gloutonTypes.LabelItem, Value: strconv.FormatInt(int64(n), 10)},
				labels.Label{Name: gloutonTypes.LabelInstanceUUID, Value: idAgentMain},
			),
		})
	}

	if err := helper.runOnceWithResult(t).CheckMethodWithoutFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	// We do 1003 request: 3 for listing and 1000 registration
	helper.wrapperClientMock.AssertCallsPerResource(t, mockAPIResourceMetric, 1003)

	metrics = helper.MetricsFromAPI()
	if len(metrics) != 1002 {
		t.Errorf("len(metrics) = %v, want %v", len(metrics), 1002)
	}

	helper.AddTime(5 * time.Minute)
	helper.store.DropAllMetrics()

	if err := helper.runOnceWithResult(t).CheckMethodWithoutFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	// We do 1001 request: 1001 to mark inactive all metrics but agent_status
	helper.wrapperClientMock.AssertCallsPerResource(t, mockAPIResourceMetric, 1001)

	metrics = helper.MetricsFromAPI()
	if len(metrics) != 1002 {
		t.Errorf("len(metrics) = %v, want %v", len(metrics), 1002)
	}

	for _, m := range metrics {
		if m.DeactivatedAt.IsZero() && m.Name != agentStatusName {
			t.Errorf("%v should be deactivated", m)

			break
		} else if !m.DeactivatedAt.IsZero() && m.Name == agentStatusName {
			t.Errorf("%v should not be deactivated", m)
		}
	}

	helper.AddTime(5 * time.Minute)

	// re-activate one metric + register one
	helper.pushPoints(t, []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "cpu_system"},
			labels.Label{Name: gloutonTypes.LabelInstanceUUID, Value: idAgentMain},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "disk_used"},
			labels.Label{Name: gloutonTypes.LabelItem, Value: "/home"},
			labels.Label{Name: gloutonTypes.LabelInstanceUUID, Value: idAgentMain},
		),
	})

	if err := helper.runOnceWithResult(t).CheckMethodWithoutFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	// We do 3 request: 1 to re-enable metric,
	// 1 search for metric before registration, 1 to register metric
	helper.wrapperClientMock.AssertCallsPerResource(t, mockAPIResourceMetric, 3)

	metrics = helper.MetricsFromAPI()
	if len(metrics) != 1003 {
		t.Errorf("len(metrics) = %v, want %v", len(metrics), 1002)
	}

	for _, m := range metrics {
		if m.Name == agentStatusName || m.Name == "cpu_system" || m.Name == "disk_used" {
			if !m.DeactivatedAt.IsZero() {
				t.Errorf("%v should be active", m)
			}
		} else if m.DeactivatedAt.IsZero() {
			t.Errorf("%v should be deactivated", m)

			break
		}

		if m.Name == "disk_used" {
			if m.Item != "/home" {
				t.Errorf("%v miss item=/home", m)
			}
		}
	}
}

// TestMetricDeleted test that Glouton can update metrics deleted on Bleemeo.
func TestMetricDeleted(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)
	helper.initSynchronizer(t)
	helper.AddTime(time.Minute)

	helper.pushPoints(t, []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric1"},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric2"},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric3"},
		),
	})

	if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	// list of active metrics,
	// 2 query per metric to register (one to find potential inactive, one to register)
	// + 1 to register agent_status
	helper.wrapperClientMock.AssertCallsPerResource(t, mockAPIResourceMetric, 8)

	metrics := helper.MetricsFromAPI()
	if len(metrics) != 4 { // 3 + agent_status
		t.Errorf("len(metrics) = %d, want 4", len(metrics))
	}

	helper.AddTime(90 * time.Minute)
	helper.pushPoints(t, []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric1"},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric2"},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric3"},
		),
	})

	// API deleted metric1
	for _, m := range metrics {
		if m.Name == "metric1" {
			helper.wrapperClientMock.resources.metrics.dropByID(m.ID)
		}
	}

	if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	helper.AddTime(1 * time.Minute)

	// metric1 is still alive
	helper.pushPoints(t, []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric1"},
		),
	})

	if err := helper.runOnceWithResult(t).CheckMethodWithoutFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	// We list active metrics, 2 query to re-register metric
	helper.wrapperClientMock.AssertCallsPerResource(t, mockAPIResourceMetric, 3)

	metrics = helper.MetricsFromAPI()
	if len(metrics) != 4 { // 3 + agent_status
		t.Errorf("len(metrics) = %d, want 4", len(metrics))
	}

	helper.s.nextFullSync = helper.Now().Add(2 * time.Hour)
	helper.AddTime(90 * time.Minute)

	// API deleted metric2
	for _, m := range metrics {
		if m.Name == "metric2" {
			helper.wrapperClientMock.resources.metrics.dropByID(m.ID)
		}
	}

	// all metrics are inactive
	if err := helper.runOnceWithResult(t).CheckMethodWithoutFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	metrics = helper.MetricsFromAPI()
	if len(metrics) != 3 {
		t.Errorf("len(metrics) = %d, want 3", len(metrics))
	}

	for _, m := range metrics {
		if m.DeactivatedAt.IsZero() && m.Name != agentStatusName {
			t.Errorf("%v should be deactivated", m)

			break
		} else if !m.DeactivatedAt.IsZero() && m.Name == agentStatusName {
			t.Errorf("%v should not be deactivated", m)
		}
	}

	helper.AddTime(1 * time.Minute)

	// API deleted metric3
	for _, m := range metrics {
		if m.Name == "metric3" {
			helper.wrapperClientMock.resources.metrics.dropByID(m.ID)
		}
	}

	helper.pushPoints(t, []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric1"},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric2"},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric3"},
		),
	})

	helper.s.requestSynchronizationLocked(types.EntityMetric, true)

	if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	metrics = helper.MetricsFromAPI()
	if len(metrics) != 4 {
		t.Errorf("len(metrics) = %d, want 4", len(metrics))
	}

	for _, m := range metrics {
		if !m.DeactivatedAt.IsZero() {
			t.Errorf("%v should not be deactivated", m)
		}
	}
}

// TestMetricError test that Glouton handle random error from Bleemeo correctly.
func TestMetricError(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)
	helper.initSynchronizer(t)

	// API fail 1/6th of the time
	helper.wrapperClientMock.resources.metrics.createHook = func(*bleemeoapi.MetricPayload) error {
		if helper.wrapperClientMock.requestCounts[mockAPIResourceMetric]%6 == 0 {
			return &bleemeo.APIError{
				StatusCode: http.StatusInternalServerError,
			}
		}

		return nil
	}
	helper.AddTime(time.Minute)

	helper.pushPoints(t, []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric1"},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric2"},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric3"},
		),
	})

	if err := helper.runUntilNoError(t, 20, 20*time.Second).CheckMethodWithFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	if helper.wrapperClientMock.errorsCount == 0 {
		t.Errorf("We should have some error, had %d", helper.wrapperClientMock.errorsCount)
	}

	if metrics := helper.MetricsFromAPI(); len(metrics) != 4 { // 3 + agent_status
		t.Errorf("len(metrics) = %d, want 4", len(metrics))
	}
}

// TestMetricUnknownError test that Glouton handle failing metric from Bleemeo correctly.
func TestMetricUnknownError(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	// API always reject registering "deny-me" metric
	helper.wrapperClientMock.resources.metrics.createHook = func(metric *bleemeoapi.MetricPayload) error {
		if metric.Name == "deny-me" {
			return clientError{
				body:       "no information about whether the error is permanent or not",
				statusCode: http.StatusBadRequest,
			}
		}

		return nil
	}

	helper.preregisterAgent(t)
	helper.initSynchronizer(t)
	helper.AddTime(time.Minute)

	helper.pushPoints(t, []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric1"},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "deny-me"},
		),
	})

	if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	if helper.wrapperClientMock.errorsCount == 0 {
		t.Errorf("We should have some client error, had %d", helper.wrapperClientMock.errorsCount)
	}

	// list active metrics, register agent_status + 2x metrics to register
	helper.wrapperClientMock.AssertCallsPerResource(t, mockAPIResourceMetric, 6)

	metrics := helper.MetricsFromAPI()
	if len(metrics) != 2 { // 1 + agent_status
		t.Errorf("len(metrics) = %d, want 2", len(metrics))
	}

	// immediately re-run: should not run at all
	if err := helper.runOnceWithResult(t).CheckMethodNotRun(types.EntityMetric); err != nil {
		t.Error(err)
	}

	// After a short delay we retry
	helper.s.nextFullSync = helper.Now().Add(24 * time.Hour)
	helper.AddTime(31 * time.Second)

	// we expect few retry in quick time
	for range 3 {
		if err := helper.runOnceWithResult(t).CheckMethodWithoutFull(types.EntityMetric); err != nil {
			t.Error(err)
		}

		// Each retry had 3 requests
		helper.wrapperClientMock.AssertCallsPerResource(t, mockAPIResourceMetric, 3)

		helper.AddTime(15 * time.Minute)
	}

	// After few retry, we stop re-trying
	for range 8 {
		if err := helper.runOnceWithResult(t).Check(); err != nil {
			t.Error(err)
		}

		if helper.wrapperClientMock.requestCounts[mockAPIResourceMetric] == 0 {
			break
		}

		helper.AddTime(15 * time.Minute)
	}

	if err := helper.runOnceWithResult(t).CheckMethodNotRun(types.EntityMetric); err != nil {
		t.Errorf("After more than 8 retry, we still try to register deny-me metric")
	}

	// Finally on next fullSync re-retry the metrics registration
	helper.SetTime(helper.s.nextFullSync.Add(5 * time.Second))
	helper.pushPoints(t, []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric1"},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "deny-me"},
		),
	})

	if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	if helper.wrapperClientMock.errorsCount == 0 {
		t.Errorf("We should have some client error, had %d", helper.wrapperClientMock.errorsCount)
	}

	// list active metrics, 2 for registration
	helper.wrapperClientMock.AssertCallsPerResource(t, mockAPIResourceMetric, 3)

	metrics = helper.MetricsFromAPI()
	if len(metrics) != 2 { // 1 + agent_status
		t.Errorf("len(metrics) = %d, want 2", len(metrics))
	}
}

// TestMetricPermanentError test that Glouton handle permanent failure metric from Bleemeo correctly.
func TestMetricPermanentError(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "allow-list",
			content: `{"label":["This metric is not whitelisted for this agent"]}`,
		},
		{
			name:    "allow-list",
			content: `{"label":["This metric is not in allow-list for this agent"]}`,
		},
		{
			name:    "too many metrics",
			content: `{"label":["Too many non standard metrics"]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := newHelper(t)
			defer helper.Close()

			helper.preregisterAgent(t)
			helper.initSynchronizer(t)

			// API always reject registering "deny-me" metric
			helper.wrapperClientMock.resources.metrics.createHook = func(metric *bleemeoapi.MetricPayload) error {
				if metric.Name == "deny-me" || metric.Name == "deny-me-also" {
					return clientError{
						body:       tt.content,
						statusCode: http.StatusBadRequest,
					}
				}

				return nil
			}

			helper.AddTime(time.Minute)

			helper.pushPoints(t, []labels.Labels{
				labels.New(
					labels.Label{Name: gloutonTypes.LabelName, Value: "metric1"},
				),
				labels.New(
					labels.Label{Name: gloutonTypes.LabelName, Value: "deny-me"},
				),
				labels.New(
					labels.Label{Name: gloutonTypes.LabelName, Value: "deny-me-also"},
				),
			})

			if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityMetric); err != nil {
				t.Error(err)
			}

			if helper.wrapperClientMock.errorsCount == 0 {
				t.Errorf("We should have some client error, had %d", helper.wrapperClientMock.errorsCount)
			}

			// list active metrics, register agent_status + 2x metrics to register
			helper.wrapperClientMock.AssertCallsPerResource(t, mockAPIResourceMetric, 8)

			metrics := helper.MetricsFromAPI()
			if len(metrics) != 2 { // 1 + agent_status
				t.Errorf("len(metrics) = %d, want 2", len(metrics))
			}

			// immediately re-run: should not run at all
			if err := helper.runOnceWithResult(t).CheckMethodNotRun(types.EntityMetric); err != nil {
				t.Error(err)
			}

			// After a short delay we do not retry because error is permanent
			helper.AddTime(31 * time.Second)

			if err := helper.runOnceWithResult(t).CheckMethodNotRun(types.EntityMetric); err != nil {
				t.Error(err)
			}

			// After a long delay we do not retry because error is permanent
			helper.AddTime(50 * time.Minute)
			// ... but be sure fullSync isn't reached yet
			helper.s.nextFullSync = helper.Now().Add(time.Hour)
			helper.pushPoints(t, []labels.Labels{
				labels.New(
					labels.Label{Name: gloutonTypes.LabelName, Value: "metric1"},
				),
				labels.New(
					labels.Label{Name: gloutonTypes.LabelName, Value: "deny-me"},
				),
				labels.New(
					labels.Label{Name: gloutonTypes.LabelName, Value: "deny-me-also"},
				),
			})

			if err := helper.runOnceWithResult(t).CheckMethodNotRun(types.EntityMetric); err != nil {
				t.Error(err)
			}

			// Finally long enough to reach fullSync, we will retry ONE
			helper.SetTimeToNextFullSync()

			if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityMetric); err != nil {
				t.Error(err)
			}

			if helper.wrapperClientMock.errorsCount == 0 {
				t.Errorf("We should have some client error, had %d", helper.wrapperClientMock.errorsCount)
			}

			// list active metrics, retry ONE register (but for now query for existence of the 2 metrics)
			helper.wrapperClientMock.AssertCallsPerResource(t, mockAPIResourceMetric, 4)

			metrics = helper.MetricsFromAPI()
			if len(metrics) != 2 { // 1 + agent_status
				t.Errorf("len(metrics) = %d, want 2", len(metrics))
			}

			// Now metric registration will succeed and retry all
			helper.wrapperClientMock.resources.metrics.createHook = nil

			helper.SetTimeToNextFullSync()
			helper.pushPoints(t, []labels.Labels{
				labels.New(
					labels.Label{Name: gloutonTypes.LabelName, Value: "metric1"},
				),
				labels.New(
					labels.Label{Name: gloutonTypes.LabelName, Value: "deny-me"},
				),
				labels.New(
					labels.Label{Name: gloutonTypes.LabelName, Value: "deny-me-also"},
				),
			})

			if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityMetric); err != nil {
				t.Error(err)
			}

			if helper.wrapperClientMock.errorsCount != 0 {
				t.Errorf("had %d client error, want 0", helper.wrapperClientMock.errorsCount)
			}

			// list active metrics, retry two register
			helper.wrapperClientMock.AssertCallsPerResource(t, mockAPIResourceMetric, 5)

			metrics = helper.MetricsFromAPI()
			if len(metrics) != 4 { // 3 + agent_status
				t.Errorf("len(metrics) = %d, want 4", len(metrics))
			}
		})
	}
}

// TestMetricTooMany test that Glouton handle too many non-standard metric correctly.
func TestMetricTooMany(t *testing.T) { //nolint:maintidx
	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)
	helper.initSynchronizer(t)

	defaultPatchHook := helper.wrapperClientMock.resources.metrics.patchHook

	// API always reject more than 3 active metrics
	helper.wrapperClientMock.resources.metrics.patchHook = func(metric *bleemeoapi.MetricPayload) error {
		if defaultPatchHook != nil {
			err := defaultPatchHook(metric)
			if err != nil {
				return err
			}
		}

		if metric.DeactivatedAt.IsZero() {
			countActive := 0

			for _, m := range helper.wrapperClientMock.resources.metrics.elems {
				if m.DeactivatedAt.IsZero() && m.ID != metric.ID {
					countActive++
				}
			}

			if countActive >= 3 {
				return clientError{
					body:       `{"label":["Too many non standard metrics"]}`,
					statusCode: http.StatusBadRequest,
				}
			}
		}

		return nil
	}

	helper.wrapperClientMock.resources.metrics.createHook = func(metric *bleemeoapi.MetricPayload) error {
		return helper.wrapperClientMock.resources.metrics.patchHook(metric)
	}

	helper.AddTime(time.Minute)
	helper.pushPoints(t, []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric1"},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric2"},
		),
	})

	if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	// list active metrics, register agent_status + 2x metrics to register
	helper.wrapperClientMock.AssertCallsPerResource(t, mockAPIResourceMetric, 6)

	metrics := helper.MetricsFromAPI()
	if len(metrics) != 3 { // 2 + agent_status
		t.Errorf("len(metrics) = %d, want 3", len(metrics))
	}

	helper.AddTime(5 * time.Minute)
	helper.pushPoints(t, []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric3"},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric4"},
		),
	})

	if err := helper.runOnceWithResult(t).CheckMethodWithoutFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	if helper.wrapperClientMock.errorsCount == 0 {
		t.Errorf("We should have some client error, had %d", helper.wrapperClientMock.errorsCount)
	}

	// list active metrics + 2x metrics to register
	helper.wrapperClientMock.AssertCallsPerResource(t, mockAPIResourceMetric, 5)

	metrics = helper.MetricsFromAPI()
	if len(metrics) != 3 { // 2 + agent_status
		t.Errorf("len(metrics) = %d, want 3", len(metrics))
	}

	helper.AddTime(5 * time.Minute)

	if err := helper.runOnceWithResult(t).CheckMethodNotRun(types.EntityMetric); err != nil {
		t.Error(err)
	}

	helper.SetTimeToNextFullSync()
	// drop all because normally store drop inactive metrics and
	// metric1 don't emitted for 70 minutes
	helper.store.DropAllMetrics()
	helper.pushPoints(t, []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric2"},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric3"},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric4"},
		),
	})

	if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	// We need two sync: one to deactivate the metric, one to register another one
	helper.AddTime(15 * time.Second)

	if err := helper.runOnceWithResult(t).CheckMethodWithoutFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	metrics = helper.MetricsFromAPI()
	if len(metrics) != 4 { // metric1 is now disabled, another get registered
		t.Errorf("len(metrics) = %d, want 4", len(metrics))
	}

	for _, m := range metrics {
		if !m.DeactivatedAt.IsZero() && m.Name != "metric1" {
			t.Errorf("metric %s is deactivated, want active", m.Name)
		}

		if m.DeactivatedAt.IsZero() && m.Name == "metric1" {
			t.Errorf("metric %s is active, want deactivated", m.Name)
		}
	}

	helper.AddTime(5 * time.Minute)
	helper.pushPoints(t, []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric1"},
		),
	})

	if err := helper.runOnceWithResult(t).CheckMethodWithoutFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	if helper.wrapperClientMock.errorsCount == 0 {
		t.Errorf("We should have some client error, had %d", helper.wrapperClientMock.errorsCount)
	}

	metrics = helper.MetricsFromAPI()
	if len(metrics) != 4 { // metric1 is still disabled and no newly registered
		t.Errorf("len(metrics) = %d, want 4", len(metrics))
	}

	for _, m := range metrics {
		if !m.DeactivatedAt.IsZero() && m.Name != "metric1" {
			t.Errorf("metric %s is deactivated, want active", m.Name)
		}

		if m.DeactivatedAt.IsZero() && m.Name == "metric1" {
			t.Errorf("metric %s is active, want deactivated", m.Name)
		}
	}

	// We do not retry to register them
	helper.AddTime(5 * time.Minute)

	if err := helper.runOnceWithResult(t).CheckMethodNotRun(types.EntityMetric); err != nil {
		t.Error(err)
	}

	// Excepted ONE per full-sync
	helper.SetTimeToNextFullSync()
	helper.pushPoints(t, []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric2"},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric3"},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "metric4"},
		),
	})

	if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	if helper.wrapperClientMock.errorsCount != 1 {
		t.Errorf("had %d client error, want 1", helper.wrapperClientMock.errorsCount)
	}

	metrics = helper.MetricsFromAPI()
	if len(metrics) != 4 {
		t.Errorf("len(metrics) = %d, want 4", len(metrics))
	}

	for _, m := range metrics {
		if !m.DeactivatedAt.IsZero() && m.Name != "metric1" {
			t.Errorf("metric %s is deactivated, want active", m.Name)
		}

		if m.DeactivatedAt.IsZero() && m.Name == "metric1" {
			t.Errorf("metric %s is active, want deactivated", m.Name)
		}
	}

	// list active metrics + check existence of the metric we want to reg +
	// retry to register
	helper.wrapperClientMock.AssertCallsPerResource(t, mockAPIResourceMetric, 3)
}

// TestMetricLongItem test that metric with very long item works.
// Long item happen with long container name, test this scenario.
func TestMetricLongItem(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)
	helper.initSynchronizer(t)
	helper.AddTime(time.Minute)

	srvRedis1 := discovery.Service{
		Name:        "redis",
		Instance:    "short-redis-container-name",
		ServiceType: discovery.RedisService,
		// ContainerID: "1234",
		Active: true,
	}

	srvRedis2 := discovery.Service{
		Name:        "redis",
		Instance:    "long-redis-container-name--this-one-is-more-than-100-char-which-is-the-limit-on-bleemeo-api-0123456789abcdef",
		ServiceType: discovery.RedisService,
		// ContainerID: "1234",
		Active: true,
	}

	helper.discovery.SetResult([]discovery.Service{srvRedis1, srvRedis2}, nil)

	helper.pushPoints(t, []labels.Labels{
		model.AnnotationToMetaLabels(labels.FromMap(srvRedis1.LabelsOfStatus()), srvRedis1.AnnotationsOfStatus()),
		model.AnnotationToMetaLabels(labels.FromMap(srvRedis2.LabelsOfStatus()), srvRedis2.AnnotationsOfStatus()),
	})

	if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	metrics := helper.MetricsFromAPI()
	// agent_status + the two service_status metrics
	if len(metrics) != 3 {
		t.Errorf("len(metrics) = %v, want %v", len(metrics), 3)
	}

	helper.SetTimeToNextFullSync()

	helper.pushPoints(t, []labels.Labels{
		model.AnnotationToMetaLabels(labels.FromMap(srvRedis1.LabelsOfStatus()), srvRedis1.AnnotationsOfStatus()),
		model.AnnotationToMetaLabels(labels.FromMap(srvRedis2.LabelsOfStatus()), srvRedis2.AnnotationsOfStatus()),
	})

	if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	// We do 1 request: list metrics.
	helper.wrapperClientMock.AssertCallsPerResource(t, mockAPIResourceMetric, 1)

	metrics = helper.MetricsFromAPI()
	// No new metrics
	if len(metrics) != 3 {
		t.Errorf("len(metrics) = %v, want %v", len(metrics), 3)
	}
}

// Few tests with SNMP metrics.
func TestWithSNMP(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	helper.SNMP = []*snmp.Target{
		snmp.NewMock(config.SNMPTarget{InitialName: "Z-The-Initial-Name", Target: snmpAddress}, map[string]string{}),
	}

	helper.preregisterAgent(t)
	helper.initSynchronizer(t)

	idAgentMain, _ := helper.state.BleemeoCredentials()
	idAgentSNMP := "1"

	helper.pushPoints(t, []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "cpu_system"},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "ifOutOctets"},
			labels.Label{Name: gloutonTypes.LabelSNMPTarget, Value: snmpAddress},
			labels.Label{Name: gloutonTypes.LabelMetaBleemeoTargetAgentUUID, Value: idAgentSNMP},
		),
	})

	result := helper.runOnceWithResult(t)

	if err := result.CheckMethodWithFull(types.EntitySNMP); err != nil {
		t.Error(err)
	}

	if err := result.CheckMethodWithFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	agents := helper.AgentsFromAPI()
	wantAgents := []bleemeoapi.AgentPayload{
		{
			Agent: bleemeoTypes.Agent{
				ID:                     idAgentMain,
				AccountID:              accountID,
				CurrentAccountConfigID: newAccountConfig.ID,
				AgentType:              agentTypeAgent.ID,
				FQDN:                   testAgentFQDN,
				DisplayName:            testAgentFQDN,
			},
			Abstracted:      false,
			InitialPassword: "password already set",
		},
		{
			Agent: bleemeoTypes.Agent{
				ID:                     idAgentSNMP,
				CreatedAt:              helper.Now(),
				AccountID:              accountID,
				CurrentAccountConfigID: newAccountConfig.ID,
				AgentType:              agentTypeSNMP.ID,
				FQDN:                   snmpAddress,
				DisplayName:            "Z-The-Initial-Name",
			},
			Abstracted:      true,
			InitialPassword: "password already set",
		},
	}

	optAgentSort := cmpopts.SortSlices(func(x, y bleemeoapi.AgentPayload) bool { return x.ID < y.ID })
	if diff := cmp.Diff(wantAgents, agents, cmpopts.EquateEmpty(), optAgentSort); diff != "" {
		t.Errorf("agents mismatch (-want +got)\n%s", diff)
	}

	metrics := helper.MetricsFromAPI()
	want := []bleemeoapi.MetricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:         "1",
				AgentID:    idAgentMain,
				LabelsText: "",
			},
			Name: agentStatusName,
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:         "2",
				AgentID:    idAgentMain,
				LabelsText: "",
			},
			Name: "cpu_system",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:         "3",
				AgentID:    idAgentSNMP,
				LabelsText: `__name__="ifOutOctets",snmp_target="127.0.0.1"`,
			},
			Name: "ifOutOctets",
		},
	}

	if diff := cmp.Diff(want, metrics); diff != "" {
		t.Errorf("metrics mismatch (-want +got):\n%s", diff)
	}
}

// Test for monitor metric deactivation.
func TestMonitorDeactivation(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)
	helper.addMonitorOnAPI(t)
	helper.initSynchronizer(t)
	helper.AddTime(time.Minute)

	// This should NOT be needed. Glouton should not need to be able to read the
	// monitor agent to works.
	// Currently Glouton public probe are allowed to view such agent. Glouton private probe aren't.
	helper.wrapperClientMock.resources.agents.add(newMonitorAgent)

	idAgentMain, _ := helper.state.BleemeoCredentials()

	initialMetrics := []bleemeoapi.MetricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:      "90c6459c-851d-4bb4-957c-afbc695c2201",
				AgentID: newMonitor.AgentID,
				LabelsText: fmt.Sprintf(
					"__name__=\"probe_success\",instance=\"http://localhost:8000/\",instance_uuid=\"%s\",scraper=\"paris\"",
					newMonitor.AgentID,
				),
			},
			Name: "probe_success",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:      "9149d491-3a6e-4f46-abf9-c1ea9b9f7227",
				AgentID: newMonitor.AgentID,
				LabelsText: fmt.Sprintf(
					"__name__=\"probe_success\",instance=\"http://localhost:8000/\",instance_uuid=\"%s\",scraper=\"milan\"",
					newMonitor.AgentID,
				),
			},
			Name: "probe_success",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:      "92c0b336-6e5a-4960-94cc-b606db8a581f",
				AgentID: newMonitor.AgentID,
				LabelsText: fmt.Sprintf(
					"__name__=\"probe_status\",instance=\"http://localhost:8000/\",instance_uuid=\"%s\"",
					newMonitor.AgentID,
				),
			},
			Name: "probe_status",
		},
	}

	pushedPoints := []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "probe_success"},
			labels.Label{Name: gloutonTypes.LabelScraper, Value: "paris"},
			labels.Label{Name: gloutonTypes.LabelInstance, Value: "http://localhost:8000/"},
			labels.Label{Name: gloutonTypes.LabelInstanceUUID, Value: newMonitor.AgentID},
			labels.Label{Name: gloutonTypes.LabelMetaBleemeoTargetAgentUUID, Value: newMonitor.AgentID},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "probe_duration"},
			labels.Label{Name: gloutonTypes.LabelScraper, Value: "paris"},
			labels.Label{Name: gloutonTypes.LabelInstance, Value: "http://localhost:8000/"},
			labels.Label{Name: gloutonTypes.LabelInstanceUUID, Value: newMonitor.AgentID},
			labels.Label{Name: gloutonTypes.LabelMetaBleemeoTargetAgentUUID, Value: newMonitor.AgentID},
		),
	}

	helper.SetAPIMetrics(initialMetrics...)
	helper.pushPoints(t, pushedPoints)

	if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	metrics := helper.MetricsFromAPI()
	want := []bleemeoapi.MetricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:         "1",
				AgentID:    idAgentMain,
				LabelsText: "",
			},
			Name: agentStatusName,
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:      "2",
				AgentID: newMonitor.AgentID,
				LabelsText: fmt.Sprintf(
					"__name__=\"probe_duration\",instance=\"http://localhost:8000/\",instance_uuid=\"%s\",scraper=\"paris\"",
					newMonitor.AgentID,
				),
				ServiceID: newMonitor.ID,
			},
			Name: "probe_duration",
		},
	}

	want = append(want, initialMetrics...)

	if diff := cmp.Diff(want, metrics); diff != "" {
		t.Fatalf("metrics mismatch (-want +got):\n%s", diff)
	}

	helper.SetTimeToNextFullSync()
	helper.pushPoints(t, pushedPoints)

	if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	metrics = helper.MetricsFromAPI()

	if diff := cmp.Diff(want, metrics); diff != "" {
		t.Errorf("metrics mismatch (-want +got):\n%s", diff)
	}

	helper.SetTimeToNextFullSync()
	helper.AddTime(60 * time.Minute)

	if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityMetric); err != nil {
		t.Error(err)
	}

	metrics = helper.MetricsFromAPI()

	want = []bleemeoapi.MetricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:         "1",
				AgentID:    idAgentMain,
				LabelsText: "",
			},
			Name: agentStatusName,
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:      "2",
				AgentID: newMonitor.AgentID,
				LabelsText: fmt.Sprintf(
					"__name__=\"probe_duration\",instance=\"http://localhost:8000/\",instance_uuid=\"%s\",scraper=\"paris\"",
					newMonitor.AgentID,
				),
				DeactivatedAt: helper.s.now(),
				ServiceID:     newMonitor.ID,
			},
			Name: "probe_duration",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:      "90c6459c-851d-4bb4-957c-afbc695c2201",
				AgentID: newMonitor.AgentID,
				LabelsText: fmt.Sprintf(
					"__name__=\"probe_success\",instance=\"http://localhost:8000/\",instance_uuid=\"%s\",scraper=\"paris\"",
					newMonitor.AgentID,
				),
				DeactivatedAt: helper.s.now(),
			},
			Name: "probe_success",
		},
		initialMetrics[1],
		initialMetrics[2],
	}

	if diff := cmp.Diff(want, metrics); diff != "" {
		t.Errorf("metrics mismatch (-want +got):\n%s", diff)
	}
}

// TestServiceStatusRename test that Glouton behave correctly on service_status rename.
// Older glouton send metric like "apache_status" which was renamed to "service_status{service_type="apache"}".
// The rename is done by Bleemeo API (actually Glouton try to register the new metric and API rename and response
// with the old renamed object). API behave as if the metric apache_status is deleted and service_status is created with
// the same UUID as apache_status.
func TestServiceStatusRename(t *testing.T) { //nolint: maintidx
	// run 0 create old metric and then new metric (note: this isn't something that should
	//       happen on real glouton, because on same run we don't have both old & then new)
	// run 1 has old metric pre-create in API and in cache and only create new metric
	// run 2 has old metric pre-create in API and not in cache and only create new metric
	for run := range 3 {
		t.Run(fmt.Sprintf("run-%d", run), func(t *testing.T) {
			t.Parallel()

			helper := newHelper(t)
			defer helper.Close()

			helper.preregisterAgent(t)
			helper.initSynchronizer(t)

			helper.wrapperClientMock.resources.metrics.createHook = func(metric *bleemeoapi.MetricPayload) error {
				// API will rename and reuse the existing $SERVICE_status metric when registering service_status metrics.
				// From Glouton point of vue, it is the same as if a new metric is created (service_status) and the old is deleted.
				metricCopy := metricFromAPI(*metric, helper.s.now())
				serviceType := metricCopy.Labels[gloutonTypes.LabelService]

				if metric.Name != "service_status" || serviceType == "" {
					return nil
				}

				for idx, existing := range helper.wrapperClientMock.resources.metrics.elems {
					if existing.Name == serviceType+"_status" && existing.Item == metricCopy.Labels[gloutonTypes.LabelServiceInstance] {
						metric.ID = existing.ID

						return reuseIDError{idx}
					}
				}

				return nil
			}

			srvApache := discovery.Service{
				Name:        "apache",
				Instance:    "",
				ServiceType: discovery.ApacheService,
				Active:      true,
			}
			srvNginx := discovery.Service{
				Name:        "nginx",
				Instance:    "container1",
				ServiceType: discovery.NginxService,
				Active:      true,
			}

			srvApacheID := "892cb229-0f8c-4b6c-866c-02a13256b618"
			srvNginxID := "809bc83b-2f28-43f1-9fb7-a84445ca1bc0"

			helper.SetAPIServices(
				syncservices.ServicePayloadFromDiscovery(srvApache, "", testAgent.AccountID, testAgent.ID, srvApacheID),
				syncservices.ServicePayloadFromDiscovery(srvNginx, "", testAgent.AccountID, testAgent.ID, srvNginxID),
			)

			helper.discovery.SetResult([]discovery.Service{srvApache, srvNginx}, nil)

			want1 := []bleemeoapi.MetricPayload{
				{
					Metric: bleemeoTypes.Metric{
						ID:         "1",
						AgentID:    testAgent.ID,
						LabelsText: "",
					},
					Name: agentStatusName,
				},
				{
					Metric: bleemeoTypes.Metric{
						ID:         "2",
						AgentID:    testAgent.ID,
						LabelsText: "",
						ServiceID:  srvApacheID,
					},
					Name: "apache_status",
				},
				{
					Metric: bleemeoTypes.Metric{
						ID:         "3",
						AgentID:    testAgent.ID,
						LabelsText: "",
						ServiceID:  srvNginxID,
					},
					Name: "nginx_status",
					Item: "container1",
				},
			}

			helper.AddTime(time.Minute)

			if run == 0 {
				helper.pushPoints(t, []labels.Labels{
					model.AnnotationToMetaLabels(
						labels.New(
							labels.Label{Name: gloutonTypes.LabelName, Value: "apache_status"},
						),
						gloutonTypes.MetricAnnotations{
							ServiceName:     srvApache.Name,
							ServiceInstance: srvApache.Instance,
						},
					),
					model.AnnotationToMetaLabels(
						labels.New(
							labels.Label{Name: gloutonTypes.LabelName, Value: "nginx_status"},
							labels.Label{Name: gloutonTypes.LabelItem, Value: "container1"},
						),
						gloutonTypes.MetricAnnotations{
							ServiceName:     srvNginx.Name,
							ServiceInstance: srvNginx.Instance,
						},
					),
				})

				if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityMetric); err != nil {
					t.Error(err)
				}

				// list of active metrics, 2*N query to register metric + 1 to register agent_status
				helper.wrapperClientMock.AssertCallsPerResource(t, mockAPIResourceMetric, 6)

				metrics := helper.MetricsFromAPI()

				if diff := cmp.Diff(want1, metrics); diff != "" {
					t.Errorf("metrics mismatch (-want +got):\n%s", diff)
				}

				helper.store.DropAllMetrics()
				helper.SetTimeToNextFullSync()
			}

			// Newer agent no longer allow apache_status & nginx_status by default.
			helper.s.option.IsMetricAllowed = func(lbls map[string]string) bool {
				if lbls[gloutonTypes.LabelName] == "apache_status" || lbls[gloutonTypes.LabelName] == "nginx_status" {
					return false
				}

				return true
			}

			if run > 0 {
				helper.SetAPIMetrics(want1...)
			}

			if run == 1 {
				helper.SetCacheMetrics(want1...)
			}

			helper.pushPoints(t, []labels.Labels{
				model.AnnotationToMetaLabels(labels.FromMap(srvNginx.LabelsOfStatus()), srvNginx.AnnotationsOfStatus()),
			})

			if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityMetric); err != nil {
				t.Error(err)
			}

			want2 := []bleemeoapi.MetricPayload{
				{
					Metric: bleemeoTypes.Metric{
						ID:         "1",
						AgentID:    testAgent.ID,
						LabelsText: "",
					},
					Name: agentStatusName,
				},
				{
					Metric: bleemeoTypes.Metric{
						ID:         "2",
						AgentID:    testAgent.ID,
						LabelsText: "",
						ServiceID:  srvApacheID,
					},
					Name: "apache_status",
				},
				{
					Metric: bleemeoTypes.Metric{
						ID:         "3",
						AgentID:    testAgent.ID,
						LabelsText: `__name__="service_status",service="nginx",service_instance="container1"`,
						ServiceID:  srvNginxID,
					},
					Name: "service_status",
				},
			}

			if run == 0 {
				// run 0 is a bit special: we simulate an impossible situation: Glouton is running and change during runtime
				// from old metric to new metric. One consequence is that old apache_status gets deactivated immediately
				want2[1].DeactivatedAt = helper.Now()
			}

			metrics := helper.MetricsFromAPI()
			if diff := cmp.Diff(want2, metrics); diff != "" {
				t.Errorf("metrics mismatch (-want +got):\n%s", diff)
			}

			helper.AddTime(1 * time.Minute)

			helper.pushPoints(t, []labels.Labels{
				model.AnnotationToMetaLabels(labels.FromMap(srvApache.LabelsOfStatus()), srvApache.AnnotationsOfStatus()),
			})

			if err := helper.runOnceWithResult(t).Check(); err != nil {
				t.Error(err)
			}

			helper.AddTime(1 * time.Minute)

			want3 := []bleemeoapi.MetricPayload{
				{
					Metric: bleemeoTypes.Metric{
						ID:         "1",
						AgentID:    testAgent.ID,
						LabelsText: "",
					},
					Name: agentStatusName,
				},
				{
					Metric: bleemeoTypes.Metric{
						ID:         "2",
						AgentID:    testAgent.ID,
						LabelsText: `__name__="service_status",service="apache"`,
						ServiceID:  srvApacheID,
					},
					Name: "service_status",
				},
				{
					Metric: bleemeoTypes.Metric{
						ID:         "3",
						AgentID:    testAgent.ID,
						LabelsText: `__name__="service_status",service="nginx",service_instance="container1"`,
						ServiceID:  srvNginxID,
					},
					Name: "service_status",
				},
			}

			metrics = helper.MetricsFromAPI()

			if diff := cmp.Diff(want3, metrics); diff != "" {
				t.Errorf("metrics mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestMonitorPrivate test for monitor private.
func TestMonitorPrivate(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	// Configure as private probe
	helper.cfg.Blackbox.ScraperName = ""
	helper.cfg.Blackbox.ScraperSendUUID = true

	helper.addMonitorOnAPI(t)
	helper.initSynchronizer(t)
	helper.AddTime(time.Minute)

	if err := helper.runOnceWithResult(t).Check(); err != nil {
		t.Error(err)
	}

	idAgentMain, _ := helper.state.BleemeoCredentials()
	if idAgentMain == "" {
		t.Fatal("idAgentMain == '', want something")
	}

	initialMetrics := []bleemeoapi.MetricPayload{
		// Metric from other probe are NOT present in API, because glouton private probe aren't allow to view them.
		{
			Metric: bleemeoTypes.Metric{
				ID:      "9149d491-3a6e-4f46-abf9-c1ea9b9f7227",
				AgentID: newMonitor.AgentID,
				LabelsText: fmt.Sprintf(
					"__name__=\"probe_success\",instance=\"%s\",instance_uuid=\"%s\",scraper_uuid=\"%s\"",
					newMonitor.URL,
					newMonitor.AgentID,
					idAgentMain,
				),
				ServiceID: newMonitor.ID,
			},
			Name: "probe_success",
		},
	}

	pushedPoints := []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "probe_success"},
			labels.Label{Name: gloutonTypes.LabelScraperUUID, Value: idAgentMain},
			labels.Label{Name: gloutonTypes.LabelInstance, Value: newMonitor.URL},
			labels.Label{Name: gloutonTypes.LabelInstanceUUID, Value: newMonitor.AgentID},
			labels.Label{Name: gloutonTypes.LabelMetaBleemeoTargetAgentUUID, Value: newMonitor.AgentID},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "probe_duration"},
			labels.Label{Name: gloutonTypes.LabelScraperUUID, Value: idAgentMain},
			labels.Label{Name: gloutonTypes.LabelInstance, Value: newMonitor.URL},
			labels.Label{Name: gloutonTypes.LabelInstanceUUID, Value: newMonitor.AgentID},
			labels.Label{Name: gloutonTypes.LabelMetaBleemeoTargetAgentUUID, Value: newMonitor.AgentID},
		),
	}

	helper.SetAPIMetrics(initialMetrics...)
	helper.pushPoints(t, pushedPoints)

	if err := helper.runOnceWithResult(t).Check(); err != nil {
		t.Error(err)
	}

	want := []bleemeoapi.MetricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:         idAny,
				AgentID:    idAgentMain,
				LabelsText: "",
			},
			Name: agentStatusName,
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:      idAny,
				AgentID: newMonitor.AgentID,
				LabelsText: fmt.Sprintf(
					"__name__=\"probe_success\",instance=\"%s\",instance_uuid=\"%s\",scraper_uuid=\"%s\"",
					newMonitor.URL,
					newMonitor.AgentID,
					idAgentMain,
				),
				ServiceID: newMonitor.ID,
			},
			Name: "probe_success",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:      idAny,
				AgentID: newMonitor.AgentID,
				LabelsText: fmt.Sprintf(
					"__name__=\"probe_duration\",instance=\"%s\",instance_uuid=\"%s\",scraper_uuid=\"%s\"",
					newMonitor.URL,
					newMonitor.AgentID,
					idAgentMain,
				),
				ServiceID: newMonitor.ID,
			},
			Name: "probe_duration",
		},
	}

	helper.assertMetricsInAPI(t, want)

	helper.SetTimeToNextFullSync()
	helper.AddTime(60 * time.Minute)
	helper.pushPoints(t, pushedPoints)

	if err := helper.runOnceWithResult(t).Check(); err != nil {
		t.Error(err)
	}

	helper.assertMetricsInAPI(t, want)

	helper.SetTimeToNextFullSync()
	helper.AddTime(60 * time.Minute)

	if err := helper.runOnceWithResult(t).Check(); err != nil {
		t.Error(err)
	}

	want = []bleemeoapi.MetricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:         idAny,
				AgentID:    idAgentMain,
				LabelsText: "",
			},
			Name: agentStatusName,
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:      idAny,
				AgentID: newMonitor.AgentID,
				LabelsText: fmt.Sprintf(
					"__name__=\"probe_success\",instance=\"%s\",instance_uuid=\"%s\",scraper_uuid=\"%s\"",
					newMonitor.URL,
					newMonitor.AgentID,
					idAgentMain,
				),
				DeactivatedAt: helper.Now(),
				ServiceID:     newMonitor.ID,
			},
			Name: "probe_success",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:      idAny,
				AgentID: newMonitor.AgentID,
				LabelsText: fmt.Sprintf(
					"__name__=\"probe_duration\",instance=\"%s\",instance_uuid=\"%s\",scraper_uuid=\"%s\"",
					newMonitor.URL,
					newMonitor.AgentID,
					idAgentMain,
				),
				DeactivatedAt: helper.Now(),
				ServiceID:     newMonitor.ID,
			},
			Name: "probe_duration",
		},
	}

	helper.assertMetricsInAPI(t, want)
}

// TestKubernetesMetrics test for kubernetes cluster metrics.
func TestKubernetesMetrics(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	// Glouton is configured as Kubernetes cluster
	helper.facts.SetFact(facts.FactKubernetesCluster, testK8SClusterName)

	helper.initSynchronizer(t)
	helper.AddTime(time.Minute)

	if err := helper.runOnceWithResult(t).Check(); err != nil {
		t.Error(err)
	}

	idAgentMain, _ := helper.state.BleemeoCredentials()
	if idAgentMain == "" {
		t.Fatal("idAgentMain == '', want something")
	}

	helper.assertFactsInAPI(t, []bleemeoTypes.AgentFact{
		{
			ID:      idAny,
			AgentID: idAgentMain,
			Key:     "fqdn",
			Value:   testAgentFQDN,
		},
		{
			ID:      idAny,
			AgentID: idAgentMain,
			Key:     facts.FactKubernetesCluster,
			Value:   testK8SClusterName,
		},
	})

	// API does a lead-election and notify us that we are leader
	helper.AddTime(time.Second)

	agent := helper.wrapperClientMock.resources.agents.findResource(func(agent bleemeoapi.AgentPayload) bool {
		return agent.ID == idAgentMain
	})
	if agent != nil {
		agent.IsClusterLeader = true

		// Currently on leader status change, API sent "config-changed" message
		helper.s.NotifyConfigUpdate(true)
	}

	// API create a global Kubernetes agent
	helper.wrapperClientMock.resources.agents.add(testK8SAgent)

	helper.AddTime(10 * time.Second)

	if err := helper.runOnceWithResult(t).CheckMethodWithFull(types.EntityAgent); err != nil {
		t.Error(err)
	}

	if !helper.cache.Agent().IsClusterLeader {
		t.Fatal("agent isn't ClusterLeader")
	}

	pushedPoints := []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "kubernetes_kubelet_status"},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "cpu_used"},
		),
		// Note: we have both meta-label & normal label because we need to simulare Registry.applyRelabel()
		// (we need both annotation & getDefaultRelabelConfig())
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "kubernetes_cpu_limits"},
			labels.Label{Name: gloutonTypes.LabelOwnerKind, Value: "daemonset"},
			labels.Label{Name: gloutonTypes.LabelOwnerName, Value: "glouton"},
			labels.Label{Name: gloutonTypes.LabelNamespace, Value: "default"},
			labels.Label{Name: gloutonTypes.LabelMetaKubernetesCluster, Value: testK8SClusterName},
			labels.Label{Name: gloutonTypes.LabelMetaBleemeoTargetAgent, Value: testK8SClusterName},
			labels.Label{Name: gloutonTypes.LabelMetaBleemeoTargetAgentUUID, Value: testK8SAgent.ID},
			labels.Label{Name: gloutonTypes.LabelInstance, Value: testK8SClusterName},
			labels.Label{Name: gloutonTypes.LabelInstanceUUID, Value: testK8SAgent.ID},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "kubernetes_cpu_requests"},
			labels.Label{Name: gloutonTypes.LabelOwnerKind, Value: "daemonset"},
			labels.Label{Name: gloutonTypes.LabelOwnerName, Value: "glouton"},
			labels.Label{Name: gloutonTypes.LabelNamespace, Value: "default"},
			labels.Label{Name: gloutonTypes.LabelMetaKubernetesCluster, Value: testK8SClusterName},
			labels.Label{Name: gloutonTypes.LabelMetaBleemeoTargetAgent, Value: testK8SClusterName},
			labels.Label{Name: gloutonTypes.LabelMetaBleemeoTargetAgentUUID, Value: testK8SAgent.ID},
			labels.Label{Name: gloutonTypes.LabelInstance, Value: testK8SClusterName},
			labels.Label{Name: gloutonTypes.LabelInstanceUUID, Value: testK8SAgent.ID},
		),
	}

	helper.pushPoints(t, pushedPoints)

	if err := helper.runOnceWithResult(t).Check(); err != nil {
		t.Error(err)
	}

	want := []bleemeoapi.MetricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:         idAny,
				AgentID:    idAgentMain,
				LabelsText: "",
			},
			Name: agentStatusName,
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:         idAny,
				AgentID:    idAgentMain,
				LabelsText: "",
			},
			Name: "cpu_used",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:         idAny,
				AgentID:    idAgentMain,
				LabelsText: "",
			},
			Name: "kubernetes_kubelet_status",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:      idAny,
				AgentID: testK8SAgent.ID,
				LabelsText: fmt.Sprintf(
					"__name__=\"kubernetes_cpu_limits\",instance=\"%s\",instance_uuid=\"%s\",namespace=\"default\",owner_kind=\"daemonset\",owner_name=\"glouton\"",
					testK8SClusterName,
					testK8SAgent.ID,
				),
			},
			Name: "kubernetes_cpu_limits",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:      idAny,
				AgentID: testK8SAgent.ID,
				LabelsText: fmt.Sprintf(
					"__name__=\"kubernetes_cpu_requests\",instance=\"%s\",instance_uuid=\"%s\",namespace=\"default\",owner_kind=\"daemonset\",owner_name=\"glouton\"",
					testK8SClusterName,
					testK8SAgent.ID,
				),
			},
			Name: "kubernetes_cpu_requests",
		},
	}

	helper.assertMetricsInAPI(t, want)

	helper.SetTimeToNextFullSync()
	helper.AddTime(60 * time.Minute)
	helper.pushPoints(t, pushedPoints)

	if err := helper.runOnceWithResult(t).Check(); err != nil {
		t.Error(err)
	}

	helper.assertMetricsInAPI(t, want)

	helper.SetTimeToNextFullSync()
	helper.AddTime(60 * time.Minute)

	// One metrics become inactive (kubernetes_cpu_requests)
	pushedPoints = pushedPoints[:len(pushedPoints)-1]
	want[len(want)-1].DeactivatedAt = helper.Now()

	helper.pushPoints(t, pushedPoints)

	if err := helper.runOnceWithResult(t).Check(); err != nil {
		t.Error(err)
	}

	helper.assertMetricsInAPI(t, want)

	// I'm no longer leader
	helper.AddTime(time.Second)

	agent = helper.wrapperClientMock.resources.agents.findResource(func(agent bleemeoapi.AgentPayload) bool {
		return agent.ID == idAgentMain
	})
	if agent != nil {
		agent.IsClusterLeader = false

		// Currently on leader status change, API sent "config-changed" message
		helper.s.NotifyConfigUpdate(true)
	}

	// Therefor Glouton stop emitting all kubernetes global metric, but it won't change which metrics is active or not
	pushedPoints = []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "kubernetes_kubelet_status"},
		),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "cpu_used"},
		),
	}

	helper.pushPoints(t, pushedPoints)

	if err := helper.runOnceWithResult(t).Check(); err != nil {
		t.Error(err)
	}

	helper.assertMetricsInAPI(t, want)

	helper.SetTimeToNextFullSync()
	helper.AddTime(60 * time.Minute)
	helper.pushPoints(t, pushedPoints)

	if err := helper.runOnceWithResult(t).Check(); err != nil {
		t.Error(err)
	}

	helper.assertMetricsInAPI(t, want)
}

// inactive and MQTT

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

func Test_MergeFirstSeenAt(t *testing.T) {
	state := newStateMock()
	now := time.Now().Add(-10 * time.Minute).Truncate(time.Second)

	cache := cache.Load(state)

	want := []bleemeoTypes.Metric{
		{
			ID:          "1",
			LabelsText:  "2",
			FirstSeenAt: now,
		},
		{
			ID:          "2",
			LabelsText:  "1",
			FirstSeenAt: now,
		},
		{
			ID:          "3",
			LabelsText:  "4",
			FirstSeenAt: now,
		},
		{
			ID:          "4",
			LabelsText:  "3",
			FirstSeenAt: now,
		},
		{
			ID:          "5",
			LabelsText:  "6",
			FirstSeenAt: now,
		},
	}

	cache.SetMetrics(want)

	metrics := []bleemeoapi.MetricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:          "1",
				LabelsText:  "2",
				FirstSeenAt: now.Add(5 * time.Minute),
			},
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:          "2",
				LabelsText:  "1",
				FirstSeenAt: now.Add(4 * time.Minute),
			},
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:          "3",
				LabelsText:  "4",
				FirstSeenAt: now.Add(3 * time.Minute),
			},
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:          "5",
				LabelsText:  "6",
				FirstSeenAt: now.Add(2 * time.Minute),
			},
		},
	}

	got := []bleemeoTypes.Metric{}
	metricsByUUID := cache.MetricsByUUID()

	for _, val := range metrics {
		metricsByUUID[val.ID] = metricFromAPI(val, metricsByUUID[val.ID].FirstSeenAt)
	}

	for _, val := range metricsByUUID {
		got = append(got, val)
	}

	got = sortList(got)
	want = sortList(want)

	if res := cmp.Diff(got, want); res != "" {
		t.Errorf("FirstSeenAt Merge did not occur correctly:\n%s", res)
	}
}
