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
	"math"
	"math/rand"
	"os"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

const maxPendingPoints = 100000
const pointsBatchSize = 1000
const minimalDelayBetweenConnect = 5 * time.Second
const maximalDelayBetweenConnect = 10 * time.Minute
const stableConnection = 5 * time.Minute

// Option are parameter for the MQTT client.
type Option struct {
	bleemeoTypes.GlobalOption
	Cache         *cache.Cache
	AgentID       types.AgentID
	AgentPassword string

	// DisableCallback is a function called when MQTT got too much connect/disconnection.
	DisableCallback func(reason bleemeoTypes.DisableReason, until time.Time)
	// UpdateConfigCallback is a function called when Agent configuration (will) change
	UpdateConfigCallback func(now bool)
	// UpdateMetrics request update for given metric UUIDs
	UpdateMetrics func(metricUUID ...string)

	InitialPoints map[types.AgentID][]types.MetricPoint
}

// Client is an MQTT client for Bleemeo Cloud platform.
type Client struct {
	option Option

	// Those variable are write once or only read/write from Run() gorouting. No lock needed
	ctx                        context.Context
	mqttClient                 paho.Client
	failedPoints               map[types.AgentID][]types.MetricPoint
	lastRegisteredMetricsCount int
	lastFailedPointsRetry      time.Time

	l                 sync.Mutex
	pendingMessage    []message
	pendingPoints     map[types.AgentID][]types.MetricPoint
	lastReport        time.Time
	failedPointsCount int
	disabledUntil     time.Time
	disableReason     bleemeoTypes.DisableReason
	connectionLost    chan interface{}
	disableNotify     chan interface{}
}

type message struct {
	token   paho.Token
	retry   bool
	topic   string
	payload []byte
}

type metricPayload struct {
	UUID              string            `json:"uuid,omitempty"`
	Measurement       string            `json:"measurement"`    // TODO: this could be dropped once consumer is updated to only use UUID or LabelsText
	BleemeoItem       string            `json:"item,omitempty"` // TODO: this could be dropped once consumer is updated to only use UUID or LabelsText
	LabelsText        string            `json:"labels_text"`
	Timestamp         int64             `json:"time"` // TODO: could drop this field once consumer is updated to support time_ms
	TimestampMS       int64             `json:"time_ms"`
	Value             forceDecimalFloat `json:"value"`
	Status            string            `json:"status,omitempty"`
	StatusDescription string            `json:"status_description,omitempty"`
	EventGracePeriod  int               `json:"event_grace_period,omitempty"`
}

// This type is only used because the Bleemeo consumer require Value to be a float,
// and assume that the JSON "5" is not a float but an int.
// So this this guarantee that the Go float value 5.0 is encoded as "5.0" and not "5".
// This should disapear when Bleemeo consumer is upgraded to support int as float.
type forceDecimalFloat float64

// MarshalJSON do what comment on forceDecimalFloat say.
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

// New create a new client.
func New(option Option, first bool) *Client {
	if first {
		paho.ERROR = logger.V(2)
		paho.CRITICAL = logger.V(2)
		paho.DEBUG = logger.V(3)
	}

	res := &Client{
		option: option,
	}

	// assignments on nil maps are a great recipes for panics, let's try to not allow such behavior
	res.failedPoints = map[types.AgentID][]types.MetricPoint{}
	if res.option.InitialPoints == nil {
		res.option.InitialPoints = map[types.AgentID][]types.MetricPoint{}
	}

	return res
}

// Connected returns true if MQTT connection is established.
func (c *Client) Connected() bool {
	c.l.Lock()
	defer c.l.Unlock()

	if c.mqttClient == nil {
		return false
	}

	return c.mqttClient.IsConnectionOpen()
}

// Disable will disable the MQTT connection until given time.
// To re-enable use the (not yet implemented) Enable().
func (c *Client) Disable(until time.Time, reason bleemeoTypes.DisableReason) {
	c.l.Lock()
	defer c.l.Unlock()

	if c.disabledUntil.Before(until) {
		c.disabledUntil = until
		c.disableReason = reason

		if reason == bleemeoTypes.DisableTooManyErrors {
			// Trigger facts synchronization to check for duplicate agent
			_, _ = c.option.Facts.Facts(c.ctx, 0)
		}
	}

	if c.disableNotify != nil {
		select {
		case c.disableNotify <- nil:
		default:
		}
	}
}

// Run connect and transmit information to Bleemeo Cloud platform.
func (c *Client) Run(ctx context.Context) error {
	c.ctx = ctx

	c.failedPoints = map[types.AgentID][]types.MetricPoint{}
	c.pendingPoints = map[types.AgentID][]types.MetricPoint{}

	c.l.Lock()
	c.disableNotify = make(chan interface{})
	c.connectionLost = make(chan interface{})
	c.pendingPoints = c.option.InitialPoints
	c.option.InitialPoints = map[types.AgentID][]types.MetricPoint{}
	c.l.Unlock()

	for !c.ready() {
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
			return nil
		}
	}

	err := c.run(ctx)

	return err
}

// LastReport returns the date of last report with Bleemeo API.
func (c *Client) LastReport() time.Time {
	c.l.Lock()
	defer c.l.Unlock()

	return c.lastReport
}

// HealthCheck perform some health check and logger any issue found.
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

func (c *Client) setupMQTT() paho.Client {
	pahoOptions := paho.NewClientOptions()

	willPayload, _ := json.Marshal(map[string]string{"disconnect-cause": "disconnect-will"})

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
	pahoOptions.SetAutoReconnect(false)
	pahoOptions.SetConnectionLostHandler(c.onConnectionLost)
	pahoOptions.SetOnConnectHandler(c.onConnect)

	return paho.NewClient(pahoOptions)
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

		c.publish(fmt.Sprintf("v1/agent/%s/disconnect", c.option.AgentID), payload, true)
	}

	c.l.Lock()
	defer c.l.Unlock()

	stillPending := c.waitPublishAndResend(c.mqttClient, deadline, true)
	if stillPending > 0 {
		logger.V(2).Printf("%d MQTT message were still pending", stillPending)
	}

	c.mqttClient.Disconnect(uint(time.Until(deadline).Seconds() * 1000))
	c.mqttClient = nil

	return nil
}

func (c *Client) run(ctx context.Context) error {
	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()
		c.connectionManager(ctx)
	}()

	storeNotifieeID := c.option.Store.AddNotifiee(c.addPoints)

	var topinfoSendAt time.Time

	time.Sleep(time.Duration(rand.Intn(10000)) * time.Millisecond)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for ctx.Err() == nil {
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
	wg.Wait()

	return nil
}

// addPoints preprocesses and appends a list of metric points to those pending transmission over MQTT.
func (c *Client) addPoints(points []types.MetricPoint) {
	c.l.Lock()
	defer c.l.Unlock()

	if time.Now().Before(c.disabledUntil) && c.disableReason == bleemeoTypes.DisableDuplicatedAgent {
		return
	}

	monitors := c.option.Cache.Monitors()

	// If we wanted to limit allocations, the best strategy would probably be a two-pass approach where we
	// first get the number of MetricPoints for each agent/monitor, so that we can allocate them only once
	// (chances are such an approach would increase performances too, as linear iteration ought to be faster
	// than reallocation(s)).
	// I settled for the simple way for now.

	// Determine the appropriate agent ID and add the point to the matching list
	for _, newPoint := range points {
		idx := c.option.AgentID

		if newPoint.Annotations.Kind == types.MonitorMetricKind {
			// search the monitor in the active monitors, if it isn't there just drop the point
			url, present := newPoint.Labels["instance"]
			if !present {
				logger.V(2).Printf("Couldn't find the URL on point %v originating from a probe (missing 'instance' label)", newPoint)
				continue
			}

			monitor, present := monitors[url]

			if !present {
				// no such monitor, let's drop this point
				continue
			}
			// Hurray, it's a monitor ! The agent ID is thus the ID of the "owner" of that monitor.
			idx = types.AgentID(monitor.AgentID)
		}

		c.pendingPoints[idx] = append(c.pendingPoints[idx], newPoint)
	}
}

// popPoints returns the mapping between the agent/monitor UUID and a list of corresponding metrics.
// When 'includeFailedPoints' is set to true, the returned data will no only include all pending points,
// but also all the points whose submission failed previously.
func (c *Client) popPoints(includeFailedPoints bool) map[types.AgentID][]types.MetricPoint {
	c.l.Lock()
	defer c.l.Unlock()

	points := map[types.AgentID][]types.MetricPoint{}

	if includeFailedPoints {
		points = c.failedPoints
		c.failedPoints = map[types.AgentID][]types.MetricPoint{}
		c.failedPointsCount = 0
	}

	for agentID, pendingPoints := range c.pendingPoints {
		points[agentID] = append(points[agentID], pendingPoints...)
	}

	c.pendingPoints = map[types.AgentID][]types.MetricPoint{}

	return points
}

func (c *Client) popNewPendingPoints() map[types.AgentID][]types.MetricPoint {
	return c.popPoints(false)
}

// PopPendingPoints get and remove all pending points from this MQTT connector, including points that previously failed.
func (c *Client) PopPendingPoints() map[types.AgentID][]types.MetricPoint {
	return c.popPoints(true)
}

func (c *Client) sendPoints() {
	metricWhitelist := c.option.Cache.AccountConfig().MetricsAgentWhitelistMap()
	points := filterPoints(c.popNewPendingPoints(), metricWhitelist)

	if !c.Connected() {
		c.l.Lock()

		// store all new points as failed ones
		for idx, v := range points {
			c.failedPoints[idx] = append(c.failedPoints[idx], v...)
		}

		// maxPendingPoints is no longer a global limit but a limit per agent and per probe
		for idx := range c.failedPoints {
			if len(c.failedPoints[idx]) > maxPendingPoints {
				c.failedPoints[idx] = c.failedPoints[idx][len(c.failedPoints)-maxPendingPoints : len(c.failedPoints)]
			}
		}

		// Make sure that when connection is back we retry failed point as soon as possible
		c.lastFailedPointsRetry = time.Time{}

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

		localExistsByKey := make(map[string]bool, len(localMetrics))

		for _, m := range localMetrics {
			key := common.LabelsToText(m.Labels(), m.Annotations(), c.option.MetricFormat == types.MetricFormatBleemeo)
			localExistsByKey[key] = true
		}

		c.lastRegisteredMetricsCount = len(registreredMetricByKey)
		c.lastFailedPointsRetry = time.Now()
		newPoints := map[types.AgentID][]types.MetricPoint{}

		for agentID, agentFailedPoints := range c.failedPoints {
			for _, p := range agentFailedPoints {
				key := common.LabelsToText(p.Labels, p.Annotations, c.option.MetricFormat == types.MetricFormatBleemeo)
				if localExistsByKey[key] {
					newPoints[agentID] = append(newPoints[agentID], p)
				}
			}
		}

		for idx, v := range filterPoints(newPoints, metricWhitelist) {
			points[idx] = append(v, points[idx]...)
		}

		c.failedPoints = map[types.AgentID][]types.MetricPoint{}
	}

	payload := make(map[types.AgentID][]metricPayload, len(points))
	payload = c.preparePoints(payload, registreredMetricByKey, points)
	nbPoints := 0

	for _, metrics := range payload {
		nbPoints += len(metrics)
	}

	logger.V(2).Printf("MQTT: sending %d points", nbPoints)

	for agentID, agentPayload := range payload {
		for i := 0; i < len(agentPayload); i += pointsBatchSize {
			end := i + pointsBatchSize
			if end > len(agentPayload) {
				end = len(agentPayload)
			}

			buffer, err := json.Marshal(agentPayload[i:end])
			if err != nil {
				logger.V(1).Printf("Unable to encode points: %v", err)
				return
			}

			c.publish(fmt.Sprintf("v1/agent/%s/data", agentID), buffer, true)
		}
	}

	c.l.Lock()
	defer c.l.Unlock()

	c.failedPointsCount = len(c.failedPoints)
}

func (c *Client) preparePoints(payload map[types.AgentID][]metricPayload, registreredMetricByKey map[string]bleemeoTypes.Metric,
	points map[types.AgentID][]types.MetricPoint) map[types.AgentID][]metricPayload {
	for agentID, agentPoints := range points {
		for _, p := range agentPoints {
			key := common.LabelsToText(p.Labels, p.Annotations, c.option.MetricFormat == types.MetricFormatBleemeo)
			if m, ok := registreredMetricByKey[key]; ok {
				value := metricPayload{
					LabelsText:  m.LabelsText,
					Timestamp:   p.Time.Unix(),
					TimestampMS: p.Time.UnixNano() / 1e6,
					Value:       forceDecimalFloat(p.Value),
				}

				value.UUID = m.ID
				if c.option.MetricFormat == types.MetricFormatBleemeo {
					value.LabelsText = ""
					value.Measurement = p.Labels[types.LabelName]
					value.BleemeoItem = common.TruncateItem(p.Annotations.BleemeoItem, p.Annotations.ServiceName != "")
				}

				if p.Annotations.Status.CurrentStatus.IsSet() {
					value.Status = p.Annotations.Status.CurrentStatus.String()
					value.StatusDescription = p.Annotations.Status.StatusDescription

					if p.Annotations.ContainerID != "" {
						lastKilledAt := c.option.Docker.ContainerLastKill(p.Annotations.ContainerID)
						gracePeriod := time.Since(lastKilledAt) + 300*time.Second

						if gracePeriod > 60*time.Second {
							value.EventGracePeriod = int(gracePeriod.Seconds())
						}
					}
				}

				payload[agentID] = append(payload[agentID], value)
			} else {
				c.failedPoints[agentID] = append(c.failedPoints[agentID], p)
			}
		}
	}

	return payload
}

func (c *Client) getDisableUntil() (time.Time, bleemeoTypes.DisableReason) {
	c.l.Lock()
	defer c.l.Unlock()

	return c.disabledUntil, c.disableReason
}

func (c *Client) onConnect(mqttClient paho.Client) {
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

	c.publish(fmt.Sprintf("v1/agent/%s/connect", c.option.AgentID), payload, true)
	mqttClient.Subscribe(
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
	c.connectionLost <- nil
}

func (c *Client) publish(topic string, payload []byte, retry bool) {
	c.l.Lock()
	defer c.l.Unlock()

	msg := message{
		retry:   retry,
		payload: payload,
		topic:   topic,
	}

	if c.mqttClient == nil && !retry {
		return
	}

	if c.mqttClient != nil {
		msg.token = c.mqttClient.Publish(topic, 1, false, payload)
	}

	c.pendingMessage = append(c.pendingMessage, msg)
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

	c.publish(topic, buffer.Bytes(), false)
}

func (c *Client) waitPublish(deadline time.Time) (stillPendingCount int) {
	c.l.Lock()
	defer c.l.Unlock()

	if c.mqttClient == nil {
		return len(c.pendingMessage)
	}

	return c.waitPublishAndResend(c.mqttClient, deadline, false)
}

func (c *Client) waitPublishAndResend(mqttClient paho.Client, deadline time.Time, resend bool) (stillPendingCount int) {
	stillPending := make([]message, 0)

	for _, m := range c.pendingMessage {
		if m.token != nil && m.token.WaitTimeout(time.Until(deadline)) {
			if m.token.Error() != nil {
				logger.V(2).Printf("MQTT publish on %s failed: %v", m.topic, m.token.Error())
			} else {
				c.lastReport = time.Now()
				continue
			}

			m.token = nil
		}

		if m.token == nil && !m.retry {
			continue
		}

		if m.token == nil && resend {
			m.token = mqttClient.Publish(m.topic, 1, false, m.payload)
		}

		stillPending = append(stillPending, m)
	}

	c.pendingMessage = stillPending

	logger.V(3).Printf("%d messages are still pending", len(c.pendingMessage))

	return len(c.pendingMessage)
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

func filterPoints(input map[types.AgentID][]types.MetricPoint, metricWhitelist map[string]bool) map[types.AgentID][]types.MetricPoint {
	result := map[types.AgentID][]types.MetricPoint{}

	for k, mps := range input {
		for _, mp := range mps {
			if common.AllowMetric(mp.Labels, mp.Annotations, metricWhitelist) {
				result[k] = append(result[k], mp)
			}
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
		if m.Labels[types.LabelName] == "agent_status" {
			return true
		}
	}

	logger.V(2).Printf("MQTT not ready, metric \"agent_status\" is not yet registered")

	return false
}

func (c *Client) connectionManager(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	var (
		lastConnectionTimes []time.Time
		lastResend          time.Time
	)

	currentConnectDelay := minimalDelayBetweenConnect / 2
	consecutiveError := 0

mainLoop:
	for ctx.Err() == nil {
		disableUntil, disableReason := c.getDisableUntil()
		switch {
		case time.Now().Before(disableUntil):
			if c.mqttClient != nil {
				logger.V(2).Printf("Disconnection from MQTT due to %v", disableReason)
				c.mqttClient.Disconnect(0)

				c.l.Lock()
				c.mqttClient = nil
				c.l.Unlock()
			}
		case c.mqttClient == nil:
			length := len(lastConnectionTimes)

			if length >= 7 && time.Since(lastConnectionTimes[length-7]) < 10*time.Minute {
				delay := common.JitterDelay(300, 0.25, 300).Round(time.Second)

				c.Disable(time.Now().Add(delay), bleemeoTypes.DisableTooManyErrors)
				logger.Printf("Too many attempt to connect to MQTT on last 10 minutes. Disable MQTT for %v", delay)
				continue
			}

			if length == 0 || time.Since(lastConnectionTimes[length-1]) > currentConnectDelay {
				lastConnectionTimes = append(lastConnectionTimes, time.Now())

				if len(lastConnectionTimes) > 20 {
					lastConnectionTimes = lastConnectionTimes[len(lastConnectionTimes)-20:]
				}

				if currentConnectDelay < maximalDelayBetweenConnect {
					consecutiveError++
					currentConnectDelay = common.JitterDelay(minimalDelayBetweenConnect.Seconds()*math.Pow(1.55, float64(consecutiveError)), 0.1, maximalDelayBetweenConnect.Seconds())
					if consecutiveError == 5 {
						// Trigger facts synchronization to check for duplicate agent
						_, _ = c.option.Facts.Facts(c.ctx, time.Minute)
					}
				}
				mqttClient := c.setupMQTT()

				optionReader := mqttClient.OptionsReader()
				logger.V(2).Printf("Connecting to MQTT broker %v", optionReader.Servers()[0])

				token := mqttClient.Connect()
				for !token.WaitTimeout(1 * time.Second) {
					if ctx.Err() != nil {
						break mainLoop
					}
				}

				if token.Error() != nil {
					delay := currentConnectDelay - time.Since(lastConnectionTimes[len(lastConnectionTimes)-1])
					logger.V(1).Printf("Unable to connect to Bleemeo MQTT (retry in %v): %v", delay, token.Error())

					// we must disconnect to stop paho gorouting that otherwise will be
					// started multiple time for each Connect()
					mqttClient.Disconnect(0)
				} else {
					c.l.Lock()
					c.waitPublishAndResend(mqttClient, time.Now().Add(10*time.Second), true)
					c.mqttClient = mqttClient
					c.l.Unlock()
				}
			}
		case c.mqttClient != nil && c.mqttClient.IsConnectionOpen() && time.Since(lastResend) > 10*time.Minute:
			lastResend = time.Now()
			c.l.Lock()
			c.waitPublishAndResend(c.mqttClient, time.Now().Add(10*time.Second), true)
			c.l.Unlock()
		}

		select {
		case <-ctx.Done():
		case <-c.connectionLost:
			c.l.Lock()
			c.mqttClient.Disconnect(0)
			c.mqttClient = nil
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

	if err := c.shutdown(); err != nil {
		logger.V(1).Printf("Unable to perform clean shutdown: %v", err)
	}

	// make sure all connectionLost are read
	for {
		select {
		case <-c.connectionLost:
		default:
			return
		}
	}
}
