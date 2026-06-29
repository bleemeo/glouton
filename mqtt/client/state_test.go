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

package client

import (
	"errors"
	"testing"
	"testing/synctest"
)

// TestOnConnectionLostIsLockFree ensures OnConnectionLost does not require rs.l
// to deliver its event. During a reload the paho connection survives but no
// consumer runs; the next run takes rs.l in client.New -> Client(). If the
// callback held rs.l while sending, that would deadlock the whole connector.
func TestOnConnectionLostIsLockFree(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		rs := NewReloadState()

		// Hold the lock to stand in for the new run calling Client()/SetClient().
		rs.l.Lock()
		defer rs.l.Unlock()

		// A consumer is ready: the only thing that could keep the callback from
		// completing is the lock we hold.
		go func() {
			<-rs.ConnectionLostChannel()
		}()

		if doesBlock(func() { rs.OnConnectionLost(nil, errors.New("boom")) }) { //nolint:err113
			t.Fatal("OnConnectionLost blocked while rs.l was held: it must not take the lock to send")
		}
	})
}
