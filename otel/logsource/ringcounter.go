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

import (
	"sync"
	"time"
)

// RingCounter is a ring-buffer of per-second counts over a sliding window (precision hard-coded to 1s).
type RingCounter struct {
	size         int
	t0           int64
	l            sync.Mutex
	buckets      []int
	lastUpdateAt int64
}

// NewRingCounter creates a RingCounter with the given size in seconds; panics if size < 1.
func NewRingCounter(size int) *RingCounter {
	if size < 1 {
		panic("ring counter size must be strictly positive")
	}

	return &RingCounter{
		size:    size,
		buckets: make([]int, size),
	}
}

// Add records the given delta for the current second.
func (rc *RingCounter) Add(delta int) {
	// Capture the time before acquiring the lock, to avoid distorting the measurement.
	now := time.Now().Unix()

	rc.l.Lock()
	defer rc.l.Unlock()

	if rc.t0 == 0 {
		rc.t0 = now
		rc.lastUpdateAt = now
	}

	rc.rebaseIfClockWentBackwards(now)
	rc.discardOutdatedValues(now)

	idx := int(now-rc.t0) % rc.size
	rc.buckets[idx] += delta
}

// Total returns the sum of all the data recorded during the last `size` seconds.
func (rc *RingCounter) Total() int {
	rc.l.Lock()
	defer rc.l.Unlock()

	now := time.Now().Unix()
	rc.rebaseIfClockWentBackwards(now)
	// Flush buckets for any gap since the last update before summing.
	rc.discardOutdatedValues(now)

	var total int

	for i := range rc.buckets {
		total += rc.buckets[i]
	}

	return total
}

// rebaseIfClockWentBackwards resets the ring and rebases t0 when the wall clock moved before it
// (NTP correction, VM snapshot restore, ...), which would otherwise make now-rc.t0 negative and
// panic on the buckets[idx] access in Add.
func (rc *RingCounter) rebaseIfClockWentBackwards(now int64) {
	if rc.t0 != 0 && now < rc.t0 {
		rc.resetRange(0, rc.size-1)
		rc.t0 = now
		rc.lastUpdateAt = now
	}
}

func (rc *RingCounter) discardOutdatedValues(now int64) {
	if now <= rc.lastUpdateAt {
		// The clock moved backward (or didn't advance) within the counter's lifetime -- rebase already
		// handles a jump before t0, so at this point now >= t0 still holds and idx stays valid. There's
		// nothing to discard for a step this small, and lastUpdateAt must NOT be regressed to now, or a
		// later genuine tick would compute its gap from this stale backward value instead of the last
		// real forward progress -- which is exactly what let a small backward jump wipe out most of the
		// window before this guard existed (now-lastUpdateAt went negative, missing the ">= size" check
		// below, then idx from the backward now landed far from lastIdx, wrapping resetRange around
		// almost the whole ring).
		return
	}

	idx := int(now-rc.t0) % rc.size
	lastIdx := int(rc.lastUpdateAt-rc.t0) % rc.size

	// All data is stale if the last update is older than the window size.
	if int(now-rc.lastUpdateAt) >= rc.size {
		rc.resetRange(0, rc.size-1)
	} else if idx != lastIdx {
		// Wrap lastIdx+1 to 0 so the just-written bucket isn't re-zeroed.
		rc.resetRange((lastIdx+1)%rc.size, idx)
	}

	rc.lastUpdateAt = now
}

func (rc *RingCounter) resetRange(from, to int) {
	if from > to {
		rc.resetRange(from, rc.size-1)
		rc.resetRange(0, to)
	} else {
		for i := from; i <= to; i++ {
			rc.buckets[i] = 0
		}
	}
}
