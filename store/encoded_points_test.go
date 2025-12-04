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
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"
)

const pointsPerMetric = 360

func BenchmarkPointsWriting(b *testing.B) {
	t0 := time.Now()
	points := newEncodedPoints()

	for i := range b.N {
		m := uint64(i)
		for p := range pointsPerMetric {
			err := points.pushPoint(m, types.Point{
				Time:  t0.Add(time.Duration(p*10) * time.Second),
				Value: float64(p),
			})
			if err != nil {
				b.Fatalf("Metric %d / Point %d: %v", m, p, err)
			}
		}
	}
}
