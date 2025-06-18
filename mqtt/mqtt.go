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

package mqtt

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/mqtt/client"
	"github.com/bleemeo/glouton/types"

	paho "github.com/eclipse/paho.mqtt.golang"
)

const pointsBatchSize = 1000

// MQTT sends points from the store to a MQTT server.
type MQTT struct {
	opts   Options
	client *client.Client

	l             sync.Mutex
	pendingPoints []types.MetricPoint
}

type Options struct {
	Config config.OpenSourceMQTT
	FQDN   string
	// State kept between reloads.
	ReloadState types.MQTTReloadState
	// The store provides the metrics to send to MQTT.
	Store               Store
	PahoLastPingCheckAt func() time.Time
}

// Store is the interface used by the client to access the Metric Store.
type Store interface {
	AddNotifiee(cb func([]types.MetricPoint)) int
	RemoveNotifiee(id int)
}

type metricPayload struct {
	LabelsText  string  `json:"labels_text"`
	TimestampMS int64   `json:"time_ms"`
	Value       float64 `json:"value"`
}

func New(opts Options) *MQTT {
	opts.FQDN = safeFQDN(opts.FQDN)

	m := MQTT{opts: opts}

	m.client = client.New(client.Options{
		OptionsFunc:         m.pahoOptions,
		ReloadState:         opts.ReloadState,
		ID:                  "Open Source",
		PahoLastPingCheckAt: opts.PahoLastPingCheckAt,
	})

	return &m
}

// safeFQDN returns a safe version of a FQDN that doesn't
// contain any special characters used by MQTT and NATS.
func safeFQDN(fqdn string) string {
	replacer := strings.NewReplacer("#", "", "+", "", "/", "", ">", "", "*", "", ".", ",")

	return replacer.Replace(fqdn)
}

func (m *MQTT) pahoOptions(_ context.Context) (*paho.ClientOptions, error) {
	pahoOptions := paho.NewClientOptions()

	for _, host := range m.opts.Config.Hosts {
		brokerURL := net.JoinHostPort(host, strconv.Itoa(m.opts.Config.Port))

		if m.opts.Config.SSL {
			brokerURL = "ssl://" + brokerURL
		} else {
			brokerURL = "tcp://" + brokerURL
		}

		pahoOptions.AddBroker(brokerURL)
	}

	if m.opts.Config.SSL {
		tlsConfig := TLSConfig(
			m.opts.Config.SSLInsecure,
			m.opts.Config.CAFile,
		)

		pahoOptions.SetTLSConfig(tlsConfig)
	}

	pahoOptions.SetUsername(m.opts.Config.Username)
	pahoOptions.SetPassword(m.opts.Config.Password)

	return pahoOptions, nil
}

// Run starts periodically sending metric points to MQTT.
func (m *MQTT) Run(ctx context.Context) error {
	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer crashreport.ProcessPanic()

		m.client.Run(ctx)
		wg.Done()
	}()

	m.run(ctx)

	wg.Wait()

	return nil
}

func (m *MQTT) run(ctx context.Context) {
	storeNotifieeID := m.opts.Store.AddNotifiee(m.addPoints)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for ctx.Err() == nil {
		m.sendPoints()

		select {
		case <-ticker.C:
		case <-ctx.Done():
		}
	}

	m.opts.Store.RemoveNotifiee(storeNotifieeID)
}

func (m *MQTT) sendPoints() {
	points := m.PopPoints()

	// Convert points to metric payloads.
	payload := make([]metricPayload, 0, len(points))

	for _, p := range points {
		payload = append(payload, metricPayload{
			TimestampMS: p.Time.UnixMilli(),
			Value:       p.Value,
			LabelsText:  types.LabelsToText(p.Labels),
		})
	}

	// Send points in batch.
	for i := 0; i < len(payload); i += pointsBatchSize {
		end := i + pointsBatchSize
		if end > len(payload) {
			end = len(payload)
		}

		if err := m.client.PublishAsJSON(fmt.Sprintf("v1/agent/%s/data", m.opts.FQDN), payload[i:end], true); err != nil {
			logger.V(1).Printf("Unable to publish points: %v", err)
		}
	}
}

// DiagnosticArchive add to a zipfile useful diagnostic information.
func (m *MQTT) DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error {
	if m.client == nil {
		return nil
	}

	return m.client.DiagnosticArchive(ctx, archive)
}

func (m *MQTT) addPoints(points []types.MetricPoint) {
	m.l.Lock()
	defer m.l.Unlock()

	m.pendingPoints = append(m.pendingPoints, points...)
}

// PopPoints returns the list of metrics to be sent.
func (m *MQTT) PopPoints() []types.MetricPoint {
	m.l.Lock()
	defer m.l.Unlock()

	points := m.pendingPoints
	m.pendingPoints = nil

	return points
}
