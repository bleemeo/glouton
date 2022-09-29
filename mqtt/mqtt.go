// Copyright 2015-2022 Bleemeo
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
	"context"
	"encoding/json"
	"fmt"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/delay"
	"glouton/logger"
	"glouton/types"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

const (
	// The minimum duration to wait before reconnecting.
	minimalDelayBetweenConnect = 5 * time.Second
	// The maximum duration to wait before reconnecting.
	maximalDelayBetweenConnect = 10 * time.Minute
	// The maximum number of messages to keep while MQTT is unreachable.
	maxPendingMessages = 1000
	// The maximum duration the messages sent to MQTT will be retried for when shutting down.
	shutdownTimeout = 5 * time.Second
	// If we stayed connected to MQTT for more stableConnection, the connection is considered stable,
	// and we won't wait long to reconnect in case of a disconnection.
	stableConnection = 5 * time.Minute
)

// TODO: Add the connector mqttRestarter.
type Client struct {
	opts            Options
	pendingMessages chan message
	connectionLost  chan interface{}
	stats           *mqttStats
	disableNotify   chan interface{}

	l       sync.Mutex
	mqtt    paho.Client
	stopped bool

	lastConnectionTimes []time.Time
	currentConnectDelay time.Duration
	consecutiveErrors   int
	lastReport          time.Time
	disabledUntil       time.Time
}

type Options struct {
	// A unique identifier for this agent.
	AgentID bleemeoTypes.AgentID
	// OptionsFunc returns the options to use when connecting to MQTT.
	OptionsFunc func(ctx context.Context) (*paho.ClientOptions, error)
	// Whether the client is created for the first time.
	FirstClient bool
	// Keep a state between reloads.
	ReloadState types.MQTTReloadState
	// Function called when too many errors happened .
	TooManyErrorsHandler func(ctx context.Context)
}

type message struct {
	token   paho.Token
	retry   bool
	topic   string
	payload interface{}
}

// New create a new client.
func New(opts Options) *Client {
	if opts.FirstClient {
		paho.ERROR = logger.V(2)
		paho.CRITICAL = logger.V(2)
		paho.WARN = logger.V(2)
		paho.DEBUG = logger.V(3)
	}

	client := &Client{
		opts:            opts,
		mqtt:            opts.ReloadState.Client(),
		stats:           newMQTTStats(),
		pendingMessages: make(chan message, maxPendingMessages),
		disableNotify:   make(chan interface{}),
		connectionLost:  make(chan interface{}),
	}

	return client
}

func (c *Client) Run(ctx context.Context) {
	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer types.ProcessPanic()
		defer wg.Done()

		c.connectionManager(ctx)
	}()

	wg.Add(1)

	go func() {
		defer types.ProcessPanic()
		defer wg.Done()

		c.ackManager(ctx)
	}()

	wg.Add(1)

	go func() {
		defer types.ProcessPanic()

		c.receiveEvents(ctx)
		wg.Done()
	}()

	wg.Wait()

	c.l.Lock()
	c.stopped = true
	c.l.Unlock()

	close(c.pendingMessages)

	// Keep the client during reloads to avoid closing the connection.
	c.opts.ReloadState.SetClient(c.mqtt)
}

func (c *Client) setupMQTT(ctx context.Context) (paho.Client, error) {
	opts, err := c.opts.OptionsFunc(ctx)
	if err != nil {
		return nil, err
	}

	opts.SetConnectionLostHandler(c.opts.ReloadState.OnConnectionLost)

	// We use our own automatic reconnection logic which is more reliable.
	opts.SetAutoReconnect(false)

	return paho.NewClient(opts), err
}

// Publish sends the payload to MQTT on the given topic.
// If retry is set to true and MQTT is currently unreachable, the client will
// retry to send the message later, else it will be dropped.
func (c *Client) Publish(topic string, payload interface{}, retry bool) {
	c.l.Lock()
	defer c.l.Unlock()

	msg := message{
		retry:   retry,
		payload: payload,
		topic:   topic,
	}

	if c.mqtt == nil && !retry {
		return
	}

	if c.mqtt != nil {
		msg.token = c.mqtt.Publish(topic, 1, false, payload)
		c.stats.messagePublished(msg.token, time.Now())
	}

	if !c.stopped {
		c.pendingMessages <- msg
	}
}

func (c *Client) onConnectionLost(_ paho.Client, err error) {
	logger.Printf("MQTT connection lost: %v", err)
	c.connectionLost <- nil
}

func (c *Client) connectionManager(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	var lastConnectionTimes []time.Time

	currentConnectDelay := minimalDelayBetweenConnect / 2
	consecutiveError := 0

mainLoop:
	for ctx.Err() == nil {
		c.l.Lock()
		c.lastConnectionTimes = lastConnectionTimes
		c.currentConnectDelay = currentConnectDelay
		c.consecutiveErrors = consecutiveError

		disabledUntil := c.disabledUntil
		c.l.Unlock()

		switch {
		case time.Now().Before(disabledUntil):
			c.l.Lock()
			if c.mqtt != nil {
				c.mqtt.Disconnect(0)

				c.mqtt = nil
			}
			c.l.Unlock()
		case c.mqtt == nil:
			length := len(lastConnectionTimes)

			if length >= 7 && time.Since(lastConnectionTimes[length-7]) < 10*time.Minute {
				delay := delay.JitterDelay(5*time.Minute, 0.25).Round(time.Second)

				c.Disable(time.Now().Add(delay))
				logger.Printf("Too many attempts to connect to MQTT were made in the last 10 minutes. Disabling MQTT for %v", delay)

				c.opts.TooManyErrorsHandler(ctx)

				continue
			}

			if length == 0 || time.Since(lastConnectionTimes[length-1]) > currentConnectDelay {
				lastConnectionTimes = append(lastConnectionTimes, time.Now())

				if len(lastConnectionTimes) > 20 {
					lastConnectionTimes = lastConnectionTimes[len(lastConnectionTimes)-20:]
				}

				if currentConnectDelay < maximalDelayBetweenConnect {
					consecutiveError++
					currentConnectDelay = delay.JitterDelay(
						delay.Exponential(minimalDelayBetweenConnect, 1.55, consecutiveError, maximalDelayBetweenConnect),
						0.1,
					)
					if consecutiveError == 5 {
						c.opts.TooManyErrorsHandler(ctx)
					}
				}

				mqtt, err := c.setupMQTT(ctx)
				if err != nil {
					delay := currentConnectDelay - time.Since(lastConnectionTimes[len(lastConnectionTimes)-1])
					logger.V(1).Printf("Unable to connect to MQTT (retry in %v): %v", delay, err)

					continue
				}

				optionReader := mqtt.OptionsReader()
				logger.V(2).Printf("Connecting to MQTT broker %v", optionReader.Servers()[0])

				var connectionTimeout bool

				deadline := time.Now().Add(time.Minute)
				token := mqtt.Connect()

				for !token.WaitTimeout(1 * time.Second) {
					if ctx.Err() != nil {
						break mainLoop
					}

					if time.Now().After(deadline) {
						connectionTimeout = true

						break
					}
				}

				if token.Error() != nil || connectionTimeout {
					delay := currentConnectDelay - time.Since(lastConnectionTimes[len(lastConnectionTimes)-1])

					logger.V(1).Printf("Unable to connect to Bleemeo MQTT (retry in %v): %v", delay, token.Error())

					// we must disconnect to stop paho gorouting that otherwise will be
					// started multiple time for each Connect()
					mqtt.Disconnect(0)
				} else {
					c.l.Lock()
					c.mqtt = mqtt
					c.l.Unlock()
				}
			}
		}

		select {
		case <-ctx.Done():
		case <-c.connectionLost:
			c.l.Lock()
			c.mqtt.Disconnect(0)
			c.mqtt = nil
			c.l.Unlock()

			length := len(lastConnectionTimes)
			if length > 0 && time.Since(lastConnectionTimes[length-1]) > stableConnection {
				logger.V(2).Printf("MQTT connection was stable, reset delay to %v", minimalDelayBetweenConnect)
				currentConnectDelay = minimalDelayBetweenConnect
				consecutiveError = 0
			} else if length > 0 {
				delay := currentConnectDelay - time.Since(lastConnectionTimes[len(lastConnectionTimes)-1])
				if delay > 0 {
					logger.V(1).Printf("Retry to connection to MQTT in %v", delay)
				}
			}
		case <-c.disableNotify:
		case <-ticker.C:
		}
	}

	// Make sure all connection lost events are read.
	for {
		select {
		case <-c.connectionLost:
		default:
			return
		}
	}
}

func (c *Client) ackManager(ctx context.Context) {
	var lastErrShowed time.Time

	for ctx.Err() == nil {
		select {
		case msg := <-c.pendingMessages:
			err := c.ackOne(msg, 10*time.Second)
			if err != nil {
				if time.Since(lastErrShowed) > time.Minute {
					logger.V(2).Printf(
						"MQTT publish on %s failed: %v (%d pending messages)",
						msg.topic, err, len(c.pendingMessages),
					)

					lastErrShowed = time.Now()
				}
			}

		case <-ctx.Done():
		}
	}

	// When the context is canceled, we keep trying to send the last pending messages for shutdownTimeout.
	deadline := time.Now().Add(shutdownTimeout)

	for timeLeft := shutdownTimeout; timeLeft > 0; timeLeft = time.Until(deadline) {
		select {
		case msg := <-c.pendingMessages:
			_ = c.ackOne(msg, timeLeft)
		default:
			// All messages have been acked.
			return
		}
	}

	logger.V(2).Printf("Ack manager stopped with %d messages still pending", len(c.pendingMessages))

	// We were not able to process all messages before the timeout.
	// Drain the pending messages channel to avoid a dead-lock when publishing messages.
	for len(c.pendingMessages) > 0 {
		<-c.pendingMessages
	}
}

func (c *Client) ackOne(msg message, timeout time.Duration) error {
	var err error

	// The token is nil when publishing failed.
	shouldWaitAgain := false
	if msg.token != nil {
		shouldWaitAgain = !msg.token.WaitTimeout(timeout)

		err = msg.token.Error()
		if err != nil {
			c.stats.ackFailed(msg.token)
		}
	}

	// Retry publishing the message if there was an error.
	if msg.token == nil || msg.token.Error() != nil {
		if !msg.retry {
			return err
		}

		c.l.Lock()
		if c.mqtt != nil {
			msg.token = c.mqtt.Publish(msg.topic, 1, false, msg.payload)
		}

		publishFailed := c.mqtt == nil || msg.token.Error() != nil
		c.l.Unlock()

		// The token will be awaited later.
		shouldWaitAgain = true

		// It's possible for Publish to return instantly with an error,
		// in this case we need to wait a bit to avoid consuming too much resources.
		if publishFailed {
			msg.token = nil

			time.Sleep(time.Second)
		}
	}

	if shouldWaitAgain {
		// Add the message back to the pending messages if there is enough space in the channel.
		select {
		case c.pendingMessages <- msg:
		default:
		}

		return err
	}

	now := time.Now()
	c.stats.ackReceived(msg.token, now)

	c.l.Lock()
	defer c.l.Unlock()

	c.lastReport = now

	return nil
}

func (c *Client) receiveEvents(ctx context.Context) {
	for {
		select {
		case err := <-c.opts.ReloadState.ConnectionLostChannel():
			c.onConnectionLost(c.mqtt, err)
		case <-ctx.Done():
			return
		}
	}
}

func (c *Client) IsConnectionOpen() bool {
	c.l.Lock()
	defer c.l.Unlock()

	if c.mqtt == nil {
		return false
	}

	return c.mqtt.IsConnectionOpen()
}

// DiagnosticArchive add to a zipfile useful diagnostic information.
func (c *Client) DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("mqtt-stats.txt")
	if err != nil {
		return err
	}

	fmt.Fprintf(file, "%s", c.stats)

	file, err = archive.Create("mqtt-state.json")
	if err != nil {
		return err
	}

	obj := struct {
		PendingMessageCount int
		LastConnectionTimes []time.Time
		CurrentConnectDelay time.Duration
		ConsecutiveErrors   int
		LastReport          time.Time
		DisabledUntil       time.Time
	}{
		PendingMessageCount: len(c.pendingMessages),
		LastConnectionTimes: c.lastConnectionTimes,
		CurrentConnectDelay: c.currentConnectDelay,
		ConsecutiveErrors:   c.consecutiveErrors,
		LastReport:          c.lastReport,
		DisabledUntil:       c.disabledUntil,
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

// LastReport returns the date of last acknowlegment received.
func (c *Client) LastReport() time.Time {
	c.l.Lock()
	defer c.l.Unlock()

	return c.lastReport
}

// Disable will disable the MQTT connection until given time.
// To re-enable use ClearDisable().
func (c *Client) Disable(until time.Time) {
	c.l.Lock()
	defer c.l.Unlock()

	c.disabledUntil = until

	if c.disableNotify != nil {
		select {
		case c.disableNotify <- nil:
		default:
		}
	}
}

func (c *Client) DisabledUntil() time.Time {
	c.l.Lock()
	defer c.l.Unlock()

	return c.disabledUntil
}

func (c *Client) Disconnect(timeout time.Duration) {
	c.l.Lock()
	defer c.l.Unlock()

	c.mqtt.Disconnect(uint(timeout.Milliseconds()))
	c.mqtt = nil
}
