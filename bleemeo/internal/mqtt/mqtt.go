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
	"glouton/bleemeo/internal/filter"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/logger"
	"glouton/mqtt"
	"glouton/types"
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
	maxPendingPoints = 100000
	pointsBatchSize  = 1000
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
	opts Option

	// Those variable are only written once or read/written exclusively from the Run() goroutine. No lock needed
	ctx                        context.Context //nolint:containedctx
	mqtt                       bleemeoTypes.MQTTClient
	encoder                    mqttEncoder
	failedPoints               []types.MetricPoint
	lastRegisteredMetricsCount int
	lastFailedPointsRetry      time.Time

	l                sync.Mutex
	startedAt        time.Time
	pendingPoints    []types.MetricPoint
	maxPointCount    int
	sendingSuspended bool // stop sending points, used when the user is is read-only mode
	consecutiveError int
	disableReason    bleemeoTypes.DisableReason
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
func New(opts Option) *Client {
	var initialPoints []types.MetricPoint

	reloadState := opts.ReloadState.MQTTReloadState()
	if reloadState != nil {
		initialPoints = reloadState.PopPendingPoints()
	}

	opts.InitialPoints = append(opts.InitialPoints, initialPoints...)

	client := &Client{
		opts: opts,
	}

	checkDuplicate := func(ctx context.Context) {
		// Trigger facts synchronization to check for duplicate agent.
		_, _ = opts.Facts.Facts(ctx, 0)
	}

	client.mqtt = mqtt.New(mqtt.Options{
		OptionsFunc:          client.pahoOptions,
		FirstClient:          opts.ReloadState.IsFirstRun(),
		ReloadState:          opts.MQTTReloadState,
		TooManyErrorsHandler: checkDuplicate,
	})

	if reloadState == nil {
		reloadState = NewReloadState(ReloadStateOptions{
			UpgradeFile:     opts.Config.String("agent.upgrade_file"),
			AutoUpgradeFile: opts.Config.String("agent.auto_upgrade_file"),
			AgentID:         opts.AgentID,
		})

		opts.ReloadState.SetMQTTReloadState(reloadState)
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
	if c.mqtt == nil {
		return false
	}

	return c.mqtt.IsConnectionOpen()
}

// Disable will disable the MQTT connection until given time.
// To re-enable use ClearDisable().
func (c *Client) Disable(until time.Time, reason bleemeoTypes.DisableReason) {
	c.l.Lock()
	defer c.l.Unlock()

	if c.mqtt.DisabledUntil().Before(until) {
		logger.V(2).Printf("Disconnecting from MQTT due to '%v'", reason)
		c.mqtt.Disable(until)

		if reason == bleemeoTypes.DisableTimeDrift {
			_ = c.shutdownTimeDrift()
		}

		c.disableReason = reason
	}
}

// ClearDisable enables MQTT if reason match reasonToClear after delay.
func (c *Client) ClearDisable(reasonToClear bleemeoTypes.DisableReason, delay time.Duration) {
	c.l.Lock()
	defer c.l.Unlock()

	if time.Now().Before(c.mqtt.DisabledUntil()) && c.disableReason == reasonToClear {
		c.mqtt.Disable(time.Now().Add(delay))
	}
}

// Run connect and transmit information to Bleemeo Cloud platform.
func (c *Client) Run(ctx context.Context) error {
	c.ctx = ctx

	c.l.Lock()
	c.pendingPoints = c.opts.InitialPoints
	c.opts.InitialPoints = nil
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
		defer types.ProcessPanic()

		c.receiveEvents(ctx)
		wg.Done()
	}()

	wg.Add(1)

	go func() {
		c.mqtt.Run(ctx)

		wg.Done()
	}()

	err := c.run(ctx)

	wg.Wait()

	rs := c.opts.ReloadState.MQTTReloadState()
	rs.SetMQTT(c.mqtt)

	// Save the pending points.
	points := c.PopPoints(true)
	rs.SetPendingPoints(points)

	return err
}

// DiagnosticPage return useful information to troubleshoot issue.
func (c *Client) DiagnosticPage() string {
	builder := &strings.Builder{}

	host := c.opts.Config.String("bleemeo.mqtt.host")
	port := c.opts.Config.Int("bleemeo.mqtt.port")

	builder.WriteString(common.DiagnosticTCP(host, port, c.tlsConfig()))

	return builder.String()
}

// DiagnosticArchive add to a zipfile useful diagnostic information.
func (c *Client) DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error {
	c.l.Lock()
	defer c.l.Unlock()

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

	c.mqtt.DiagnosticArchive(ctx, archive)

	file, err := archive.Create("bleemeo-mqtt-state.json")
	if err != nil {
		return err
	}

	obj := struct {
		StartedAt                  time.Time
		LastRegisteredMetricsCount int
		LastFailedPointsRetry      time.Time
		PendingPointsCount         int
		MaxPointCount              int
		SendingSuspended           bool
		DisableReason              bleemeoTypes.DisableReason
	}{
		StartedAt:                  c.startedAt,
		LastRegisteredMetricsCount: c.lastRegisteredMetricsCount,
		LastFailedPointsRetry:      c.lastFailedPointsRetry,
		PendingPointsCount:         len(c.pendingPoints),
		MaxPointCount:              c.maxPointCount,
		SendingSuspended:           c.sendingSuspended,
		DisableReason:              c.disableReason,
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

// LastReport returns the date of last report with Bleemeo API.
func (c *Client) LastReport() time.Time {
	return c.mqtt.LastReport()
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

func (c *Client) pahoOptions(ctx context.Context) (*paho.ClientOptions, error) {
	pahoOptions := paho.NewClientOptions()

	// Allow for slightly larger timeout value, just avoid disconnection
	// with bad network connection.
	pahoOptions.SetPingTimeout(20 * time.Second)
	pahoOptions.SetKeepAlive(45 * time.Second)

	willPayload, _ := json.Marshal(disconnectCause{"disconnect-will"}) //nolint:errchkjson // False positive.

	pahoOptions.SetBinaryWill(
		fmt.Sprintf("v1/agent/%s/disconnect", c.opts.AgentID),
		willPayload,
		1,
		false,
	)

	brokerURL := fmt.Sprintf("%s:%d", c.opts.Config.String("bleemeo.mqtt.host"), c.opts.Config.Int("bleemeo.mqtt.port"))

	if c.opts.Config.Bool("bleemeo.mqtt.ssl") {
		tlsConfig := c.tlsConfig()

		pahoOptions.SetTLSConfig(tlsConfig)

		brokerURL = "ssl://" + brokerURL
	} else {
		brokerURL = "tcp://" + brokerURL
	}

	pahoOptions.AddBroker(brokerURL)
	pahoOptions.SetUsername(fmt.Sprintf("%s@bleemeo.com", c.opts.AgentID))

	rs := c.opts.ReloadState.MQTTReloadState()
	pahoOptions.SetOnConnectHandler(rs.OnConnect)

	password, err := c.opts.GetJWT(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to get JWT: %w", err)
	}

	pahoOptions.SetPassword(password)

	return pahoOptions, nil
}

func (c *Client) tlsConfig() *tls.Config {
	if !c.opts.Config.Bool("bleemeo.mqtt.ssl") {
		return nil
	}

	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}

	caFile := c.opts.Config.String("bleemeo.mqtt.cafile")
	if caFile != "" {
		if rootCAs, err := loadRootCAs(caFile); err != nil {
			logger.Printf("Unable to load CAs from %#v", caFile)
		} else {
			tlsConfig.RootCAs = rootCAs
		}
	}

	if c.opts.Config.Bool("bleemeo.mqtt.ssl_insecure") {
		tlsConfig.InsecureSkipVerify = true
	}

	return tlsConfig
}

func (c *Client) shutdownTimeDrift() error {
	deadline := time.Now().Add(5 * time.Second)

	if c.mqtt.IsConnectionOpen() {
		payload, err := json.Marshal(map[string]string{"disconnect-cause": "Clean shutdown, time drift"})
		if err != nil {
			return err
		}

		c.mqtt.Publish(fmt.Sprintf("v1/agent/%s/disconnect", c.opts.AgentID), payload, true)
	}

	c.mqtt.Disconnect(time.Until(deadline))

	return nil
}

// SuspendSending sets whether the mqtt client should stop sendings metrics (and topinfo).
func (c *Client) SuspendSending(suspended bool) {
	c.l.Lock()
	defer c.l.Unlock()

	// Send a message on /connect when reconnecting after being in maintenance mode.
	if c.sendingSuspended && !suspended {
		if c.mqtt.IsConnectionOpen() {
			c.sendConnectMessage()
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
	storeNotifieeID := c.opts.Store.AddNotifiee(c.addPoints)

	var topinfoSendAt time.Time

	time.Sleep(time.Duration(rand.Intn(10000)) * time.Millisecond) //nolint:gosec

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for ctx.Err() == nil {
		cfg, ok := c.opts.Cache.CurrentAccountConfig()

		c.sendPoints()

		if !c.IsSendingSuspended() && ok && cfg.LiveProcess && time.Since(topinfoSendAt) >= cfg.LiveProcessResolution {
			topinfoSendAt = time.Now()

			c.sendTopinfo(ctx, cfg)
		}

		select {
		case <-ticker.C:
		case <-ctx.Done():
		}
	}

	c.opts.Store.RemoveNotifiee(storeNotifieeID)

	return nil
}

// addPoints preprocesses and appends a list of metric points to those pending transmission over MQTT.
func (c *Client) addPoints(points []types.MetricPoint) {
	c.l.Lock()
	defer c.l.Unlock()

	disabledUntil := c.mqtt.DisabledUntil()

	if time.Now().Before(disabledUntil) && c.disableReason == bleemeoTypes.DisableDuplicatedAgent {
		return
	}

	if time.Now().Before(disabledUntil) && c.disableReason == bleemeoTypes.DisableTimeDrift {
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

	registreredMetricByKey := c.opts.Cache.MetricLookupFromList()

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

			c.mqtt.Publish(fmt.Sprintf("v1/agent/%s/data", agentID), buffer, true)
		}
	}
}

// addFailedPoints add given points to list of failed points.
func (c *Client) addFailedPoints(points ...types.MetricPoint) {
	for _, p := range points {
		key := types.LabelsToText(p.Labels)
		if reg := c.opts.Cache.MetricRegistrationsFailByKey()[key]; reg.FailCounter > 5 || reg.LastFailKind.IsPermanentFailure() {
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
	localMetrics, err := c.opts.Store.Metrics(nil)
	if err != nil {
		return
	}

	localExistsByKey := make(map[string]bool, len(localMetrics))

	for _, m := range localMetrics {
		key := types.LabelsToText(m.Labels())
		localExistsByKey[key] = true
	}

	newPoints := make([]types.MetricPoint, 0, len(c.failedPoints))

	for _, p := range c.failedPoints {
		key := types.LabelsToText(p.Labels)
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
		key := common.MetricKey(p.Labels, p.Annotations, string(c.opts.AgentID))
		if m, ok := registreredMetricByKey[key]; ok && m.DeactivatedAt.IsZero() {
			value := metricPayload{
				LabelsText:  m.LabelsText,
				Timestamp:   p.Time.Unix(),
				TimestampMS: p.Time.UnixNano() / 1e6,
				Value:       forceDecimalFloat(p.Value),
			}

			if c.opts.MetricFormat == types.MetricFormatBleemeo && common.MetricOnlyHasItem(m.Labels, m.AgentID) {
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
					lastKilledAt := c.opts.Docker.ContainerLastKill(p.Annotations.ContainerID)
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

func (c *Client) onConnect(mqttClient paho.Client) {
	logger.Printf("MQTT connection established")

	// refresh 'info' to check the maintenance mode (for which we normally are notified by MQTT message)
	c.opts.UpdateMaintenance()

	if !c.IsSendingSuspended() {
		// when in maintenance mode, we do not send messages to /connect
		c.sendConnectMessage()
	}

	mqttClient.Subscribe(
		fmt.Sprintf("v1/agent/%s/notification", c.opts.AgentID),
		0,
		c.opts.ReloadState.MQTTReloadState().OnNotification,
	)
}

func (c *Client) sendConnectMessage() {
	// Use short max-age to force a refresh facts since a reconnection to MQTT may
	// means that public IP change.
	facts, err := c.opts.Facts.Facts(c.ctx, 10*time.Second)
	if err != nil {
		logger.V(2).Printf("Unable to get facts: %v", err)
	}

	payload, err := json.Marshal(map[string]string{"public_ip": facts["public_ip"]})
	if err != nil {
		logger.V(2).Printf("Unable to encode connect message: %v", err)

		return
	}

	c.mqtt.Publish(fmt.Sprintf("v1/agent/%s/connect", c.opts.AgentID), payload, true)
}

type notificationPayload struct {
	MessageType          string `json:"message_type"`
	MetricUUID           string `json:"metric_uuid,omitempty"`
	MonitorUUID          string `json:"monitor_uuid,omitempty"`
	AlertingRuleUUID     string `json:"alertingrule_uuid"`
	MonitorOperationType string `json:"monitor_operation_type,omitempty"`
}

func (c *Client) onNotification(msg paho.Message) {
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
		c.opts.UpdateConfigCallback(true)
	case "config-will-change":
		c.opts.UpdateConfigCallback(false)
	case "maintenance-toggle":
		c.opts.UpdateMaintenance()
	case "threshold-update":
		c.opts.UpdateMetrics(payload.MetricUUID)
	case "alertingrule-update":
		c.opts.UpdateAlertingRule(payload.AlertingRuleUUID)
	case "monitor-update":
		c.opts.UpdateMonitor(payload.MonitorOperationType, payload.MonitorUUID)
	}
}

func (c *Client) sendTopinfo(ctx context.Context, cfg bleemeoTypes.GloutonAccountConfig) {
	topinfo, err := c.opts.Process.TopInfo(ctx, cfg.LiveProcessResolution-time.Second)
	if err != nil {
		logger.V(1).Printf("Unable to get topinfo: %v", err)

		return
	}

	topic := fmt.Sprintf("v1/agent/%s/top_info", c.opts.AgentID)

	compressed, err := c.encoder.Encode(topinfo)
	if err != nil {
		logger.V(1).Printf("Unable to encode topinfo: %v", err)

		return
	}

	c.mqtt.Publish(topic, compressed, false)
}

func loadRootCAs(caFile string) (*x509.CertPool, error) {
	rootCAs := x509.NewCertPool()

	certs, err := os.ReadFile(caFile)
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

	f := filter.NewFilter(c.opts.Cache)

	for _, mp := range input {
		// json encoder can't encode NaN (JSON standard don't allow it).
		// There isn't huge value in storing NaN anyway (it's the default when no value).
		if math.IsNaN(mp.Value) {
			continue
		}

		isAllowed, _, err := f.IsAllowed(mp.Labels, mp.Annotations)
		if err != nil {
			logger.V(2).Printf("mqtt: %s", err)

			continue
		}

		if isAllowed {
			result = append(result, mp)
		}
	}

	return result
}

func (c *Client) ready() bool {
	cfg, ok := c.opts.Cache.CurrentAccountConfig()
	if !ok || cfg.LiveProcessResolution == 0 || cfg.AgentConfigByName[bleemeoTypes.AgentTypeAgent].MetricResolution == 0 {
		logger.V(2).Printf("MQTT not ready, Agent has no configuration")

		return false
	}

	for _, m := range c.opts.Cache.Metrics() {
		if m.Labels[types.LabelName] == "agent_status" {
			return true
		}
	}

	logger.V(2).Printf("MQTT not ready, metric \"agent_status\" is not yet registered")

	return false
}

func (c *Client) receiveEvents(ctx context.Context) {
	rs := c.opts.ReloadState.MQTTReloadState()

	for {
		select {
		case msg := <-rs.NotificationChannel():
			c.onNotification(msg)
		case client := <-rs.ConnectChannel():
			// We can't use c.mqtt here because it is either nil or outdated,
			// c.mqtt is updated only after a successful connection.
			c.onConnect(client)
		case <-ctx.Done():
			return
		}
	}
}
