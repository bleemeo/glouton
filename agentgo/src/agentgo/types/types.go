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

// Types for agentgo

package types

import (
	"time"
)

// Metric represent a metric object
type Metric interface {
	// Labels returns labels of the metric
	Labels() map[string]string

	// Points returns points between the two given time range (boundary are included).
	Points(start, end time.Time) ([]Point, error)
}

// Point is the value of one metric at a given time
type Point struct {
	Time  time.Time
	Value float64
}
