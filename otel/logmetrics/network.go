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
	"github.com/bleemeo/glouton/otel/logsource"

	"github.com/open-telemetry/opentelemetry-collector-contrib/connector/countconnector"
	otelconnector "go.opentelemetry.io/collector/connector"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
)

const networkReceiverIDName = "logmetrics-otlp-receiver"

// networkSource counts matches in logs pushed by an external OTLP gRPC/HTTP
// client, rather than tailing a file. There is no StorageID/persisted offset
// here: unlike a file, there's no byte position to resume from.
type networkSource struct {
	recv  receiver.Logs
	conns []otelconnector.Logs
}

// newNetworkSource returns (nil, nil) if the network receiver is disabled or
// has no counters configured -- neither is an error, just "nothing to start".
func newNetworkSource(
	ctx context.Context,
	telemetry component.TelemetrySettings,
	netCfg config.LogMetricsNetworkReceiver,
	sink consumer.Metrics,
) (*networkSource, error) {
	if !netCfg.GRPC.Enable && !netCfg.HTTP.Enable {
		return nil, nil //nolint:nilnil
	}

	if len(netCfg.Counters) == 0 {
		return nil, nil //nolint:nilnil
	}

	connFactory := countconnector.NewFactory()

	conns, err := buildConnectors(ctx, connFactory, telemetry, netCfg.Counters, sink)
	if err != nil {
		return nil, err
	}

	recv, err := logsource.SetupOTLPNetworkReceiver(ctx, telemetry, netCfg.GRPC, netCfg.HTTP, nextConsumer(conns), networkReceiverIDName)
	if err != nil {
		shutdownConns(ctx, conns)

		return nil, err
	}

	return &networkSource{recv: recv, conns: conns}, nil
}

func (s *networkSource) stop(ctx context.Context) error {
	recvErr := s.recv.Shutdown(ctx)

	var connErr error

	for _, conn := range s.conns {
		connErr = errors.Join(connErr, conn.Shutdown(ctx))
	}

	if recvErr != nil {
		return recvErr
	}

	return connErr
}
