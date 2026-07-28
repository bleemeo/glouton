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

	"github.com/bleemeo/glouton/config"

	"github.com/open-telemetry/opentelemetry-collector-contrib/connector/countconnector"
	"go.opentelemetry.io/collector/component"
	otelconnector "go.opentelemetry.io/collector/connector"
	"go.opentelemetry.io/collector/consumer"
)

// networkSource counts matches in logs pushed by an external OTLP gRPC/HTTP
// client, rather than tailing a file. There is no StorageID/persisted offset
// here: unlike a file, there's no byte position to resume from.
//
// Unlike other sources, this doesn't own an OTLP receiver: the physical
// listener is shared with otel/logprocessing (see
// logsource.SetupOTLPNetworkReceiver/FanoutLogs), so a client only ever needs
// one endpoint regardless of which features consume what it sends.
// entryConsumer is what the shared receiver's owner feeds into, exposed via
// Manager.NetworkLogsConsumer.
type networkSource struct {
	conns         []otelconnector.Logs
	entryConsumer consumer.Logs
}

// newNetworkSource returns (nil, nil) if the network receiver is disabled or
// there's nothing configured to count at all -- neither is an error, just
// "nothing to start". Like any other source, it counts against the global
// count map (see LogMetricsConfig.Count's doc comment): there's no separate
// counter list for network-pushed logs.
func newNetworkSource(
	ctx context.Context,
	telemetry component.TelemetrySettings,
	netCfg config.LogMetricsNetworkReceiver,
	count map[string]config.LogMetricsCount,
	sink consumer.Metrics,
) (*networkSource, error) {
	if len(netCfg.Receivers) == 0 && !netCfg.Enable {
		return nil, nil //nolint:nilnil
	}

	if len(count) == 0 {
		return nil, nil //nolint:nilnil
	}

	connFactory := countconnector.NewFactory()

	conns, err := buildConnectors(ctx, connFactory, telemetry, count, sink)
	if err != nil {
		return nil, err
	}

	return &networkSource{conns: conns, entryConsumer: nextConsumer(conns)}, nil
}

func (s *networkSource) stop(ctx context.Context) error {
	var connErr error

	for _, conn := range s.conns {
		connErr = errors.Join(connErr, conn.Shutdown(ctx))
	}

	return connErr
}
