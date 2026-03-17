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

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/filterprocessor"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/journaldreceiver"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configgrpc"
	"go.opentelemetry.io/collector/config/confighttp"
	"go.opentelemetry.io/collector/config/confignet"
	"go.opentelemetry.io/collector/config/configoptional"
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

var errUnexpectedConfig = errors.New("unexpected config type")

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

	journalctlCounter         *atomic.Int64
	journalctlThroughputMeter *ringCounter

	logProcessedCount  atomic.Int64
	logThroughputMeter *ringCounter
}

type pipelineOptions struct {
	batcherTimeout           time.Duration
	logsAvailabilityCacheTTL time.Duration
}

func makePipeline(
	ctx context.Context,
	cfg config.OpenTelemetry,
	hostroot string,
	commandRunner CommandRunner,
	facter Facter,
	pushLogs func(context.Context, []byte) error,
	streamAvailabilityStatusFn func() bleemeoTypes.LogsAvailability,
	persister *persistHost,
	addWarnings func(...error),
	knownLogFormats map[string][]config.OTELOperator,
	lastFileSizes map[string]int64,
	opts pipelineOptions,
) (
	*pipelineContext,
	error,
) {
	// Avoid using the return parameter 'pipelineCtx' because it will be set to <nil> when returning an error,
	// and we won't be able to access its fields anymore, e.g., startedComponents in deferred function.
	pipeline := &pipelineContext{
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

	err := pipeline.init(ctx, cfg, facter, pushLogs, opts, streamAvailabilityStatusFn, addWarnings, knownLogFormats)
	if err != nil {
		pipeline.shutdownAll()

		return nil, err
	}

	return pipeline, nil
}

// init setups and start the pipeline components.
func (p *pipelineContext) init(
	ctx context.Context,
	cfg config.OpenTelemetry,
	facter Facter,
	pushLogs func(context.Context, []byte) error,
	opts pipelineOptions,
	streamAvailabilityStatusFn func() bleemeoTypes.LogsAvailability,
	addWarnings func(...error),
	knownLogFormats map[string][]config.OTELOperator,
) error {
	p.l.Lock()
	defer p.l.Unlock()

	chunker := logChunker{
		maxChunkSize: types.MaxMQTTPayloadSize,
		pushLogsFn:   pushLogs,
	}

	logExporter, err := exporterhelper.NewLogs(
		ctx,
		exporter.Settings{TelemetrySettings: p.telemetry},
		"unused",
		func(ctx context.Context, ld plog.Logs) error {
			if err := chunker.push(ctx, ld); err != nil {
				logger.V(1).Printf("Failed to push logs: %v", err)
				// returning error goes nowhere (not visible anywhere), that's why we log it here
				return err
			}

			count := ld.LogRecordCount()
			p.logProcessedCount.Add(int64(count))
			p.logThroughputMeter.Add(count)

			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("setup exporter: %w", err)
	}

	if err = logExporter.Start(ctx, nil); err != nil {
		return fmt.Errorf("start exporter: %w", err)
	}

	p.startedComponents = append(p.startedComponents, logExporter)

	factoryBatch := batchprocessor.NewFactory()

	logBatcher, err := factoryBatch.CreateLogs(
		ctx,
		processor.Settings{
			ID:                component.NewIDWithName(factoryBatch.Type(), "log-batcher"),
			TelemetrySettings: p.telemetry,
		},
		&batchprocessor.Config{
			Timeout:                  opts.batcherTimeout,
			SendBatchSize:            8192, // config default
			MetadataCardinalityLimit: 1000, // config default
		},
		logExporter,
	)
	if err != nil {
		return fmt.Errorf("setup batcher: %w", err)
	}

	if err = logBatcher.Start(ctx, nil); err != nil {
		return fmt.Errorf("start batcher: %w", err)
	}

	p.startedComponents = append(p.startedComponents, logBatcher)

	logBackPressureEnforcer, err := processorhelper.NewLogs(
		ctx,
		processor.Settings{TelemetrySettings: p.telemetry},
		nil,
		logBatcher,
		makeEnforceBackPressureFn(streamAvailabilityStatusFn, opts.logsAvailabilityCacheTTL),
	)
	if err != nil {
		return fmt.Errorf("setup log back-pressure enforcer: %w", err)
	}

	p.startedComponents = append(p.startedComponents, logBackPressureEnforcer)

	factoryFilter := filterprocessor.NewFactory()

	logFilterConfig, warn, err := buildLogFilterConfig(cfg.GlobalFilters)
	if err != nil {
		return fmt.Errorf("build log filter config: %w", err)
	}

	if warn != nil {
		addWarnings(errorf("Global log filters warning: %w", warn))
	}

	logFilter, err := factoryFilter.CreateLogs(
		ctx,
		processor.Settings{
			ID:                component.NewIDWithName(factoryFilter.Type(), "log-filter"),
			TelemetrySettings: withoutDebugLogs(p.telemetry),
		},
		logFilterConfig,
		logBackPressureEnforcer,
	)
	if err != nil {
		return fmt.Errorf("setup log filter: %w", err)
	}

	if err = logFilter.Start(ctx, nil); err != nil {
		return fmt.Errorf("start log filter: %w", err)
	}

	p.startedComponents = append(p.startedComponents, logFilter)

	logResourceAttribute, err := processorhelper.NewLogs(
		ctx,
		processor.Settings{TelemetrySettings: withoutDebugLogs(p.telemetry)},
		nil,
		logFilter,
		makeResourceAttributeFn(facter),
	)
	if err != nil {
		return fmt.Errorf("setup log filter: %w", err)
	}

	if err = logResourceAttribute.Start(ctx, nil); err != nil {
		return fmt.Errorf("start log resource attribute: %w", err)
	}

	p.startedComponents = append(p.startedComponents, logResourceAttribute)

	p.inputConsumer = logResourceAttribute

	if cfg.GRPC.Enable || cfg.HTTP.Enable {
		if err := p.setupNetworkReceiver(ctx, cfg); err != nil {
			logger.V(1).Printf("Unable to configure GRPC/HTTP receiver: %v", err)
		}
	}

	if cfg.AutoDiscovery.JournalctlEnable {
		if err := p.setupJournalctl(ctx, knownLogFormats); err != nil {
			logger.V(1).Printf("Unable to configure Journalctl receiver: %v", err)
		}
	}

	if cfg.AutoDiscovery.SyslogEnable {
		if err := p.setupSyslog(ctx, knownLogFormats); err != nil {
			logger.V(1).Printf("Unable to configure Journalctl receiver: %v", err)
		}
	}

	if cfg.AutoDiscovery.AuditdEnable {
		if err := p.setupAuditD(ctx, knownLogFormats); err != nil {
			logger.V(1).Printf("Unable to configure Journalctl receiver: %v", err)
		}
	}

	p.setupConfigReceivers(ctx, cfg, addWarnings, knownLogFormats)

	go func() {
		defer crashreport.ProcessPanic()

		ticker := time.NewTicker(receiversUpdatePeriod)
		defer ticker.Stop()

		for ctx.Err() == nil {
			select {
			case <-ticker.C:
				p.l.Lock()

				for i, rcvr := range p.receivers {
					// Here we use logWarnings instead of addWarnings,
					// because that would make the description of
					// agent_config_warning grow indefinitely.
					err := rcvr.update(ctx, p, logWarnings)
					if err != nil {
						logger.V(1).Printf("Failed to update log receiver n°%d: %v", i+1, err)
					}
				}

				p.l.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}

func (p *pipelineContext) setupNetworkReceiver(
	ctx context.Context,
	cfg config.OpenTelemetry,
) error {
	factoryReceiver := otlpreceiver.NewFactory()
	receiverCfg := factoryReceiver.CreateDefaultConfig()

	receiverTypedCfg, ok := receiverCfg.(*otlpreceiver.Config)
	if !ok {
		return fmt.Errorf("%w for receiver default config: %T", errUnexpectedConfig, receiverCfg)
	}

	if cfg.GRPC.Enable {
		receiverTypedCfg.Protocols.GRPC = configoptional.Some(configgrpc.ServerConfig{
			NetAddr: confignet.AddrConfig{Endpoint: net.JoinHostPort(cfg.GRPC.Address, strconv.Itoa(cfg.GRPC.Port))},
		})
	} else {
		receiverTypedCfg.Protocols.GRPC = configoptional.None[configgrpc.ServerConfig]()
	}

	if cfg.HTTP.Enable {
		netaddr := confignet.NewDefaultAddrConfig()
		netaddr.Endpoint = net.JoinHostPort(cfg.GRPC.Address, strconv.Itoa(cfg.GRPC.Port))
		netaddr.Transport = "ip"

		receiverTypedCfg.Protocols.HTTP = configoptional.Some(otlpreceiver.HTTPConfig{
			ServerConfig: confighttp.ServerConfig{
				NetAddr: netaddr,
			},
		})
	} else {
		receiverTypedCfg.Protocols.HTTP = configoptional.None[otlpreceiver.HTTPConfig]()
	}

	p.otlpRecvCounter = new(atomic.Int64)
	p.otlpRecvThroughputMeter = newRingCounter(throughputMeterResolutionSecs)

	otlpLogReceiver, err := factoryReceiver.CreateLogs(
		ctx,
		receiver.Settings{
			ID:                component.NewIDWithName(factoryReceiver.Type(), "otlp-receiver"),
			TelemetrySettings: p.telemetry,
		},
		receiverTypedCfg,
		wrapWithInstrumentation(p.inputConsumer, p.otlpRecvCounter, p.otlpRecvThroughputMeter),
	)
	if err != nil {
		return fmt.Errorf("failed to setup OTLP receiver: %w", err)
	}

	if err = otlpLogReceiver.Start(ctx, nil); err != nil {
		return fmt.Errorf("failed to start OTLP receiver: %w", err)
	}

	p.startedComponents = append(p.startedComponents, otlpLogReceiver)

	return nil
}

func (p *pipelineContext) setupJournalctl(
	ctx context.Context,
	knownLogFormats map[string][]config.OTELOperator,
) error {
	factoryReceiver := journaldreceiver.NewFactory()
	receiverCfg := factoryReceiver.CreateDefaultConfig()

	receiverTypedCfg, ok := receiverCfg.(*journaldreceiver.JournaldConfig)
	if !ok {
		return fmt.Errorf("%w for receiver default config: %T", errUnexpectedConfig, receiverCfg)
	}

	if p.hostroot != "/" {
		receiverTypedCfg.InputConfig.RootPath = p.hostroot
	}

	receiverTypedCfg.InputConfig.JournalctlPath = "/usr/bin/journalctl"

	opsGroup, found := knownLogFormats["journalctl"]
	if !found {
		return fmt.Errorf("%w missing log format %q", errUnexpectedConfig, "journalctl")
	}

	// Operators from known log formats have already been expanded.
	referencedOps, err := buildOperators(append(operatorsForServiceName("journalctl"), opsGroup...))
	if err != nil {
		return fmt.Errorf("building globally-defined operators: %w", err)
	}

	receiverTypedCfg.Operators = referencedOps

	p.journalctlCounter = new(atomic.Int64)
	p.journalctlThroughputMeter = newRingCounter(throughputMeterResolutionSecs)

	journaldReceiver, err := factoryReceiver.CreateLogs(
		ctx,
		receiver.Settings{
			ID:                component.NewIDWithName(factoryReceiver.Type(), "journald-receiver"),
			TelemetrySettings: p.telemetry,
		},
		receiverTypedCfg,
		wrapWithInstrumentation(p.inputConsumer, p.journalctlCounter, p.journalctlThroughputMeter),
	)
	if err != nil {
		return fmt.Errorf("failed to setup journalctl receiver: %w", err)
	}

	if err = journaldReceiver.Start(ctx, nil); err != nil {
		return fmt.Errorf("failed to start journalctl receiver: %w", err)
	}

	p.startedComponents = append(p.startedComponents, journaldReceiver)

	return nil
}

func (p *pipelineContext) setupSyslog(
	ctx context.Context,
	knownLogFormats map[string][]config.OTELOperator,
) error {
	opsGroup, found := knownLogFormats["syslog"]
	if !found {
		return fmt.Errorf("%w missing log format %q", errUnexpectedConfig, "syslog")
	}

	opsGroup2, found := knownLogFormats["syslogAuth"]
	if !found {
		return fmt.Errorf("%w missing log format %q", errUnexpectedConfig, "syslogAuth")
	}

	recvConfig := config.OTLPReceiver{
		Include:   []string{"/var/log/syslog"},
		Operators: append(operatorsForServiceName("syslog"), opsGroup...),
	}

	recvConfig2 := config.OTLPReceiver{
		Include:   []string{"/var/log/auth.log"},
		Operators: append(operatorsForServiceName("syslog"), opsGroup2...),
	}

	recv, warn, err := newLogReceiver("syslog", recvConfig, true, p.getInput(), nil, statFileImpl)
	if err != nil {
		return fmt.Errorf("failed to start Syslog receiver: %w", err)
	}

	if warn != nil {
		logWarnings(errorf("A warning occurred while setting up log receiver for syslog: %w", warn))
	}

	recv2, warn, err := newLogReceiver("syslog-auth", recvConfig2, true, p.getInput(), nil, statFileImpl)
	if err != nil {
		return fmt.Errorf("failed to start Syslog receiver: %w", err)
	}

	if warn != nil {
		logWarnings(errorf("A warning occurred while setting up log receiver for syslog: %w", warn))
	}

	err = recv.update(ctx, p, logWarnings)
	if err != nil {
		return err
	}

	p.receivers = append(p.receivers, recv)

	err = recv2.update(ctx, p, logWarnings)
	if err != nil {
		return err
	}

	p.receivers = append(p.receivers, recv2)

	return nil
}

func (p *pipelineContext) setupAuditD(
	ctx context.Context,
	knownLogFormats map[string][]config.OTELOperator,
) error {
	opsGroup, found := knownLogFormats["auditd"]
	if !found {
		logger.V(1).Printf("auditd receiver requires the log format %q, which is not defined", "auditd")
	}

	recvConfig := config.OTLPReceiver{
		Include:   []string{"/var/log/audit/audit.log"},
		Operators: append(operatorsForServiceName("auditd"), opsGroup...),
	}

	recv, warn, err := newLogReceiver("auditd", recvConfig, true, p.getInput(), nil, statFileImpl)
	if err != nil {
		return fmt.Errorf("failed to start AuditD receiver: %w", err)
	}

	if warn != nil {
		logWarnings(errorf("A warning occurred while setting up log receiver for auditd: %w", warn))
	}

	err = recv.update(ctx, p, logWarnings)
	if err != nil {
		return err
	}

	p.receivers = append(p.receivers, recv)

	return nil
}

func (p *pipelineContext) setupConfigReceivers(
	ctx context.Context,
	cfg config.OpenTelemetry,
	addWarnings func(...error),
	knownLogFormats map[string][]config.OTELOperator,
) {
	for name, rcvrCfg := range cfg.Receivers {
		recv, warn, err := newLogReceiver(name, rcvrCfg, false, p.inputConsumer, knownLogFormats, statFileImpl)
		if err != nil {
			addWarnings(errorf("Failed to setup log receiver %q (ignoring it): %w", name, err))

			continue
		}

		if warn != nil {
			addWarnings(errorf("Warning while setting up log receiver %q: %w", name, warn))
		}

		err = recv.update(ctx, p, addWarnings)
		if err != nil {
			addWarnings(errorf("Failed to start log receiver %q (ignoring it): %w", name, err))

			continue
		}

		p.receivers = append(p.receivers, recv)
	}

	if len(p.receivers) == 0 && len(cfg.Receivers) > 0 {
		logger.V(1).Printf("None of the %d configured log receiver(s) are valid.", len(cfg.Receivers))
	}
}

// shutdownAll shutdown all started components.
func (p *pipelineContext) shutdownAll() {
	stopComponents(p.startedComponents)
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

func makeResourceAttributeFn(
	facter Facter,
) processorhelper.ProcessLogsFunc {
	return func(ctx context.Context, logs plog.Logs) (plog.Logs, error) {
		facts, err := facter.Facts(ctx, 24*time.Hour)
		if err != nil {
			return logs, err
		}

		if facts["hostname"] == "" {
			return logs, nil
		}

		for _, r := range logs.ResourceLogs().All() {
			attr := r.Resource().Attributes()

			if _, ok := attr.Get("host.name"); !ok {
				r.Resource().Attributes().PutStr("host.name", facts["hostname"])
			}
		}

		return logs, nil
	}
}
