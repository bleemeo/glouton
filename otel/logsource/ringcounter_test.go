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

package logsource

import "testing"

// newTestRingCounter builds a RingCounter with an explicit clock state for testing.
func newTestRingCounter(size int, t0, lastUpdateAt int64, buckets []int) *RingCounter { //nolint:unparam
	return &RingCounter{size: size, t0: t0, lastUpdateAt: lastUpdateAt, buckets: buckets}
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

	// Advance from idx 1 to 3: buckets 2-3 clear, 1 and 4 survive.
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

// TestRingCounterDiscardWraparound checks that wraparound clears only the newly-entered bucket, not the previous one.
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

func TestRingCounterClockWentBackwards(t *testing.T) {
	t.Parallel()

	rc := newTestRingCounter(5, 100, 104, []int{1, 2, 3, 4, 7})

	// Wall clock jumped back before t0 (NTP correction, VM snapshot restore, ...): must not
	// leave now-t0 negative (which would panic on buckets[idx] in Add).
	rc.rebaseIfClockWentBackwards(95)

	if got := rc.t0; got != 95 {
		t.Errorf("Expected t0 to be rebased to 95, got %d", got)
	}

	if got := rc.lastUpdateAt; got != 95 {
		t.Errorf("Expected lastUpdateAt to be rebased to 95, got %d", got)
	}

	for i, got := range rc.buckets {
		if got != 0 {
			t.Errorf("Expected bucket %d to be cleared on rebase, got %d", i, got)
		}
	}

	idx := int(95-rc.t0) % rc.size
	if idx < 0 || idx >= rc.size {
		t.Fatalf("idx %d out of range after rebase", idx)
	}
}

func TestRingCounterAddAndTotal(t *testing.T) {
	t.Parallel()

	rc := NewRingCounter(60)

	rc.Add(2)
	rc.Add(3)

	if got := rc.Total(); got != 5 {
		t.Errorf("Expected total 5, got %d", got)
	}
}
