package mqtt

import (
	"agentgo/bleemeo/internal/cache"
	"agentgo/bleemeo/internal/common"
	"agentgo/bleemeo/types"
	"agentgo/logger"
	agentTypes "agentgo/types"
	"bytes"
	"compress/zlib"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

const maxPendingPoints = 100000
const pointsBatchSize = 1000

// Option are parameter for the MQTT client
type Option struct {
	types.GlobalOption
	Cache *cache.Cache

	// DisableCallback is a function called when MQTT got too much connect/disconnection.
	DisableCallback func(reason types.DisableReason, until time.Time)
}

// Client is an MQTT client for Bleemeo Cloud platform
type Client struct {
	option Option

	// Those variable are write once or only read/write from Run() gorouting. No lock needed
	ctx                        context.Context
	mqttClient                 paho.Client
	failedPoints               []agentTypes.MetricPoint
	lastRegisteredMetricsCount int
	lastFailedPointsRetry      time.Time

	l                 sync.Mutex
	setupDone         bool
	pendingToken      []paho.Token
	pendingPoints     []agentTypes.MetricPoint
	lastReport        time.Time
	failedPointsCount int
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
	agentID := c.option.State.AgentID()
	pahoOptions.SetBinaryWill(
		fmt.Sprintf("v1/agent/%s/disconnect", agentID),
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
			if c.option.Config.Bool("bleemeo.mqtt.ssl_insecure") {
				tlsConfig.InsecureSkipVerify = true
			}
		}
		brokerURL = "ssl://" + brokerURL
	} else {
		brokerURL = "tcp://" + brokerURL
	}
	pahoOptions.SetUsername(fmt.Sprintf("%s@bleemeo.com", agentID))
	pahoOptions.SetPassword(c.option.State.AgentPassword())
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
		// TODO if upgrade_in_progress
		payload, err := json.Marshal(map[string]string{"disconnect-cause": "Clean shutdown"})
		if err != nil {
			return err
		}
		c.publish(fmt.Sprintf("v1/agent/%s/disconnect", c.option.State.AgentID()), payload)
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

func (c *Client) addPoints(points []agentTypes.MetricPoint) {
	c.l.Lock()
	defer c.l.Unlock()
	c.pendingPoints = append(c.pendingPoints, points...)
}

func (c *Client) popPendingPoints() []agentTypes.MetricPoint {
	c.l.Lock()
	defer c.l.Unlock()
	points := c.pendingPoints
	c.pendingPoints = nil
	return points
}

func (c *Client) sendPoints() {
	registreredMetrics := c.option.Cache.Metrics()
	registreredMetricByKey := make(map[common.MetricLabelItem]types.Metric)
	for _, m := range registreredMetrics {
		key := common.MetricLabelItemFromMetric(m)
		registreredMetricByKey[key] = m
	}

	points := c.popPendingPoints()
	if len(c.failedPoints) > 0 && c.Connected() && (time.Since(c.lastFailedPointsRetry) > 5*time.Minute || len(registreredMetricByKey) != c.lastRegisteredMetricsCount) {
		c.lastRegisteredMetricsCount = len(registreredMetricByKey)
		c.lastFailedPointsRetry = time.Now()
		points = append(c.failedPoints, points...)
		c.failedPoints = nil
	}

	payload := make([]metricPayload, 0, len(points))
	if !c.Connected() {
		c.failedPoints = append(c.failedPoints, points...)
		if len(c.failedPoints) > maxPendingPoints {
			c.failedPoints = c.failedPoints[len(c.failedPoints)-maxPendingPoints : len(c.failedPoints)]
		}
		return
	}
	payload = c.preparePoints(payload, registreredMetricByKey, points)
	if len(payload) == 0 {
		return
	}
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
		c.publish(fmt.Sprintf("v1/agent/%s/data", c.option.State.AgentID()), buffer)
	}
	c.l.Lock()
	defer c.l.Unlock()
	c.failedPointsCount = len(c.failedPoints)
}

func (c *Client) preparePoints(payload []metricPayload, registreredMetricByKey map[common.MetricLabelItem]types.Metric, points []agentTypes.MetricPoint) []metricPayload {
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
			}
			// TODO: fill value.EventGracePeriod
			payload = append(payload, value)
		} else {
			c.failedPoints = append(c.failedPoints, p)
		}
	}
	return payload
}

func (c *Client) connect(ctx context.Context) {
	optionReader := c.mqttClient.OptionsReader()
	delay := 5 * time.Second
	logger.V(2).Printf("Connecting to MQTT broker %v", optionReader.Servers()[0])
	for ctx.Err() == nil {
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
		select {
		case <-time.After(delay):
		case <-ctx.Done():
		}
		delay *= 2
		if delay > optionReader.MaxReconnectInterval() {
			delay = optionReader.MaxReconnectInterval()
		}
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
	agentID := c.option.State.AgentID()
	c.publish(fmt.Sprintf("v1/agent/%s/connect", agentID), payload)
}

func (c *Client) onConnectionLost(_ paho.Client, err error) {
	logger.Printf("MQTT connection lost: %v", err)
	// TODO: last disconnect & disabling if too many connect/disconnect
}

func (c *Client) publish(topic string, payload []byte) {
	token := c.mqttClient.Publish(topic, 1, false, payload)
	c.l.Lock()
	defer c.l.Unlock()
	c.pendingToken = append(c.pendingToken, token)
}

func (c *Client) sendTopinfo(ctx context.Context, cfg types.AccountConfig) {
	topinfo, err := c.option.Process.TopInfo(ctx, time.Duration(cfg.LiveProcessResolution)*time.Second/2)
	if err != nil {
		logger.V(1).Printf("Unable to get topinfo: %v", err)
		return
	}
	topic := fmt.Sprintf("v1/agent/%s/top_info", c.option.State.AgentID())

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

func (c *Client) ready() bool {
	if c.option.State.AgentID() == "" {
		logger.V(2).Printf("MQTT not ready, Agent not yet registrered")
		return false
	}
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
