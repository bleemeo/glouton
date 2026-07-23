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

	"github.com/google/uuid"
	"github.com/open-telemetry/opentelemetry-collector-contrib/connector/countconnector"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/parser/container"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/filelogreceiver"
	"go.opentelemetry.io/collector/component"
	otelconnector "go.opentelemetry.io/collector/connector"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
)

var errUnexpectedConfigType = errors.New("unexpected receiver config type")

// source is one real OpenTelemetry Collector mini-pipeline dedicated to a single
// log-to-metric source (a static path or a resolved container log file):
//
//	filelogreceiver --(plog.Logs)--> countconnector --(pmetric.Metrics)--> shared registry sink
//
// The countconnector evaluates one OTTL "IsMatch(body, ...)" condition per
// configured metric and reports a delta count each time it processes a batch --
// matching happens natively in OpenTelemetry (OTTL)
type source struct {
	recv receiver.Logs
	conn otelconnector.Logs
}

// newSource builds and starts a source. include is a list of glob patterns
// (hostroot already applied by the caller). If isContainer is true, the Docker/CRI
// log envelope is unwrapped (via the same "container" operator otel/logprocessing
// uses) before matching, so OTTL sees the actual log message rather than the raw
// JSON/CRI-wrapped line.
func newSource(
	ctx context.Context,
	telemetry component.TelemetrySettings,
	include []string,
	isContainer bool,
	filters []config.LogFilter,
	sink consumer.Metrics,
) (*source, error) {
	connCfg := &countconnector.Config{Logs: make(map[string]countconnector.MetricInfo, len(filters))}

	for _, filter := range filters {
		connCfg.Logs[filter.Metric] = countconnector.MetricInfo{
			Description: "log-to-metric: " + filter.Metric,
			Conditions:  []string{fmt.Sprintf("IsMatch(body, %q)", filter.Regex)},
		}
	}

	if err := connCfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid filter: %w", err)
	}

	connFactory := countconnector.NewFactory()

	conn, err := connFactory.CreateLogsToMetrics(
		ctx,
		otelconnector.Settings{
			ID:                component.NewIDWithName(connFactory.Type(), uuid.NewString()),
			TelemetrySettings: telemetry,
		},
		connCfg,
		sink,
	)
	if err != nil {
		return nil, fmt.Errorf("build connector: %w", err)
	}

	if err := conn.Start(ctx, nil); err != nil {
		return nil, fmt.Errorf("start connector: %w", err)
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
		_ = conn.Shutdown(ctx)

		return nil, fmt.Errorf("%w: %T", errUnexpectedConfigType, defaultCfg)
	}

	recvCfg.InputConfig.Include = include
	recvCfg.Operators = operators
	// No StorageID: matches aren't persisted across restarts.
	// A restart may miss a handful of lines around the moment it happens.

	recv, err := recvFactory.CreateLogs(
		ctx,
		receiver.Settings{
			ID:                component.NewIDWithName(recvFactory.Type(), uuid.NewString()),
			TelemetrySettings: telemetry,
		},
		recvCfg,
		conn,
	)
	if err != nil {
		_ = conn.Shutdown(ctx)

		return nil, fmt.Errorf("build receiver: %w", err)
	}

	if err := recv.Start(ctx, nil); err != nil {
		_ = conn.Shutdown(ctx)

		return nil, fmt.Errorf("start receiver: %w", err)
	}

	return &source{recv: recv, conn: conn}, nil
}

func (s *source) stop(ctx context.Context) error {
	recvError := s.recv.Shutdown(ctx)
	connError := s.conn.Shutdown(ctx)

	if recvError != nil {
		return recvError
	}

	return connError
}
