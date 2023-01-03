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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"glouton/bleemeo/internal/cache"
	"glouton/bleemeo/internal/common"
	"glouton/bleemeo/internal/filter"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/logger"
	"glouton/mqtt"
	"glouton/mqtt/client"
	"glouton/types"
	"math"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

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
	encoder                    mqtt.Encoder
	failedPoints               failedPointsCache
	lastRegisteredMetricsCount int
	lastFailedPointsRetry      time.Time
	// Whether we should log that all failed points have
	// been processed during the next health check.
	shouldLogRecovery bool

	l                sync.Mutex
	startedAt        time.Time
	pendingPoints    []types.MetricPoint
	sendingSuspended bool // stop sending points, used when the user is read-only mode
	disableReason    bleemeoTypes.DisableReason
}

type metricPayload struct {
	UUID              string  `json:"uuid,omitempty"`
	LabelsText        string  `json:"labels_text"`
	TimestampMS       int64   `json:"time_ms"`
	Value             float64 `json:"value"`
	Status            string  `json:"status,omitempty"`
	StatusDescription string  `json:"status_description,omitempty"`
	EventGracePeriod  int     `json:"event_grace_period,omitempty"`
}

// New create a new client.
func New(opts Option) *Client {
	var initialPoints []types.MetricPoint

	reloadState := opts.ReloadState.MQTTReloadState()
	if reloadState != nil {
		initialPoints = reloadState.PopPendingPoints()
	}

	opts.InitialPoints = append(opts.InitialPoints, initialPoints...)

	c := &Client{
		opts: opts,
	}

	c.failedPoints = failedPointsCache{
		metricExists:        make(map[string]struct{}),
		maxPendingPoints:    maxPendingPoints,
		cleanupFailedPoints: c.cleanupFailedPoints,
	}

	checkDuplicate := func(ctx context.Context) {
		// Trigger facts synchronization to check for duplicate agent.
		_, _ = opts.Facts.Facts(ctx, 0)
	}

	if reloadState == nil {
		reloadState = NewReloadState(ReloadStateOptions{
			UpgradeFile:     opts.Config.Agent.UpgradeFile,
			AutoUpgradeFile: opts.Config.Agent.AutoUpgradeFile,
			AgentID:         opts.AgentID,
		})

		opts.ReloadState.SetMQTTReloadState(reloadState)
	}

	c.mqtt = client.New(client.Options{
		OptionsFunc:          c.pahoOptions,
		ReloadState:          reloadState.ClientState(),
		TooManyErrorsHandler: checkDuplicate,
		ID:                   "Bleemeo",
	})

	return c
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
		defer types.ProcessPanic()

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

	host := c.opts.Config.Bleemeo.MQTT.Host
	port := c.opts.Config.Bleemeo.MQTT.Port

	var tlsConfig *tls.Config

	if c.opts.Config.Bleemeo.MQTT.SSL {
		tlsConfig = mqtt.TLSConfig(
			c.opts.Config.Bleemeo.MQTT.SSLInsecure,
			c.opts.Config.Bleemeo.MQTT.CAFile,
		)
	}

	builder.WriteString(common.DiagnosticTCP(host, port, tlsConfig))

	return builder.String()
}

// DiagnosticArchive add to a zipfile useful diagnostic information.
func (c *Client) DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error {
	c.l.Lock()
	defer c.l.Unlock()

	if c.failedPoints.Len() > 100 {
		file, err := archive.Create("mqtt-failed-metric-points.txt")
		if err != nil {
			return err
		}

		const maxSample = 50

		fmt.Fprintf(file, "MQTT connector has %d points that are failing.\n", c.failedPoints.Len())
		fmt.Fprintf(file, "It usually happen when MQTT is not connector OR when metric are not registered with Bleemeo.\n")

		if c.failedPoints.Len() < maxSample {
			fmt.Fprintf(file, "Here is the list of all blocked metrics:\n")

			for _, p := range c.failedPoints.Copy() {
				fmt.Fprintf(file, "%v\n", p.Labels)
			}
		} else {
			fmt.Fprintf(file, "Here is a sample of %d blocked metrics:\n", maxSample)

			indices := rand.Perm(c.failedPoints.Len())
			failedPointsCopy := c.failedPoints.Copy()

			for _, i := range indices[:maxSample] {
				p := failedPointsCopy[i]
				fmt.Fprintf(file, "%v\n", p.Labels)
			}
		}
	}

	if err := c.mqtt.DiagnosticArchive(ctx, archive); err != nil {
		return err
	}

	file, err := archive.Create("bleemeo-mqtt-state.json")
	if err != nil {
		return err
	}

	obj := struct {
		StartedAt                  time.Time
		LastRegisteredMetricsCount int
		LastFailedPointsRetry      time.Time
		PendingPointsCount         int
		SendingSuspended           bool
		DisableReason              bleemeoTypes.DisableReason
	}{
		StartedAt:                  c.startedAt,
		LastRegisteredMetricsCount: c.lastRegisteredMetricsCount,
		LastFailedPointsRetry:      c.lastFailedPointsRetry,
		PendingPointsCount:         len(c.pendingPoints),
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

	failedPointsCount := c.failedPoints.Len()

	switch {
	case failedPointsCount >= maxPendingPoints:
		logger.Printf("%d points are waiting to be sent to the Bleemeo Cloud platform. Older points are being dropped", failedPointsCount)

		c.shouldLogRecovery = true
	case failedPointsCount > 1000:
		logger.Printf("%d points are waiting to be sent to the Bleemeo Cloud platform", failedPointsCount)

		c.shouldLogRecovery = true
	case failedPointsCount == 0 && c.shouldLogRecovery:
		logger.Printf("Successfully recovered, all points have been sent to the Bleemeo Cloud platform")

		c.shouldLogRecovery = false
	}

	return ok
}

func (c *Client) pahoOptions(ctx context.Context) (*paho.ClientOptions, error) {
	pahoOptions := paho.NewClientOptions()

	willPayload, _ := json.Marshal(disconnectCause{"disconnect-will"}) //nolint:errchkjson // False positive.

	pahoOptions.SetBinaryWill(
		fmt.Sprintf("v1/agent/%s/disconnect", c.opts.AgentID),
		willPayload,
		1,
		false,
	)

	brokerURL := net.JoinHostPort(c.opts.Config.Bleemeo.MQTT.Host, fmt.Sprint(c.opts.Config.Bleemeo.MQTT.Port))

	if c.opts.Config.Bleemeo.MQTT.SSL {
		tlsConfig := mqtt.TLSConfig(
			c.opts.Config.Bleemeo.MQTT.SSLInsecure,
			c.opts.Config.Bleemeo.MQTT.CAFile,
		)

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
		points = c.failedPoints.Pop()
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

	registeredMetricByKey := c.opts.Cache.MetricLookupFromList()

	// Retry failed points every 5 minutes, or instantly if a new metric was registered.
	if c.failedPoints.Len() > 0 && c.connected() &&
		(time.Since(c.lastFailedPointsRetry) > 5*time.Minute ||
			len(registeredMetricByKey) != c.lastRegisteredMetricsCount) {
		c.lastRegisteredMetricsCount = len(registeredMetricByKey)
		c.lastFailedPointsRetry = time.Now()

		points = append(c.failedPoints.Pop(), points...)
	}

	points = c.filterPoints(points)
	payload := c.preparePoints(registeredMetricByKey, points)
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

		// Ignore metrics that failed to register too many times.
		reg := c.opts.Cache.MetricRegistrationsFailByKey()[key]
		if reg.FailCounter > 5 || reg.LastFailKind.IsPermanentFailure() {
			continue
		}

		c.failedPoints.Add(p)
	}
}

// cleanupFailedPoints remove points from deleted metrics or points for metric denied by configuration.
func (c *Client) cleanupFailedPoints(failedPoints []types.MetricPoint) []types.MetricPoint {
	// Remove points for points that are no longer present in the store.
	localMetrics, err := c.opts.Store.Metrics(nil)
	if err != nil {
		return failedPoints
	}

	localExistsByKey := make(map[string]bool, len(localMetrics))

	for _, m := range localMetrics {
		key := types.LabelsToText(m.Labels())
		localExistsByKey[key] = true
	}

	newPoints := make([]types.MetricPoint, 0, len(failedPoints))

	for _, p := range failedPoints {
		key := types.LabelsToText(p.Labels)
		if localExistsByKey[key] {
			newPoints = append(newPoints, p)
		}
	}

	// Remove points that are not allowed in the current plan.
	return c.filterPoints(newPoints)
}

// preparePoints returns the MQTT payload by processing some points and returning a map of metrics by agent ID.
func (c *Client) preparePoints(
	registeredMetricByKey map[string]bleemeoTypes.Metric,
	points []types.MetricPoint,
) map[bleemeoTypes.AgentID][]metricPayload {
	payloadByAgent := make(map[bleemeoTypes.AgentID][]metricPayload, 1)

	for _, p := range points {
		key := common.MetricKey(p.Labels, p.Annotations, string(c.opts.AgentID))
		metric, ok := registeredMetricByKey[key]

		// Don't send point if its associated metric is deactivated or not yet registered.
		// Also add the point to the failed points if a previous point from the same
		// metric is in the failed points. This ensures that points are sent in the right
		// order when a metric is registered.
		if !ok || !metric.DeactivatedAt.IsZero() || c.failedPoints.Contains(p.Labels) {
			c.addFailedPoints(p)

			continue
		}

		payload := metricPayload{
			LabelsText:  metric.LabelsText,
			TimestampMS: p.Time.UnixMilli(),
			Value:       p.Value,
		}

		// Don't send labels text if the metric only has a name and an item.
		if c.opts.MetricFormat == types.MetricFormatBleemeo && common.MetricOnlyHasItem(metric.Labels, metric.AgentID) {
			// The metric ID is not used when labels text are present
			// because they already uniquely identify the metric.
			payload.UUID = metric.ID
			payload.LabelsText = ""
		}

		if p.Annotations.Status.CurrentStatus.IsSet() {
			payload.Status = p.Annotations.Status.CurrentStatus.String()
			payload.StatusDescription = p.Annotations.Status.StatusDescription

			// Add a 5 minutes grace period after a container was killed.
			// This should prevent notifications when running "docker compose up -d --pull always".
			if p.Annotations.ContainerID != "" {
				lastKilledAt := c.opts.Docker.ContainerLastKill(p.Annotations.ContainerID)
				gracePeriod := 5*time.Minute - time.Since(lastKilledAt)

				// Ignore grace period shorter than 1 minute. Without an explicit
				// grace period, a default of 1 minute will be used.
				if gracePeriod > time.Minute {
					payload.EventGracePeriod = int(gracePeriod.Seconds())
				}
			}
		}

		bleemeoAgentID := bleemeoTypes.AgentID(metric.AgentID)

		payloadByAgent[bleemeoAgentID] = append(payloadByAgent[bleemeoAgentID], payload)
	}

	return payloadByAgent
}

func (c *Client) onConnect(mqttClient paho.Client) {
	// refresh 'info' to check the maintenance mode (for which we normally are notified by MQTT message)
	c.opts.UpdateMaintenance()

	if !c.IsSendingSuspended() {
		// when in maintenance mode, we do not send messages to /connect
		c.sendConnectMessage()
	}

	token := mqttClient.Subscribe(
		fmt.Sprintf("v1/agent/%s/notification", c.opts.AgentID),
		0,
		c.opts.ReloadState.MQTTReloadState().OnNotification,
	)
	token.Wait()

	// If there is an error, the client should reconnect so the subscription will be retried.
	if token.Error() != nil {
		logger.V(1).Printf("Failed to subscribe to notifications: %s", token.Error())
	}
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

// failedPointsCache stores failed points and can check if a metric is present in the points.
type failedPointsCache struct {
	maxPendingPoints int

	l      sync.Mutex
	points []types.MetricPoint
	// Map of metric names contained in the points.
	metricExists        map[string]struct{}
	cleanupFailedPoints func(failedPoints []types.MetricPoint) []types.MetricPoint
	tooManyPointsCount  int
}

// Add points to the cache.
func (p *failedPointsCache) Add(points ...types.MetricPoint) {
	p.l.Lock()
	defer p.l.Unlock()

	p.addNoLock(points...)
}

// Add points to the cache
// The lock should be held when calling this method.
func (p *failedPointsCache) addNoLock(points ...types.MetricPoint) {
	for _, point := range points {
		p.metricExists[types.LabelsToText(point.Labels)] = struct{}{}
	}

	p.points = append(p.points, points...)

	// Remove some old points if there are too many points.
	if len(p.points) > p.maxPendingPoints {
		p.tooManyPointsCount++

		newPoints := p.popNoLock()

		// To avoid a memory leak (due to "purge" by re-slicing),
		// copy points to a fresh array from time-to-time. We don't do it
		// every run to reduce the impact of copy().
		if p.tooManyPointsCount%50 == 0 {
			// At the same time, cleanup any points that won't be sent. Clean will copy points.
			newPoints = p.cleanupFailedPoints(newPoints)
		}

		numberToDrop := len(newPoints) - p.maxPendingPoints
		if numberToDrop > 0 {
			newPoints = newPoints[numberToDrop:]
		}

		p.addNoLock(newPoints...)
	}
}

// Pop the metric points.
func (p *failedPointsCache) Pop() []types.MetricPoint {
	p.l.Lock()
	defer p.l.Unlock()

	return p.popNoLock()
}

// Pop the metric points.
// The lock should be held when calling this method.
func (p *failedPointsCache) popNoLock() []types.MetricPoint {
	points := p.points
	p.points = nil

	for metric := range p.metricExists {
		delete(p.metricExists, metric)
	}

	return points
}

// Copy returns a copy of the metric points.
func (p *failedPointsCache) Copy() []types.MetricPoint {
	p.l.Lock()
	defer p.l.Unlock()

	pointsCopy := make([]types.MetricPoint, len(p.points))

	copy(pointsCopy, p.points)

	return pointsCopy
}

// Len returns the number of points in the cache.
func (p *failedPointsCache) Len() int {
	p.l.Lock()
	defer p.l.Unlock()

	return len(p.points)
}

// Contains returns whether the labels correspond to a failed point.
func (p *failedPointsCache) Contains(labels map[string]string) bool {
	_, ok := p.metricExists[types.LabelsToText(labels)]

	return ok
}
