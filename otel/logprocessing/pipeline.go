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

package logprocessing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/batchprocessor"
	"go.opentelemetry.io/collector/processor/processorhelper"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/otlpreceiver"
	noopM "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace/noop"
)

const (
	throughputMeterResolutionSecs = 60 // we want to measure the throughput over the last minute (60s)
	receiversUpdatePeriod         = 1 * time.Minute
	saveFileSizesToCachePeriod    = 1 * time.Minute
	shutdownTimeout               = 5 * time.Second
)

var errUnexpectedType = errors.New("unexpected type")

type pipelineContext struct {
	hostroot      string
	lastFileSizes map[string]int64
	telemetry     component.TelemetrySettings
	commandRunner CommandRunner
	persister     *persistHost

	l sync.Mutex
	// startedComponents represents all the components that must be shut down at the end of the context's lifetime.
	startedComponents []component.Component
	receivers         []*logReceiver

	inputConsumer consumer.Logs

	otlpRecvCounter         *atomic.Int64
	otlpRecvThroughputMeter *ringCounter

	logProcessedCount  atomic.Int64
	logThroughputMeter *ringCounter
}

type pipelineOptions struct {
	batcherTimeout           time.Duration
	logsAvailabilityCacheTTL time.Duration
}

func makePipeline( //nolint:maintidx
	ctx context.Context,
	cfg config.OpenTelemetry,
	hostroot string,
	commandRunner CommandRunner,
	pushLogs func(context.Context, []byte) error,
	streamAvailabilityStatusFn func() bleemeoTypes.LogsAvailability,
	persister *persistHost,
	addWarnings func(...error),
	lastFileSizes map[string]int64,
	opts pipelineOptions,
) (
	pipeline *pipelineContext,
	err error,
) { //nolint:wsl
	// TODO: no logs & metrics at the same time

	pipeline = &pipelineContext{
		lastFileSizes: lastFileSizes,
		hostroot:      hostroot,
		telemetry: component.TelemetrySettings{
			Logger:         logger.ZapLogger(),
			TracerProvider: noop.NewTracerProvider(),
			MeterProvider:  noopM.NewMeterProvider(),
			Resource:       pcommon.NewResource(),
		},
		commandRunner:      commandRunner,
		persister:          persister,
		startedComponents:  make([]component.Component, 0, 3), // 3 should be the minimum number of components
		receivers:          make([]*logReceiver, 0, len(cfg.Receivers)),
		logThroughputMeter: newRingCounter(throughputMeterResolutionSecs),
	}

	pipeline.l.Lock()
	defer pipeline.l.Unlock()

	defer func() {
		if err != nil {
			shutdownAll(pipeline.startedComponents)
		}
	}()

	chunker := logChunker{
		maxChunkSize: types.MaxMQTTPayloadSize,
		pushLogsFn:   pushLogs,
	}

	logExporter, err := exporterhelper.NewLogs(
		ctx,
		exporter.Settings{TelemetrySettings: pipeline.telemetry},
		"unused",
		func(ctx context.Context, ld plog.Logs) error {
			if err := chunker.push(ctx, ld); err != nil {
				logger.V(1).Printf("Failed to push logs: %v", err)
				// returning error goes nowhere (not visible anywhere), that's why we log it here
				return err
			}

			count := ld.LogRecordCount()
			pipeline.logProcessedCount.Add(int64(count))
			pipeline.logThroughputMeter.Add(count)

			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("setup exporter: %w", err)
	}

	if err = logExporter.Start(ctx, nil); err != nil {
		return nil, fmt.Errorf("start exporter: %w", err)
	}

	pipeline.startedComponents = append(pipeline.startedComponents, logExporter)

	factoryBatch := batchprocessor.NewFactory()

	logBatcher, err := factoryBatch.CreateLogs(
		ctx,
		processor.Settings{
			ID:                component.NewIDWithName(factoryBatch.Type(), "log-batcher"),
			TelemetrySettings: pipeline.telemetry,
		},
		&batchprocessor.Config{
			Timeout:                  opts.batcherTimeout,
			SendBatchSize:            8192, // config default
			MetadataCardinalityLimit: 1000, // config default
		},
		logExporter,
	)
	if err != nil {
		return nil, fmt.Errorf("setup batcher: %w", err)
	}

	if err = logBatcher.Start(ctx, nil); err != nil {
		return nil, fmt.Errorf("start batcher: %w", err)
	}

	pipeline.startedComponents = append(pipeline.startedComponents, logBatcher)

	logBackPressureEnforcer, err := processorhelper.NewLogs(
		ctx,
		processor.Settings{TelemetrySettings: pipeline.telemetry},
		nil,
		logBatcher,
		makeEnforceBackPressureFn(streamAvailabilityStatusFn, opts.logsAvailabilityCacheTTL),
	)
	if err != nil {
		return nil, fmt.Errorf("setup log back-pressure enforcer: %w", err)
	}

	pipeline.startedComponents = append(pipeline.startedComponents, logBackPressureEnforcer)
	pipeline.inputConsumer = logBackPressureEnforcer

	if cfg.GRPC.Enable || cfg.HTTP.Enable {
		factoryReceiver := otlpreceiver.NewFactory()
		receiverCfg := factoryReceiver.CreateDefaultConfig()

		receiverTypedCfg, ok := receiverCfg.(*otlpreceiver.Config)
		if !ok {
			logger.V(1).Printf("Unexpected config type for receiver default config: %T", receiverCfg)

			goto AfterOTLPReceiversSetup // avoid adding it to the list of started components
		}

		if cfg.GRPC.Enable {
			receiverTypedCfg.Protocols.GRPC.NetAddr.Endpoint = net.JoinHostPort(cfg.GRPC.Address, strconv.Itoa(cfg.GRPC.Port))
		} else {
			receiverTypedCfg.Protocols.GRPC = nil
		}

		if cfg.HTTP.Enable {
			receiverTypedCfg.Protocols.HTTP.ServerConfig.Endpoint = net.JoinHostPort(cfg.HTTP.Address, strconv.Itoa(cfg.HTTP.Port))
		} else {
			receiverTypedCfg.Protocols.HTTP = nil
		}

		pipeline.otlpRecvCounter = new(atomic.Int64)
		pipeline.otlpRecvThroughputMeter = newRingCounter(throughputMeterResolutionSecs)

		otlpLogReceiver, err := factoryReceiver.CreateLogs(
			ctx,
			receiver.Settings{
				ID:                component.NewIDWithName(factoryReceiver.Type(), "otlp-receiver"),
				TelemetrySettings: pipeline.telemetry,
			},
			receiverTypedCfg,
			wrapWithCounters(pipeline.inputConsumer, pipeline.otlpRecvCounter, pipeline.otlpRecvThroughputMeter),
		)
		if err != nil {
			logger.V(1).Printf("Failed to setup OTLP receiver: %v", err)

			goto AfterOTLPReceiversSetup // avoid adding it to the list of started components
		}

		if err = otlpLogReceiver.Start(ctx, nil); err != nil {
			logger.V(1).Printf("Failed to start OTLP receiver: %v", err)

			goto AfterOTLPReceiversSetup // avoid adding it to the list of started components
		}

		pipeline.startedComponents = append(pipeline.startedComponents, otlpLogReceiver)
	}
AfterOTLPReceiversSetup: // this label must be right after the OTLP receivers block

	for name, rcvrCfg := range cfg.Receivers {
		recv, err := newLogReceiver(name, rcvrCfg, false, pipeline.inputConsumer, cfg.KnownLogFormats)
		if err != nil {
			addWarnings(errorf("Failed to setup log receiver %q (ignoring it): %w", name, err))

			continue
		}

		err = recv.update(ctx, pipeline, addWarnings)
		if err != nil {
			logger.V(1).Printf("Failed to start log receiver %q (ignoring it): %v", name, err)

			continue
		}

		pipeline.receivers = append(pipeline.receivers, recv)
	}

	if len(pipeline.receivers) == 0 && len(cfg.Receivers) > 0 {
		logger.V(1).Printf("None of the %d configured log receiver(s) are valid.", len(cfg.Receivers))
	}

	go func() {
		defer crashreport.ProcessPanic()

		ticker := time.NewTicker(receiversUpdatePeriod)
		defer ticker.Stop()

		for ctx.Err() == nil {
			select {
			case <-ticker.C:
				pipeline.l.Lock()

				for i, rcvr := range pipeline.receivers {
					// Here we use logWarnings instead of addWarnings,
					// because that would make the description of
					// agent_config_warning grow indefinitely.
					err := rcvr.update(ctx, pipeline, logWarnings)
					if err != nil {
						logger.V(1).Printf("Failed to update log receiver n°%d: %v", i+1, err)
					}
				}

				pipeline.l.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()

	return pipeline, nil
}

// getInput returns a log consumer which can be used to append logs into the pipeline.
func (p *pipelineContext) getInput() consumer.Logs {
	return p.inputConsumer
}

func makeEnforceBackPressureFn(
	streamAvailabilityStatusFn func() bleemeoTypes.LogsAvailability,
	logsAvailabilityCacheTTL time.Duration,
) processorhelper.ProcessLogsFunc { //nolint: wsl
	// Since streamAvailabilityStatusFn needs to acquire both the connector and the MQTT client locks,
	// we want to avoid calling it too frequently.

	var (
		l sync.Mutex
		// Well ... we still need to prevent concurrent access to these variables,
		// since the back-pressure function we return can be called
		// simultaneously by multiple log emitters.
		lastCacheValue  bleemeoTypes.LogsAvailability
		lastCacheUpdate time.Time
	)

	applyBackPressureDebounced := func() bleemeoTypes.LogsAvailability {
		l.Lock()
		defer l.Unlock()

		if time.Since(lastCacheUpdate) > logsAvailabilityCacheTTL {
			newState := streamAvailabilityStatusFn()
			if newState != lastCacheValue {
				logger.V(2).Printf("Logs stream availability status is now %[1]d (policy: %[1]s)", newState)
			}

			lastCacheValue = newState
			lastCacheUpdate = time.Now()
		}

		return lastCacheValue
	}

	return func(_ context.Context, logs plog.Logs) (plog.Logs, error) {
		switch status := applyBackPressureDebounced(); status {
		case bleemeoTypes.LogsAvailabilityOk:
			return logs, nil
		case bleemeoTypes.LogsAvailabilityShouldBuffer:
			return logs, types.ErrBackPressureSignal
		case bleemeoTypes.LogsAvailabilityShouldDiscard:
			return logs, processorhelper.ErrSkipProcessingData
		default:
			logger.V(1).Printf("Bad logs availability status: %s", status)
		}

		return logs, nil
	}
}
