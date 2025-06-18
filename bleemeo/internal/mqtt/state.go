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

package mqtt

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/mqtt/client"

	gloutonTypes "github.com/bleemeo/glouton/types"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// If we get more than notificationChannelSize notifications during a reload,
// the next notifications will be lost.
const notificationChannelSize = 1000

// reloadState implements the types.MQTTReloadState interface.
type reloadState struct {
	l                   sync.Mutex
	isClosed            bool
	connectChannel      chan paho.Client
	notificationChannel chan paho.Message

	mqtt            types.MQTTClient
	upgradeFile     string
	autoUpgradeFile string
	agentID         types.AgentID
	pendingPoints   []gloutonTypes.MetricPoint
	clientState     gloutonTypes.MQTTReloadState
}

type ReloadStateOptions struct {
	UpgradeFile     string
	AutoUpgradeFile string
	AgentID         types.AgentID
}

type disconnectCause struct {
	Cause string `json:"disconnect-cause"`
}

func NewReloadState(opts ReloadStateOptions) types.MQTTReloadState {
	rs := &reloadState{
		connectChannel:      make(chan paho.Client),
		notificationChannel: make(chan paho.Message, notificationChannelSize),
		upgradeFile:         opts.UpgradeFile,
		autoUpgradeFile:     opts.AutoUpgradeFile,
		agentID:             opts.AgentID,
		clientState:         client.NewReloadState(),
	}

	return rs
}

func (rs *reloadState) SetMQTT(mqtt types.MQTTClient) {
	rs.mqtt = mqtt
}

func (rs *reloadState) OnConnect(cli paho.Client) {
	rs.l.Lock()
	defer rs.l.Unlock()

	if rs.isClosed {
		return
	}

	rs.connectChannel <- cli
}

func (rs *reloadState) ConnectChannel() <-chan paho.Client {
	return rs.connectChannel
}

func (rs *reloadState) OnNotification(_ paho.Client, msg paho.Message) {
	rs.l.Lock()
	defer rs.l.Unlock()

	if rs.isClosed {
		return
	}

	select {
	case rs.notificationChannel <- msg:
	default:
	}
}

func (rs *reloadState) NotificationChannel() <-chan paho.Message {
	return rs.notificationChannel
}

func (rs *reloadState) PopPendingPoints() []gloutonTypes.MetricPoint {
	rs.l.Lock()
	defer rs.l.Unlock()

	points := rs.pendingPoints
	rs.pendingPoints = nil

	return points
}

func (rs *reloadState) SetPendingPoints(points []gloutonTypes.MetricPoint) {
	rs.l.Lock()
	defer rs.l.Unlock()

	rs.pendingPoints = points
}

func (rs *reloadState) Close() {
	// Consume all events on channels to make sure the paho client is not blocked.
	go func() {
		for range rs.notificationChannel {
		}
	}()
	go func() {
		for range rs.connectChannel {
		}
	}()

	if rs.mqtt != nil && rs.mqtt.IsConnectionOpen() {
		cause := "Clean shutdown"

		if _, err := os.Stat(rs.upgradeFile); err == nil {
			cause = "Upgrade"
		}

		if _, err := os.Stat(rs.autoUpgradeFile); err == nil {
			cause = "Auto upgrade"
		}

		if err := rs.mqtt.PublishAsJSON(fmt.Sprintf("v1/agent/%s/disconnect", rs.agentID), disconnectCause{cause}, false); err != nil {
			logger.V(1).Printf("Unable to publish on disconnect topic: %v", err)
		}

		rs.mqtt.Disconnect(5 * time.Second)
	}

	// The callbacks need to know when the channel are closed
	// so they don't send on a closed channel.
	rs.l.Lock()
	defer rs.l.Unlock()

	rs.isClosed = true

	close(rs.notificationChannel)
	close(rs.connectChannel)
}

// ClientState returns the reload state of the mqtt client.
func (rs *reloadState) ClientState() gloutonTypes.MQTTReloadState {
	return rs.clientState
}
