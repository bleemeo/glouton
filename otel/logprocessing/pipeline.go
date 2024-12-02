// Copyright 2015-2023 Bleemeo
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
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/execlogreceiver"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/filelogreceiver"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configtelemetry"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/batchprocessor"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/otlpreceiver"
	"go.opentelemetry.io/otel/metric"
	noopM "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace/noop"
	"gopkg.in/yaml.v3"
)

const shutdownTimeout = 5 * time.Second

var errUnexpectedType = errors.New("unexpected type")

func MakePipeline(ctx context.Context, cfg config.OpenTelemetry, pushLogs func(context.Context, []byte) error) error {
	// TODO: no logs & metrics at the same time
	// TODO: buffering / back pressure not implemented (we don't send the ErrBackPressureSignal back to client that send us logs)

	// startedComponents represents all the components that must be shut down at the end of the context's lifecycle.
	startedComponents := make([]component.Component, 0, 3+len(cfg.LogFiles)) // 3 == 1 exporter + 1 GRPC receiver + 1 HTTP receiver.

	telemetry := component.TelemetrySettings{
		Logger:               logger.ZapLogger(),
		TracerProvider:       noop.NewTracerProvider(),
		MeterProvider:        noopM.NewMeterProvider(),
		LeveledMeterProvider: func(_ configtelemetry.Level) metric.MeterProvider { return noopM.NewMeterProvider() },
		MetricsLevel:         configtelemetry.LevelBasic,
		Resource:             pcommon.NewResource(),
	}

	logExporter, err := exporterhelper.NewLogs(
		ctx,
		exporter.Settings{TelemetrySettings: telemetry},
		"unused",
		func(ctx context.Context, ld plog.Logs) error {
			b, err := new(plog.ProtoMarshaler).MarshalLogs(ld)
			if err != nil {
				return err
			}

			logger.Printf("Pushing logs using %p", pushLogs) // TODO: remove

			if err = pushLogs(ctx, b); err != nil {
				logger.V(1).Printf("Failed to push logs: %v", err)
				// returning error goes nowhere (not visible anywhere), that why we do the log
				return err
			}

			return nil
		},
	)
	if err != nil {
		shutdownAll(startedComponents)

		return fmt.Errorf("setup exporter: %w", err)
	}

	if err = logExporter.Start(ctx, nil); err != nil {
		shutdownAll(startedComponents)

		return fmt.Errorf("start exporter: %w", err)
	}

	startedComponents = append(startedComponents, logExporter)

	factoryBatch := batchprocessor.NewFactory()

	logBatcher, err := factoryBatch.CreateLogs(
		ctx,
		processor.Settings{TelemetrySettings: telemetry},
		factoryBatch.CreateDefaultConfig(),
		logExporter,
	)
	if err != nil {
		shutdownAll(startedComponents)

		return fmt.Errorf("setup batcher: %w", err)
	}

	if err = logBatcher.Start(ctx, nil); err != nil {
		shutdownAll(startedComponents)

		return fmt.Errorf("start batcher: %w", err)
	}

	startedComponents = append(startedComponents, logBatcher)

	if cfg.GRPC.Enable || cfg.HTTP.Enable {
		factoryReceiver := otlpreceiver.NewFactory()
		receiverCfg := factoryReceiver.CreateDefaultConfig()

		receiverTypedCfg, ok := receiverCfg.(*otlpreceiver.Config)
		if !ok {
			return fmt.Errorf("%w for receiver default config: %T", errUnexpectedType, receiverCfg)
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
			receiver.Settings{TelemetrySettings: telemetry},
			receiverTypedCfg,
			logBatcher,
		)
		if err != nil {
			shutdownAll(startedComponents)

			return fmt.Errorf("setup OTLP receiver: %w", err)
		}

		if err = otlpLogReceiver.Start(ctx, nil); err != nil {
			shutdownAll(startedComponents)

			return fmt.Errorf("start OTLP receiver: %w", err)
		}

		startedComponents = append(startedComponents, otlpLogReceiver)
	}

	if len(cfg.LogFiles) > 0 {
		logReceiverFactory, logReceiverCfg, err := chooseLogReceiver(cfg.LogFiles[0], []byte(cfg.OperatorsYAML))
		if err != nil {
			shutdownAll(startedComponents)

			return fmt.Errorf("chosing log receiver: %w", err)
		}

		logReceiver, err := logReceiverFactory.CreateLogs(
			ctx,
			receiver.Settings{TelemetrySettings: telemetry},
			logReceiverCfg,
			logBatcher,
		)
		if err != nil {
			shutdownAll(startedComponents)

			return fmt.Errorf("setup log receiver: %w", err)
		}

		if err = logReceiver.Start(ctx, nil); err != nil {
			shutdownAll(startedComponents)

			return fmt.Errorf("start log receiver: %w", err)
		}

		startedComponents = append(startedComponents, logReceiver)
	}

	// Scheduling the shutdown of all the components we've started
	go func() {
		defer crashreport.ProcessPanic()

		<-ctx.Done()

		shutdownAll(startedComponents)
	}()

	return nil
}

func chooseLogReceiver(file string, operatorsYaml []byte) (factory receiver.Factory, cfg component.Config, err error) {
	canReadFile := true

	f, err := os.OpenFile(file, os.O_RDONLY, 0) // the mode perm isn't needed for read
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, err
		}

		canReadFile = false
	} else {
		err = f.Close()
		if err != nil {
			logger.Printf("Failed to close log file %q: %v", file, err)
		}
	}

	var ops []operator.Config

	if err = yaml.Unmarshal(operatorsYaml, &ops); err != nil {
		return nil, nil, fmt.Errorf("log receiver operators: %w", err)
	}

	if canReadFile {
		factory = filelogreceiver.NewFactory()
		fileCfg := factory.CreateDefaultConfig()

		fileTypedCfg, ok := fileCfg.(*filelogreceiver.FileLogConfig)
		if !ok {
			return nil, nil, fmt.Errorf("%w for file log receiver: %T", errUnexpectedType, fileCfg)
		}

		fileTypedCfg.InputConfig.Include = []string{file}
		fileTypedCfg.Operators = ops

		cfg = fileTypedCfg

		logger.Printf("Chose file log receiver for file %q", file) // TODO: remove
	} else {
		factory = execlogreceiver.NewFactory()
		execCfg := factory.CreateDefaultConfig()

		execTypedCfg, ok := execCfg.(*execlogreceiver.ExecLogConfig)
		if !ok {
			return nil, nil, fmt.Errorf("%w for exec log receiver: %T", errUnexpectedType, execCfg)
		}

		execTypedCfg.InputConfig.Argv = []string{"sudo", "tail", "--follow=name", file}
		execTypedCfg.Operators = ops

		cfg = execTypedCfg

		logger.Printf("Chose exec log receiver for file %q", file) // TODO: remove
	}

	return factory, cfg, nil
}

// shutdownAll stops all the given components (in reverse order).
// It should be called before every unsuccessful return of the log pipeline initialization.
func shutdownAll(components []component.Component) {
	logger.Printf("Shutting down %d log processing components: %s", len(components), formatTypes(components)) // TODO: remove

	// Shutting down first the components that are at the beginning of the log production chain.
	for _, comp := range slices.Backward(components) {
		go func() {
			defer crashreport.ProcessPanic()

			shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()

			err := comp.Shutdown(shutdownCtx)
			if err != nil {
				logger.Printf("Failed to shutdown log processing component %T: %v", comp, err)
			}
		}()
	}
}

// formatTypes acts like a mix of %T and %v, on a slice.
func formatTypes[E any](a []E) string {
	result := "["

	for i, e := range a {
		result += fmt.Sprintf("%T", e)

		if i < len(a)-1 {
			result += " "
		}
	}

	return result + "]"
}
