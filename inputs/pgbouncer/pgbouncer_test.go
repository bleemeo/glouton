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

package pgbouncer

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/types"
)

// collectFinalMetrics replicates the measurement/field -> final metric name
// convention applied downstream (inputs.Accumulator.addMetrics): the metric
// name is the field name alone when the measurement was renamed to "", or
// "<measurement>_<field>" otherwise.
func collectFinalMetrics(store *internal.StoreAccumulator) map[string]float64 {
	got := make(map[string]float64)

	for _, m := range store.Measurement {
		for field, value := range m.Fields {
			name := field
			if m.Name != "" {
				name = m.Name + "_" + field
			}

			switch v := value.(type) {
			case float64:
				got[name] = v
			case uint64:
				got[name] = float64(v)
			}
		}
	}

	return got
}

// newStore returns an empty accumulator store. It exists so the zero value is written out
// once rather than in every test, which also keeps exhaustruct quiet.
func newStore() *internal.StoreAccumulator {
	return &internal.StoreAccumulator{Measurement: nil, Errors: nil}
}

func newAccumulator(store *internal.StoreAccumulator) internal.Accumulator {
	return internal.Accumulator{
		RenameGlobal:     renameGlobal,
		TransformMetrics: transformMetrics,
		DifferentiatedMetrics: []string{
			"total_query_count",
			"total_query_time",
			"total_received",
			"total_sent",
		},
		Accumulator: store,
	}
}

func assertMetrics(t *testing.T, got map[string]float64, want map[string]float64) {
	t.Helper()

	for name, value := range want {
		gotValue, ok := got[name]
		if !ok {
			t.Errorf("metric %q not emitted, got metrics: %v", name, got)

			continue
		}

		if math.Abs(gotValue-value) > 0.0001 {
			t.Errorf("metric %q == %v, want %v", name, gotValue, value)
		}
	}
}

// TestRenamePipelineStats exercises the "pgbouncer" measurement (SHOW
// STATS), where cumulative counters are converted to per-second rates then
// renamed (total_query_count -> query, total_received -> received_bytes,
// total_sent -> sent_bytes) and total_query_time is combined with
// total_query_count into an average query_time_seconds.
func TestRenamePipelineStats(t *testing.T) {
	store := newStore()
	acc := newAccumulator(store)

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)

	acc.PrepareGather()
	acc.AddFields("pgbouncer", map[string]any{
		"total_query_count": int64(1000),
		"total_query_time":  int64(5000000),
		"total_received":    int64(200000),
		"total_sent":        int64(100000),
	}, nil, t0)

	// Discard the first gather: every field is a differentiated counter, so
	// it has no rate yet (no history).
	store.Measurement = nil

	acc.PrepareGather()
	acc.AddFields("pgbouncer", map[string]any{
		"total_query_count": int64(1000 + 50),         // rate = 5/s
		"total_query_time":  int64(5000000 + 5000000), // rate = 500 000/s
		"total_received":    int64(200000 + 1000),     // rate = 100/s
		"total_sent":        int64(100000 + 500),      // rate = 50/s
	}, nil, t1)

	got := collectFinalMetrics(store)

	// query_time_seconds = queryTimeRate / queryCountRate / 1e6 = 500 000 / 5 / 1e6 = 0.1
	assertMetrics(t, got, map[string]float64{
		"pgbouncer_query":              5,
		"pgbouncer_received_bytes":     100,
		"pgbouncer_sent_bytes":         50,
		"pgbouncer_query_time_seconds": 0.1,
	})

	// The raw duration rate is meaningless by itself and must not be emitted alongside
	// the average derived from it.
	if value, ok := got["pgbouncer_total_query_time"]; ok {
		t.Errorf("raw duration rate should have been dropped, got value %v", value)
	}
}

// TestRenamePipelinePools exercises the "pgbouncer_pools" measurement (SHOW
// POOLS), which is untouched by transformMetrics: fields are emitted as-is.
func TestRenamePipelinePools(t *testing.T) {
	store := newStore()
	acc := newAccumulator(store)

	acc.PrepareGather()
	acc.AddFields("pgbouncer_pools", map[string]any{
		"cl_active":  int64(3),
		"cl_waiting": int64(1),
		"sv_active":  int64(2),
		"sv_idle":    int64(4),
		"maxwait":    int64(7),
	}, nil, time.Now())

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"pgbouncer_pools_cl_active":  3,
		"pgbouncer_pools_cl_waiting": 1,
		"pgbouncer_pools_sv_active":  2,
		"pgbouncer_pools_sv_idle":    4,
		"pgbouncer_pools_maxwait":    7,
	})
}

// identitiesFor returns "db/user" for every stored row of a measurement, in the order they
// were emitted. Those are kept as real labels, so this reads them back from the tags rather
// than from the item.
func identitiesFor(store *internal.StoreAccumulator, measurement string) []string {
	ids := make([]string, 0, len(store.Measurement))

	for _, m := range store.Measurement {
		if m.Name == measurement {
			ids = append(ids, m.Tags["db"]+"/"+m.Tags["user"])
		}
	}

	return ids
}

// tagsFor returns the tags of the first stored row of a measurement.
func tagsFor(store *internal.StoreAccumulator, measurement string) map[string]string {
	for _, m := range store.Measurement {
		if m.Name == measurement {
			return m.Tags
		}
	}

	return nil
}

// poolTags are the tags telegraf's pgbouncer plugin attaches to pgbouncer_pools: the base
// {server, db} plus user and pool_mode.
func poolTags(db, user, poolMode string) map[string]string {
	return map[string]string{
		"server":    "host=127.0.0.1 port=6432 user=pgbouncer dbname=pgbouncer",
		"db":        db,
		"user":      user,
		"pool_mode": poolMode,
	}
}

// TestLabelsSeparatePoolRows covers the collision that made 8 of the 9 published metrics
// error on every /metrics scrape: SHOW POOLS returns one row per database and user, always
// including PgBouncer's own admin pseudo-database, so without db and user in the series
// identity both rows became the same series.
func TestLabelsSeparatePoolRows(t *testing.T) {
	store := newStore()
	acc := newAccumulator(store)

	acc.PrepareGather()
	acc.AddFields("pgbouncer_pools", map[string]any{"sv_idle": int64(4)},
		poolTags("bleemeo", "app", "session"), time.Now())
	acc.AddFields("pgbouncer_pools", map[string]any{"sv_idle": int64(0)},
		poolTags("pgbouncer", "pgbouncer", "statement"), time.Now())

	got := identitiesFor(store, "pgbouncer_pools")
	want := []string{"bleemeo/app", "pgbouncer/pgbouncer"}

	if len(got) != len(want) {
		t.Fatalf("got %d pool rows (%v), want %d", len(got), got, len(want))
	}

	for i, id := range want {
		if got[i] != id {
			t.Errorf("pool row %d db/user = %q, want %q", i, got[i], id)
		}
	}

	// Two rows that differ must not share an identity, or one silently replaces the other.
	if got[0] == got[1] {
		t.Errorf("both pool rows got the same db/user %q, they would collide", got[0])
	}
}

// TestLabelsSeparateStatsRows is the same for SHOW STATS, which is keyed on the database
// alone: db must be kept, and no "user" label invented for a measurement that has none.
func TestLabelsSeparateStatsRows(t *testing.T) {
	store := newStore()
	acc := newAccumulator(store)

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)
	server := "host=127.0.0.1 port=6432 user=pgbouncer dbname=pgbouncer"

	for i, at := range []time.Time{t0, t1} {
		acc.PrepareGather()
		acc.AddFields("pgbouncer", map[string]any{"total_query_count": int64(10)},
			map[string]string{"server": server, "db": "bleemeo"}, at)
		acc.AddFields("pgbouncer", map[string]any{"total_query_count": int64(20)},
			map[string]string{"server": server, "db": "pgbouncer"}, at)

		if i == 0 {
			// Differentiated counters have no rate on the first gather.
			store.Measurement = nil
		}
	}

	got := identitiesFor(store, "pgbouncer")
	// SHOW STATS carries no user tag, so the user half is empty rather than fabricated.
	want := []string{"bleemeo/", "pgbouncer/"}

	if len(got) != len(want) {
		t.Fatalf("got %d stats rows (%v), want %d", len(got), got, len(want))
	}

	for i, id := range want {
		if got[i] != id {
			t.Errorf("stats row %d db/user = %q, want %q", i, got[i], id)
		}
	}
}

// TestDropsServerAndPoolModeTags pins which tags survive as labels. "server" is the same
// connection string on every row and would put host, port and dbname into a label;
// "pool_mode" describes a pool rather than identifying one, so keeping it would start a new
// series whenever an operator changes a pool's mode. The item is left unset on purpose --
// it is the service instance, not a place to concatenate a database and a user.
func TestDropsServerAndPoolModeTags(t *testing.T) {
	store := newStore()
	acc := newAccumulator(store)

	acc.PrepareGather()
	acc.AddFields("pgbouncer_pools", map[string]any{"sv_idle": int64(1)}, map[string]string{
		"server": "host=10.0.0.1 port=6432 dbname=secret", "db": "bleemeo",
		"user": "app", "pool_mode": "transaction",
	}, time.Now())

	tags := tagsFor(store, "pgbouncer_pools")
	if tags == nil {
		t.Fatal("no pgbouncer_pools row stored")
	}

	for _, kept := range []string{"db", "user"} {
		if tags[kept] == "" {
			t.Errorf("label %q should have been kept, tags: %v", kept, tags)
		}
	}

	for _, dropped := range []string{"server", "pool_mode"} {
		if value, ok := tags[dropped]; ok {
			t.Errorf("tag %q should have been dropped, got %q", dropped, value)
		}
	}

	if item, ok := tags[types.LabelItem]; ok {
		t.Errorf("item should be left to the service instance, got %q", item)
	}

	// Nothing from the connection string may leak into any label.
	for key, value := range tags {
		for _, secret := range []string{"10.0.0.1", "6432", "secret"} {
			if strings.Contains(value, secret) {
				t.Errorf("label %s=%q leaks %q from the connection string", key, value, secret)
			}
		}
	}
}
