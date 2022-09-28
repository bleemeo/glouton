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
	l sync.Mutex

	opts            Options
	mqtt            paho.Client
	stats           *mqttStats
	pendingMessages chan message
	connectionLost  chan interface{}
}

type Options struct {
	// OptionsFunc returns the options to use when connecting to MQTT.
	OptionsFunc func(ctx context.Context) (*paho.ClientOptions, error)
	// Whether the client is created for the first time.
	FirstClient bool
	// Keep a state between reloads.
	ReloadState bleemeoTypes.BleemeoReloadState
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
		stats:           newMQTTStats(),
		pendingMessages: make(chan message, maxPendingMessages),
		connectionLost:  make(chan interface{}),
	}

	// TODO: Get the previous MQTT client from the reload state to avoid closing the connection.
	// var (
	// 	mqttClient    paho.Client
	// )

	// pahoWrapper := option.ReloadState.PahoWrapper()
	// if pahoWrapper != nil {
	// 	mqttClient = pahoWrapper.Client()
	// }

	return client
}

func (c *Client) run(ctx context.Context) {
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

	close(c.pendingMessages)
}

func (c *Client) setupMQTT(ctx context.Context) (paho.Client, error) {
	opts, err := c.opts.OptionsFunc(ctx)
	if err != nil {
		return nil, err
	}

	// TODO
	// opts.SetConnectionLostHandler(pahoWrapper.OnConnectionLost)
	opts.SetConnectionLostHandler(c.onConnectionLost)

	return paho.NewClient(opts), err
}

// Publish sends the payload to MQTT on the given topic.
// If retry is set to true and MQTT is currently unreachable, the client will
// retry to send the message later, else it will be dropped.
func (c *Client) Publish(topic string, payload interface{}, retry bool) {
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

	c.pendingMessages <- msg
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
		// TODO: Diagnostic
		// c.l.Lock()
		// c.lastConnectionTimes = lastConnectionTimes
		// c.currentConnectDelay = currentConnectDelay
		// c.consecutiveError = consecutiveError
		// c.l.Unlock()

		// disableUntil, disableReason := c.getDisableUntil()
		switch {
		// TODO
		// case time.Now().Before(disableUntil):
		// 	if disableReason == bleemeoTypes.DisableTimeDrift {
		// 		_ = c.shutdownTimeDrift()
		// 	}
		// 	if c.mqttClient != nil {
		// 		logger.V(2).Printf("Disconnecting from MQTT due to '%v'", disableReason)
		// 		c.mqttClient.Disconnect(0)

		// 		c.l.Lock()
		// 		c.mqttClient = nil
		// 		c.l.Unlock()

		// 		c.option.ReloadState.PahoWrapper().SetClient(nil)
		// 	}
		case c.mqtt == nil:
			length := len(lastConnectionTimes)

			// TODO
			// if length >= 7 && time.Since(lastConnectionTimes[length-7]) < 10*time.Minute {
			// 	delay := delay.JitterDelay(5*time.Minute, 0.25).Round(time.Second)

			// 	c.Disable(time.Now().Add(delay), bleemeoTypes.DisableTooManyErrors)
			// 	logger.Printf("Too many attempts to connect to MQTT were made in the last 10 minutes. Disabling MQTT for %v", delay)

			// 	continue
			// }

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
						// TODO
						// Trigger facts synchronization to check for duplicate agent
						// _, _ = c.option.Facts.Facts(ctx, time.Minute)
					}
				}

				mqttClient, err := c.setupMQTT(ctx)
				if err != nil {
					delay := currentConnectDelay - time.Since(lastConnectionTimes[len(lastConnectionTimes)-1])
					logger.V(1).Printf("Unable to connect to MQTT (retry in %v): %v", delay, err)

					continue
				}

				optionReader := mqttClient.OptionsReader()
				logger.V(2).Printf("Connecting to MQTT broker %v", optionReader.Servers()[0])

				var connectionTimeout bool

				deadline := time.Now().Add(time.Minute)
				token := mqttClient.Connect()

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
					mqttClient.Disconnect(0)
				} else {
					c.l.Lock()
					c.mqtt = mqttClient
					c.l.Unlock()

					// TODO
					// c.option.ReloadState.PahoWrapper().SetClient(mqttClient)
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

			// TODO
			// c.option.ReloadState.PahoWrapper().SetClient(nil)

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
		case <-ticker.C:
		}
	}

	// TODO
	// c.onReloadAndShutdown()

	// make sure all connectionLost are read
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

	// TODO
	// c.lastReport = now

	return nil
}

func (c *Client) receiveEvents(ctx context.Context) {
	pahoWrapper := c.opts.ReloadState.PahoWrapper()

	for {
		select {
		case <-pahoWrapper.NotificationChannel():
			// TODO
			// c.onNotification(c.mqtt, msg)
		case err := <-pahoWrapper.ConnectionLostChannel():
			c.onConnectionLost(c.mqtt, err)
		case <-pahoWrapper.ConnectChannel():
			// We can't use c.mqtt here because it is either nil or outdated,
			// c.mqtt is updated only after a successful connection.
			// TODO
			// c.onConnect(client)
		case <-ctx.Done():
			return
		}
	}
}
