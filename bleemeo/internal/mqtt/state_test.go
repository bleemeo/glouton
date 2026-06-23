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

package mqtt

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
)

func doesBlock(fn func()) bool {
	var completed atomic.Bool

	go func() {
		fn()
		completed.Store(true)
	}()

	// Wait for the operation to either complete or durably block.
	synctest.Wait()

	return !completed.Load()
}

// TestOnConnectIsLockFree ensures OnConnect does not require rs.l to deliver its
// event. During a reload the paho connection survives but no consumer runs; the
// next run takes rs.l. If the callback held rs.l while sending, that would
// deadlock the whole connector.
func TestOnConnectIsLockFree(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		rs := NewReloadState(ReloadStateOptions{}).(*reloadState) //nolint:forcetypeassert

		// Hold the lock to stand in for the new run taking rs.l.
		rs.l.Lock()
		defer rs.l.Unlock()

		go func() {
			<-rs.ConnectChannel()
		}()

		if doesBlock(func() { rs.OnConnect(nil) }) {
			t.Fatal("OnConnect blocked while rs.l was held: it must not take the lock to send")
		}
	})
}

// TestOnNotificationIsLockFree ensures OnNotification never blocks, even when
// rs.l is held: it must drop (non-blocking send) without touching the lock.
func TestOnNotificationIsLockFree(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		rs := NewReloadState(ReloadStateOptions{}).(*reloadState) //nolint:forcetypeassert

		rs.l.Lock()
		defer rs.l.Unlock()

		if doesBlock(func() { rs.OnNotification(nil, nil) }) {
			t.Fatal("OnNotification blocked while rs.l was held: it must not take the lock")
		}
	})
}
