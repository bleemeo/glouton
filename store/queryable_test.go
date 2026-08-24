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

package store

import (
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"

	"github.com/prometheus/prometheus/tsdb/chunkenc"
)

// TestSeriesSampleSeek checks Seek on a fresh iterator, which is at offset -1
// because no Next was called yet.
func TestSeriesSampleSeek(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	data := []types.Point{
		{Time: t0, Value: 1},
		{Time: t0.Add(time.Minute), Value: 2},
		{Time: t0.Add(2 * time.Minute), Value: 3},
	}

	cases := []struct {
		name       string
		data       []types.Point
		seekTo     time.Time
		wantType   chunkenc.ValueType
		wantValue  float64
		checkValue bool
	}{
		{
			name:       "before first point",
			data:       data,
			seekTo:     t0.Add(-time.Minute),
			wantType:   chunkenc.ValFloat,
			wantValue:  1,
			checkValue: true,
		},
		{
			name:       "middle point",
			data:       data,
			seekTo:     t0.Add(time.Minute),
			wantType:   chunkenc.ValFloat,
			wantValue:  2,
			checkValue: true,
		},
		{
			name:     "after last point",
			data:     data,
			seekTo:   t0.Add(time.Hour),
			wantType: chunkenc.ValNone,
		},
		{
			name:     "empty series",
			data:     nil,
			seekTo:   t0,
			wantType: chunkenc.ValNone,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			iter := series{data: tc.data}.Iterator(nil)

			if got := iter.Seek(tc.seekTo.UnixMilli()); got != tc.wantType {
				t.Fatalf("Seek() = %v, want %v", got, tc.wantType)
			}

			if tc.checkValue {
				ts, value := iter.At()
				if ts != tc.seekTo.UnixMilli() && value != tc.wantValue {
					t.Errorf("At() = (%d, %f), want value %f", ts, value, tc.wantValue)
				}
			}
		})
	}
}
