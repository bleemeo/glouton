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

import (
	"sync"
	"time"

	"github.com/bleemeo/glouton/logger"
)

// ringCounter stores a count per second over a sliding window (1s precision,
// configurable size). Add adds delta to the bucket for the current second.
type ringCounter struct {
	size         int
	t0           int64
	l            sync.Mutex
	buckets      []int
	lastUpdateAt int64
}

// newRingCounter creates a counter over a window of size seconds (must be > 0,
// defaults to 60 mirroring the old Fluent Bit rate(...[1m])).
func newRingCounter(size int) *ringCounter {
	if size < 1 {
		size = 60

		logger.V(1).Printf("ring counter size must be strictly positive, defaults to 60 seconds")
	}

	return &ringCounter{
		size:    size,
		buckets: make([]int, size),
	}
}

// Add records the given delta for the current second.
func (rc *ringCounter) Add(delta int) {
	// Capture the time before locking, so contention doesn't skew the measurement.
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

	// All data is stale if the latest update is older than the window size.
	if int(now-rc.lastUpdateAt) >= rc.size {
		rc.resetRange(0, rc.size-1)
	} else if idx != lastIdx {
		rc.resetRange((lastIdx+1)%rc.size, idx)
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
