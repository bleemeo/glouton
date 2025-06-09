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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/delay"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"

	paho "github.com/eclipse/paho.mqtt.golang"
)

const (
	// The minimum duration to wait before reconnecting.
	minimalDelayBetweenConnect = 5 * time.Second
	// The maximum duration to wait before reconnecting.
	maximalDelayBetweenConnect = 10 * time.Minute
	// The maximum number of messages to keep while MQTT is unreachable.
	maxPendingMessages = 1000
	// If we stayed connected to MQTT for more stableConnection, the connection is considered stable,
	// and we won't wait long to reconnect in case of a disconnection.
	stableConnection    = 5 * time.Minute
	maxDelayWithoutPing = 90 * time.Second
)

var ErrPayloadTooLarge = errors.New("payload is too large")

type Client struct {
	opts           Options
	connectionLost chan any
	stats          *mqttStats
	disableNotify  chan any
	encoder        *encoder

	l    sync.Mutex
	mqtt paho.Client

	lastConnectionTimes []time.Time
	currentConnectDelay time.Duration
	consecutiveErrors   int
	lastReport          time.Time
	disabledUntil       time.Time
}

type Options struct {
	// OptionsFunc returns the options to use when connecting to MQTT.
	OptionsFunc func(ctx context.Context) (*paho.ClientOptions, error)
	// Keep a state between reloads.
	ReloadState types.MQTTReloadState
	// Function called when too many errors happened.
	TooManyErrorsHandler func(ctx context.Context)
	// A unique identifier for this client.
	ID                  string
	PahoLastPingCheckAt func() time.Time
}

// New creates a new client.
func New(opts Options) *Client {
	client := &Client{
		opts:           opts,
		mqtt:           opts.ReloadState.Client(),
		stats:          newMQTTStats(),
		disableNotify:  make(chan any),
		connectionLost: make(chan any),
		encoder:        &encoder{},
	}

	return client
}

func (c *Client) Run(ctx context.Context) {
	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer crashreport.ProcessPanic()
		defer wg.Done()

		c.connectionManager(ctx)
	}()

	wg.Add(1)

	go func() {
		defer crashreport.ProcessPanic()
		defer wg.Done()

		c.ackManager(ctx)
	}()

	wg.Add(1)

	go func() {
		defer crashreport.ProcessPanic()

		c.receiveEvents(ctx)
		wg.Done()
	}()

	wg.Wait()

	// Keep the client during reloads to avoid closing the connection.
	c.opts.ReloadState.SetClient(c.mqtt)
}

func (c *Client) setupMQTT(ctx context.Context) (paho.Client, error) {
	opts, err := c.opts.OptionsFunc(ctx)
	if err != nil {
		return nil, err
	}

	// Allow for slightly larger timeout value, to avoid disconnection
	// with bad network connection.
	opts.SetPingTimeout(20 * time.Second)
	opts.SetKeepAlive(45 * time.Second)

	// We use our own automatic reconnection logic which is more reliable.
	opts.SetAutoReconnect(false)
	opts.SetConnectionLostHandler(c.opts.ReloadState.OnConnectionLost)

	return paho.NewClient(opts), err
}

// PublishAsJSON sends the payload to MQTT on the given topic after encoding it as JSON.
// If retry is set to true and MQTT is currently unreachable, the client will
// retry to send the message later, else it will be dropped.
func (c *Client) PublishAsJSON(topic string, payload any, retry bool) error {
	payloadBuffer, err := c.encoder.EncodeObject(payload)
	if err != nil {
		c.encoder.PutBuffer(payloadBuffer)

		return err
	}

	return c.publishWrapper(context.Background(), topic, payloadBuffer, retry)
}

// PublishBytes sends the payload to MQTT on the given topic.
// If retry is set to true and MQTT is currently unreachable, the client will
// retry to send the message later, else it will be dropped.
func (c *Client) PublishBytes(ctx context.Context, topic string, payload []byte, retry bool) error {
	payloadBuffer, err := c.encoder.EncodeBytes(payload)
	if err != nil {
		c.encoder.PutBuffer(payloadBuffer)

		return err
	}

	return c.publishWrapper(ctx, topic, payloadBuffer, retry)
}

func (c *Client) publishWrapper(ctx context.Context, topic string, payloadBuffer []byte, retry bool) error {
	if len(payloadBuffer) > types.MaxMQTTPayloadSize {
		c.encoder.PutBuffer(payloadBuffer)

		return fmt.Errorf("%w: size is %d which is > %d", ErrPayloadTooLarge, len(payloadBuffer), types.MaxMQTTPayloadSize)
	}

	msg, ok := c.publish(topic, payloadBuffer, retry)

	if ok {
		c.opts.ReloadState.AddPendingMessage(ctx, msg, true)
	} else {
		c.encoder.PutBuffer(payloadBuffer)
	}

	return nil
}

func (c *Client) publish(topic string, payload []byte, retry bool) (types.Message, bool) {
	c.l.Lock()
	mqtt := c.mqtt
	c.l.Unlock()

	msg := types.Message{
		Retry:   retry,
		Payload: payload,
		Topic:   topic,
	}

	if mqtt == nil && !retry {
		return msg, false
	}

	if mqtt != nil {
		msg.Token = mqtt.Publish(topic, 1, false, payload)
		c.stats.messagePublished(msg.Token, time.Now())
	}

	return msg, true
}

func (c *Client) onConnectionLost(err error) {
	logger.Printf("%s MQTT connection lost: %v", c.opts.ID, err)
	c.connectionLost <- nil
}

func (c *Client) connectionManager(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	var lastConnectionTimes []time.Time

	currentConnectDelay := minimalDelayBetweenConnect / 2
	consecutiveError := 0
	pingMissingConsecutive := 0

mainLoop:
	for ctx.Err() == nil {
		c.l.Lock()
		c.lastConnectionTimes = lastConnectionTimes
		c.currentConnectDelay = currentConnectDelay
		c.consecutiveErrors = consecutiveError

		mqtt := c.mqtt
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
		case mqtt == nil:
			length := len(lastConnectionTimes)

			if length >= 7 && time.Since(lastConnectionTimes[length-7]) < 10*time.Minute {
				delay := delay.JitterDelay(5*time.Minute, 0.25).Round(time.Second)

				c.Disable(time.Now().Add(delay))
				logger.Printf("Too many attempts to connect to %s MQTT were made in the last 10 minutes. Disabling MQTT for %v", c.opts.ID, delay)

				if c.opts.TooManyErrorsHandler != nil {
					c.opts.TooManyErrorsHandler(ctx)
				}

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
						if c.opts.TooManyErrorsHandler != nil {
							c.opts.TooManyErrorsHandler(ctx)
						}
					}
				}

				mqtt, err := c.setupMQTT(ctx)
				if err != nil {
					delay := currentConnectDelay - time.Since(lastConnectionTimes[len(lastConnectionTimes)-1])
					logger.V(1).Printf("Unable to connect to %s MQTT (retry in %v): %v", c.opts.ID, delay, err)

					continue
				}

				optionReader := mqtt.OptionsReader()
				logger.V(2).Printf("Connecting to %s MQTT broker %v", c.opts.ID, optionReader.Servers()[0])

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

					logger.V(1).Printf("Unable to connect to %s MQTT (retry in %v): %v", c.opts.ID, delay, token.Error())

					// we must disconnect to stop paho gorouting that otherwise will be
					// started multiple time for each Connect()
					mqtt.Disconnect(0)
				} else {
					c.l.Lock()
					c.mqtt = mqtt
					c.l.Unlock()

					logger.Printf("%s MQTT connection established", c.opts.ID)
				}
			}
		case mqtt != nil && mqtt.IsConnectionOpen():
			if c.opts.PahoLastPingCheckAt != nil && !c.opts.PahoLastPingCheckAt().IsZero() && time.Since(c.opts.PahoLastPingCheckAt()) > maxDelayWithoutPing {
				pingMissingConsecutive++

				if pingMissingConsecutive >= 2 {
					logger.V(1).Println("Paho ping check seems blocked, forcing a disconnection")

					c.l.Lock()

					if c.mqtt != nil {
						c.mqtt.Disconnect(100)
					}

					c.mqtt = nil

					c.l.Unlock()
				}
			} else {
				pingMissingConsecutive = 0
			}
		}

		select {
		case <-ctx.Done():
		case <-c.connectionLost:
			c.l.Lock()
			if c.mqtt != nil {
				c.mqtt.Disconnect(0)
			}

			c.mqtt = nil
			c.l.Unlock()

			length := len(lastConnectionTimes)
			if length > 0 && time.Since(lastConnectionTimes[length-1]) > stableConnection {
				logger.V(2).Printf("%s MQTT connection was stable, reset delay to %v", c.opts.ID, minimalDelayBetweenConnect)
				currentConnectDelay = minimalDelayBetweenConnect
				consecutiveError = 0
			} else if length > 0 {
				delay := currentConnectDelay - time.Since(lastConnectionTimes[len(lastConnectionTimes)-1])
				if delay > 0 {
					logger.V(1).Printf("Retry to connection to %s MQTT in %v", c.opts.ID, delay)
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
		msg, open := c.opts.ReloadState.PendingMessage(ctx)
		if !open {
			logger.V(2).Println("MQTT messages queue has been closed.")

			<-ctx.Done()

			break
		}

		err := c.ackOne(msg, 10*time.Second)
		if err != nil {
			if time.Since(lastErrShowed) > time.Minute {
				logger.V(2).Printf(
					"%s MQTT publish on %s failed: %v (%d pending messages)",
					c.opts.ID, msg.Topic, err, c.opts.ReloadState.PendingMessagesCount(),
				)

				lastErrShowed = time.Now()
			}
		}
	}
}

func (c *Client) ackOne(msg types.Message, timeout time.Duration) error {
	var err error

	// The token is nil when publishing failed.
	shouldWaitForACKAgain := false
	if msg.Token != nil {
		shouldWaitForACKAgain = !msg.Token.WaitTimeout(timeout)

		err = msg.Token.Error()
		if err != nil {
			c.stats.ackFailed(msg.Token)
		}
	}

	// Retry publishing the message if there was an error.
	if msg.Token == nil || msg.Token.Error() != nil {
		if !msg.Retry {
			c.encoder.PutBuffer(msg.Payload)

			return err
		}

		c.l.Lock()
		mqtt := c.mqtt
		c.l.Unlock()

		if mqtt != nil {
			msg.Token = mqtt.Publish(msg.Topic, 1, false, msg.Payload)
		}

		publishFailed := mqtt == nil || msg.Token.Error() != nil

		// The Token will be awaited later.
		shouldWaitForACKAgain = true

		// It's possible for Publish to return instantly with an error,
		// in this case we need to wait a bit to avoid consuming too much resources.
		if publishFailed {
			msg.Token = nil

			time.Sleep(time.Second)
		}
	}

	if shouldWaitForACKAgain { // Note: we don't care about the context here: we won't wait anyway.
		ok := c.opts.ReloadState.AddPendingMessage(context.Background(), msg, false)

		if !ok {
			logger.V(1).Printf("%s: MQTT pending message queue is full, message on topic %s is dropped", c.opts.ID, msg.Topic)
		}

		return err
	}

	now := time.Now()

	c.encoder.PutBuffer(msg.Payload)
	c.stats.ackReceived(msg.Token, now)

	c.l.Lock()
	defer c.l.Unlock()

	c.lastReport = now

	return nil
}

func (c *Client) receiveEvents(ctx context.Context) {
	for {
		select {
		case err := <-c.opts.ReloadState.ConnectionLostChannel():
			c.onConnectionLost(err)
		case <-ctx.Done():
			return
		}
	}
}

func (c *Client) IsConnectionOpen() bool {
	c.l.Lock()
	defer c.l.Unlock()

	return c.isConnectionOpen()
}

func (c *Client) isConnectionOpen() bool {
	if c.mqtt == nil {
		return false
	}

	return c.mqtt.IsConnectionOpen()
}

// DiagnosticArchive add to a zipfile useful diagnostic information.
func (c *Client) DiagnosticArchive(_ context.Context, archive types.ArchiveWriter) error {
	c.l.Lock()
	defer c.l.Unlock()

	fileID := strings.ToLower(strings.ReplaceAll(c.opts.ID, " ", "-"))

	file, err := archive.Create(fileID + "-mqtt-stats.txt")
	if err != nil {
		return err
	}

	fmt.Fprintf(file, "%s", c.stats)

	file, err = archive.Create(fileID + "-mqtt-client.json")
	if err != nil {
		return err
	}

	obj := struct {
		ConnectionOpen      bool
		PendingMessageCount int
		LastConnectionTimes []time.Time
		CurrentConnectDelay string
		ConsecutiveErrors   int
		LastReport          time.Time
		DisabledUntil       time.Time
	}{
		ConnectionOpen:      c.isConnectionOpen(),
		PendingMessageCount: c.opts.ReloadState.PendingMessagesCount(),
		LastConnectionTimes: c.lastConnectionTimes,
		CurrentConnectDelay: c.currentConnectDelay.String(),
		ConsecutiveErrors:   c.consecutiveErrors,
		LastReport:          c.lastReport,
		DisabledUntil:       c.disabledUntil,
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

// LastReport returns the date of last acknowledgment received.
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

	if c.mqtt != nil {
		c.mqtt.Disconnect(uint(timeout.Milliseconds())) //nolint: gosec
	}

	c.mqtt = nil
}
