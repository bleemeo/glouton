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

package delay

import (
	"math"
	"math/rand"
	"time"
)

// Exponential return an exponential delay. N should be the number of successive iteration/errors (counting from 1).
// The value returned is "base * powerFactor ^ n". The value is capped at maxDur.
// powerFactor should be > 1 (or the delay will be smaller and smaller).
// Exponential works at seconds resolution. Base should be > of few seconds.
func Exponential(base time.Duration, powerFactor float64, n int, maxDur time.Duration) time.Duration {
	n--
	if n < 0 {
		n = 0
	}

	baseSeconds := base.Seconds()
	seconds := baseSeconds * math.Pow(powerFactor, float64(n))

	if seconds > maxDur.Seconds() {
		seconds = maxDur.Seconds()
	}

	return time.Duration(seconds) * time.Second
}

// JitterDelay return a number between value * [1-factor; 1+factor[.
// factor should be less than 1.
func JitterDelay(baseDelay time.Duration, factor float64) time.Duration {
	valueSecond := baseDelay.Seconds()
	scale := rand.Float64() * 2 * factor //nolint:gosec
	scale += 1 - factor

	result := int(valueSecond * scale)

	return time.Duration(result) * time.Second
}

// JitterMs is the same as JitterDelay, but with millisecond precision.
func JitterMs(baseDelay time.Duration, factor float64) time.Duration {
	valueMs := baseDelay.Milliseconds()
	scale := rand.Float64() * 2 * factor //nolint:gosec
	scale += 1 - factor

	result := int64(float64(valueMs) * scale)

	return time.Duration(result) * time.Millisecond
}
