// Copyright 2015-2026 Bleemeo
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

package logmetrics

import (
	"context"
	"errors"
	"fmt"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"

	"github.com/google/uuid"
	"github.com/open-telemetry/opentelemetry-collector-contrib/connector/countconnector"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/parser/container"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/filelogreceiver"
	"go.opentelemetry.io/collector/component"
	otelconnector "go.opentelemetry.io/collector/connector"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/receiver"
)

var (
	errUnexpectedConfigType = errors.New("unexpected receiver config type")
	errNoValidFilter        = errors.New("no valid filter for source")
)

// source is one OTel mini-pipeline for a single log-to-metric source (a static
// path or a resolved container log file):
//
//	filelogreceiver --(plog.Logs)--> countconnector(s) --(pmetric.Metrics)--> shared registry sink
//
// Normally one connector handles all of a source's filters. If that combined
// config fails validation, it falls back to one connector per filter (fanned
// out) so a bad regex only disables its own metric.
type source struct {
	recv  receiver.Logs
	conns []otelconnector.Logs

	persister *persistHost // nil if this source runs without persisted offsets

	extID component.ID // valid only if persister != nil
}

// newSource builds and starts a source. include is a list of glob patterns
// (hostroot already applied). If isContainer, the Docker/CRI envelope is
// unwrapped first (same "container" operator otel/logprocessing uses). If
// persister is non-nil, name is used as this source's stable persisted-offset
// identity (a joined path list for static sources, a container ID for container
// sources), so a restart resumes tailing instead of skipping to the file's end.
func newSource(
	ctx context.Context,
	telemetry component.TelemetrySettings,
	include []string,
	isContainer bool,
	filters []config.LogFilter,
	sink consumer.Metrics,
	persister *persistHost,
	name string,
) (*source, error) {
	connFactory := countconnector.NewFactory()

	conns, err := buildConnectors(ctx, connFactory, telemetry, filters, sink)
	if err != nil {
		return nil, err
	}

	var operators []operator.Config

	if isContainer {
		containerCfg := container.NewConfig()
		containerCfg.AddMetadataFromFilePath = false
		operators = append(operators, operator.Config{Builder: containerCfg})
	}

	recvFactory := filelogreceiver.NewFactory()

	defaultCfg := recvFactory.CreateDefaultConfig()

	recvCfg, ok := defaultCfg.(*filelogreceiver.FileLogConfig)
	if !ok {
		shutdownConns(ctx, conns)

		return nil, fmt.Errorf("%w: %T", errUnexpectedConfigType, defaultCfg)
	}

	recvCfg.InputConfig.Include = include
	recvCfg.Operators = operators

	var (
		host  component.Host
		extID component.ID
	)

	if persister != nil {
		extID = persister.newPersistentExt(name)
		recvCfg.StorageID = &extID
		host = persister
	}

	recv, err := recvFactory.CreateLogs(
		ctx,
		receiver.Settings{
			ID:                component.NewIDWithName(recvFactory.Type(), uuid.NewString()),
			TelemetrySettings: telemetry,
		},
		recvCfg,
		nextConsumer(conns),
	)
	if err != nil {
		shutdownConns(ctx, conns)

		if persister != nil {
			persister.removePersistentExt(extID)
		}

		return nil, fmt.Errorf("build receiver: %w", err)
	}

	if err := recv.Start(ctx, host); err != nil {
		shutdownConns(ctx, conns)

		if persister != nil {
			persister.removePersistentExt(extID)
		}

		return nil, fmt.Errorf("start receiver: %w", err)
	}

	return &source{recv: recv, conns: conns, persister: persister, extID: extID}, nil
}

// buildConnectors tries one connector for all filters together (fast path: one
// tree walk and one registry lock per batch). If that combined config fails
// validation, it falls back to one connector per filter so a bad regex only
// disables its own metric instead of every filter on this source.
func buildConnectors(
	ctx context.Context,
	connFactory otelconnector.Factory,
	telemetry component.TelemetrySettings,
	filters []config.LogFilter,
	sink consumer.Metrics,
) ([]otelconnector.Logs, error) {
	combinedCfg := &countconnector.Config{Logs: make(map[string]countconnector.MetricInfo, len(filters))}

	for _, filter := range filters {
		if _, exists := combinedCfg.Logs[filter.Metric]; exists {
			logger.Printf("logmetrics: metric %q declared more than once for the same source, ignoring the duplicate", filter.Metric)

			continue
		}

		combinedCfg.Logs[filter.Metric] = metricInfo(filter)
	}

	if combinedCfg.Validate() == nil {
		if conn, err := createConnector(ctx, connFactory, telemetry, combinedCfg, sink); err == nil {
			return []otelconnector.Logs{conn}, nil
		}
	}

	conns := make([]otelconnector.Logs, 0, len(filters))

	for _, filter := range filters {
		filterCfg := &countconnector.Config{Logs: map[string]countconnector.MetricInfo{filter.Metric: metricInfo(filter)}}

		if err := filterCfg.Validate(); err != nil {
			logger.Printf("logmetrics: metric %q disabled, invalid filter: %v", filter.Metric, err)

			continue
		}

		conn, err := createConnector(ctx, connFactory, telemetry, filterCfg, sink)
		if err != nil {
			logger.Printf("logmetrics: metric %q disabled: %v", filter.Metric, err)

			continue
		}

		conns = append(conns, conn)
	}

	if len(conns) == 0 {
		return nil, errNoValidFilter
	}

	return conns, nil
}

func metricInfo(filter config.LogFilter) countconnector.MetricInfo {
	return countconnector.MetricInfo{
		Description: "log-to-metric: " + filter.Metric,
		Conditions:  []string{fmt.Sprintf("IsMatch(log.body, %q)", filter.Regex)},
	}
}

func createConnector(
	ctx context.Context,
	factory otelconnector.Factory,
	telemetry component.TelemetrySettings,
	cfg *countconnector.Config,
	sink consumer.Metrics,
) (otelconnector.Logs, error) {
	conn, err := factory.CreateLogsToMetrics(
		ctx,
		otelconnector.Settings{
			ID:                component.NewIDWithName(factory.Type(), uuid.NewString()),
			TelemetrySettings: telemetry,
		},
		cfg,
		sink,
	)
	if err != nil {
		return nil, fmt.Errorf("build connector: %w", err)
	}

	if err := conn.Start(ctx, nil); err != nil {
		_ = conn.Shutdown(ctx)

		return nil, fmt.Errorf("start connector: %w", err)
	}

	return conn, nil
}

// nextConsumer avoids the fan-out wrapper entirely in the common case (a
// single connector, e.g. one filter or the combined fast path).
func nextConsumer(conns []otelconnector.Logs) consumer.Logs {
	if len(conns) == 1 {
		return conns[0]
	}

	return fanoutLogs(conns)
}

// fanoutLogs forwards each batch to every conn. Sharing one plog.Logs across
// all of them is safe: countconnector never mutates its input.
func fanoutLogs(conns []otelconnector.Logs) consumer.Logs {
	fanout, err := consumer.NewLogs(func(ctx context.Context, ld plog.Logs) error {
		var errs error

		for _, conn := range conns {
			errs = errors.Join(errs, conn.ConsumeLogs(ctx, ld))
		}

		return errs
	})
	if err != nil {
		panic(err) // only fails if the func were nil
	}

	return fanout
}

func shutdownConns(ctx context.Context, conns []otelconnector.Logs) {
	for _, conn := range conns {
		_ = conn.Shutdown(ctx)
	}
}

func (s *source) stop(ctx context.Context) error {
	recvErr := s.recv.Shutdown(ctx)

	if s.persister != nil {
		s.persister.removePersistentExt(s.extID)
	}

	var connErr error

	for _, conn := range s.conns {
		connErr = errors.Join(connErr, conn.Shutdown(ctx))
	}

	if recvErr != nil {
		return recvErr
	}

	return connErr
}
