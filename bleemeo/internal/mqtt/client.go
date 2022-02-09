package mqtt

import (
	"glouton/bleemeo/types"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// pahoWrapper implements the bleemeoTypes.PahoWrapper interface.
type pahoWrapper struct {
	client paho.Client

	connectionLostHandler paho.ConnectionLostHandler
	connectHandler        paho.OnConnectHandler
	notificationHandler   paho.MessageHandler
}

type PahoWrapperOptions struct {
	ConnectionLostHandler paho.ConnectionLostHandler
	ConnectHandler        paho.OnConnectHandler
	NotificationHandler   paho.MessageHandler
}

func NewPahoWrapper(opts PahoWrapperOptions) types.PahoWrapper {
	return &pahoWrapper{
		connectionLostHandler: opts.ConnectionLostHandler,
		connectHandler:        opts.ConnectHandler,
		notificationHandler:   opts.NotificationHandler,
	}
}

func (c *pahoWrapper) Client() paho.Client {
	return c.client
}

func (c *pahoWrapper) SetClient(cli paho.Client) {
	c.client = cli
}

func (c *pahoWrapper) OnConnectionLost(cli paho.Client, err error) {
	if c.connectionLostHandler != nil {
		c.connectionLostHandler(cli, err)
	}
}

func (c *pahoWrapper) SetOnConnectionLost(f paho.ConnectionLostHandler) {
	c.connectionLostHandler = f
}

func (c *pahoWrapper) OnConnect(cli paho.Client) {
	if c.connectHandler != nil {
		c.connectHandler(cli)
	}
}

func (c *pahoWrapper) SetOnConnect(f paho.OnConnectHandler) {
	c.connectHandler = f
}

func (c *pahoWrapper) OnNotification(cli paho.Client, msg paho.Message) {
	if c.notificationHandler != nil {
		c.notificationHandler(cli, msg)
	}
}

func (c *pahoWrapper) SetOnNotification(f paho.MessageHandler) {
	c.notificationHandler = f
}
