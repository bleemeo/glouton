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

// pahoWrapper implements the types.PahoWrapper interface.
type pahoWrapper struct {
	client paho.Client

	l                     sync.Mutex
	connectionLostHandler paho.ConnectionLostHandler
	connectHandler        paho.OnConnectHandler
	notificationHandler   paho.MessageHandler

	upgradeFile   string
	agentID       types.AgentID
	pendingPoints []gloutonTypes.MetricPoint
}

type PahoWrapperOptions struct {
	ConnectionLostHandler paho.ConnectionLostHandler
	ConnectHandler        paho.OnConnectHandler
	NotificationHandler   paho.MessageHandler
	UpgradeFile           string
	AgentID               types.AgentID
}

func NewPahoWrapper(opts PahoWrapperOptions) types.PahoWrapper {
	return &pahoWrapper{
		connectionLostHandler: opts.ConnectionLostHandler,
		connectHandler:        opts.ConnectHandler,
		notificationHandler:   opts.NotificationHandler,
		upgradeFile:           opts.UpgradeFile,
		agentID:               opts.AgentID,
	}
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

	if c.connectionLostHandler != nil {
		c.connectionLostHandler(cli, err)
	}
}

func (c *pahoWrapper) SetOnConnectionLost(f paho.ConnectionLostHandler) {
	c.l.Lock()
	defer c.l.Unlock()

	c.connectionLostHandler = f
}

func (c *pahoWrapper) OnConnect(cli paho.Client) {
	c.l.Lock()
	defer c.l.Unlock()

	if c.connectHandler != nil {
		c.connectHandler(cli)
	}
}

func (c *pahoWrapper) SetOnConnect(f paho.OnConnectHandler) {
	c.l.Lock()
	defer c.l.Unlock()

	c.connectHandler = f
}

func (c *pahoWrapper) OnNotification(cli paho.Client, msg paho.Message) {
	c.l.Lock()
	defer c.l.Unlock()

	if c.notificationHandler != nil {
		c.notificationHandler(cli, msg)
	}
}

func (c *pahoWrapper) SetOnNotification(f paho.MessageHandler) {
	c.l.Lock()
	defer c.l.Unlock()

	c.notificationHandler = f
}

func (c *pahoWrapper) PendingPoints() []gloutonTypes.MetricPoint {
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

	deadline := time.Now().Add(5 * time.Second)

	if c.client.IsConnectionOpen() {
		cause := "Clean shutdown"

		if _, err := os.Stat(c.upgradeFile); err == nil {
			cause = "Upgrade"
		}

		payload, _ := json.Marshal(map[string]string{"disconnect-cause": cause})

		token := c.client.Publish(fmt.Sprintf("v1/agent/%s/disconnect", c.agentID), 1, false, payload)
		if !token.WaitTimeout(time.Until(deadline)) {
			logger.V(1).Printf("Failed to send MQTT disconnect message")
		}
	}

	c.client.Disconnect(uint(time.Until(deadline).Seconds() * 1000))
}
