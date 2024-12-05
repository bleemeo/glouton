// Copyright 2015-2024 Bleemeo
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

type ringCounter struct {
	l            sync.Mutex
	size         int
	t0           int64
	cells        []int
	lastUpdateAt int64
}

// newRingCounter initialises a new "throughput meter".
// Since its granularity is 1 second, the size must be given as seconds as well.
// The Total method will then return the sum of the data recorded in a sliding time window of this width.
func newRingCounter(size int) *ringCounter {
	return &ringCounter{
		size:  size,
		cells: make([]int, size),
	}
}

// Inc records the given delta for the current second.
func (rc *ringCounter) Inc(delta int) {
	now := time.Now().Unix()

	rc.l.Lock()
	defer rc.l.Unlock()

	if rc.t0 == 0 {
		rc.t0 = now
		rc.lastUpdateAt = now
	}

	rc.resetOutdatedValues(now)

	idx := int(now-rc.t0) % rc.size
	rc.cells[idx] += delta
	rc.lastUpdateAt = now
}

// Total returns the sum of all the data recorded during the last `size` seconds.
func (rc *ringCounter) Total() int {
	rc.l.Lock()
	defer rc.l.Unlock()

	now := time.Now().Unix()

	rc.resetOutdatedValues(now)

	rc.lastUpdateAt = now

	var total int

	for i := range rc.cells {
		total += rc.cells[i]
	}

	return total
}

func (rc *ringCounter) resetOutdatedValues(now int64) {
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
			rc.cells[i] = 0
		}
	}
}
