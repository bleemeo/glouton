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

package logprocessing

import (
	"sync"
	"time"
)

// ringCounter is kind of a ring-buffer, but serves for storing
// a count for each second of a sliding time window.
// Its precision is hard-coded (1s), but its size is configurable.
// When the Add method is called, the given delta is added
// to the bucket that corresponds to the current second.
// The buckets can be represented as a ring, so when we want to increment
// the counter for, say, the 61st second after the first call, we start
// a new loop over the buckets and replace the content of the 1st one.
type ringCounter struct {
	size         int
	t0           int64
	l            sync.Mutex
	buckets      []int
	lastUpdateAt int64
}

// newRingCounter initialises a new "throughput meter".
// Since its granularity is 1 second, the size must be given as seconds as well.
// The size must be strictly positive, otherwise it panics.
// The Total method will then return the sum of the data recorded in a sliding time window of this width.
func newRingCounter(size int) *ringCounter { //nolint:unparam
	if size < 1 {
		panic("ring counter size must be strictly positive")
	}

	return &ringCounter{
		size:    size,
		buckets: make([]int, size),
	}
}

// Add records the given delta for the current second.
func (rc *ringCounter) Add(delta int) {
	// We want to insert the delta for the time when the method was called,
	// so storing the time now rather than after acquiring the lock
	// avoids distorting the measurement.
	now := time.Now().Unix()

	rc.l.Lock()
	defer rc.l.Unlock()

	if rc.t0 == 0 {
		rc.t0 = now
		rc.lastUpdateAt = now
	}

	rc.discardOutdatedValues(now)

	idx := int(now-rc.t0) % rc.size
	rc.buckets[idx] += delta
	rc.lastUpdateAt = now
}

// Total returns the sum of all the data recorded during the last `size` seconds.
func (rc *ringCounter) Total() int {
	rc.l.Lock()
	defer rc.l.Unlock()

	now := time.Now().Unix()
	// If no data has been recorded for a moment until now,
	// we need to flush the buckets corresponding to this period
	// before evaluating the buckets' sum.
	rc.discardOutdatedValues(now)

	rc.lastUpdateAt = now

	var total int

	for i := range rc.buckets {
		total += rc.buckets[i]
	}

	return total
}

func (rc *ringCounter) discardOutdatedValues(now int64) {
	idx := int(now-rc.t0) % rc.size
	lastIdx := int(rc.lastUpdateAt-rc.t0) % rc.size

	// If the latest update is older than the total size,
	// it means that all the data is stale.
	if int(now-rc.lastUpdateAt) >= rc.size {
		rc.resetRange(0, rc.size-1)
	} else if idx != lastIdx {
		rc.resetRange(min(lastIdx+1, rc.size-1), idx)
	}
}

func (rc *ringCounter) resetRange(from, to int) {
	if from > to {
		rc.resetRange(from, rc.size-1)
		rc.resetRange(0, to)
	} else {
		for i := from; i <= to; i++ {
			rc.buckets[i] = 0
		}
	}
}
