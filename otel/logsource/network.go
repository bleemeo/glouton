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
	"sort"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configgrpc"
	"go.opentelemetry.io/collector/config/confignet"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/otlpreceiver"
	noopM "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace/noop"
)

var errUnexpectedConfig = errors.New("unexpected config type")

// SetupOTLPNetworkReceiver builds, starts and returns an OTLP log receiver for
// protocols (mirrors otlpreceiver.Config.Protocols: a nil field means that
// protocol is disabled, its presence enables it -- same "configoptional"
// convention real OTel itself uses, so config.NetworkProtocols can be passed
// straight from config.NetworkReceiver.Protocols), forwarding everything it
// receives to sink. idName should be unique per caller.
//
// Unlike a normal Collector config load, building receiverTypedCfg here never
// goes through confmap's automatic validation, so this explicitly calls
// Validate() itself: without it, a receiver with every protocol disabled
// wouldn't error at all -- otlpReceiver.Start silently no-ops per protocol
// when absent, so pushed logs would just vanish with nothing logged anywhere.
//
// The caller is responsible for wrapping sink with WrapWithInstrumentation
// beforehand if it wants processed-count/throughput bookkeeping, and for
// calling Shutdown on the returned receiver.
func SetupOTLPNetworkReceiver(
	ctx context.Context,
	telemetry component.TelemetrySettings,
	protocols config.NetworkProtocols,
	sink consumer.Logs,
	idName string,
) (receiver.Logs, error) {
	factoryReceiver := otlpreceiver.NewFactory()
	receiverCfg := factoryReceiver.CreateDefaultConfig()

	receiverTypedCfg, ok := receiverCfg.(*otlpreceiver.Config)
	if !ok {
		return nil, fmt.Errorf("%w for receiver default config: %T", errUnexpectedConfig, receiverCfg)
	}

	if protocols.GRPC != nil {
		// Mutate the factory's default GRPC config in place (rather than building a
		// ServerConfig from scratch) so defaults like ReadBufferSize survive.
		grpc := receiverTypedCfg.Protocols.GRPC.GetOrInsertDefault()
		if protocols.GRPC.Endpoint != "" {
			grpc.NetAddr = confignet.AddrConfig{
				Endpoint:  protocols.GRPC.Endpoint,
				Transport: confignet.TransportTypeTCP,
			}
		}
	} else {
		receiverTypedCfg.Protocols.GRPC = configoptional.None[configgrpc.ServerConfig]()
	}

	if protocols.HTTP != nil {
		// Same as above: mutate in place, so the factory's default TracesURLPath/
		// MetricsURLPath/LogsURLPath survive (otlpreceiver panics on Start if the
		// logs URL path is empty).
		http := receiverTypedCfg.Protocols.HTTP.GetOrInsertDefault()
		if protocols.HTTP.Endpoint != "" {
			http.ServerConfig.NetAddr = confignet.AddrConfig{
				Endpoint:  protocols.HTTP.Endpoint,
				Transport: confignet.TransportTypeTCP,
			}
		}
	} else {
		receiverTypedCfg.Protocols.HTTP = configoptional.None[otlpreceiver.HTTPConfig]()
	}

	if err := receiverTypedCfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid OTLP receiver config: %w", err)
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

// NewTelemetrySettings builds the component.TelemetrySettings every OTel
// component constructed by otel/logprocessing and otel/logmetrics needs,
// wired to Glouton's own logger and to no-op tracing/metrics providers (this
// embedded usage never exports OTel's own telemetry about itself).
func NewTelemetrySettings() component.TelemetrySettings {
	return component.TelemetrySettings{
		Logger:         logger.ZapLogger(),
		TracerProvider: noop.NewTracerProvider(),
		MeterProvider:  noopM.NewMeterProvider(),
		Resource:       pcommon.NewResource(),
	}
}

// FanoutLogs returns a consumer.Logs that forwards every batch to every
// non-nil sink, in argument order. Some consumers mutate a plog.Logs in place
// (e.g. otel/logprocessing's resource-attribute processor adds "host.name" to
// its resource attributes), so every sink after the first receives its own
// deep copy -- mutations made while feeding one sink must never be visible to
// another. Returns nil if every sink is nil, or the sink itself, unmodified
// (no copy, not even wrapped), if there is exactly one.
func FanoutLogs(sinks ...consumer.Logs) consumer.Logs {
	active := make([]consumer.Logs, 0, len(sinks))

	for _, sink := range sinks {
		if sink != nil {
			active = append(active, sink)
		}
	}

	switch len(active) {
	case 0:
		return nil
	case 1:
		return active[0]
	}

	fanout, err := consumer.NewLogs(func(ctx context.Context, ld plog.Logs) error {
		// Every clone must be taken from the pristine ld before any sink runs:
		// if a sink mutates its batch in place and only then we cloned from
		// ld, a later sink would see that earlier mutation too.
		batches := make([]plog.Logs, len(active))
		batches[0] = ld

		for i := 1; i < len(active); i++ {
			batches[i] = plog.NewLogs()
			ld.ResourceLogs().CopyTo(batches[i].ResourceLogs())
		}

		var errs error

		for i, sink := range active {
			errs = errors.Join(errs, sink.ConsumeLogs(ctx, batches[i]))
		}

		return errs
	})
	if err != nil {
		panic(err) // only fails if the func were nil
	}

	return fanout
}

// NetworkWant describes one feature's opt-in to named shared network
// receivers (config.Log.Network.Receivers): Consumer is its entry point, and
// Receivers is the list of entries it pulls from -- mirrors an OTel
// pipeline's own `receivers: [...]` list. A feature gets whatever protocols
// each named receiver has configured; there's no separate per-protocol opt-in
// at this level, same as real OTel (a pipeline either includes a receiver, in
// full, or doesn't).
type NetworkWant struct {
	Consumer  consumer.Logs
	Receivers []string
}

// PlannedReceiver is what SetupOTLPNetworkReceiver needs to start one named
// shared receiver, as computed by PlanSharedNetworkReceivers.
type PlannedReceiver struct {
	Name      string
	Protocols config.NetworkProtocols
	Sink      consumer.Logs
}

// PlanSharedNetworkReceivers groups wants by receiver name, so features
// naming the same config.Log.Network.Receivers entry share one physical
// listener and one FanoutLogs sink, while features naming different entries
// get independent listeners. An entry nobody references, or referenced but
// producing no consumer, is omitted from the result -- there's nothing to
// start. Each planned receiver's protocols come straight from that entry's
// own config: unlike an earlier design, there's no "did a participant want
// this protocol" derivation here, matching real OTel where a receiver's
// protocols are intrinsic to the receiver, not to whichever pipelines include
// it.
func PlanSharedNetworkReceivers(receivers map[string]config.NetworkReceiver, wants []NetworkWant) []PlannedReceiver {
	consumersByName := make(map[string][]consumer.Logs)

	for _, want := range wants {
		if want.Consumer == nil {
			continue
		}

		for _, name := range want.Receivers {
			consumersByName[name] = append(consumersByName[name], want.Consumer)
		}
	}

	names := make([]string, 0, len(consumersByName))

	for name := range consumersByName {
		names = append(names, name)
	}

	sort.Strings(names) // deterministic order: map iteration order isn't

	planned := make([]PlannedReceiver, 0, len(names))

	for _, name := range names {
		sink := FanoutLogs(consumersByName[name]...)
		if sink == nil {
			continue
		}

		planned = append(planned, PlannedReceiver{
			Name:      name,
			Protocols: receivers[name].Protocols,
			Sink:      sink,
		})
	}

	return planned
}

// nopHost is a component.Host with no extensions, sufficient for a receiver
// that doesn't need to look any up (this OTLP receiver doesn't use auth
// extensions).
type nopHost struct{}

func (nopHost) GetExtensions() map[component.ID]component.Component {
	return nil
}
