// Copyright 2015-2019 Bleemeo
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

package types

import (
	"time"
)

// Status is an enumeration of status (ok, warning, critical, unknown).
type Status uint8

// Possible values for the StatusValue enum.
const (
	StatusUnset Status = iota
	StatusOk
	StatusWarning
	StatusCritical
	StatusUnknown
)

// IsSet return true if the status is set.
func (s Status) IsSet() bool {
	return s != StatusUnset
}

func (s Status) String() string {
	switch s {
	case StatusUnset:
		return "unset"
	case StatusOk:
		return "ok"
	case StatusWarning:
		return "warning"
	case StatusCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// NagiosCode return the Nagios value for a Status.
func (s Status) NagiosCode() int {
	switch s {
	case StatusOk:
		return 0
	case StatusWarning:
		return 1
	case StatusCritical:
		return 2
	default:
		return 3
	}
}

// FromNagios return a Status from a Nagios status code.
func FromNagios(value int) Status {
	switch value {
	case 0:
		return StatusOk
	case 1:
		return StatusWarning
	case 2:
		return StatusCritical
	default:
		return StatusUnknown
	}
}

// Metric represent a metric object.
type Metric interface {
	// Labels returns labels of the metric
	Labels() map[string]string

	// Points returns points between the two given time range (boundary are included).
	Points(start, end time.Time) ([]PointStatus, error)
}

// Point is the value of one metric at a given time.
type Point struct {
	Time  time.Time
	Value float64
}

// MetricPoint is one point for one metrics (identified by labels).
type MetricPoint struct {
	PointStatus
	Labels map[string]string
}

// PointStatus is a point (value) and a status.
type PointStatus struct {
	Point
	StatusDescription
}

// StatusDescription store a service/metric status with an optional description.
type StatusDescription struct {
	CurrentStatus     Status
	StatusDescription string
}
