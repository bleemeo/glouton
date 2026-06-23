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
	"context"
	"sync"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// ReloadState implements the types.PahoWrapper interface.
type ReloadState struct {
	l                     sync.Mutex
	client                paho.Client
	connectionLostChannel chan error
	// stopped is closed by Close() to unblock any in-flight callback.
	// The data channels are never closed (a callback fired during a reload
	// must keep its lock-free send pending until the next run consumes it).
	stopped         chan struct{}
	pendingMessages *fifo[types.Message]
}

func NewReloadState() *ReloadState {
	return &ReloadState{
		connectionLostChannel: make(chan error),
		stopped:               make(chan struct{}),
		pendingMessages:       newFifo[types.Message](maxPendingMessages),
	}
}

func (rs *ReloadState) Client() paho.Client {
	rs.l.Lock()
	defer rs.l.Unlock()

	client := rs.client

	return client
}

func (rs *ReloadState) SetClient(cli paho.Client) {
	rs.l.Lock()
	defer rs.l.Unlock()

	rs.client = cli
}

func (rs *ReloadState) OnConnectionLost(_ paho.Client, err error) {
	// Don't hold rs.l while sending: paho may call this during the reload
	// window when no consumer is running. Blocking here without the lock lets
	// the next run construct its client (which takes rs.l) and drain the event.
	select {
	case rs.connectionLostChannel <- err:
	case <-rs.stopped:
	}
}

func (rs *ReloadState) ConnectionLostChannel() <-chan error {
	return rs.connectionLostChannel
}

func (rs *ReloadState) AddPendingMessage(ctx context.Context, m types.Message, shouldWait bool) bool {
	if shouldWait {
		rs.pendingMessages.Put(ctx, m)

		return true
	}

	return rs.pendingMessages.PutNoWait(m)
}

func (rs *ReloadState) PendingMessage(ctx context.Context) (m types.Message, open bool) {
	return rs.pendingMessages.Get(ctx)
}

func (rs *ReloadState) PendingMessagesCount() int {
	return rs.pendingMessages.Len()
}

func (rs *ReloadState) Close() {
	if rs.client == nil {
		return
	}

	// Unblock any in-flight callback (the data channel is never closed, so
	// callbacks can't panic on a send) before disconnecting.
	close(rs.stopped)

	rs.client.Disconnect(uint(5 * time.Second.Milliseconds())) //nolint:gosec // constant value, always positive

	logger.V(2).Printf("Stopped MQTT with %d messages still pending", rs.pendingMessages.Len())

	rs.pendingMessages.Close()
}
