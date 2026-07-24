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

package logsource

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/bleemeo/glouton/config"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configgrpc"
	"go.opentelemetry.io/collector/config/confignet"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/otlpreceiver"
)

var errUnexpectedConfig = errors.New("unexpected config type")

// SetupOTLPNetworkReceiver builds, starts and returns an OTLP log receiver
// listening on grpcCfg/httpCfg's configured protocols (at least one of which
// must be enabled), forwarding everything it receives to sink. idName should
// be unique per caller (each feature that sets up its own network receiver).
//
// The caller is responsible for wrapping sink with WrapWithInstrumentation
// beforehand if it wants processed-count/throughput bookkeeping, and for
// calling Shutdown on the returned receiver.
func SetupOTLPNetworkReceiver(
	ctx context.Context,
	telemetry component.TelemetrySettings,
	grpcCfg, httpCfg config.EnableListener,
	sink consumer.Logs,
	idName string,
) (receiver.Logs, error) {
	factoryReceiver := otlpreceiver.NewFactory()
	receiverCfg := factoryReceiver.CreateDefaultConfig()

	receiverTypedCfg, ok := receiverCfg.(*otlpreceiver.Config)
	if !ok {
		return nil, fmt.Errorf("%w for receiver default config: %T", errUnexpectedConfig, receiverCfg)
	}

	if grpcCfg.Enable {
		// Mutate the factory's default GRPC config in place (rather than building a
		// ServerConfig from scratch) so defaults like ReadBufferSize survive.
		grpc := receiverTypedCfg.Protocols.GRPC.GetOrInsertDefault()
		grpc.NetAddr = confignet.AddrConfig{
			Endpoint:  net.JoinHostPort(grpcCfg.Address, strconv.Itoa(grpcCfg.Port)),
			Transport: confignet.TransportTypeTCP,
		}
	} else {
		receiverTypedCfg.Protocols.GRPC = configoptional.None[configgrpc.ServerConfig]()
	}

	if httpCfg.Enable {
		// Same as above: mutate in place, so the factory's default TracesURLPath/
		// MetricsURLPath/LogsURLPath survive (otlpreceiver panics on Start if the
		// logs URL path is empty, and "ip" is not a valid net.Listen transport --
		// both defaults must come from the factory, not be rebuilt from scratch).
		http := receiverTypedCfg.Protocols.HTTP.GetOrInsertDefault()
		http.ServerConfig.NetAddr = confignet.AddrConfig{
			Endpoint:  net.JoinHostPort(httpCfg.Address, strconv.Itoa(httpCfg.Port)),
			Transport: confignet.TransportTypeTCP,
		}
	} else {
		receiverTypedCfg.Protocols.HTTP = configoptional.None[otlpreceiver.HTTPConfig]()
	}

	otlpLogReceiver, err := factoryReceiver.CreateLogs(
		ctx,
		receiver.Settings{
			ID:                component.NewIDWithName(factoryReceiver.Type(), idName),
			TelemetrySettings: telemetry,
		},
		receiverTypedCfg,
		sink,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to setup OTLP receiver: %w", err)
	}

	// otlpreceiver's gRPC startup path calls host.GetExtensions() unconditionally,
	// so a nil component.Host (a nil interface, not just a nil map) panics.
	if err = otlpLogReceiver.Start(ctx, nopHost{}); err != nil {
		if shutdownErr := otlpLogReceiver.Shutdown(ctx); shutdownErr != nil {
			return nil, fmt.Errorf("failed to start OTLP receiver: %w (and failed to stop it too: %w)", err, shutdownErr)
		}

		return nil, fmt.Errorf("failed to start OTLP receiver: %w", err)
	}

	return otlpLogReceiver, nil
}

// nopHost is a component.Host with no extensions, sufficient for a receiver
// that doesn't need to look any up (this OTLP receiver doesn't use auth
// extensions).
type nopHost struct{}

func (nopHost) GetExtensions() map[component.ID]component.Component {
	return nil
}
