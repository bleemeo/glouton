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
	"slices"
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
	"go.opentelemetry.io/collector/config/configtelemetry"
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
	retrySetupFileReceiversPeriod = 1 * time.Minute
	saveFileSizesToCachePeriod    = 1 * time.Minute
	shutdownTimeout               = 5 * time.Second
)

var errUnexpectedType = errors.New("unexpected type")

type pipelineContext struct {
	config        config.OpenTelemetry
	lastFileSizes map[string]int64
	telemetry     component.TelemetrySettings
	commandRunner CommandRunner
	persister     *persistHost

	l sync.Mutex
	// startedComponents represents all the components that must be shut down at the end of the context's lifetime.
	startedComponents []component.Component
	receivers         []*logReceiver

	logProcessedCount  atomic.Int64
	logThroughputMeter *ringCounter
}

func MakePipeline( //nolint:maintidx
	ctx context.Context,
	cfg config.OpenTelemetry,
	state bleemeoTypes.State,
	commandRunner CommandRunner,
	pushLogs func(context.Context, []byte) error,
	streamAvailabilityStatusFn func() bleemeoTypes.LogsAvailability,
) (diagnosticFn func(context.Context, types.ArchiveWriter) error, err error) { //nolint:wsl
	// TODO: no logs & metrics at the same time

	persister, err := newPersistHost(state)
	if err != nil {
		return nil, fmt.Errorf("can't create persist host: %w", err)
	}

	pipeline := &pipelineContext{
		lastFileSizes: getLastFileSizesFromCache(state),
		config:        cfg,
		telemetry: component.TelemetrySettings{
			Logger:         logger.ZapLogger(),
			TracerProvider: noop.NewTracerProvider(),
			MeterProvider:  noopM.NewMeterProvider(),
			MetricsLevel:   configtelemetry.LevelBasic,
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

	logExporter, err := exporterhelper.NewLogs(
		ctx,
		exporter.Settings{TelemetrySettings: pipeline.telemetry},
		"unused",
		func(ctx context.Context, ld plog.Logs) error {
			b, err := new(plog.ProtoMarshaler).MarshalLogs(ld)
			if err != nil {
				return err
			}

			if err = pushLogs(ctx, b); err != nil {
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
		processor.Settings{TelemetrySettings: pipeline.telemetry},
		&batchprocessor.Config{
			Timeout:                  10 * time.Second,
			SendBatchSize:            1 << 16, // 64KiB
			SendBatchMaxSize:         1 << 21, // 2MiB
			MetadataCardinalityLimit: 1000,    // config default
		},
		logExporter,
	)
	if err != nil {
		return nil, fmt.Errorf("setup batcher: %w", err)
	}

	if err = logBatcher.Start(ctx, nil); err != nil {
		return nil, fmt.Errorf("start batcher: %w", err)
	}

	logBackPressureEnforcer, err := processorhelper.NewLogs(
		ctx,
		processor.Settings{TelemetrySettings: pipeline.telemetry},
		nil,
		logBatcher,
		makeEnforceBackPressureFn(streamAvailabilityStatusFn),
	)
	if err != nil {
		return nil, fmt.Errorf("setup log back-pressure enforcer: %w", err)
	}

	pipeline.startedComponents = append(pipeline.startedComponents, logBatcher)

	if cfg.GRPC.Enable || cfg.HTTP.Enable {
		factoryReceiver := otlpreceiver.NewFactory()
		receiverCfg := factoryReceiver.CreateDefaultConfig()

		receiverTypedCfg, ok := receiverCfg.(*otlpreceiver.Config)
		if !ok {
			logger.V(1).Printf("Unexpected config type for receiver default config: %T", receiverCfg)

			goto AfterOTLPReceiversSetup
		}

		if cfg.GRPC.Enable {
			receiverTypedCfg.Protocols.GRPC.NetAddr.Endpoint = net.JoinHostPort(cfg.GRPC.Address, strconv.Itoa(cfg.GRPC.Port))
		} else {
			receiverTypedCfg.Protocols.GRPC = nil
		}

		if cfg.HTTP.Enable {
			receiverTypedCfg.Protocols.HTTP.Endpoint = net.JoinHostPort(cfg.HTTP.Address, strconv.Itoa(cfg.HTTP.Port))
		} else {
			receiverTypedCfg.Protocols.HTTP = nil
		}

		otlpLogReceiver, err := factoryReceiver.CreateLogs(
			ctx,
			receiver.Settings{TelemetrySettings: pipeline.telemetry},
			receiverTypedCfg,
			logBackPressureEnforcer,
		)
		if err != nil {
			logger.V(1).Printf("Failed to setup OTLP receiver: %v", err)

			goto AfterOTLPReceiversSetup
		}

		if err = otlpLogReceiver.Start(ctx, nil); err != nil {
			logger.V(1).Printf("Failed to start OTLP receiver: %v", err)

			goto AfterOTLPReceiversSetup
		}

		pipeline.startedComponents = append(pipeline.startedComponents, otlpLogReceiver)
	}
AfterOTLPReceiversSetup: // this label must be right after the OTLP receivers block

	for name, rcvrCfg := range cfg.Receivers {
		recv, err := newLogReceiver(name, rcvrCfg, logBackPressureEnforcer)
		if err != nil {
			logger.V(1).Printf("Failed to setup log receiver %q (ignoring it): %v", name, err)

			continue
		}

		err = recv.update(ctx, pipeline)
		if err != nil {
			logger.V(1).Printf("Failed to start log receiver %q (ignoring it): %v", name, err)

			continue
		}

		pipeline.receivers = append(pipeline.receivers, recv)
	}

	if len(pipeline.receivers) == 0 {
		if len(cfg.Receivers) > 0 {
			logger.V(1).Printf("None of the %d configured log receiver(s) is valid.", len(cfg.Receivers))
		}

		if !cfg.HTTP.Enable && !cfg.GRPC.Enable {
			logger.V(1).Printf("No receiver to start; disabling log processing.")

			shutdownAll(pipeline.startedComponents)

			return nil, nil //nolint:nilnil
		}
	}

	go func() {
		defer crashreport.ProcessPanic()

		ticker := time.NewTicker(retrySetupFileReceiversPeriod)
		defer ticker.Stop()

		for ctx.Err() == nil {
			select {
			case <-ticker.C:
				pipeline.l.Lock()

				for i, rcvr := range pipeline.receivers {
					err = rcvr.update(ctx, pipeline)
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

	// Scheduling the save of file sizes to cache
	go func() {
		defer crashreport.ProcessPanic()

		ticker := time.NewTicker(saveFileSizesToCachePeriod)
		defer ticker.Stop()

		for ctx.Err() == nil {
			select {
			case <-ticker.C:
				pipeline.l.Lock()

				saveLastFileSizesToCache(state, pipeline.receivers)
				saveFileMetadataToCache(state, pipeline.persister.getAllMetadata())

				pipeline.l.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()

	// Scheduling the shutdown of all the components we've started
	go func() {
		defer crashreport.ProcessPanic()

		<-ctx.Done()

		pipeline.l.Lock()
		defer pipeline.l.Unlock()

		shutdownAll(pipeline.startedComponents)

		saveLastFileSizesToCache(state, pipeline.receivers)
		saveFileMetadataToCache(state, pipeline.persister.getAllMetadata())
	}()

	diagnosticFn = func(_ context.Context, writer types.ArchiveWriter) error {
		pipeline.l.Lock()
		receiversInfo := make(map[string]receiverDiagnosticInformation, len(pipeline.receivers))

		for _, rcvr := range pipeline.receivers {
			receiversInfo[rcvr.name] = rcvr.diagnosticInfo()
		}

		pipeline.l.Unlock()

		diagnosticInfo := diagnosticInformation{
			LogProcessedCount:      pipeline.logProcessedCount.Load(),
			LogThroughputPerMinute: pipeline.logThroughputMeter.Total(),
			ProcessingStatus:       streamAvailabilityStatusFn().String(),
			Receivers:              receiversInfo,
		}

		return diagnosticInfo.writeToArchive(writer)
	}

	return diagnosticFn, nil
}

func makeEnforceBackPressureFn(streamAvailabilityStatusFn func() bleemeoTypes.LogsAvailability) processorhelper.ProcessLogsFunc {
	// Since streamAvailabilityStatusFn needs to acquire both the connector and the MQTT client locks,
	// we want to avoid calling it too frequently.
	const cacheLifetime = 5 * time.Second

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

		if time.Since(lastCacheUpdate) > cacheLifetime {
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

// shutdownAll stops all the given components (in reverse order).
// It should be called before every unsuccessful return of the log pipeline initialization.
func shutdownAll(components []component.Component) {
	// Shutting down first the components that are at the beginning of the log production chain.
	for _, comp := range slices.Backward(components) {
		go func() {
			defer crashreport.ProcessPanic()

			shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()

			err := comp.Shutdown(shutdownCtx)
			if err != nil {
				logger.V(1).Printf("Failed to shutdown log processing component %T: %v", comp, err)
			}
		}()
	}
}
