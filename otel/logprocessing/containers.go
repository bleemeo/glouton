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

	"github.com/bleemeo/glouton/facts"
	crTypes "github.com/bleemeo/glouton/facts/container-runtime/types"
	"github.com/bleemeo/glouton/logger"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/entry"
	stanzaErrors "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/errors"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/helper"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/parser/container"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/parser/regex"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/transformer/add"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
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

var errContainerLogFileUnavailable = errors.New("no log file available")

type Container struct {
	LogFilePath  string
	ReceiverKind receiverKind
	Attributes   ContainerAttributes

	potentialLogFilePath string
	AdditionalOperators  []operator.Config // TODO: unexport
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
	pipeline      *pipelineContext
	logConsumer   consumer.Logs
	lastFileSizes map[string]int64

	l                 sync.Mutex
	startedComponents []component.Component
	containers        map[string]Container
	sizeFnByFile      map[string]func() (int64, error)

	logCounter      *atomic.Int64
	throughputMeter *ringCounter
}

func newContainerReceiver(pipeline *pipelineContext, logConsumer consumer.Logs) *containerReceiver {
	lastFileSizes := make(map[string]int64)

	for filePath, size := range pipeline.lastFileSizes {
		if strings.HasPrefix(filePath, containerFileSizePrefix) {
			lastFileSizes[filePath[len(containerFileSizePrefix):]] = size
		}
	}

	return &containerReceiver{
		pipeline:          pipeline,
		logConsumer:       logConsumer,
		lastFileSizes:     lastFileSizes,
		startedComponents: make([]component.Component, 0),
		containers:        make(map[string]Container),
		sizeFnByFile:      make(map[string]func() (int64, error)),
		logCounter:        new(atomic.Int64),
		throughputMeter:   newRingCounter(throughputMeterResolutionSecs),
	}
}

func (cr *containerReceiver) handleContainersLogs(ctx context.Context, crRuntime crTypes.RuntimeInterface, containers []facts.Container) {
	cr.l.Lock()
	defer cr.l.Unlock()

	for _, ctr := range containers {
		if _, alreadyWatching := cr.containers[ctr.ID()]; alreadyWatching {
			continue
		}

		logCtr, err := makeLogContainer(ctx, crRuntime, ctr)
		if err != nil {
			logger.V(1).Printf("Can't create a log receiver for container %s (%s): %v", ctr.ContainerName(), ctr.ID(), err)

			continue
		}

		// logCtr.AdditionalOperators = makeOperatorsForContainer(logCtr.Attributes)

		logger.Printf("Handling container logs from %s (attributes: %v) ...", logCtr.Attributes.Name, logCtr.Attributes) // TODO: remove

		err = cr.setupContainerLogReceiver(ctx, logCtr)
		if err != nil {
			logger.V(1).Printf("Can't create a log receiver for container %s (%s): %v", logCtr.Attributes.Name, logCtr.Attributes.ID, err)

			continue
		}
	}
}

func (cr *containerReceiver) setupContainerLogReceiver(ctx context.Context, ctr Container) error {
	containerOpCfg := container.NewConfig()
	containerOpCfg.AddMetadataFromFilePath = false

	ops := append([]operator.Config{{Builder: containerOpCfg}}, ctr.AdditionalOperators...)
	makeStorageFn := func(logFile string) *component.ID {
		id := cr.pipeline.persister.newPersistentExt("container/" + ctr.Attributes.ID + metadataKeySeparator + logFile)

		return &id
	}

	factories, readFiles, execFiles, sizeFnByFile, err := setupLogReceiverFactories(
		[]string{ctr.potentialLogFilePath},
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

	switch {
	case len(readFiles) == 1:
		ctr.LogFilePath = readFiles[0]
		ctr.ReceiverKind = receiverFileLog
	case len(execFiles) == 1:
		ctr.LogFilePath = execFiles[0]
		ctr.ReceiverKind = receiverExecLog
	default:
		logger.V(1).Printf("No log file found for container %s (%s)", ctr.Attributes.Name, ctr.Attributes.ID)
	}

	cr.containers[ctr.Attributes.ID] = ctr
	maps.Insert(cr.sizeFnByFile, maps.All(sizeFnByFile))

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

func (cr *containerReceiver) diagnostic() containerReceiverDiagnosticInformation {
	cr.l.Lock()
	defer cr.l.Unlock()

	return containerReceiverDiagnosticInformation{
		LogProcessedCount:      cr.logCounter.Load(),
		LogThroughputPerMinute: cr.throughputMeter.Total(),
		Containers:             cr.containers,
	}
}

func (cr *containerReceiver) stop() {
	cr.l.Lock()
	defer cr.l.Unlock()

	shutdownAll(cr.startedComponents)
}

func makeLogContainer(ctx context.Context, crRuntime crTypes.RuntimeInterface, container facts.Container) (Container, error) {
	logFilePath := container.LogPath()
	if logFilePath == "" {
		return Container{}, errContainerLogFileUnavailable
	}

	imageTags, err := crRuntime.ImageTags(ctx, container.ImageID(), container.ImageName())
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
		potentialLogFilePath: logFilePath,
		Attributes:           attributes,
	}, nil
}

// NOTE: all the content in this function is temporary.
func makeOperatorsForContainer(attributes ContainerAttributes) []operator.Config {
	opService := add.NewConfigWithID("op_add_service")
	opService.Field = entry.NewResourceField("service.name")
	opService.Value = "container:" + attributes.Runtime

	ops := []operator.Config{{Builder: opService}}

	// FIXME: timestamps from containers are in UTC, but the timezone isn't printed ...
	// so they are considered as from the local TZ, then dropped by the consumer
	// since they are older than 1h :d
	// Postgres is one of the few that display the timezone in the timestamp.

	switch attributes.ImageName {
	case "squirreldb":
		attributeTimeField := entry.NewAttributeField("time")

		timeParser := helper.NewTimeParser()
		timeParser.ParseFrom = &attributeTimeField
		timeParser.Layout = "2006-01-02 15:04:05.000"
		timeParser.LayoutType = helper.GotimeKey

		opTime := regex.NewConfigWithID("op_regex_squirreldb")
		opTime.Regex = `^.*(?P<time>\d{4}-\d{2}-\d{2}\s\d{2}:\d{2}:\d{2}\.\d{3})`
		opTime.TimeParser = &timeParser

		ops = append(ops, operator.Config{Builder: opTime})
	case "postgres":
		attributeTimeField := entry.NewAttributeField("time")

		timeParser := helper.NewTimeParser()
		timeParser.ParseFrom = &attributeTimeField
		timeParser.Layout = "2006-01-02 15:04:05.000 MST"
		timeParser.LayoutType = helper.GotimeKey

		opTime := regex.NewConfigWithID("op_regex_postgres")
		opTime.Regex = `^(?P<time>\d{4}-\d{2}-\d{2}\s\d{2}:\d{2}:\d{2}\.\d{3}\s\w+)`
		opTime.TimeParser = &timeParser
		opTime.OnError = helper.DropOnError // TODO: remove (this avoids flooding the db)

		ops = append(ops, operator.Config{Builder: opTime})
	}

	return ops
}
