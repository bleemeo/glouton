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
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/facts"
	crTypes "github.com/bleemeo/glouton/facts/container-runtime/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

type LogSource struct {
	container   facts.Container
	serviceID   *discovery.NameInstance
	logFilePath string

	operators []config.OTELOperator
}

type Manager struct {
	config                     config.OpenTelemetry
	state                      bleemeoTypes.State
	crRuntime                  crTypes.RuntimeInterface
	streamAvailabilityStatusFn func() bleemeoTypes.LogsAvailability

	persister     *persistHost
	pipeline      *pipelineContext
	containerRecv *containerReceiver

	l                 sync.Mutex
	watchedServices   map[discovery.NameInstance]struct{}
	watchedContainers map[string]struct{} // map key: container ID
	// serviceReceivers only contains services that don't run in a container
	serviceReceivers map[discovery.NameInstance][]*logReceiver
}

func New(
	ctx context.Context,
	cfg config.OpenTelemetry,
	hostroot string,
	state bleemeoTypes.State,
	commandRunner CommandRunner,
	crRuntime crTypes.RuntimeInterface,
	pushLogs func(context.Context, []byte) error,
	streamAvailabilityStatusFn func() bleemeoTypes.LogsAvailability,
	addWarnings func(...error),
) (*Manager, error) {
	persister, err := newPersistHost(state)
	if err != nil {
		return nil, fmt.Errorf("can't create persist host: %w", err)
	}

	pipelineOpts := pipelineOptions{
		batcherTimeout:           10 * time.Second,
		logsAvailabilityCacheTTL: 5 * time.Second,
	}

	pipeline, err := makePipeline(
		ctx,
		cfg,
		hostroot,
		commandRunner,
		pushLogs,
		streamAvailabilityStatusFn,
		persister,
		addWarnings,
		getLastFileSizesFromCache(state),
		pipelineOpts,
	)
	if err != nil {
		return nil, fmt.Errorf("building pipeline: %w", err)
	}

	containerRecv := newContainerReceiver(pipeline, cfg.ContainerFormat, cfg.KnownLogFormats)

	processingManager := &Manager{
		config:                     cfg,
		state:                      state,
		crRuntime:                  crRuntime,
		streamAvailabilityStatusFn: streamAvailabilityStatusFn,
		persister:                  persister,
		pipeline:                   pipeline,
		containerRecv:              containerRecv,
		watchedServices:            make(map[discovery.NameInstance]struct{}),
		watchedContainers:          make(map[string]struct{}),
		serviceReceivers:           make(map[discovery.NameInstance][]*logReceiver),
	}

	go processingManager.handleProcessingLifecycle(ctx)

	return processingManager, nil
}

// handleProcessingLifecycle periodically saves file sizes to the state cache,
// and shutdowns all the started components when the given context expires.
func (man *Manager) handleProcessingLifecycle(ctx context.Context) {
	defer crashreport.ProcessPanic()

	recvUpdateTicker := time.NewTicker(receiversUpdatePeriod)
	defer recvUpdateTicker.Stop()

	saveFileSizesTicker := time.NewTicker(saveFileSizesToCachePeriod)
	defer saveFileSizesTicker.Stop()

ctxLoop:
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			break ctxLoop
		case <-recvUpdateTicker.C:
			man.l.Lock()

			err := man.updateServiceReceivers(ctx)
			if err != nil {
				logger.V(1).Printf("Failed to update service receivers: %v", err)
			}

			man.l.Unlock()
		case <-saveFileSizesTicker.C:
			man.pipeline.l.Lock()
			fileSizers := mergeLastFileSizes(man.pipeline.receivers, man.containerRecv)
			man.pipeline.l.Unlock()

			saveLastFileSizesToCache(man.state, fileSizers)
			saveFileMetadataToCache(man.state, man.persister.getAllMetadata())
		}
	}

	// ctx has expired, shutting everything down

	man.pipeline.l.Lock()
	defer man.pipeline.l.Unlock()

	for _, receivers := range man.serviceReceivers {
		stopReceivers(receivers)
	}

	man.containerRecv.stop()

	shutdownAll(man.pipeline.startedComponents)

	saveLastFileSizesToCache(man.state, mergeLastFileSizes(man.pipeline.receivers, man.containerRecv))
	saveFileMetadataToCache(man.state, man.persister.getAllMetadata())
}

func (man *Manager) updateServiceReceivers(ctx context.Context) error {
	man.pipeline.l.Lock()
	defer man.pipeline.l.Unlock()

	errGrp := new(errgroup.Group)

	for _, receivers := range man.serviceReceivers {
		for _, recv := range receivers {
			// We can run several logReceiver.update() in parallel without taking the pipeline lock in each,
			// since they only do read-access to the lock-protected fields.
			errGrp.Go(func() error {
				err := recv.update(ctx, man.pipeline, logWarnings)
				if err != nil {
					return fmt.Errorf("log receiver %q: %w", recv.name, err)
				}

				return nil
			})
		}
	}

	return errGrp.Wait()
}

func (man *Manager) HandleLogsFromDynamicSources(ctx context.Context, services []discovery.Service, containers []facts.Container) {
	man.l.Lock()
	defer man.l.Unlock()

	man.removeOldSources(ctx, services, containers)

	logSources := man.processLogSources(services, containers)

	for _, logSource := range logSources {
		err := man.setupProcessingForSource(ctx, logSource)
		if err != nil {
			if logSource.serviceID != nil {
				if logSource.container != nil {
					logger.V(1).Printf(
						"Failed to set up log processing for service %q on container %s (%s): %v",
						logSource.serviceID.Name, logSource.container.ContainerName(), logSource.container.ID(), err,
					)
				} else {
					logger.V(1).Printf(
						"Failed to set up log processing for service %q file %q: %v",
						logSource.serviceID.Name, logSource.logFilePath, err,
					)
				}
			} else {
				logger.V(1).Printf(
					"Failed to set up log processing for container %s (%s): %v",
					logSource.container.ContainerName(), logSource.container.ID(), err,
				)
			}
		}
	}
}

func (man *Manager) processLogSources(services []discovery.Service, containers []facts.Container) []LogSource {
	const gloutonContainerLabelPrefix = "glouton."

	containersByID := make(map[string]facts.Container, len(containers))

	for _, ctr := range containers {
		containersByID[ctr.ID()] = ctr
	}

	var logSources []LogSource //nolint:prealloc

	for _, service := range services {
		if service.LogProcessing == nil || !service.Active {
			continue
		}

		key := discovery.NameInstance{
			Name:     service.Name,
			Instance: service.Instance,
		}

		if _, alreadyWatching := man.watchedServices[key]; alreadyWatching {
			continue
		}

		for _, serviceLogProcessing := range service.LogProcessing {
			logSource := LogSource{
				serviceID:   &key,
				logFilePath: serviceLogProcessing.FilePath, // ignored if in a container
				operators:   append(operatorsForService(service), man.config.KnownLogFormats[serviceLogProcessing.Format]...),
			}

			if service.ContainerID != "" {
				ctr, found := containersByID[service.ContainerID]
				if !found {
					logger.V(1).Printf("Can't find container with id %q (related to service %q); ignoring it", service.ContainerID, service.Name)

					continue
				}

				logSource.container = ctr
				man.watchedContainers[service.ContainerID] = struct{}{}
			} else {
				logSource.logFilePath = serviceLogProcessing.FilePath
			}

			logSources = append(logSources, logSource)
			man.watchedServices[key] = struct{}{}
		}
	}

	for ctrID, ctr := range containersByID {
		if _, alreadyWatching := man.watchedContainers[ctrID]; alreadyWatching {
			continue
		}

		logSource := LogSource{
			container: ctr,
		}

		ctrFacts := facts.LabelsAndAnnotations(ctr)

		logFormat, found := ctrFacts[gloutonContainerLabelPrefix+"log_format"]
		if found {
			ops, found := man.config.KnownLogFormats[logFormat]
			if found {
				logSource.operators = ops
			} else {
				logger.V(1).Printf("Container %s (%s) requires an unknown log format: %q", ctr.ContainerName(), ctrID, logFormat)
			}
		}

		logSources = append(logSources, logSource)
		man.watchedContainers[ctrID] = struct{}{}
	}

	return logSources
}

func (man *Manager) setupProcessingForSource(ctx context.Context, logSource LogSource) error {
	operators, err := buildOperators(logSource.operators)
	if err != nil {
		return fmt.Errorf("building operators: %w", err)
	}

	if logSource.container != nil {
		err = man.containerRecv.handleContainerLogs(ctx, man.crRuntime, logSource.container, operators)
		if err != nil {
			return err
		}
	} else {
		recvName := fmt.Sprintf("service_%s-%q_%s", logSource.serviceID.Name, logSource.serviceID.Instance, uuid.NewString())
		recvConfig := config.OTLPReceiver{
			Include:   []string{logSource.logFilePath},
			Operators: logSource.operators,
		}

		recv, err := newLogReceiver(recvName, recvConfig, true, man.pipeline.getInput(), nil)
		if err != nil {
			return err
		}

		man.pipeline.l.Lock()
		defer man.pipeline.l.Unlock()

		err = recv.update(ctx, man.pipeline, logWarnings)
		if err != nil {
			return err
		}

		man.serviceReceivers[*logSource.serviceID] = append(man.serviceReceivers[*logSource.serviceID], recv)
	}

	return nil
}

func (man *Manager) removeOldSources(ctx context.Context, services []discovery.Service, containers []facts.Container) {
	watchedServices := slices.Collect(maps.Keys(man.watchedServices))
	watchedContainers := slices.Collect(maps.Keys(man.watchedContainers))
	latestServices := make(map[discovery.NameInstance]bool, len(services)) // map[service] -> is a container
	latestContainers := make(map[string]struct{}, len(containers))         // map key: container ID

	for _, service := range services {
		if service.LogProcessing != nil {
			latestServices[discovery.NameInstance{Name: service.Name, Instance: service.Instance}] = service.ContainerID != ""
		}
	}

	for _, ctr := range containers {
		latestContainers[ctr.ID()] = struct{}{}
	}

	noLongerExistingServices := diffBetween(watchedServices, latestServices)
	noLongerExistingContainers := diffBetween(watchedContainers, latestContainers)

	if len(noLongerExistingServices)+len(noLongerExistingContainers) > 0 {
		logger.V(2).Printf("Removing sources from log processing: services=%s / containers=%s", noLongerExistingServices, noLongerExistingContainers)
	}

	for _, service := range noLongerExistingServices {
		if latestServices[service] {
			continue // containers will be handled below
		}

		receivers, found := man.serviceReceivers[service]
		if found {
			stopReceivers(receivers)
			delete(man.serviceReceivers, service)
		}

		delete(man.watchedServices, service)
	}

	if len(noLongerExistingContainers) > 0 {
		man.containerRecv.stopWatchingForContainers(ctx, noLongerExistingContainers)

		for _, ctrID := range noLongerExistingContainers {
			delete(man.watchedContainers, ctrID)
		}
	}
}

func (man *Manager) DiagnosticArchive(_ context.Context, writer types.ArchiveWriter) error {
	man.pipeline.l.Lock()

	receiversInfo := make(map[string]receiverDiagnosticInformation, len(man.pipeline.receivers))

	for _, rcvr := range man.pipeline.receivers {
		receiversInfo[rcvr.name] = rcvr.diagnosticInfo()
	}

	wServices := make(map[string][]receiverDiagnosticInformation, len(man.watchedServices))

	for serv := range man.watchedServices {
		receivers, found := man.serviceReceivers[serv]
		if found {
			key := serv.Name + "/" + serv.Instance
			wServices[key] = make([]receiverDiagnosticInformation, len(receivers))

			for i, recv := range receivers {
				wServices[key][i] = recv.diagnosticInfo()
			}
		}
	}

	man.pipeline.l.Unlock()

	diagnosticInfo := diagnosticInformation{
		LogProcessedCount:      man.pipeline.logProcessedCount.Load(),
		LogThroughputPerMinute: man.pipeline.logThroughputMeter.Total(),
		ProcessingStatus:       man.streamAvailabilityStatusFn().String(),
		Receivers:              receiversInfo,
		ContainerReceivers:     man.containerRecv.diagnostic(),
		WatchedServices:        wServices,
		KnownLogFormats:        man.config.KnownLogFormats,
	}

	if man.pipeline.otlpRecvCounter != nil {
		diagnosticInfo.OTLPReceiver = &otlpReceiverDiagnosticInformation{
			GRPCEnabled:            man.config.GRPC.Enable,
			HTTPEnabled:            man.config.HTTP.Enable,
			LogProcessedCount:      man.pipeline.otlpRecvCounter.Load(),
			LogThroughputPerMinute: man.pipeline.otlpRecvThroughputMeter.Total(),
		}
	}

	return diagnosticInfo.writeToArchive(writer)
}

func operatorsForService(service discovery.Service) []config.OTELOperator {
	return []config.OTELOperator{
		{
			"type":  "add",
			"field": "resource['service.name']",
			"value": service.Name,
		},
	}
}
