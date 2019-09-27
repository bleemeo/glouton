// Copyright 2015-2019 Bleemeo
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
	"bytes"
	"compress/zlib"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"glouton/bleemeo/internal/cache"
	"glouton/bleemeo/internal/common"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/logger"
	"glouton/types"
	"io/ioutil"
	"os"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

const maxPendingPoints = 100000
const pointsBatchSize = 1000

// Option are parameter for the MQTT client
type Option struct {
	bleemeoTypes.GlobalOption
	Cache         *cache.Cache
	AgentID       string
	AgentPassword string

	// DisableCallback is a function called when MQTT got too much connect/disconnection.
	DisableCallback func(reason bleemeoTypes.DisableReason, until time.Time)
	// UpdateConfigCallback is a function called when Agent configuration (will) change
	UpdateConfigCallback func(now bool)
	// UpdateMetrics request update for given metric UUIDs
	UpdateMetrics func(metricUUID ...string)
}

// Client is an MQTT client for Bleemeo Cloud platform
type Client struct {
	option Option

	// Those variable are write once or only read/write from Run() gorouting. No lock needed
	ctx                        context.Context
	mqttClient                 paho.Client
	failedPoints               []types.MetricPoint
	lastRegisteredMetricsCount int
	lastFailedPointsRetry      time.Time

	l                     sync.Mutex
	setupDone             bool
	pendingToken          []paho.Token
	pendingPoints         []types.MetricPoint
	lastReport            time.Time
	failedPointsCount     int
	lastDisconnectionTime []time.Time
	disabledUntil         time.Time
	disableReason         bleemeoTypes.DisableReason
}

type metricPayload struct {
	UUID             string            `json:"uuid"`
	Measurement      string            `json:"measurement"`
	Timestamp        int64             `json:"time"`
	Value            forceDecimalFloat `json:"value"`
	Item             string            `json:"item,omitempty"`
	Status           string            `json:"status,omitempty"`
	EventGracePeriod int               `json:"event_grace_period,omitempty"`
	ProblemOrigin    string            `json:"check_output,omitempty"`
}

// This type is only used because the Bleemeo consumer require Value to be a float,
// and assume that the JSON "5" is not a float but an int.
// So this this guarantee that the Go float value 5.0 is encoded as "5.0" and not "5".
// This should disapear when Bleemeo consumer is upgraded to support int as float
type forceDecimalFloat float64

// MarshalJSON do what comment on forceDecimalFloat say
func (f forceDecimalFloat) MarshalJSON() ([]byte, error) {
	buffer, err := json.Marshal(float64(f))
	if err != nil {
		return buffer, err
	}
	for _, b := range buffer {
		if b == '.' || b == 'e' {
			return buffer, err
		}
	}
	buffer = append(buffer, '.', '0')
	return buffer, err
}

// New create a new client
func New(option Option) *Client {
	return &Client{
		option: option,
	}
}

// Connected returns true if MQTT connection is established
func (c *Client) Connected() bool {
	c.l.Lock()
	defer c.l.Unlock()
	if !c.setupDone {
		return false
	}
	return c.mqttClient.IsConnectionOpen()
}

// Disable will disable (or re-enable) the MQTT connection until given time.
// To re-enable, set a time in the past.
func (c *Client) Disable(until time.Time, reason bleemeoTypes.DisableReason) {
	c.l.Lock()
	defer c.l.Unlock()
	c.disabledUntil = until
	c.disableReason = reason
}

// Run connect and transmit information to Bleemeo Cloud platform
func (c *Client) Run(ctx context.Context) error {
	c.ctx = ctx
	paho.ERROR = logger.V(2)
	paho.CRITICAL = logger.V(2)
	paho.DEBUG = logger.V(3)
	for !c.ready() {
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
			return nil
		}
	}
	err := c.run(ctx)
	shutdownErr := c.shutdown()
	if err != nil {
		return err
	}
	return shutdownErr
}

// LastReport returns the date of last report with Bleemeo API
func (c *Client) LastReport() time.Time {
	c.l.Lock()
	defer c.l.Unlock()
	return c.lastReport
}

// HealthCheck perform some health check and logger any issue found
func (c *Client) HealthCheck() bool {
	ok := true
	if !c.Connected() {
		logger.Printf("Bleemeo connection (MQTT) is currently not established")
		ok = false
	}
	c.l.Lock()
	defer c.l.Unlock()
	if c.failedPointsCount >= maxPendingPoints {
		logger.Printf("%d points are waiting to be sent to Bleemeo Cloud platform. Older points are being dropped", c.failedPointsCount)
	} else if c.failedPointsCount > 1000 {
		logger.Printf("%d points are waiting to be sent to Bleemeo Cloud platform", c.failedPointsCount)
	}
	return ok
}

func (c *Client) setupMQTT() error {
	pahoOptions := paho.NewClientOptions()
	willPayload, err := json.Marshal(map[string]string{"disconnect-cause": "disconnect-will"})
	if err != nil {
		return err
	}
	pahoOptions.SetBinaryWill(
		fmt.Sprintf("v1/agent/%s/disconnect", c.option.AgentID),
		willPayload,
		1,
		false,
	)
	brokerURL := fmt.Sprintf("%s:%d", c.option.Config.String("bleemeo.mqtt.host"), c.option.Config.Int("bleemeo.mqtt.port"))
	if c.option.Config.Bool("bleemeo.mqtt.ssl") {
		tlsConfig := &tls.Config{}
		caFile := c.option.Config.String("bleemeo.mqtt.cafile")
		if caFile != "" {
			if rootCAs, err := loadRootCAs(caFile); err != nil {
				logger.Printf("Unable to load CAs from %#v", caFile)
			} else {
				tlsConfig.RootCAs = rootCAs
			}
		}
		if c.option.Config.Bool("bleemeo.mqtt.ssl_insecure") {
			tlsConfig.InsecureSkipVerify = true
		}
		pahoOptions.SetTLSConfig(tlsConfig)
		brokerURL = "ssl://" + brokerURL
	} else {
		brokerURL = "tcp://" + brokerURL
	}
	pahoOptions.SetUsername(fmt.Sprintf("%s@bleemeo.com", c.option.AgentID))
	pahoOptions.SetPassword(c.option.AgentPassword)
	pahoOptions.AddBroker(brokerURL)
	pahoOptions.SetConnectionLostHandler(c.onConnectionLost)
	pahoOptions.SetOnConnectHandler(c.onConnect)
	c.mqttClient = paho.NewClient(pahoOptions)
	c.l.Lock()
	defer c.l.Unlock()
	c.setupDone = true
	return nil
}

func (c *Client) shutdown() error {
	if c.mqttClient == nil {
		return nil
	}
	deadline := time.Now().Add(5 * time.Second)
	if c.mqttClient.IsConnectionOpen() {
		cause := "Clean shutdown"
		if _, err := os.Stat(c.option.Config.String("agent.upgrade_file")); err == nil {
			cause = "Upgrade"
		}
		payload, err := json.Marshal(map[string]string{"disconnect-cause": cause})
		if err != nil {
			return err
		}
		c.publish(fmt.Sprintf("v1/agent/%s/disconnect", c.option.AgentID), payload)
	}
	stillPending := c.waitPublish(deadline)
	if stillPending > 0 {
		logger.V(2).Printf("%d MQTT message were still pending", stillPending)
	}
	c.mqttClient.Disconnect(uint(time.Until(deadline).Seconds() * 1000))
	return nil
}

func (c *Client) run(ctx context.Context) error {
	if err := c.setupMQTT(); err != nil {
		return err
	}
	c.connect(ctx)

	storeNotifieeID := c.option.Store.AddNotifiee(c.addPoints)

	var topinfoSendAt time.Time

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		disableUntil, _ := c.getDisableUntil()
		if time.Now().Before(disableUntil) {
			c.mqttClient.Disconnect(0)
			c.connect(ctx) // connect wait for disableUntil to be passed
		}
		cfg := c.option.Cache.AccountConfig()
		c.sendPoints()
		if time.Since(topinfoSendAt) >= time.Duration(cfg.LiveProcessResolution)*time.Second {
			topinfoSendAt = time.Now()
			c.sendTopinfo(ctx, cfg)
		}
		c.waitPublish(time.Now().Add(5 * time.Second))
		select {
		case <-ticker.C:
		case <-ctx.Done():
		}
	}
	c.option.Store.RemoveNotifiee(storeNotifieeID)
	return nil
}

func (c *Client) addPoints(points []types.MetricPoint) {
	c.l.Lock()
	defer c.l.Unlock()
	if time.Now().Before(c.disabledUntil) && c.disableReason == bleemeoTypes.DisableDuplicatedAgent {
		return
	}
	c.pendingPoints = append(c.pendingPoints, points...)
}

func (c *Client) popPendingPoints() []types.MetricPoint {
	c.l.Lock()
	defer c.l.Unlock()
	points := c.pendingPoints
	c.pendingPoints = nil
	return points
}

func (c *Client) sendPoints() {
	metricWhitelist := c.option.Cache.AccountConfig().MetricsAgentWhitelistMap()
	points := filterPoints(c.popPendingPoints(), metricWhitelist)
	if !c.Connected() {
		c.failedPoints = append(c.failedPoints, points...)
		if len(c.failedPoints) > maxPendingPoints {
			c.failedPoints = c.failedPoints[len(c.failedPoints)-maxPendingPoints : len(c.failedPoints)]
		}
		// Make sure that when connection is back we retry failed point as soon as possible
		c.lastFailedPointsRetry = time.Time{}
		c.l.Lock()
		defer c.l.Unlock()
		c.failedPointsCount = len(c.failedPoints)
		return
	}
	registreredMetrics := c.option.Cache.Metrics()
	registreredMetricByKey := common.MetricLookupFromList(registreredMetrics)

	if len(c.failedPoints) > 0 && c.Connected() && (time.Since(c.lastFailedPointsRetry) > 5*time.Minute || len(registreredMetricByKey) != c.lastRegisteredMetricsCount) {
		localMetrics, err := c.option.Store.Metrics(nil)
		if err != nil {
			return
		}
		localExistsByKey := make(map[common.MetricLabelItem]bool, len(localMetrics))
		for _, m := range localMetrics {
			key := common.MetricLabelItemFromMetric(m)
			localExistsByKey[key] = true
		}
		c.lastRegisteredMetricsCount = len(registreredMetricByKey)
		c.lastFailedPointsRetry = time.Now()
		newPoints := make([]types.MetricPoint, 0, len(c.failedPoints))
		for _, p := range c.failedPoints {
			key := common.MetricLabelItemFromMetric(p.Labels)
			if localExistsByKey[key] {
				newPoints = append(newPoints, p)
			}
		}
		points = append(filterPoints(newPoints, metricWhitelist), points...)
		c.failedPoints = nil
	}

	payload := make([]metricPayload, 0, len(points))
	payload = c.preparePoints(payload, registreredMetricByKey, points)
	logger.V(2).Printf("MQTT send %d points", len(payload))
	for i := 0; i < len(payload); i += pointsBatchSize {
		end := i + pointsBatchSize
		if end > len(payload) {
			end = len(payload)
		}
		buffer, err := json.Marshal(payload[i:end])
		if err != nil {
			logger.V(1).Printf("Unable to encode points: %v", err)
			return
		}
		c.publish(fmt.Sprintf("v1/agent/%s/data", c.option.AgentID), buffer)
	}
	c.l.Lock()
	defer c.l.Unlock()
	c.failedPointsCount = len(c.failedPoints)
}

func (c *Client) preparePoints(payload []metricPayload, registreredMetricByKey map[common.MetricLabelItem]bleemeoTypes.Metric, points []types.MetricPoint) []metricPayload {
	for _, p := range points {
		key := common.MetricLabelItemFromMetric(p.Labels)
		if m, ok := registreredMetricByKey[key]; ok {
			value := metricPayload{
				UUID:        m.ID,
				Measurement: p.Labels["__name__"],
				Timestamp:   p.Time.Unix(),
				Value:       forceDecimalFloat(p.Value),
				Item:        p.Labels["item"],
			}
			if p.CurrentStatus.IsSet() {
				value.Status = p.CurrentStatus.String()
				value.ProblemOrigin = p.StatusDescription.StatusDescription
				if p.Labels["container_id"] != "" {
					lastKilledAt := c.option.Docker.ContainerLastKill(p.Labels["container_id"])
					gracePeriod := time.Since(lastKilledAt) + 300*time.Second
					if gracePeriod > 60*time.Second {
						value.EventGracePeriod = int(gracePeriod.Seconds())
					}
				}
			}
			payload = append(payload, value)
		} else {
			c.failedPoints = append(c.failedPoints, p)
		}
	}
	return payload
}

func (c *Client) getDisableUntil() (time.Time, bleemeoTypes.DisableReason) {
	c.l.Lock()
	defer c.l.Unlock()
	return c.disabledUntil, c.disableReason
}

func (c *Client) connect(ctx context.Context) {
	optionReader := c.mqttClient.OptionsReader()
	delay := 0 * time.Second
	firstConnect := true

	for ctx.Err() == nil {
		delay *= 2
		if delay > optionReader.MaxReconnectInterval() {
			delay = optionReader.MaxReconnectInterval()
		}
		common.WaitDeadline(c.ctx, delay, c.getDisableUntil, "Bleemeo MQTT connection")

		if firstConnect {
			logger.V(2).Printf("Connecting to MQTT broker %v", optionReader.Servers()[0])
			firstConnect = false
			delay = 5 * time.Second
		}
		token := c.mqttClient.Connect()
		for !token.WaitTimeout(1 * time.Second) {
			if ctx.Err() != nil {
				return
			}
		}
		if token.Error() == nil {
			break
		}
		logger.V(1).Printf("Unable to connect to Bleemeo MQTT (retry in %v): %v", delay, token.Error())
	}
}

func (c *Client) onConnect(_ paho.Client) {
	logger.Printf("MQTT connection established")
	// Use short max-age to force a refresh facts since a reconnection to MQTT may
	// means that public IP change.
	facts, err := c.option.Facts.Facts(c.ctx, 10*time.Second)
	if err != nil {
		logger.V(2).Printf("Unable to get facts: %v", err)
	}
	payload, err := json.Marshal(map[string]string{"public_ip": facts["public_ip"]})
	if err != nil {
		logger.V(2).Printf("Unable to encode connect message: %v", err)
		return
	}
	c.publish(fmt.Sprintf("v1/agent/%s/connect", c.option.AgentID), payload)
	c.mqttClient.Subscribe(
		fmt.Sprintf("v1/agent/%s/notification", c.option.AgentID),
		0,
		c.onNotification,
	)
}

type notificationPayload struct {
	MessageType string `json:"message_type"`
	MetricUUID  string `json:"metric_uuid,omitempty"`
}

func (c *Client) onNotification(_ paho.Client, msg paho.Message) {
	if len(msg.Payload()) > 1024*60 {
		logger.V(1).Printf("Ignoring abnormally big MQTT message")
		return
	}
	var payload notificationPayload
	if err := json.Unmarshal(msg.Payload(), &payload); err != nil {
		logger.V(1).Printf("Failed to decode MQTT message: %v", err)
		return
	}
	logger.V(2).Printf("Got notification message %s", payload.MessageType)
	switch payload.MessageType {
	case "config-changed":
		c.option.UpdateConfigCallback(true)
	case "config-will-change":
		c.option.UpdateConfigCallback(false)
	case "threshold-update":
		c.option.UpdateMetrics(payload.MetricUUID)
	}
}

func (c *Client) onConnectionLost(_ paho.Client, err error) {
	logger.Printf("MQTT connection lost: %v", err)
	c.l.Lock()
	defer c.l.Unlock()
	c.lastDisconnectionTime = append(c.lastDisconnectionTime, time.Now())
	if len(c.lastDisconnectionTime) > 15 {
		c.lastDisconnectionTime = c.lastDisconnectionTime[len(c.lastDisconnectionTime)-15:]
	}
	length := len(c.lastDisconnectionTime)
	until := time.Time{}
	if length >= 6 && time.Since(c.lastDisconnectionTime[length-6]) < time.Minute {
		delay := common.JitterDelay(60, 0.25, 60).Round(time.Second)
		until = time.Now().Add(delay)
		logger.Printf("Too many attempt to connect to MQTT on last minute. Disable MQTT for %v", delay)
	}
	if length >= 15 && time.Since(c.lastDisconnectionTime[length-15]) < 10*time.Minute {
		delay := common.JitterDelay(300, 0.25, 300).Round(time.Second)
		until = time.Now().Add(delay)
		logger.Printf("Too many attempt to connect to MQTT on last 10 minutes. Disable MQTT for %v", delay)
	}
	if c.disabledUntil.Before(until) {
		c.disabledUntil = until
		c.disableReason = bleemeoTypes.DisableTooManyErrors
		c.mqttClient.Disconnect(0)
		// Trigger facts synchronization to check for duplicate agent
		_, _ = c.option.Facts.Facts(c.ctx, 0)
	}
}

func (c *Client) publish(topic string, payload []byte) {
	token := c.mqttClient.Publish(topic, 1, false, payload)
	c.l.Lock()
	defer c.l.Unlock()
	c.pendingToken = append(c.pendingToken, token)
}

func (c *Client) sendTopinfo(ctx context.Context, cfg bleemeoTypes.AccountConfig) {
	topinfo, err := c.option.Process.TopInfo(ctx, time.Duration(cfg.LiveProcessResolution)*time.Second/2)
	if err != nil {
		logger.V(1).Printf("Unable to get topinfo: %v", err)
		return
	}
	topic := fmt.Sprintf("v1/agent/%s/top_info", c.option.AgentID)

	var buffer bytes.Buffer
	w := zlib.NewWriter(&buffer)
	err = json.NewEncoder(w).Encode(topinfo)
	if err != nil {
		logger.V(1).Printf("Unable to get encode topinfo: %v", err)
		w.Close()
		return
	}
	err = w.Close()
	if err != nil {
		logger.V(1).Printf("Unable to get encode topinfo: %v", err)
		return
	}
	c.publish(topic, buffer.Bytes())
}

func (c *Client) waitPublish(deadline time.Time) (stillPendingCount int) {
	stillPending := make([]paho.Token, 0)
	c.l.Lock()
	defer c.l.Unlock()
	for _, t := range c.pendingToken {
		if t.WaitTimeout(time.Until(deadline)) {
			if t.Error() != nil {
				logger.V(2).Printf("MQTT publish failed: %v", t.Error())
			}
			c.lastReport = time.Now()
		} else {
			stillPending = append(stillPending, t)
		}
	}
	c.pendingToken = stillPending
	return len(c.pendingToken)
}

func loadRootCAs(caFile string) (*x509.CertPool, error) {
	rootCAs := x509.NewCertPool()
	certs, err := ioutil.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	ok := rootCAs.AppendCertsFromPEM(certs)
	if !ok {
		return nil, errors.New("not a PEM file")
	}
	return rootCAs, nil
}

func filterPoints(input []types.MetricPoint, metricWhitelist map[string]bool) []types.MetricPoint {
	result := make([]types.MetricPoint, 0)
	for _, m := range input {
		if common.AllowMetric(m.Labels, metricWhitelist) {
			result = append(result, m)
		}
	}
	return result
}

func (c *Client) ready() bool {
	cfg := c.option.Cache.AccountConfig()
	if cfg.LiveProcessResolution == 0 || cfg.MetricAgentResolution == 0 {
		logger.V(2).Printf("MQTT not ready, Agent as no configuration")
		return false
	}
	for _, m := range c.option.Cache.Metrics() {
		if m.Label == "agent_status" && m.Labels["item"] == "" {
			return true
		}
	}
	logger.V(2).Printf("MQTT not ready, metric \"agent_status\" is not yet registered")
	return false
}
