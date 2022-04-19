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
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"glouton/bleemeo/internal/cache"
	"glouton/bleemeo/internal/common"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/delay"
	"glouton/logger"
	"glouton/types"
	"io/ioutil"
	"math"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

var errNotPem = errors.New("not a PEM file")

const (
	maxPendingPoints           = 100000
	pointsBatchSize            = 1000
	minimalDelayBetweenConnect = 5 * time.Second
	maximalDelayBetweenConnect = 10 * time.Minute
	stableConnection           = 5 * time.Minute
)

// Option are parameter for the MQTT client.
type Option struct {
	bleemeoTypes.GlobalOption
	Cache         *cache.Cache
	AgentID       bleemeoTypes.AgentID
	AgentPassword string

	// UpdateConfigCallback is a function called when Agent configuration (will) change
	UpdateConfigCallback func(now bool)
	// UpdateMetrics request update for given metric UUIDs
	UpdateMetrics func(metricUUID ...string)
	// UpdateMonitor requests a sync of a monitor
	UpdateMonitor func(op string, uuid string)
	// UpdateAlertingRule requests an update for the given alerting rule UUID.
	UpdateAlertingRule func(uuid string)
	// UpdateMaintenance requests to check for the maintenance mode again
	UpdateMaintenance func()
	// GetJWT returns the JWT used to talk with the Bleemeo API.
	GetJWT func(ctx context.Context) (string, error)

	InitialPoints []types.MetricPoint
}

// Client is an MQTT client for Bleemeo Cloud platform.
type Client struct {
	option Option

	// Those variable are only written once or read/written exclusively from the Run() goroutine. No lock needed
	ctx                        context.Context //nolint:containedctx
	mqttClient                 paho.Client
	failedPoints               []types.MetricPoint
	lastRegisteredMetricsCount int
	lastFailedPointsRetry      time.Time
	encoder                    mqttEncoder

	l                   sync.Mutex
	startedAt           time.Time
	pendingMessage      []message
	pendingPoints       []types.MetricPoint
	lastReport          time.Time
	maxPointCount       int
	sendingSuspended    bool // stop sending points, used when the user is is read-only mode
	disabledUntil       time.Time
	disableReason       bleemeoTypes.DisableReason
	connectionLost      chan interface{}
	disableNotify       chan interface{}
	lastConnectionTimes []time.Time
	lastResend          time.Time
	currentConnectDelay time.Duration
	consecutiveError    int
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
	CheckOutput       string            `json:"check_output,omitempty"` // TODO: drop this field once consumer is updated to support status_description
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

	// Get the previous MQTT client from the reload state to avoid closing the connection
	// and keep the previous pending points.
	var (
		mqttClient    paho.Client
		initialPoints []types.MetricPoint
	)

	pahoWrapper := option.ReloadState.PahoWrapper()
	if pahoWrapper != nil {
		mqttClient = pahoWrapper.Client()
		initialPoints = pahoWrapper.PopPendingPoints()
	}

	option.InitialPoints = append(option.InitialPoints, initialPoints...)

	client := &Client{
		option:     option,
		mqttClient: mqttClient,
	}

	if pahoWrapper == nil {
		pahoWrapper = NewPahoWrapper(PahoWrapperOptions{
			UpgradeFile: client.option.Config.String("agent.upgrade_file"),
			AgentID:     client.option.AgentID,
		})

		option.ReloadState.SetPahoWrapper(pahoWrapper)
	}

	return client
}

// Connected returns true if MQTT connection is established.
func (c *Client) Connected() bool {
	c.l.Lock()
	defer c.l.Unlock()

	return c.connected()
}

func (c *Client) connected() bool {
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

// ClearDisable remove disabling if reason match reasonToClear. It remove the disabling only after delay.
func (c *Client) ClearDisable(reasonToClear bleemeoTypes.DisableReason, delay time.Duration) {
	c.l.Lock()
	defer c.l.Unlock()

	if time.Now().Before(c.disabledUntil) && c.disableReason == reasonToClear {
		c.disabledUntil = time.Now().Add(delay)
	}
}

// Run connect and transmit information to Bleemeo Cloud platform.
func (c *Client) Run(ctx context.Context) error {
	c.ctx = ctx

	c.l.Lock()
	c.disableNotify = make(chan interface{})
	c.connectionLost = make(chan interface{})
	c.pendingPoints = c.option.InitialPoints
	c.option.InitialPoints = nil
	c.l.Unlock()

	for !c.ready() {
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
			return nil
		}
	}

	c.l.Lock()
	c.startedAt = time.Now()
	c.l.Unlock()

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		c.receiveEvents(ctx)
		wg.Done()
	}()

	err := c.run(ctx)

	wg.Wait()

	return err
}

// DiagnosticPage return useful information to troubleshoot issue.
func (c *Client) DiagnosticPage() string {
	builder := &strings.Builder{}

	host := c.option.Config.String("bleemeo.mqtt.host")
	port := c.option.Config.Int("bleemeo.mqtt.port")

	builder.WriteString(common.DiagnosticTCP(host, port, c.tlsConfig()))

	return builder.String()
}

// DiagnosticArchive add to a zipfile useful diagnostic information.
func (c *Client) DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error {
	c.l.Lock()
	if len(c.failedPoints) > 100 {
		file, err := archive.Create("mqtt-failed-metric-points.txt")
		if err != nil {
			return err
		}

		maxSample := 50

		fmt.Fprintf(file, "MQTT connector has %d points that are failing.\n", len(c.failedPoints))
		fmt.Fprintf(file, "It usually happen when MQTT is not connector OR when metric are not registered with Bleemeo.\n")

		if maxSample > len(c.failedPoints) {
			fmt.Fprintf(file, "Here is the list of all blocked metrics:\n")

			for _, p := range c.failedPoints {
				fmt.Fprintf(file, "%v\n", p.Labels)
			}
		} else {
			fmt.Fprintf(file, "Here is a sample of %d blocked metrics:\n", maxSample)
			indices := rand.Perm(len(c.failedPoints))
			for _, i := range indices[:maxSample] {
				p := c.failedPoints[i]
				fmt.Fprintf(file, "%v\n", p.Labels)
			}
		}
	}

	file, err := archive.Create("bleemeo-mqtt-state.json")
	if err != nil {
		return err
	}

	obj := struct {
		StartedAt                  time.Time
		LastRegisteredMetricsCount int
		LastFailedPointsRetry      time.Time
		PendingMessageCount        int
		PendingPointsCount         int
		LastReport                 time.Time
		MaxPointCount              int
		SendingSuspended           bool
		DisabledUntil              time.Time
		DisableReason              bleemeoTypes.DisableReason
		LastConnectionTimes        []time.Time
		LastResend                 time.Time
	}{
		StartedAt:                  c.startedAt,
		LastRegisteredMetricsCount: c.lastRegisteredMetricsCount,
		LastFailedPointsRetry:      c.lastFailedPointsRetry,
		PendingMessageCount:        len(c.pendingMessage),
		PendingPointsCount:         len(c.pendingPoints),
		LastReport:                 c.lastReport,
		MaxPointCount:              c.maxPointCount,
		SendingSuspended:           c.sendingSuspended,
		DisabledUntil:              c.disabledUntil,
		DisableReason:              c.disableReason,
		LastConnectionTimes:        c.lastConnectionTimes,
		LastResend:                 c.lastResend,
	}

	c.l.Unlock()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
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

	failedPointsCount := len(c.failedPoints)

	if failedPointsCount >= maxPendingPoints {
		logger.Printf("%d points are waiting to be sent to Bleemeo Cloud platform. Older points are being dropped", failedPointsCount)
	} else if failedPointsCount > 1000 {
		logger.Printf("%d points are waiting to be sent to Bleemeo Cloud platform", failedPointsCount)
	}

	return ok
}

func (c *Client) setupMQTT(ctx context.Context) (paho.Client, error) {
	pahoOptions := paho.NewClientOptions()

	willPayload, _ := json.Marshal(disconnectCause{"disconnect-will"}) //nolint:errchkjson // False positive.

	pahoOptions.SetBinaryWill(
		fmt.Sprintf("v1/agent/%s/disconnect", c.option.AgentID),
		willPayload,
		1,
		false,
	)

	brokerURL := fmt.Sprintf("%s:%d", c.option.Config.String("bleemeo.mqtt.host"), c.option.Config.Int("bleemeo.mqtt.port"))

	if c.option.Config.Bool("bleemeo.mqtt.ssl") {
		tlsConfig := c.tlsConfig()

		pahoOptions.SetTLSConfig(tlsConfig)

		brokerURL = "ssl://" + brokerURL
	} else {
		brokerURL = "tcp://" + brokerURL
	}

	pahoOptions.AddBroker(brokerURL)
	pahoOptions.SetAutoReconnect(false)
	pahoOptions.SetUsername(fmt.Sprintf("%s@bleemeo.com", c.option.AgentID))

	pahoWrapper := c.option.ReloadState.PahoWrapper()
	pahoOptions.SetConnectionLostHandler(pahoWrapper.OnConnectionLost)
	pahoOptions.SetOnConnectHandler(pahoWrapper.OnConnect)

	password, err := c.option.GetJWT(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to get JWT: %w", err)
	}

	pahoOptions.SetPassword(password)

	return paho.NewClient(pahoOptions), nil
}

func (c *Client) tlsConfig() *tls.Config {
	if !c.option.Config.Bool("bleemeo.mqtt.ssl") {
		return nil
	}

	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}

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

	return tlsConfig
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

		disableUntil, disableReason := c.getDisableUntil()
		if time.Now().Before(disableUntil) && disableReason == bleemeoTypes.DisableTimeDrift {
			cause = "Clean shutdown, time drift"
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

	c.option.ReloadState.PahoWrapper().SetClient(nil)

	return nil
}

// SuspendSending sets whether the mqtt client should stop sendings metrics (and topinfo).
func (c *Client) SuspendSending(suspended bool) {
	c.l.Lock()
	defer c.l.Unlock()

	// send a message on /connect when reconnecting after being in maintenance mode
	if c.sendingSuspended && !suspended {
		if c.mqttClient != nil && c.mqttClient.IsConnectionOpen() {
			// we need to unlock/relock() as sendConnectMessage calls publish() which locks
			// the mutex too
			c.l.Unlock()
			c.sendConnectMessage()
			c.l.Lock()
		}
	}

	c.sendingSuspended = suspended
}

// IsSendingSuspended returns true if the mqtt connector is suspended from sending merics (and topinfo).
func (c *Client) IsSendingSuspended() bool {
	c.l.Lock()
	defer c.l.Unlock()

	return c.isSendingSuspended()
}

func (c *Client) isSendingSuspended() bool {
	return c.sendingSuspended
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

	time.Sleep(time.Duration(rand.Intn(10000)) * time.Millisecond) //nolint:gosec

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for ctx.Err() == nil {
		cfg, ok := c.option.Cache.CurrentAccountConfig()

		c.sendPoints()

		if !c.IsSendingSuspended() && ok && cfg.LiveProcess && time.Since(topinfoSendAt) >= cfg.LiveProcessResolution {
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

	if time.Now().Before(c.disabledUntil) && c.disableReason == bleemeoTypes.DisableTimeDrift {
		return
	}

	c.pendingPoints = append(c.pendingPoints, points...)
}

// PopPoints returns the list of metrics to be sent. When 'includeFailedPoints' is set to true, the returned
// data will not only include all pending points, but also all the points whose submission failed previously.
func (c *Client) PopPoints(includeFailedPoints bool) []types.MetricPoint {
	c.l.Lock()
	defer c.l.Unlock()

	var points []types.MetricPoint

	if includeFailedPoints {
		points = c.failedPoints
		c.failedPoints = nil
	}

	points = append(points, c.pendingPoints...)

	c.pendingPoints = nil

	return points
}

func (c *Client) sendPoints() {
	points := c.PopPoints(false)

	c.l.Lock()
	defer c.l.Unlock()

	if !c.connected() || c.isSendingSuspended() {
		// store all new points as failed ones
		c.addFailedPoints(c.filterPoints(points)...)

		// Make sure that when connection is back we retry failed points as soon as possible
		c.lastFailedPointsRetry = time.Time{}

		return
	}

	registreredMetricByKey := c.option.Cache.MetricLookupFromList()

	if len(c.failedPoints) > 0 && c.connected() && (time.Since(c.lastFailedPointsRetry) > 5*time.Minute || len(registreredMetricByKey) != c.lastRegisteredMetricsCount) {
		c.lastRegisteredMetricsCount = len(registreredMetricByKey)
		c.lastFailedPointsRetry = time.Now()

		points = append(c.failedPoints, points...)
		c.failedPoints = nil
	}

	points = c.filterPoints(points)
	payload := c.preparePoints(registreredMetricByKey, points)
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

			buffer, err := c.encoder.Encode(agentPayload[i:end])
			if err != nil {
				logger.V(1).Printf("Unable to encode points: %v", err)

				return
			}

			c.publishNoLock(fmt.Sprintf("v1/agent/%s/data", agentID), buffer, true)
		}
	}
}

// addFailedPoints add given points to list of failed points.
func (c *Client) addFailedPoints(points ...types.MetricPoint) {
	for _, p := range points {
		key := common.LabelsToText(p.Labels, p.Annotations, c.option.MetricFormat == types.MetricFormatBleemeo)
		if reg := c.option.Cache.MetricRegistrationsFailByKey()[key]; reg.FailCounter > 5 || reg.LastFailKind.IsPermanentFailure() {
			continue
		}

		c.failedPoints = append(c.failedPoints, p)
	}

	if len(c.failedPoints) > maxPendingPoints {
		c.maxPointCount++

		// To avoid a memory leak (due to "purge" by re-slicing),
		// copy points to a fresh array from time-to-time. We don't do it
		// every run to reduce the impact of copy().
		if c.maxPointCount%50 == 0 {
			// At the same time, cleanup any points that won't be sent. Clean will copy points.
			c.cleanupFailedPoints()
			numberToDrop := len(c.failedPoints) - maxPendingPoints

			if numberToDrop > 0 {
				c.failedPoints = c.failedPoints[numberToDrop:len(c.failedPoints)]
			}
		} else {
			c.failedPoints = c.failedPoints[len(c.failedPoints)-maxPendingPoints : len(c.failedPoints)]
		}
	}
}

// cleanupFailedPoints remove points from deleted metrics or points for metric denied by configuration.
func (c *Client) cleanupFailedPoints() {
	localMetrics, err := c.option.Store.Metrics(nil)
	if err != nil {
		return
	}

	localExistsByKey := make(map[string]bool, len(localMetrics))

	for _, m := range localMetrics {
		key := common.LabelsToText(m.Labels(), m.Annotations(), c.option.MetricFormat == types.MetricFormatBleemeo)
		localExistsByKey[key] = true
	}

	newPoints := make([]types.MetricPoint, 0, len(c.failedPoints))

	for _, p := range c.failedPoints {
		key := common.LabelsToText(p.Labels, p.Annotations, c.option.MetricFormat == types.MetricFormatBleemeo)
		if localExistsByKey[key] {
			newPoints = append(newPoints, p)
		}
	}

	c.failedPoints = c.filterPoints(newPoints)
}

// preparePoints updates the MQTT payload by processing some points and returning the a map between agent uuids and the metrics.
func (c *Client) preparePoints(registreredMetricByKey map[string]bleemeoTypes.Metric, points []types.MetricPoint) map[bleemeoTypes.AgentID][]metricPayload {
	payload := make(map[bleemeoTypes.AgentID][]metricPayload, 1)

	for _, p := range points {
		key := common.LabelsToText(p.Labels, p.Annotations, c.option.MetricFormat == types.MetricFormatBleemeo)
		if m, ok := registreredMetricByKey[key]; ok && m.DeactivatedAt.IsZero() {
			value := metricPayload{
				LabelsText:  m.LabelsText,
				Timestamp:   p.Time.Unix(),
				TimestampMS: p.Time.UnixNano() / 1e6,
				Value:       forceDecimalFloat(p.Value),
			}

			if c.option.MetricFormat == types.MetricFormatBleemeo && common.MetricOnlyHasItem(m.Labels, m.AgentID) {
				value.UUID = m.ID
				value.LabelsText = ""
				value.Measurement = m.Labels[types.LabelName]
				value.BleemeoItem = m.Labels[types.LabelItem]
			}

			if p.Annotations.Status.CurrentStatus.IsSet() {
				value.Status = p.Annotations.Status.CurrentStatus.String()
				value.StatusDescription = p.Annotations.Status.StatusDescription
				value.CheckOutput = value.StatusDescription

				if p.Annotations.ContainerID != "" {
					lastKilledAt := c.option.Docker.ContainerLastKill(p.Annotations.ContainerID)
					gracePeriod := time.Since(lastKilledAt) + 300*time.Second

					if gracePeriod > 60*time.Second {
						value.EventGracePeriod = int(gracePeriod.Seconds())
					}
				}
			}

			bleemeoAgentID := bleemeoTypes.AgentID(m.AgentID)

			payload[bleemeoAgentID] = append(payload[bleemeoAgentID], value)
		} else {
			c.addFailedPoints(p)
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

	// refresh 'info' to check the maintenance mode (for which we normally are notified by MQTT message)
	c.option.UpdateMaintenance()

	if !c.IsSendingSuspended() {
		// when in maintenance mode, we do not send messages to /connect
		c.sendConnectMessage()
	}

	mqttClient.Subscribe(
		fmt.Sprintf("v1/agent/%s/notification", c.option.AgentID),
		0,
		c.option.ReloadState.PahoWrapper().OnNotification,
	)
}

func (c *Client) sendConnectMessage() {
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
}

type notificationPayload struct {
	MessageType          string `json:"message_type"`
	MetricUUID           string `json:"metric_uuid,omitempty"`
	MonitorUUID          string `json:"monitor_uuid,omitempty"`
	AlertingRuleUUID     string `json:"alertingrule_uuid"`
	MonitorOperationType string `json:"monitor_operation_type,omitempty"`
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
	case "maintenance-toggle":
		c.option.UpdateMaintenance()
	case "threshold-update":
		c.option.UpdateMetrics(payload.MetricUUID)
	case "alertingrule-update":
		c.option.UpdateAlertingRule(payload.AlertingRuleUUID)
	case "monitor-update":
		c.option.UpdateMonitor(payload.MonitorOperationType, payload.MonitorUUID)
	}
}

func (c *Client) onConnectionLost(_ paho.Client, err error) {
	logger.Printf("MQTT connection lost: %v", err)
	c.connectionLost <- nil
}

func (c *Client) publish(topic string, payload []byte, retry bool) {
	c.l.Lock()
	defer c.l.Unlock()

	c.publishNoLock(topic, payload, retry)
}

func (c *Client) publishNoLock(topic string, payload []byte, retry bool) {
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

func (c *Client) sendTopinfo(ctx context.Context, cfg bleemeoTypes.GloutonAccountConfig) {
	topinfo, err := c.option.Process.TopInfo(ctx, cfg.LiveProcessResolution-time.Second)
	if err != nil {
		logger.V(1).Printf("Unable to get topinfo: %v", err)

		return
	}

	topic := fmt.Sprintf("v1/agent/%s/top_info", c.option.AgentID)

	compressed, err := c.encoder.Encode(topinfo)
	if err != nil {
		logger.V(1).Printf("Unable to encode topinfo: %v", err)

		return
	}

	c.publish(topic, compressed, false)
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
		return nil, errNotPem
	}

	return rootCAs, nil
}

func (c *Client) filterPoints(input []types.MetricPoint) []types.MetricPoint {
	result := make([]types.MetricPoint, 0, len(input))

	defaultConfigID := c.option.Cache.Agent().CurrentConfigID
	accountConfigs := c.option.Cache.AccountConfigsByUUID()
	monitors := c.option.Cache.MonitorsByAgentUUID()
	agents := c.option.Cache.AgentsByUUID()

	for _, mp := range input {
		whitelist, found := c.whitelistForMetricPoint(mp, defaultConfigID, accountConfigs, monitors, agents)
		if !found {
			continue
		}

		// json encoder can't encode NaN (JSON standard don't allow it).
		// There isn't huge value in storing NaN anyway (it's the default when no value).
		if math.IsNaN(mp.Value) {
			continue
		}

		if common.AllowMetric(mp.Labels, mp.Annotations, whitelist) {
			result = append(result, mp)
		}
	}

	return result
}

func (c *Client) whitelistForMetricPoint(mp types.MetricPoint, defaultConfigID string, accountConfigs map[string]bleemeoTypes.GloutonAccountConfig, monitors map[bleemeoTypes.AgentID]bleemeoTypes.Monitor, agents map[string]bleemeoTypes.Agent) (map[string]bool, bool) {
	allowList, err := common.AllowListForMetric(accountConfigs, defaultConfigID, mp.Annotations, monitors, agents)
	if err != nil {
		logger.V(2).Printf("mqtt: %s", err)

		return nil, false
	}

	return allowList, true
}

func (c *Client) ready() bool {
	cfg, ok := c.option.Cache.CurrentAccountConfig()
	if !ok || cfg.LiveProcessResolution == 0 || cfg.AgentConfigByName[bleemeoTypes.AgentTypeAgent].MetricResolution == 0 {
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
		c.l.Lock()

		c.lastConnectionTimes = lastConnectionTimes
		c.lastResend = lastResend
		c.currentConnectDelay = currentConnectDelay
		c.consecutiveError = consecutiveError

		c.l.Unlock()

		disableUntil, disableReason := c.getDisableUntil()
		switch {
		case time.Now().Before(disableUntil):
			if disableReason == bleemeoTypes.DisableTimeDrift {
				_ = c.shutdown()
			}
			if c.mqttClient != nil {
				logger.V(2).Printf("Disconnecting from MQTT due to '%v'", disableReason)
				c.mqttClient.Disconnect(0)

				c.l.Lock()
				c.mqttClient = nil
				c.l.Unlock()

				c.option.ReloadState.PahoWrapper().SetClient(nil)
			}
		case c.mqttClient == nil:
			length := len(lastConnectionTimes)

			if length >= 7 && time.Since(lastConnectionTimes[length-7]) < 10*time.Minute {
				delay := delay.JitterDelay(5*time.Minute, 0.25).Round(time.Second)

				c.Disable(time.Now().Add(delay), bleemeoTypes.DisableTooManyErrors)
				logger.Printf("Too many attempts to connect to MQTT were made in the last 10 minutes. Disabling MQTT for %v", delay)

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
						// Trigger facts synchronization to check for duplicate agent
						_, _ = c.option.Facts.Facts(ctx, time.Minute)
					}
				}

				mqttClient, err := c.setupMQTT(ctx)
				if err != nil {
					delay := currentConnectDelay - time.Since(lastConnectionTimes[len(lastConnectionTimes)-1])
					logger.V(1).Printf("Unable to connect to Bleemeo MQTT (retry in %v): %v", delay, err)

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
					c.waitPublishAndResend(mqttClient, time.Now().Add(10*time.Second), true)
					c.mqttClient = mqttClient
					c.l.Unlock()

					c.option.ReloadState.PahoWrapper().SetClient(mqttClient)
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

			c.option.ReloadState.PahoWrapper().SetClient(nil)

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

	c.onReloadAndShutdown()

	// make sure all connectionLost are read
	for {
		select {
		case <-c.connectionLost:
		default:
			return
		}
	}
}

// onReload is called when reloading or on a graceful shutdown.
// The pending points are saved in the reload state and we try to push the pending messages.
func (c *Client) onReloadAndShutdown() {
	pahoWrapper := c.option.ReloadState.PahoWrapper()

	// Save pending points.
	points := c.PopPoints(true)
	pahoWrapper.SetPendingPoints(points)

	// Push the pending messages.
	deadline := time.Now().Add(5 * time.Second)

	c.l.Lock()
	defer c.l.Unlock()

	if c.mqttClient != nil {
		stillPending := c.waitPublishAndResend(c.mqttClient, deadline, true)
		if stillPending > 0 {
			logger.V(2).Printf("%d MQTT message were still pending", stillPending)
		}
	}
}

func (c *Client) receiveEvents(ctx context.Context) {
	pahoWrapper := c.option.ReloadState.PahoWrapper()

	for {
		select {
		case msg := <-pahoWrapper.NotificationChannel():
			c.onNotification(c.mqttClient, msg)
		case err := <-pahoWrapper.ConnectionLostChannel():
			c.onConnectionLost(c.mqttClient, err)
		case client := <-pahoWrapper.ConnectChannel():
			// We can't use c.mqttClient here because it is either nil or outdated,
			// c.mqttClient is updated only after a successful connection.
			c.onConnect(client)
		case <-ctx.Done():
			return
		}
	}
}
