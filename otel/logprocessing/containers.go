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

package logprocessing

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/logsource"
	"github.com/bleemeo/glouton/utils/hostrootsymlink"

	"github.com/google/uuid"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	stanzaErrors "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/stanzaerrors"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/filterprocessor"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/receiver"
)

const containerFileSizePrefix = "container://"

var (
	errContainerLogFileUnavailable = errors.New("no log file available")
	errNoLogFound                  = errors.New("no log file found")
	errWrongNumberOfLogs           = errors.New("container should have a single log file")
)

// Container represents a container whose logs are tailed directly by this
// package, via Glouton's built-in per-service-type log format detection.
type Container struct {
	LogFilePath string
	// RealLogFilePath is LogFilePath with hostroot symlinks resolved, i.e. the path actually tailed and
	// the key this container's sizeFnByFile entries live under. Stored rather than re-resolved on demand:
	// on Kubernetes/containerd the two differ (/var/log/containers/X -> /var/log/pods/X), and once the
	// container is gone its symlink usually is too, so resolving at teardown time would no longer produce
	// the key that was inserted -- leaving a dead entry to be re-stat'd by every SaveState, forever.
	RealLogFilePath string
	ReceiverKind    logsource.ReceiverKind
	Attributes      logsource.ContainerAttributes

	logCounter      *atomic.Int64
	throughputMeter *logsource.RingCounter
}

// containerReceiver's tail lifecycle bookkeeping (startedComponents/registeredExtensions/sizeFnByFile)
// independently parallels otel/logsource's managedSource (receiver_manager.go) and this package's own
// logReceiver (receiver.go) -- each tracks a differently-shaped fan-out chain, so they haven't been
// unified, but a fix to one's tail-start/stop or offset-forget logic likely applies to the others too.
type containerReceiver struct {
	pipeline      *pipelineContext
	logConsumer   consumer.Logs
	lastFileSizes map[string]int64 // map key: log file path

	l                    sync.Mutex
	startedComponents    map[string][]component.Component // map key: container ID
	registeredExtensions map[string][]component.ID        // map key: container ID
	containers           map[string]Container             // map key: container ID
	sizeFnByFile         map[string]func() (int64, error) // map key: log file path
}

func newContainerReceiver(pipeline *pipelineContext) *containerReceiver {
	lastFileSizes := make(map[string]int64)

	for filePath, size := range pipeline.lastFileSizes {
		if strings.HasPrefix(filePath, containerFileSizePrefix) {
			lastFileSizes[filePath[len(containerFileSizePrefix):]] = size
		}
	}

	return &containerReceiver{
		pipeline:             pipeline,
		logConsumer:          pipeline.getInput(),
		lastFileSizes:        lastFileSizes,
		startedComponents:    make(map[string][]component.Component),
		registeredExtensions: make(map[string][]component.ID),
		containers:           make(map[string]Container),
		sizeFnByFile:         make(map[string]func() (int64, error)),
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

	logCtr := makeLogContainer(ctx, ctr, logFilePath)

	err = cr.setupContainerLogReceiver(ctx, logCtr, operators, logFilterConfig)
	if err != nil {
		return logFilePath, fmt.Errorf("setting up log receiver: %w", err)
	}

	return logFilePath, nil
}

func (cr *containerReceiver) setupContainerLogReceiver(ctx context.Context, ctr Container, operators []operator.Config, filtersCfg *filterprocessor.Config) error {
	ops := append([]operator.Config{logsource.BuildContainerEnvelopeOperator()}, operators...)

	// Accumulated locally (not written straight into cr.registeredExtensions) so any error below can roll
	// these back with RemovePersistentExts instead of leaking them under a container whose setup never
	// actually completed.
	var newExtIDs []component.ID

	makeStorageFn := func(logFile string) *component.ID {
		id := cr.pipeline.persister.NewPersistentExt("container/" + ctr.Attributes.ID + metadataKeySeparator + logFile)

		newExtIDs = append(newExtIDs, id)

		return &id
	}

	realLogFile := ctr.LogFilePath
	// Resolve symlinks relative to hostroot (Kubernetes/containerd's /var/log/containers/XXX -> /var/log/pods/XXX).
	if cr.pipeline.hostroot != "/" {
		realLogFile = hostrootsymlink.EvalSymlinks(cr.pipeline.hostroot, realLogFile)
	}

	// Recorded on the Container stored below so teardown can purge sizeFnByFile by the very key inserted
	// from it here, instead of re-resolving a symlink that's gone along with the container.
	ctr.RealLogFilePath = realLogFile

	factories, readFiles, execFiles, sizeFnByFile, err := logsource.SetupLogReceiverFactories(
		[]string{realLogFile},
		cr.pipeline.hostroot,
		ops,
		cr.lastFileSizes,
		cr.pipeline.commandRunner,
		makeStorageFn,
		logsource.StatFile,
		ctr.Attributes.AsMap(),
		nil, // containers have no named receiver entry to paste a raw config into
	)
	if err != nil {
		cr.pipeline.persister.RemovePersistentExts(newExtIDs)

		return fmt.Errorf("setting up receiver factories: %w", err)
	}

	if len(factories) != 1 {
		cr.pipeline.persister.RemovePersistentExts(newExtIDs)

		return fmt.Errorf("%w: is had %d logs", errWrongNumberOfLogs, len(factories))
	}

	switch {
	case len(readFiles) == 1:
		ctr.ReceiverKind = logsource.ReceiverFileLog
	case len(execFiles) == 1:
		ctr.ReceiverKind = logsource.ReceiverExecLog
	default:
		cr.pipeline.persister.RemovePersistentExts(newExtIDs)

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
		logsource.WrapWithInstrumentation(cr.logConsumer, ctr.logCounter, ctr.throughputMeter),
	)
	if err != nil {
		cr.pipeline.persister.RemovePersistentExts(newExtIDs)

		return fmt.Errorf("setup log filter: %w", err)
	}

	if err = logFilter.Start(ctx, nil); err != nil {
		cr.pipeline.persister.RemovePersistentExts(newExtIDs)

		return fmt.Errorf("start log filter: %w", err)
	}

	startedComponents := []component.Component{logFilter}

	for logReceiverFactory, logReceiverCfg := range factories {
		settings := receiver.Settings{
			ID:                component.NewIDWithName(logReceiverFactory.Type(), uuid.NewString()),
			TelemetrySettings: cr.pipeline.telemetry,
		}

		logRcvr, err := logReceiverFactory.CreateLogs(ctx, settings, logReceiverCfg, logFilter)
		if err != nil {
			stopComponents(startedComponents)
			cr.pipeline.persister.RemovePersistentExts(newExtIDs)

			var agentErr stanzaErrors.AgentError
			if errors.As(err, &agentErr) && agentErr.Suggestion != "" {
				return fmt.Errorf("setup receiver: %w (%s)", err, agentErr.Suggestion)
			}

			return fmt.Errorf("setup receiver: %w", err)
		}

		if err = logRcvr.Start(ctx, cr.pipeline.persister); err != nil {
			stopComponents(startedComponents)
			cr.pipeline.persister.RemovePersistentExts(newExtIDs)

			return fmt.Errorf("start receiver: %w", err)
		}

		startedComponents = append(startedComponents, logRcvr)
	}

	cr.startedComponents[ctr.Attributes.ID] = append(cr.startedComponents[ctr.Attributes.ID], startedComponents...)
	cr.registeredExtensions[ctr.Attributes.ID] = append(cr.registeredExtensions[ctr.Attributes.ID], newExtIDs...)
	cr.containers[ctr.Attributes.ID] = ctr
	maps.Insert(cr.sizeFnByFile, maps.All(sizeFnByFile))

	return nil
}

// SizesByFile implements FileSizer. A single file's stat error only skips that file -- it must not
// discard every other file's already-successfully-read size (see the equivalent fix and rationale on
// otel/logsource's managedSource.SizesByFile).
func (cr *containerReceiver) SizesByFile() (map[string]int64, error) {
	cr.l.Lock()
	defer cr.l.Unlock()

	sizes := make(map[string]int64, len(cr.sizeFnByFile))

	for logFile, sizeFn := range cr.sizeFnByFile {
		size, err := sizeFn()
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				// May not catch errors from the "sudo stat" command.
				logger.V(1).Printf("Can't get size of file %q (ignoring it): %v", logFile, err)
			}

			continue
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
		if recvComponents, ok := cr.startedComponents[ctrID]; ok {
			for _, comp := range recvComponents {
				err := comp.Shutdown(shutdownCtx)
				if err != nil {
					logger.V(1).Printf("Failed to stop log receiver component for container %s: %v", ctrID, err)
				}
			}
		} else {
			logger.V(1).Printf("Can't stop log receiver for container %s: it doesn't have one ...", ctrID)
		}

		// Cleaned up unconditionally, even with no startedComponents entry above. setupContainerLogReceiver
		// writes all four maps together only once it has fully succeeded (rolling back its extensions on
		// every earlier error), so a container whose setup failed has no entry in any of them and the
		// deletes below are simply no-ops -- cheaper than checking, and safe if that ever stops holding.
		// The container is gone for good (not just a restart): forget its offset too.
		cr.pipeline.persister.RemovePersistentExtsAndForget(cr.registeredExtensions[ctrID])

		realLogFilePath := cr.containers[ctrID].RealLogFilePath

		delete(cr.startedComponents, ctrID)
		delete(cr.registeredExtensions, ctrID)
		delete(cr.containers, ctrID)
		delete(cr.sizeFnByFile, realLogFilePath)
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
			LogFileRealPath:        ctr.RealLogFilePath,
			ReceiverKind:           ctr.ReceiverKind,
			Attributes:             ctr.Attributes,
		}
	}

	return infos
}

// isTailing reports whether this receiver actually has a live tail for ctrID, i.e. whether its
// setupContainerLogReceiver completed successfully. Deliberately not keyed off
// Manager.watchedContainers: that map is diagnostic bookkeeping, recorded before setup runs and kept
// (with SetupError set) even when it fails -- treating a failed setup as "already tailed" would make
// WantSource decline the glouton.* label source for a container nothing is tailing, silently dropping
// its logs entirely instead of merely duplicating them.
func (cr *containerReceiver) isTailing(ctrID string) bool {
	cr.l.Lock()
	defer cr.l.Unlock()

	_, found := cr.containers[ctrID]

	return found
}

// tailedContainerIDs is the whole-set form of isTailing, with the same "live tail only" semantics.
func (cr *containerReceiver) tailedContainerIDs() map[string]bool {
	cr.l.Lock()
	defer cr.l.Unlock()

	ids := make(map[string]bool, len(cr.containers))

	for ctrID := range cr.containers {
		ids[ctrID] = true
	}

	return ids
}

func (cr *containerReceiver) StartedComponentKeys() []string {
	cr.l.Lock()
	defer cr.l.Unlock()

	startedComponentKeys := slices.Collect(maps.Keys(cr.startedComponents))

	return startedComponentKeys
}

func (cr *containerReceiver) stop() {
	cr.l.Lock()
	defer cr.l.Unlock()

	wg := new(sync.WaitGroup)
	wg.Add(len(cr.startedComponents))

	// Stop all container receivers in parallel; they don't depend on each other.
	for _, components := range cr.startedComponents {
		go func() {
			defer crashreport.ProcessPanic()
			defer wg.Done()

			stopComponents(components)
		}()
	}

	wg.Wait()

	for _, extIDs := range cr.registeredExtensions {
		cr.pipeline.persister.RemovePersistentExts(extIDs)
	}
}

// makeLogContainer builds a Container, delegating attribute resolution to
// logsource.BuildContainerAttributes so it matches other container-derived sources.
func makeLogContainer(ctx context.Context, container facts.Container, logFilePath string) Container {
	return Container{
		LogFilePath:     logFilePath,
		Attributes:      logsource.BuildContainerAttributes(ctx, container),
		logCounter:      new(atomic.Int64),
		throughputMeter: logsource.NewRingCounter(throughputMeterResolutionSecs),
	}
}
