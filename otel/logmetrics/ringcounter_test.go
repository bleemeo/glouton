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

package logmetrics

import "testing"

// newTestRingCounter builds a ringCounter with its bucket-tracking fields set
// directly, so discardOutdatedValues can be driven with an explicit "now"
// instead of depending on the wall clock.
func newTestRingCounter(size int, t0, lastUpdateAt int64, buckets []int) *ringCounter { //nolint:unparam
	return &ringCounter{size: size, t0: t0, lastUpdateAt: lastUpdateAt, buckets: buckets}
}

func TestRingCounterDiscardSameSecond(t *testing.T) {
	t.Parallel()

	rc := newTestRingCounter(5, 100, 104, []int{0, 0, 0, 0, 7})

	rc.discardOutdatedValues(104)

	if got := rc.buckets[4]; got != 7 {
		t.Errorf("Expected bucket 4 to still hold 7 (no time passed), got %d", got)
	}
}

func TestRingCounterDiscardAdvanceNoWrap(t *testing.T) {
	t.Parallel()

	// lastIdx=1, idx=3: buckets 2 and 3 should be cleared, bucket 4 (unrelated,
	// still within the window) and bucket 1 (just-written) must survive.
	rc := newTestRingCounter(5, 100, 101, []int{0, 9, 5, 5, 3})

	rc.discardOutdatedValues(103)

	if got := rc.buckets[1]; got != 9 {
		t.Errorf("Expected bucket 1 (last updated) to survive, got %d", got)
	}

	if got := rc.buckets[2]; got != 0 {
		t.Errorf("Expected bucket 2 to be cleared, got %d", got)
	}

	if got := rc.buckets[3]; got != 0 {
		t.Errorf("Expected bucket 3 to be cleared, got %d", got)
	}

	if got := rc.buckets[4]; got != 3 {
		t.Errorf("Expected bucket 4 (untouched by this advance) to survive, got %d", got)
	}
}

// TestRingCounterDiscardWraparound is the regression test for the ring
// wraparound bug: when the current second's bucket index wraps from size-1
// back to 0, the bucket at size-1 holds data written just one second earlier
// and must survive -- only the newly-entered bucket 0 should be cleared.
func TestRingCounterDiscardWraparound(t *testing.T) {
	t.Parallel()

	rc := newTestRingCounter(5, 100, 104, []int{0, 0, 0, 0, 7})

	rc.discardOutdatedValues(105)

	if got := rc.buckets[4]; got != 7 {
		t.Errorf("Expected bucket 4 (written 1s ago) to survive the wraparound, got %d", got)
	}

	if got := rc.buckets[0]; got != 0 {
		t.Errorf("Expected bucket 0 (newly entered) to be cleared, got %d", got)
	}
}

func TestRingCounterDiscardFullyStale(t *testing.T) {
	t.Parallel()

	rc := newTestRingCounter(5, 100, 104, []int{1, 2, 3, 4, 7})

	rc.discardOutdatedValues(200)

	for i, got := range rc.buckets {
		if got != 0 {
			t.Errorf("Expected bucket %d to be cleared after a gap >= size, got %d", i, got)
		}
	}
}

func TestRingCounterAddAndTotal(t *testing.T) {
	t.Parallel()

	rc := newRingCounter(60)

	rc.Add(2)
	rc.Add(3)

	if got := rc.Total(); got != 5 {
		t.Errorf("Expected total 5, got %d", got)
	}
}
