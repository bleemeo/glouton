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

package poc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
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

func MakePipeline(ctx context.Context, cfg config.POC, pushLogs func(context.Context, []byte) error) error {
	// TODO: no logs & metrics at the same time
	// TODO: probably issue when configuration is reloaded (might be fixed when shutdown is correctly implemented)
	// TODO: buffering / back pressure not implemented (we don't send the ErrBackPressureSignal back to client that send us logs)

	// startedComponents represents all the components that must be shut down at the end of the context's lifecycle.
	startedComponents := make([]component.Component, 0, 4)

	factoryReceiver := otlpreceiver.NewFactory()
	factoryBatch := batchprocessor.NewFactory()

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
		return fmt.Errorf("setup exporter: %w", err)
	}

	if err = logExporter.Start(ctx, nil); err != nil {
		return fmt.Errorf("start exporter: %w", err)
	}

	startedComponents = append(startedComponents, logExporter)

	logBatcher, err := factoryBatch.CreateLogs(
		ctx,
		processor.Settings{TelemetrySettings: telemetry},
		factoryBatch.CreateDefaultConfig(),
		logExporter,
	)
	if err != nil {
		return fmt.Errorf("setup batcher: %w", err)
	}

	if err = logBatcher.Start(ctx, nil); err != nil {
		return fmt.Errorf("start batcher: %w", err)
	}

	startedComponents = append(startedComponents, logBatcher)

	receiverCfg := factoryReceiver.CreateDefaultConfig()

	receiverTypedCfg, ok := receiverCfg.(*otlpreceiver.Config)
	if !ok {
		return fmt.Errorf("%w for receiver default config: %T", errUnexpectedType, receiverCfg)
	}

	receiverTypedCfg.Protocols.GRPC.NetAddr.Endpoint = cfg.GRPCAddress // TODO: config
	// receiverTypedCfg.Protocols.HTTP.Endpoint = "localhost:4318"

	otlpLogReceiver, err := factoryReceiver.CreateLogs(
		ctx,
		receiver.Settings{TelemetrySettings: telemetry},
		receiverTypedCfg,
		logBatcher,
	)
	if err != nil {
		return fmt.Errorf("setup OTLP receiver: %w", err)
	}

	if err = otlpLogReceiver.Start(ctx, nil); err != nil {
		return fmt.Errorf("start OTLP receiver: %w", err)
	}

	startedComponents = append(startedComponents, otlpLogReceiver)

	if len(cfg.LogFiles) > 0 {
		logReceiverFactory, logReceiverCfg, err := chooseLogReceiver(cfg.LogFiles[0], []byte(cfg.OperatorsYAML))
		if err != nil {
			return err
		}

		logReceiver, err := logReceiverFactory.CreateLogs(
			ctx,
			receiver.Settings{TelemetrySettings: telemetry},
			logReceiverCfg,
			logBatcher,
		)
		if err != nil {
			return fmt.Errorf("setup log receiver: %w", err)
		}

		if err = logReceiver.Start(ctx, nil); err != nil {
			return fmt.Errorf("start log receiver: %w", err)
		}

		startedComponents = append(startedComponents, logReceiver)
	}

	go waitThenShutdown(ctx, startedComponents)

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

		cfg = fileTypedCfg
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
	}

	return factory, cfg, nil
}

// waitThenShutdown waits for the given context to expire,
// then stops all the given components (starting by the last one).
// It must be started in a new goroutine.
func waitThenShutdown(ctx context.Context, components []component.Component) {
	defer crashreport.ProcessPanic()

	<-ctx.Done()

	logger.Printf("Shutting down %d log processing components ...", len(components)) // TODO: remove

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
