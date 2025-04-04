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
	isClosed              bool
	connectionLostChannel chan error
	pendingMessages       *fifo[types.Message]
}

func NewReloadState() *ReloadState {
	return &ReloadState{
		connectionLostChannel: make(chan error),
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
	rs.l.Lock()
	defer rs.l.Unlock()

	if rs.isClosed {
		return
	}

	rs.connectionLostChannel <- err
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

	// Consume all events on channel to make sure the paho client is not blocked.
	go func() {
		for range rs.connectionLostChannel {
		}
	}()

	rs.client.Disconnect(uint(5 * time.Second.Milliseconds())) //nolint: gosec

	logger.V(2).Printf("Stopped MQTT with %d messages still pending", rs.pendingMessages.Len())

	// The callbacks need to know when the channel are closed
	// so they don't send on a closed channel.
	rs.l.Lock()
	defer rs.l.Unlock()

	rs.isClosed = true

	close(rs.connectionLostChannel)
	rs.pendingMessages.Close()
}
