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

package debouncer

import (
	"context"
	"glouton/types"
	"sync"
	"time"
)

// Debouncer make sure target function is not called too often. Run() must be called to works.
type Debouncer struct {
	target func(context.Context)
	period time.Duration
	delay  time.Duration
	ctx    context.Context //nolint:containedctx

	l          sync.Mutex
	runPending bool
	lastRun    time.Time
}

// New create a Debouncer. Two call to target won't be called with less that delay between them.
func New(ctx context.Context, target func(context.Context), delay time.Duration, period time.Duration) *Debouncer {
	return &Debouncer{
		target: target,
		period: period,
		delay:  delay,
		ctx:    ctx,
	}
}

// Trigger will run the target() and ensure target() isn't run more than once every period.
//
// Unless a previous call to target() is pending, target() won't be called before delay.
// For example after a quiet period (no call to Triger()) quickly calling Trigger() multiple
// times will result in one call to target() delay time after first call to Trigger().
//
// Trigger will always return immediately, and most likely before target() was run.
func (dd *Debouncer) Trigger() {
	dd.l.Lock()
	defer dd.l.Unlock()

	if dd.runPending {
		return
	}

	dd.runPending = true
	runAgo := time.Since(dd.lastRun)
	startDelay := dd.delay

	if runAgo < dd.period {
		startDelay = dd.period - runAgo
	}

	go func() {
		defer types.ProcessPanic()

		time.Sleep(startDelay)

		dd.l.Lock()

		dd.runPending = false
		dd.lastRun = time.Now()

		dd.l.Unlock()

		if dd.ctx.Err() != nil {
			return
		}

		dd.target(dd.ctx)
	}()
}
