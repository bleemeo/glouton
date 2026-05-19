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

package tsdb

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"

	"github.com/prometheus/prometheus/model/labels"
)

const (
	labelName    = "__name__"
	testCPUUsed  = "cpu_used"
	testHost1    = "host1"
	testInstance = "instance"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()

	store, err := Open(Options{Path: dir, Retention: 24 * time.Hour})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	now := time.Now().Truncate(time.Second)

	points := []types.MetricPoint{
		{
			Point:  types.Point{Time: now.Add(-2 * time.Minute), Value: 1.5},
			Labels: map[string]string{labelName: testCPUUsed, testInstance: testHost1},
		},
		{
			Point:  types.Point{Time: now.Add(-1 * time.Minute), Value: 2.5},
			Labels: map[string]string{labelName: testCPUUsed, testInstance: testHost1},
		},
		{
			Point:  types.Point{Time: now, Value: 3.5},
			Labels: map[string]string{labelName: testCPUUsed, testInstance: testHost1},
		},
	}

	store.PushPoints(context.Background(), points)

	q, err := store.Querier(now.Add(-5*time.Minute).UnixMilli(), now.Add(time.Minute).UnixMilli())
	if err != nil {
		t.Fatalf("Querier: %v", err)
	}

	defer q.Close()

	matcher := labels.MustNewMatcher(labels.MatchEqual, labelName, testCPUUsed)
	ss := q.Select(context.Background(), false, nil, matcher)

	var got []float64

	for ss.Next() {
		s := ss.At()
		it := s.Iterator(nil)

		for it.Next() == 1 { // chunkenc.ValFloat == 1
			_, v := it.At()
			got = append(got, v)
		}
	}

	if err := ss.Err(); err != nil {
		t.Fatalf("Select error: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 points, got %d (%v)", len(got), got)
	}

	for i, want := range []float64{1.5, 2.5, 3.5} {
		if got[i] != want {
			t.Errorf("point %d: got %v want %v", i, got[i], want)
		}
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)

	store, err := Open(Options{Path: dir, Retention: 24 * time.Hour})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	store.PushPoints(context.Background(), []types.MetricPoint{
		{
			Point:  types.Point{Time: now, Value: 42},
			Labels: map[string]string{labelName: "x"},
		},
	})

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	store2, err := Open(Options{Path: dir, Retention: 24 * time.Hour})
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}

	defer store2.Close()

	q, err := store2.Querier(now.Add(-time.Minute).UnixMilli(), now.Add(time.Minute).UnixMilli())
	if err != nil {
		t.Fatalf("Querier: %v", err)
	}

	defer q.Close()

	matcher := labels.MustNewMatcher(labels.MatchEqual, labelName, "x")
	ss := q.Select(context.Background(), false, nil, matcher)

	var found bool

	for ss.Next() {
		s := ss.At()
		it := s.Iterator(nil)

		for it.Next() == 1 {
			_, v := it.At()
			if v == 42 {
				found = true
			}
		}
	}

	if !found {
		t.Fatalf("point pushed before Close was lost after reopen")
	}
}

func TestOpenCreatesDir(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "nested", "tsdb")

	store, err := Open(Options{Path: subdir, Retention: 24 * time.Hour})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	defer store.Close()

	if got := store.Path(); got != subdir {
		t.Errorf("Path() = %q want %q", got, subdir)
	}
}

func TestPushPointsSkipsNaN(t *testing.T) {
	dir := t.TempDir()

	store, err := Open(Options{Path: dir, Retention: 24 * time.Hour})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	defer store.Close()

	now := time.Now()

	store.PushPoints(context.Background(), []types.MetricPoint{
		{
			Point:  types.Point{Time: now, Value: nanFloat()},
			Labels: map[string]string{labelName: "y"},
		},
	})

	q, err := store.Querier(now.Add(-time.Minute).UnixMilli(), now.Add(time.Minute).UnixMilli())
	if err != nil {
		t.Fatalf("Querier: %v", err)
	}

	defer q.Close()

	matcher := labels.MustNewMatcher(labels.MatchEqual, labelName, "y")
	ss := q.Select(context.Background(), false, nil, matcher)

	for ss.Next() {
		t.Fatalf("expected no series, got %v", ss.At().Labels())
	}
}

func nanFloat() float64 {
	zero := 0.0

	return zero / zero
}
