package mqtt

import (
	"encoding/json"
	"fmt"
	"glouton/bleemeo/types"
	"glouton/logger"
	"os"
	"sync"
	"time"

	gloutonTypes "glouton/types"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// If we get more than notificationChannelSize notifications during a reload,
// the next notifications will be lost.
const notificationChannelSize = 1000

// pahoWrapper implements the types.PahoWrapper interface.
type pahoWrapper struct {
	client paho.Client

	l                     sync.Mutex
	isClosed              bool
	connectionLostChannel chan error
	connectChannel        chan paho.Client
	notificationChannel   chan paho.Message

	upgradeFile     string
	autoUpgradeFile string
	agentID         types.AgentID
	pendingPoints   []gloutonTypes.MetricPoint
}

type PahoWrapperOptions struct {
	UpgradeFile     string
	AutoUpgradeFile string
	AgentID         types.AgentID
}

type disconnectCause struct {
	Cause string `json:"disconnect-cause"`
}

func NewPahoWrapper(opts PahoWrapperOptions) types.PahoWrapper {
	wrapper := &pahoWrapper{
		connectChannel:        make(chan paho.Client),
		connectionLostChannel: make(chan error),
		notificationChannel:   make(chan paho.Message, notificationChannelSize),
		upgradeFile:           opts.UpgradeFile,
		autoUpgradeFile:       opts.AutoUpgradeFile,
		agentID:               opts.AgentID,
	}

	return wrapper
}

func (c *pahoWrapper) Client() paho.Client {
	c.l.Lock()
	client := c.client
	c.l.Unlock()

	return client
}

func (c *pahoWrapper) SetClient(cli paho.Client) {
	c.l.Lock()
	defer c.l.Unlock()

	c.client = cli
}

func (c *pahoWrapper) OnConnectionLost(cli paho.Client, err error) {
	c.l.Lock()
	defer c.l.Unlock()

	if c.isClosed {
		return
	}

	c.connectionLostChannel <- err
}

func (c *pahoWrapper) ConnectionLostChannel() <-chan error {
	return c.connectionLostChannel
}

func (c *pahoWrapper) OnConnect(cli paho.Client) {
	c.l.Lock()
	defer c.l.Unlock()

	if c.isClosed {
		return
	}

	c.connectChannel <- cli
}

func (c *pahoWrapper) ConnectChannel() <-chan paho.Client {
	return c.connectChannel
}

func (c *pahoWrapper) OnNotification(cli paho.Client, msg paho.Message) {
	c.l.Lock()
	defer c.l.Unlock()

	if c.isClosed {
		return
	}

	select {
	case c.notificationChannel <- msg:
	default:
	}
}

func (c *pahoWrapper) NotificationChannel() <-chan paho.Message {
	return c.notificationChannel
}

func (c *pahoWrapper) PopPendingPoints() []gloutonTypes.MetricPoint {
	c.l.Lock()
	defer c.l.Unlock()

	points := c.pendingPoints
	c.pendingPoints = nil

	return points
}

func (c *pahoWrapper) SetPendingPoints(points []gloutonTypes.MetricPoint) {
	c.l.Lock()
	defer c.l.Unlock()

	c.pendingPoints = points
}

func (c *pahoWrapper) Close() {
	if c.client == nil {
		return
	}

	// Consume all events on channels to make sure the paho client is not blocked.
	go func() {
		for range c.notificationChannel {
		}
	}()
	go func() {
		for range c.connectChannel {
		}
	}()
	go func() {
		for range c.connectionLostChannel {
		}
	}()

	deadline := time.Now().Add(5 * time.Second)

	if c.client.IsConnectionOpen() {
		cause := "Clean shutdown"

		if _, err := os.Stat(c.upgradeFile); err == nil {
			cause = "Upgrade"
		}

		if _, err := os.Stat(c.autoUpgradeFile); err == nil {
			cause = "Auto upgrade"
		}

		payload, _ := json.Marshal(disconnectCause{cause}) //nolint:errchkjson // False positive.

		token := c.client.Publish(fmt.Sprintf("v1/agent/%s/disconnect", c.agentID), 1, false, payload)
		if !token.WaitTimeout(time.Until(deadline)) {
			logger.V(1).Printf("Failed to send MQTT disconnect message")
		}
	}

	c.client.Disconnect(uint(time.Until(deadline).Milliseconds()))

	// The callbacks need to know when the channel are closed
	// so they don't send on a closed channel.
	c.l.Lock()
	defer c.l.Unlock()

	c.isClosed = true

	close(c.notificationChannel)
	close(c.connectChannel)
	close(c.connectionLostChannel)
}
