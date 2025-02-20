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
	"slices"
	"sync"
	"sync/atomic"

	"github.com/bleemeo/glouton/logger"

	stanzaErrors "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/errors"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/helper"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/parser/container"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
)

type ContainerAttributes struct {
	ID    string
	Name  string
	Image string
	Other map[string]string
}

type ContainerLogFiles struct {
	PotentialLogFilePaths []string
	Attributes            ContainerAttributes
}

type containerReceiver struct {
	pipeline    *pipelineContext
	logConsumer consumer.Logs

	l                  sync.Mutex
	startedComponents  []component.Component
	containersLogFiles map[string][]string

	logCounter      *atomic.Int64
	throughputMeter *ringCounter
}

func newContainerReceiver(pipeline *pipelineContext, logConsumer consumer.Logs) *containerReceiver {
	return &containerReceiver{
		pipeline:           pipeline,
		logConsumer:        logConsumer,
		startedComponents:  make([]component.Component, 0),
		containersLogFiles: make(map[string][]string),
		logCounter:         new(atomic.Int64),
		throughputMeter:    newRingCounter(throughputMeterResolutionSecs),
	}
}

func (cr *containerReceiver) handleContainersLogs(ctx context.Context, containerLogFilesBatch []ContainerLogFiles) {
	cr.l.Lock()
	defer cr.l.Unlock()

	for _, ctnrLogFiles := range containerLogFilesBatch {
		logger.Printf("Handling container logs from %s (attributes: %v) ...", ctnrLogFiles.Attributes.Name, ctnrLogFiles.Attributes) // TODO: remove

		err := cr.setupContainerLogReceiver(ctx, ctnrLogFiles)
		if err != nil {
			logger.V(1).Printf("Can't create a log receiver for container %s (%s): %v", ctnrLogFiles.Attributes.Name, ctnrLogFiles.Attributes.ID, err)

			continue
		}
	}
}

func (cr *containerReceiver) setupContainerLogReceiver(ctx context.Context, ctnrLogFiles ContainerLogFiles) error {
	containerOpCfg := container.NewConfig()
	containerOpCfg.OnError = helper.SendOnError

	ops := []operator.Config{{Builder: containerOpCfg}}
	makeStorageFn := func(logFile string) *component.ID {
		id := cr.pipeline.persister.newPersistentExt("container/" + ctnrLogFiles.Attributes.ID + metadataKeySeparator + logFile)

		return &id
	}

	factories, readFiles, execFiles, sizeFnByFile, err := setupLogReceiverFactories(
		ctnrLogFiles.PotentialLogFilePaths,
		cr.pipeline.hostroot,
		ops,
		cr.pipeline.lastFileSizes,
		cr.pipeline.commandRunner,
		makeStorageFn,
	)
	if err != nil {
		return fmt.Errorf("setting up receiver factories: %w", err)
	}

	_ = sizeFnByFile // TODO

	cr.containersLogFiles[ctnrLogFiles.Attributes.ID] = slices.Concat(readFiles, execFiles)

	for logReceiverFactory, logReceiverCfg := range factories {
		logRcvr, err := logReceiverFactory.CreateLogs(
			ctx,
			receiver.Settings{TelemetrySettings: cr.pipeline.telemetry},
			logReceiverCfg,
			wrapWithCounters(cr.logConsumer, cr.logCounter, cr.throughputMeter),
		)
		if err != nil {
			var agentErr stanzaErrors.AgentError
			if errors.As(err, &agentErr) && agentErr.Suggestion != "" {
				return fmt.Errorf("setup receiver: %w (%s)", err, agentErr.Suggestion)
			}

			return fmt.Errorf("setup receiver: %w", err)
		}

		if err = logRcvr.Start(ctx, cr.pipeline.persister); err != nil {
			return fmt.Errorf("start receiver: %w", err)
		}

		cr.startedComponents = append(cr.startedComponents, logRcvr)
	}

	return nil
}

func (cr *containerReceiver) diagnostic() containerReceiverDiagnosticInformation {
	cr.l.Lock()
	defer cr.l.Unlock()

	return containerReceiverDiagnosticInformation{
		LogProcessedCount:      cr.logCounter.Load(),
		LogThroughputPerMinute: cr.throughputMeter.Total(),
		LogFilesByContainers:   cr.containersLogFiles,
	}
}

func (cr *containerReceiver) stop() {
	cr.l.Lock()
	defer cr.l.Unlock()

	shutdownAll(cr.startedComponents)
}
