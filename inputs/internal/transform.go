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

package internal

// Units a cumulative duration counter can count in, as the number of them in one second.
// They are what AvgDuration divides by to report seconds whatever the input reports.
const (
	NsPerSecond = 1e9
	UsPerSecond = 1e6
	MsPerSecond = 1e3
)

// AvgDuration replaces the rate of a cumulative duration counter by the average duration
// of one operation, in seconds: the duration accumulated per second divided by the number
// of operations completed per second. Both fields must already be rates -- the accumulator
// differentiates them -- and the raw duration rate is dropped, being meaningless on its own.
//
// unitDivisor is how many of the duration's own units make a second: NsPerSecond when the
// counter counts nanoseconds, MsPerSecond when it counts milliseconds.
//
// Nothing is written when either field is missing, or when no operation completed during
// the period (which would otherwise be a division by zero). A counter that resets between
// two gathers is already handled upstream: the accumulator's differentiation drops it
// instead of reporting a negative rate, so it simply won't be present in fields. The raw
// duration is dropped in every one of those cases all the same, being meaningless on its
// own.
func AvgDuration(fields map[string]float64, durationField, countField, outputName string, unitDivisor float64) {
	durationRate, hasDuration := fields[durationField]
	countRate, hasCount := fields[countField]

	delete(fields, durationField)

	if hasDuration && hasCount && countRate > 0 {
		fields[outputName] = durationRate / countRate / unitDivisor
	}
}
