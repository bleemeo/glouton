package mqtt

import (
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// ReloadState implements the types.PahoWrapper interface.
type ReloadState struct {
	l                     sync.Mutex
	client                paho.Client
	isClosed              bool
	connectionLostChannel chan error
}

func NewReloadState() *ReloadState {
	return &ReloadState{
		connectionLostChannel: make(chan error),
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

func (rs *ReloadState) OnConnectionLost(cli paho.Client, err error) {
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

func (rs *ReloadState) Close() {
	if rs.client == nil {
		return
	}

	// Consume all events on channel to make sure the paho client is not blocked.
	go func() {
		for range rs.connectionLostChannel {
		}
	}()

	rs.client.Disconnect(uint(5 * time.Second.Milliseconds()))

	// The callbacks need to know when the channel are closed
	// so they don't send on a closed channel.
	rs.l.Lock()
	defer rs.l.Unlock()

	rs.isClosed = true

	close(rs.connectionLostChannel)
}
