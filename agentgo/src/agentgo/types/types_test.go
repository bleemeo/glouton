// Copyright 2015-2018 Bleemeo
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

// Test of types module

package types

import (
	"testing"
)

func TestAddFields(t *testing.T) {
	accumulator := Accumulator{}
	fields := map[string]interface{}{
		"metric1": 1.26,
		"metric2": 2.58,
	}
	accumulator.AddGauge("test", fields, nil)
	if len(accumulator.GetMetricPointSlice()) != 2 {
		t.Errorf("len of accumulator = %d, want 2", len(accumulator.GetMetricPointSlice()))
	}
	fields = map[string]interface{}{
		"metric3": 1.245,
	}
	accumulator.AddGauge("test", fields, nil)
	if len(accumulator.GetMetricPointSlice()) != 3 {
		t.Errorf("len of accumulator = %d, want 3", len(accumulator.GetMetricPointSlice()))
	}

}
