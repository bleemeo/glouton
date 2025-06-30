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
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/logger"

	"github.com/google/uuid"
	stanzaErrors "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/errors"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/helper"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/parser/container"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/filterprocessor"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/receiver"
)

const (
	attrContainerID        = "container.id"
	attrContainerImageName = "container.image.name"
	attrContainerImageTags = "container.image.tags"
	attrContainerName      = "container.name"
	attrContainerRuntime   = "container.runtime"

	attrContainerNamespace = "k8s.namespace.name"
	attrContainerPod       = "k8s.pod.name"
)

const containerFileSizePrefix = "container://"

var (
	errContainerLogFileUnavailable = errors.New("no log file available")
	errNoLogFound                  = errors.New("no log file found")
	errWrongNumberOfLogs           = errors.New("container should have a single log file")
)

type Container struct {
	LogFilePath  string
	ReceiverKind receiverKind
	Attributes   ContainerAttributes

	logCounter      *atomic.Int64
	throughputMeter *ringCounter
}

type ContainerAttributes struct {
	Runtime   string
	ID        string
	Name      string
	ImageName string
	ImageTags string
	Namespace string `json:",omitempty"`
	Pod       string `json:",omitempty"`
}

func (ctrAttrs ContainerAttributes) asMap() map[string]helper.ExprStringConfig {
	attrs := map[string]helper.ExprStringConfig{
		attrContainerID:        helper.ExprStringConfig(ctrAttrs.ID),
		attrContainerImageName: helper.ExprStringConfig(ctrAttrs.ImageName),
		attrContainerImageTags: helper.ExprStringConfig(ctrAttrs.ImageTags),
		attrContainerName:      helper.ExprStringConfig(ctrAttrs.Name),
		attrContainerRuntime:   helper.ExprStringConfig(ctrAttrs.Runtime),
	}

	if ctrAttrs.Namespace != "" {
		attrs[attrContainerNamespace] = helper.ExprStringConfig(ctrAttrs.Namespace)
	}

	if ctrAttrs.Pod != "" {
		attrs[attrContainerPod] = helper.ExprStringConfig(ctrAttrs.Pod)
	}

	return attrs
}

type containerReceiver struct {
	pipeline           *pipelineContext
	logConsumer        consumer.Logs
	lastFileSizes      map[string]int64  // map key: log file path
	containerOperators map[string]string // map key: container name
	containerFilters   map[string]string // map key: container name

	l                 sync.Mutex
	startedComponents map[string][]component.Component // map key: container ID
	containers        map[string]Container             // map key: container ID
	sizeFnByFile      map[string]func() (int64, error) // map key: log file path
}

func newContainerReceiver(
	pipeline *pipelineContext,
	containerOperators map[string]string,
	knownOperators map[string][]config.OTELOperator,
	containerFilter map[string]string,
	knownFilters map[string]config.OTELFilters,
) *containerReceiver {
	lastFileSizes := make(map[string]int64)

	for filePath, size := range pipeline.lastFileSizes {
		if strings.HasPrefix(filePath, containerFileSizePrefix) {
			lastFileSizes[filePath[len(containerFileSizePrefix):]] = size
		}
	}

	return &containerReceiver{
		pipeline:           pipeline,
		logConsumer:        pipeline.getInput(),
		lastFileSizes:      lastFileSizes,
		containerOperators: validateContainerOperators(containerOperators, knownOperators),
		containerFilters:   validateContainerFilters(containerFilter, knownFilters),
		startedComponents:  make(map[string][]component.Component),
		containers:         make(map[string]Container),
		sizeFnByFile:       make(map[string]func() (int64, error)),
	}
}

func (cr *containerReceiver) handleContainerLogs(
	ctx context.Context,
	ctr facts.Container,
	operators []operator.Config,
	filters config.OTELFilters,
) (string, error) {
	cr.l.Lock()
	defer cr.l.Unlock()

	logFilterConfig, warn, err := buildLogFilterConfig(filters)
	if err != nil {
		return "", err
	}

	if warn != nil {
		logWarnings(errorf("Containers log processing warning: %w", warn))
	}

	logFilePath := ctr.LogPath()
	if logFilePath == "" {
		return "", errContainerLogFileUnavailable
	}

	logCtr, err := makeLogContainer(ctx, ctr, logFilePath)
	if err != nil {
		return logFilePath, err
	}

	err = cr.setupContainerLogReceiver(ctx, logCtr, operators, logFilterConfig)
	if err != nil {
		return logFilePath, fmt.Errorf("setting up log receiver: %w", err)
	}

	return logFilePath, nil
}

func (cr *containerReceiver) setupContainerLogReceiver(ctx context.Context, ctr Container, operators []operator.Config, filtersCfg *filterprocessor.Config) error {
	containerOpCfg := container.NewConfig()
	containerOpCfg.AddMetadataFromFilePath = false

	ops := append([]operator.Config{{Builder: containerOpCfg}}, operators...)
	makeStorageFn := func(logFile string) *component.ID {
		id := cr.pipeline.persister.newPersistentExt("container/" + ctr.Attributes.ID + metadataKeySeparator + logFile)

		return &id
	}

	factories, readFiles, execFiles, sizeFnByFile, err := setupLogReceiverFactories(
		[]string{ctr.LogFilePath},
		cr.pipeline.hostroot,
		ops,
		cr.lastFileSizes,
		cr.pipeline.commandRunner,
		makeStorageFn,
		ctr.Attributes.asMap(),
	)
	if err != nil {
		return fmt.Errorf("setting up receiver factories: %w", err)
	}

	if len(factories) != 1 {
		return fmt.Errorf("%w: is had %d logs", errWrongNumberOfLogs, len(factories))
	}

	switch {
	case len(readFiles) == 1:
		ctr.ReceiverKind = receiverFileLog
	case len(execFiles) == 1:
		ctr.ReceiverKind = receiverExecLog
	default:
		return errNoLogFound
	}

	factoryFilter := filterprocessor.NewFactory()

	logFilter, err := factoryFilter.CreateLogs(
		ctx,
		processor.Settings{
			ID:                component.NewIDWithName(factoryFilter.Type(), "log-filter-ctnr-"+ctr.Attributes.ID),
			TelemetrySettings: withoutDebugLogs(cr.pipeline.telemetry),
		},
		filtersCfg,
		wrapWithInstrumentation(cr.logConsumer, ctr.logCounter, ctr.throughputMeter),
	)
	if err != nil {
		return fmt.Errorf("setup log filter: %w", err)
	}

	if err = logFilter.Start(ctx, nil); err != nil {
		return fmt.Errorf("start log filter: %w", err)
	}

	cr.startedComponents[ctr.Attributes.ID] = append(cr.startedComponents[ctr.Attributes.ID], logFilter)
	cr.containers[ctr.Attributes.ID] = ctr
	maps.Insert(cr.sizeFnByFile, maps.All(sizeFnByFile))

	for logReceiverFactory, logReceiverCfg := range factories {
		settings := receiver.Settings{
			ID:                component.NewIDWithName(logReceiverFactory.Type(), uuid.NewString()),
			TelemetrySettings: cr.pipeline.telemetry,
		}

		logRcvr, err := logReceiverFactory.CreateLogs(ctx, settings, logReceiverCfg, logFilter)
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

		cr.startedComponents[ctr.Attributes.ID] = append(cr.startedComponents[ctr.Attributes.ID], logRcvr)
	}

	return nil
}

func (cr *containerReceiver) sizesByFile() (map[string]int64, error) {
	cr.l.Lock()
	defer cr.l.Unlock()

	sizes := make(map[string]int64, len(cr.sizeFnByFile))

	for logFile, sizeFn := range cr.sizeFnByFile {
		size, err := sizeFn()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// We may not catch errors produced by the "sudo stat" cmd,
				// but this would not really be convenient ...
				continue
			}

			return nil, err
		}

		sizes[containerFileSizePrefix+logFile] = size
	}

	return sizes, nil
}

func (cr *containerReceiver) stopWatchingForContainers(ctx context.Context, ids []string) {
	cr.l.Lock()
	defer cr.l.Unlock()

	shutdownCtx, cancel := context.WithTimeout(ctx, shutdownTimeout)
	defer cancel()

	for _, ctrID := range ids {
		recvComponents, ok := cr.startedComponents[ctrID]
		if !ok {
			logger.V(1).Printf("Can't stop log receiver for container %s: it doesn't have one ...", ctrID)

			continue
		}

		for _, comp := range recvComponents {
			err := comp.Shutdown(shutdownCtx)
			if err != nil {
				logger.V(1).Printf("Failed to stop log receiver component for container %s: %v", ctrID, err)
			}
		}

		logFilePath := cr.containers[ctrID].LogFilePath

		delete(cr.startedComponents, ctrID)
		delete(cr.containers, ctrID)
		delete(cr.sizeFnByFile, logFilePath)
	}
}

func (cr *containerReceiver) diagnostic() map[string]containerDiagnosticInformation {
	cr.l.Lock()
	defer cr.l.Unlock()

	infos := make(map[string]containerDiagnosticInformation, len(cr.containers))

	for ctrID, ctr := range cr.containers {
		infos[ctrID] = containerDiagnosticInformation{
			LogProcessedCount:      ctr.logCounter.Load(),
			LogThroughputPerMinute: ctr.throughputMeter.Total(),
			LogFilePath:            ctr.LogFilePath,
			ReceiverKind:           ctr.ReceiverKind,
			Attributes:             ctr.Attributes,
		}
	}

	return infos
}

func (cr *containerReceiver) stop() {
	cr.l.Lock()
	defer cr.l.Unlock()

	wg := new(sync.WaitGroup)
	wg.Add(len(cr.startedComponents))

	// Stopping all the container receivers in parallel,
	// since they don't depend on each other.
	for _, components := range cr.startedComponents {
		go func() {
			defer crashreport.ProcessPanic()
			defer wg.Done()

			shutdownAll(components)
		}()
	}

	wg.Wait()
}

func makeLogContainer(ctx context.Context, container facts.Container, logFilePath string) (Container, error) {
	imageTags, err := container.ImageTags(ctx)
	if err != nil {
		return Container{}, fmt.Errorf("can't get tags for image %q (%s): %w", container.ImageName(), container.ImageID(), err)
	}

	imageTagsJSON, err := json.Marshal(imageTags)
	if err != nil {
		return Container{}, fmt.Errorf("can't marshal tags for image %q (%s): %w", container.ImageName(), container.ImageID(), err)
	}

	attributes := ContainerAttributes{
		Runtime:   container.RuntimeName(),
		ID:        container.ID(),
		Name:      container.ContainerName(),
		ImageName: strings.SplitN(container.ImageName(), ":", 2)[0],
		ImageTags: string(imageTagsJSON),
	}

	namespace := container.PodNamespace()
	pod := container.PodName()

	if namespace != "" && pod != "" {
		attributes.Namespace = namespace
		attributes.Pod = pod
	}

	return Container{
		LogFilePath:     logFilePath,
		Attributes:      attributes,
		logCounter:      new(atomic.Int64),
		throughputMeter: newRingCounter(throughputMeterResolutionSecs),
	}, nil
}
