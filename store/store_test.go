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

package store

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/prometheus/model/value"
)

var timeComparer = cmp.Comparer(func(x, y time.Time) bool { //nolint:gochecknoglobals
	return x.Truncate(time.Millisecond).Equal(y.Truncate(time.Millisecond))
})

// metricGetOrCreate will return the metric that exactly match given labels.
//
// It just metricGet followed by update to s.metrics in case of creation.
func (s *Store) metricGetOrCreate(lbls map[string]string) metric {
	m, ok, _ := s.metricGet(lbls, types.MetricAnnotations{})
	if !ok {
		s.metrics[m.metricID] = m
	}

	return m
}

func TestLabelsMatchNotExact(t *testing.T) {
	cases := []struct {
		labels, filter map[string]string
		want           bool
	}{
		{
			map[string]string{
				types.LabelName: "cpu_used",
			},
			map[string]string{
				types.LabelName: "cpu_used",
			},
			true,
		},
		{
			map[string]string{
				types.LabelName: "disk_used",
				"item":          "/home",
			},
			map[string]string{
				types.LabelName: "disk_used",
			},
			true,
		},
		{
			map[string]string{
				types.LabelName: "disk_used",
				"item":          "/home",
			},
			map[string]string{
				types.LabelName: "cpu_used",
			},
			false,
		},
		{
			map[string]string{
				types.LabelName: "disk_used",
				"item":          "/home",
			},
			map[string]string{
				types.LabelName: "disk_used",
				"item":          "/",
			},
			false,
		},
		{
			map[string]string{
				types.LabelName: "disk_used",
				"item":          "/home",
			},
			map[string]string{
				types.LabelName: "disk_used",
				"extra":         "label",
			},
			false,
		},
	}

	for _, c := range cases {
		got := labelsMatch(c.labels, c.filter, false)
		if got != c.want {
			t.Errorf("labelsMatch(%v, %v, false) == %v, want %v", c.labels, c.filter, got, c.want)
		}
	}
}

func TestLabelsMatchExact(t *testing.T) {
	cases := []struct {
		labels, filter map[string]string
		want           bool
	}{
		{
			map[string]string{
				types.LabelName: "cpu_used",
			},
			map[string]string{
				types.LabelName: "cpu_used",
			},
			true,
		},
		{
			map[string]string{
				types.LabelName: "disk_used",
				"item":          "/home",
			},
			map[string]string{
				types.LabelName: "disk_used",
			},
			false,
		},
		{
			map[string]string{
				types.LabelName: "disk_used",
				"item":          "/home",
			},
			map[string]string{
				types.LabelName: "cpu_used",
			},
			false,
		},
		{
			map[string]string{
				types.LabelName: "disk_used",
				"item":          "/home",
			},
			map[string]string{
				types.LabelName: "disk_used",
				"item":          "/",
			},
			false,
		},
		{
			map[string]string{
				types.LabelName: "disk_used",
				"item":          "/home",
			},
			map[string]string{
				types.LabelName: "disk_used",
				"item":          "/home",
			},
			true,
		},
		{
			map[string]string{
				types.LabelName: "disk_used",
				"item":          "/home",
			},
			map[string]string{
				types.LabelName: "disk_used",
				"item":          "/home",
				"extra":         "label",
			},
			false,
		},
	}

	for _, c := range cases {
		got := labelsMatch(c.labels, c.filter, true)
		if got != c.want {
			t.Errorf("labelsMatch(%v, %v, false) == %v, want %v", c.labels, c.filter, got, c.want)
		}
	}
}

func TestMetricsSimple(t *testing.T) {
	labels := map[string]string{
		types.LabelName: "measurement_fieldFloat",
	}
	db := New("test store", time.Hour, time.Hour)
	m := db.metricGetOrCreate(labels)

	if _, ok := db.metrics[m.metricID]; !ok {
		t.Errorf("db.metrics[%v] == nil, want it to exists", m.metricID)
	}

	metrics, err := db.Metrics(labels)
	if err != nil {
		t.Error(err)
	}

	if len(metrics) != 1 {
		t.Errorf("len(metrics) == %v, want %v", len(metrics), 1)
	}

	if !reflect.DeepEqual(metrics[0].Labels(), labels) {
		t.Errorf("metrics[0].Labels() == %v, want %v", metrics[0].Labels(), labels)
	}
}

func TestMetricsMultiple(t *testing.T) {
	labels1 := map[string]string{
		types.LabelName: "cpu_used",
	}
	labels2 := map[string]string{
		types.LabelName: "disk_used",
		"item":          "/home",
	}
	labels3 := map[string]string{
		types.LabelName: "disk_used",
		"item":          "/srv",
		"fstype":        "ext4",
	}
	db := New("test store", time.Hour, time.Hour)
	db.metricGetOrCreate(labels1)
	db.metricGetOrCreate(labels2)
	db.metricGetOrCreate(labels3)

	metrics, err := db.Metrics(labels1)
	if err != nil {
		t.Error(err)
	}

	if len(metrics) != 1 {
		t.Errorf("len(metrics) == %v, want %v", len(metrics), 1)
	}

	if !reflect.DeepEqual(metrics[0].Labels(), labels1) {
		t.Errorf("metrics[0].Labels() == %v, want %v", metrics[0].Labels(), labels1)
	}

	metrics, err = db.Metrics(map[string]string{types.LabelName: "disk_used"})
	if err != nil {
		t.Error(err)
	}

	if len(metrics) != 2 {
		t.Errorf("len(metrics) == %v, want %v", len(metrics), 2)
	}

	for _, m := range metrics {
		if m.Labels()["item"] != "/home" && m.Labels()["item"] != "/srv" {
			t.Errorf("m.Labels()[item] == %v, want %v or %v", m.Labels()["item"], "/home", "/srv")
		}
	}

	metrics, err = db.Metrics(map[string]string{types.LabelName: "disk_used", "item": "/srv"})
	if err != nil {
		t.Error(err)
	}

	if len(metrics) != 1 {
		t.Errorf("len(metrics) == %v, want %v", len(metrics), 1)
	}

	if !reflect.DeepEqual(metrics[0].Labels(), labels3) {
		t.Errorf("metrics[0].Labels() == %v, want %v", metrics[0].Labels(), labels3)
	}
}

func TestPoints(t *testing.T) {
	labels := map[string]string{
		types.LabelName: "cpu_used",
	}
	db := New("test store", time.Hour, time.Hour)
	m := db.metricGetOrCreate(labels)

	t0 := time.Now().Add(-60 * time.Second)
	t1 := t0.Add(10 * time.Second)
	t2 := t0.Add(20 * time.Second)
	p0 := types.Point{Time: t0, Value: 42.0}
	p1 := types.Point{Time: t1, Value: -88}
	p2 := types.Point{Time: t2, Value: 13.37}

	db.PushPoints(t.Context(), []types.MetricPoint{
		{Point: p0, Labels: labels},
	})

	if len(db.points.pointsPerMetric) != 1 {
		t.Errorf("len(db.points) == %v, want %v", len(db.points.pointsPerMetric), 1)
	}

	if db.points.count(m.metricID) != 1 {
		t.Errorf("len(db.points[%v]) == %v, want %v", m.metricID, db.points.count(m.metricID), 1)
	}

	p := db.points.getPoint(m.metricID, 0)
	if diff := cmp.Diff(p, p0, timeComparer); diff != "" {
		t.Errorf("Unexpected value for db.points[%v][0]:\n%v", m.metricID, diff)
	}

	db.PushPoints(t.Context(), []types.MetricPoint{
		{Point: p1, Labels: labels},
	})
	db.PushPoints(t.Context(), []types.MetricPoint{
		{Point: p2, Labels: labels},
	})

	if len(db.points.pointsPerMetric) != 1 {
		t.Errorf("len(db.points) == %v, want %v", len(db.points.pointsPerMetric), 1)
	}

	if db.points.count(m.metricID) != 3 {
		t.Errorf("len(db.points[%v]) == %v, want %v", m.metricID, db.points.count(m.metricID), 3)
	}

	points, err := m.Points(t0, t2)
	if err != nil {
		t.Error(err)
	}

	if len(points) != 3 {
		t.Errorf("len(points) == %v, want %v", len(points), 3)
	}

	points, err = m.Points(t1, t1)
	if err != nil {
		t.Error(err)
	}

	if len(points) != 1 {
		t.Fatalf("len(points) == %v, want %v", len(points), 1)
	}

	if diff := cmp.Diff(points[0], p1, timeComparer); diff != "" {
		t.Errorf("Unexpected value for db.points[0]:\n%v", diff)
	}

	oldest, youngest := db.points.pointsPerMetric[m.metricID].timeBounds()

	if diff := cmp.Diff(oldest, t0); diff != "" {
		t.Errorf("Unexpected oldest timestamp:\n%v", diff)
	}

	if diff := cmp.Diff(youngest, t2); diff != "" {
		t.Errorf("Unexpected youngest timestamp:\n%v", diff)
	}

	db.points.dropPoints(m.metricID)

	if len(db.points.pointsPerMetric) != 0 {
		t.Errorf("len(points) == %v, want %v", len(db.points.pointsPerMetric), 0)
	}

	// p0 will be dropped by db.points.setPoints()
	err = db.points.pushPoint(m.metricID, p0)
	if err != nil {
		t.Error(err)
	}

	err = db.points.setPoints(m.metricID, []types.Point{p1, p2})
	if err != nil {
		t.Error(err)
	}

	// Appending some unordered points

	p3 := types.Point{Time: t1.Add(5 * time.Second), Value: 15}

	err = db.points.pushPoint(m.metricID, p3)
	if err != nil {
		t.Error(err)
	}

	p4 := types.Point{Time: t2.Add(5 * time.Second), Value: 25}

	err = db.points.pushPoint(m.metricID, p4)
	if err != nil {
		t.Error(err)
	}

	points, err = db.points.getPoints(m.metricID)
	if err != nil {
		t.Error(err)
	}

	if diff := cmp.Diff(points, []types.Point{p1, p2, p3, p4}, timeComparer); diff != "" {
		t.Errorf("Unexpected points:\n%v", diff)
	}

	point := db.points.getPoint(m.metricID, 2)
	if diff := cmp.Diff(point, p3, timeComparer); diff != "" {
		t.Errorf("Unexpected point at index 2:\n%v", diff)
	}

	if count := db.points.count(m.metricID); count != 4 {
		t.Errorf("Unexpected point count: want %d, got %d", 4, count)
	}
}

func makeMetric(b *testing.B, rnd *rand.Rand, labelCount int) map[string]string {
	b.Helper()

	metricNames := []string{
		"cpu_used",
		"agent_config_warning",
		"container_cpu_used",
		"container_io_write_bytes",
		"go_memstats_mcache_inuse_bytes",
		"mysql_cache_result_qcache_not_cached",
		"namedprocess_namegroup_memory_bytes",
		"net_bits_recv",
		"node_network_receive_bytes_total",
		"process_cpu_user",
		"process_resident_memory_bytes",
		"prometheus_remote_storage_samples_in_total",
		"redis_uptime",
		"uptime",
	}

	labelNames := []string{
		"action",
		"address",
		"alias",
		"application",
		"build_date",
		"collector",
		"container",
		"container_name",
		"core",
		"cpu",
		"db",
		"fstype",
		"generation",
		"golang_version",
		"goversion",
		"groupname",
		"handler",
		"hrStorageDescr",
		"hrStorageType",
		"instance",
		"instance_uuid",
		"item",
		"job",
		"method",
		"mode",
		"nodename",
		"op",
		"port",
		"quantile",
		"role",
		"scrape_instance",
		"scrape_job",
		"state",
		"status",
		"status_code",
		"type",
		"url",
	}

	lbls := make(map[string]string, labelCount)

	for n := range labelCount {
		if n == 0 {
			lbls[types.LabelName] = metricNames[rnd.Intn(len(metricNames))]
		} else {
			name := labelNames[rnd.Intn(len(labelNames))]
			value := fmt.Sprintf(
				"%s-%s-%d",
				labelNames[rnd.Intn(len(labelNames))],
				labelNames[rnd.Intn(len(labelNames))][:2],
				rnd.Intn(1000),
			)
			lbls[name] = value
		}
	}

	return lbls
}

func makeMetrics(b *testing.B, rnd *rand.Rand, metricsCount int, labelsCount int) []map[string]string {
	b.Helper()

	metricsLabels := make([]map[string]string, 0, metricsCount)

	for range metricsCount {
		var lbls map[string]string

		for try := range 3 {
			lbls = makeMetric(b, rnd, labelsCount)
			duplicate := false

			for _, v := range metricsLabels {
				if reflect.DeepEqual(v, lbls) {
					duplicate = true

					break
				}
			}

			if !duplicate {
				break
			}

			if duplicate && try == 2 {
				b.Logf("A metric will be duplicated")
			}
		}

		metricsLabels = append(metricsLabels, lbls)
	}

	return metricsLabels
}

func Benchmark_metricGetOrCreate(b *testing.B) {
	tests := []struct {
		name        string
		metricCount int
		labelsCount int
	}{
		{
			name:        "one",
			metricCount: 1,
			labelsCount: 1,
		},
		{
			name:        "7 metrics",
			metricCount: 7,
			labelsCount: 1,
		},
		{
			name:        "100 metrics",
			metricCount: 100,
			labelsCount: 2,
		},
		{
			name:        "1000 metrics",
			metricCount: 1000,
			labelsCount: 3,
		},
		{
			name:        "4000 metrics",
			metricCount: 4000,
			labelsCount: 3,
		},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			db := New("test store", time.Hour, time.Hour)

			rnd := rand.New(rand.NewSource(42)) //nolint:gosec
			metricsLabels := makeMetrics(b, rnd, tt.metricCount, tt.labelsCount)

			// we do not benchmark first time add
			for _, lbls := range metricsLabels {
				db.metricGetOrCreate(lbls)
			}

			b.ResetTimer()

			for b.Loop() {
				for _, lbls := range metricsLabels {
					db.metricGetOrCreate(lbls)
				}
			}
		})
	}
}

func TestStore_run(t *testing.T) {
	type metricWant struct {
		labels map[string]string
		points []types.Point
	}

	t0 := time.Now()
	testMinTime := t0.Add(-time.Hour)
	testMaxTime := t0.Add(72 * time.Hour)

	steps := []struct {
		pushPoints []types.MetricPoint
		now        time.Time
		want       []metricWant
	}{
		{
			pushPoints: []types.MetricPoint{
				{
					Point:  types.Point{Time: t0, Value: 5},
					Labels: map[string]string{types.LabelName: "metric1"},
				},
			},
			now: t0,
			want: []metricWant{
				{
					labels: map[string]string{types.LabelName: "metric1"},
					points: []types.Point{{Time: t0, Value: 5}},
				},
			},
		},
		{
			pushPoints: []types.MetricPoint{
				{
					Point:  types.Point{Time: t0, Value: 5},
					Labels: map[string]string{types.LabelName: "metric2"},
				},
				{
					Point:  types.Point{Time: t0.Add(time.Hour), Value: 6},
					Labels: map[string]string{types.LabelName: "metric2"},
				},
				{
					Point:  types.Point{Time: t0.Add(2 * time.Hour), Value: 7},
					Labels: map[string]string{types.LabelName: "metric2"},
				},
			},
			now: t0.Add(3 * time.Hour),
			want: []metricWant{
				{
					labels: map[string]string{types.LabelName: "metric1"},
					points: []types.Point{{Time: t0, Value: 5}},
				},
				{
					labels: map[string]string{types.LabelName: "metric2"},
					points: []types.Point{
						{Time: t0, Value: 5},
						{Time: t0.Add(time.Hour), Value: 6},
						{Time: t0.Add(2 * time.Hour), Value: 7},
					},
				},
			},
		},
		{
			pushPoints: []types.MetricPoint{
				{
					Point:  types.Point{Time: t0.Add(20 * time.Hour), Value: 5},
					Labels: map[string]string{types.LabelName: "metric3"},
				},
				{
					Point:  types.Point{Time: t0.Add(21 * time.Hour), Value: 6},
					Labels: map[string]string{types.LabelName: "metric3"},
				},
				{
					Point:  types.Point{Time: t0.Add(22 * time.Hour), Value: 7},
					Labels: map[string]string{types.LabelName: "metric3"},
				},
			},
			now: t0.Add(24 * time.Hour),
			want: []metricWant{
				{
					labels: map[string]string{types.LabelName: "metric1"},
					points: []types.Point{},
				},
				{
					labels: map[string]string{types.LabelName: "metric2"},
					points: []types.Point{
						{Time: t0.Add(time.Hour), Value: 6},
						{Time: t0.Add(2 * time.Hour), Value: 7},
					},
				},
				{
					labels: map[string]string{types.LabelName: "metric3"},
					points: []types.Point{
						{Time: t0.Add(20 * time.Hour), Value: 5},
						{Time: t0.Add(21 * time.Hour), Value: 6},
						{Time: t0.Add(22 * time.Hour), Value: 7},
					},
				},
			},
		},
		{
			pushPoints: []types.MetricPoint{
				{
					Point:  types.Point{Time: t0.Add(25 * time.Hour), Value: 50},
					Labels: map[string]string{types.LabelName: "metric1"},
				},
				{
					Point:  types.Point{Time: t0.Add(25 * time.Hour), Value: 50},
					Labels: map[string]string{types.LabelName: "metric2"},
				},
				{
					Point:  types.Point{Time: t0.Add(25 * time.Hour), Value: 50},
					Labels: map[string]string{types.LabelName: "metric3"},
				},
			},
			now: t0.Add(25 * time.Hour),
			want: []metricWant{
				{
					labels: map[string]string{types.LabelName: "metric1"},
					points: []types.Point{
						{Time: t0.Add(25 * time.Hour), Value: 50},
					},
				},
				{
					labels: map[string]string{types.LabelName: "metric2"},
					points: []types.Point{
						{Time: t0.Add(2 * time.Hour), Value: 7},
						{Time: t0.Add(25 * time.Hour), Value: 50},
					},
				},
				{
					labels: map[string]string{types.LabelName: "metric3"},
					points: []types.Point{
						{Time: t0.Add(20 * time.Hour), Value: 5},
						{Time: t0.Add(21 * time.Hour), Value: 6},
						{Time: t0.Add(22 * time.Hour), Value: 7},
						{Time: t0.Add(25 * time.Hour), Value: 50},
					},
				},
			},
		},
		{
			pushPoints: nil,
			now:        t0.Add(50 * time.Hour),
			want:       []metricWant{},
		},
		{
			pushPoints: []types.MetricPoint{
				{
					Point:  types.Point{Time: t0.Add(51 * time.Hour), Value: 5},
					Labels: map[string]string{types.LabelName: "metric1"},
				},
			},
			now: t0.Add(51 * time.Hour),
			want: []metricWant{
				{
					labels: map[string]string{types.LabelName: "metric1"},
					points: []types.Point{{Time: t0.Add(51 * time.Hour), Value: 5}},
				},
			},
		},
		{
			pushPoints: []types.MetricPoint{
				{
					Point:  types.Point{Time: t0.Add(51 * time.Hour).Add(time.Second), Value: math.Float64frombits(value.StaleNaN)},
					Labels: map[string]string{types.LabelName: "metric1"},
				},
			},
			now:  t0.Add(51 * time.Hour).Add(time.Second),
			want: []metricWant{},
		},
	}

	store := New("test store", 24*time.Hour, 25*time.Hour)

	for i, tt := range steps {
		t.Run(fmt.Sprintf("step-%d", i), func(t *testing.T) {
			store.nowFunc = func() time.Time {
				return tt.now
			}
			store.PushPoints(t.Context(), tt.pushPoints)
			store.run(tt.now)

			for _, want := range tt.want {
				result, err := store.Metrics(want.labels)
				if err != nil {
					t.Fatal(err)
				}

				if len(result) != 1 {
					t.Fatalf("len(result) = %d, want 1", len(result))
				}

				got, err := result[0].Points(testMinTime, testMaxTime)
				if err != nil {
					t.Fatal(err)
				}

				if diff := cmp.Diff(want.points, got, timeComparer); diff != "" {
					t.Errorf("Points of %v mismatch: (-want +got):\n%s", result[0].Labels(), diff)
				}
			}

			if len(tt.want) != store.MetricsCount() {
				t.Errorf("MetricsCount() = %d, want %d", store.MetricsCount(), len(tt.want))
			}
		})
	}
}
