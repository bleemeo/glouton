// Copyright 2015-2023 Bleemeo
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

package common

import (
	"context"
	"glouton/bleemeo/types"
	"glouton/logger"
	"time"
)

// WaitDeadline will wait for a deadline to pass.
// The getDeadline will be called ~every minutes to get newest deadline
// what is used for log message, to tell what is waiting the deadline.
func WaitDeadline(ctx context.Context, minimalDelay time.Duration, getDeadline func() (time.Time, types.DisableReason), what string) {
	deadline, reason := getDeadline()
	sleepUntil := deadline

	minimalDeadline := time.Now().Add(minimalDelay)
	if sleepUntil.Before(minimalDeadline) {
		sleepUntil = minimalDeadline
	}

	for time.Now().Before(sleepUntil) && ctx.Err() == nil {
		delay := time.Until(sleepUntil)
		if delay < 0 {
			break
		}

		if delay > 60*time.Second {
			if time.Now().Before(deadline) {
				logger.V(1).Printf(
					"%s still have to wait %v due to %v", what, delay.Truncate(time.Second), reason,
				)
			}

			delay = 60 * time.Second
		}

		select {
		case <-time.After(delay):
		case <-ctx.Done():
		}

		deadline, reason = getDeadline()
		sleepUntil = deadline

		if sleepUntil.Before(minimalDeadline) {
			sleepUntil = minimalDeadline
		}
	}
}
