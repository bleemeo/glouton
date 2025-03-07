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
	"sync"
	"time"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/discovery"
	crTypes "github.com/bleemeo/glouton/facts/container-runtime/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
)

type Manager struct {
	config                     config.OpenTelemetry
	state                      bleemeoTypes.State
	crRuntime                  crTypes.RuntimeInterface
	streamAvailabilityStatusFn func() bleemeoTypes.LogsAvailability

	persister     *persistHost
	pipeline      *pipelineContext
	containerRecv *containerReceiver

	l               sync.Mutex
	watchedServices map[discovery.NameInstance][]discovery.ServiceLogReceiver // TODO: revert
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
	)
	if err != nil {
		return nil, fmt.Errorf("building pipeline: %w", err)
	}

	containerRecv := newContainerReceiver(pipeline, cfg.ContainerOperators, cfg.GlobalOperators)

	processingManager := &Manager{
		config:                     cfg,
		state:                      state,
		crRuntime:                  crRuntime,
		streamAvailabilityStatusFn: streamAvailabilityStatusFn,
		persister:                  persister,
		pipeline:                   pipeline,
		containerRecv:              containerRecv,
		watchedServices:            make(map[discovery.NameInstance][]discovery.ServiceLogReceiver),
	}

	go processingManager.handleProcessingLifecycle(ctx)

	return processingManager, nil
}

// handleProcessingLifecycle periodically saves file sizes to the state cache,
// and closes shutdowns all the started components when the given context expires.
func (man *Manager) handleProcessingLifecycle(ctx context.Context) {
	defer crashreport.ProcessPanic()

	ticker := time.NewTicker(saveFileSizesToCachePeriod)
	defer ticker.Stop()

ctxLoop:
	for ctx.Err() == nil {
		select {
		case <-ticker.C:
			man.pipeline.l.Lock()
			fileSizers := mergeLastFileSizes(man.pipeline.receivers, man.containerRecv)
			man.pipeline.l.Unlock()

			saveLastFileSizesToCache(man.state, fileSizers)
			saveFileMetadataToCache(man.state, man.persister.getAllMetadata())
		case <-ctx.Done():
			break ctxLoop
		}
	}

	// ctx has expired, shutting everything down

	man.pipeline.l.Lock()
	defer man.pipeline.l.Unlock()

	shutdownAll(man.pipeline.startedComponents)

	saveLastFileSizesToCache(man.state, mergeLastFileSizes(man.pipeline.receivers, man.containerRecv))
	saveFileMetadataToCache(man.state, man.persister.getAllMetadata())
}

func (man *Manager) ContainerReceiver() ContainerReceiver { // temporary
	return man.containerRecv
}

// TODO: remove receivers for services that no longer exist
func (man *Manager) HandleLogFromServices(ctx context.Context, services []discovery.Service) {
	man.l.Lock()
	defer man.l.Unlock()

	for _, service := range services {
		key := discovery.NameInstance{
			Name:     service.Name,
			Instance: service.Instance,
		}

		if _, alreadyWatching := man.watchedServices[key]; alreadyWatching {
			continue
		}

		logger.Printf("Handling logs for service %q", service.Name) // TODO: remove

		man.setupProcessingForService(ctx, service.Name, service.LogProcessing, service.ContainerID)

		man.watchedServices[key] = service.LogProcessing
	}
}

func (man *Manager) setupProcessingForService(ctx context.Context, serviceName string, serviceLogReceivers []discovery.ServiceLogReceiver, ctrID string) {
	for _, serviceRecv := range serviceLogReceivers {
		operators, err := buildOperators(serviceRecv.Operators)
		if err != nil {
			if ctrID != "" {
				logger.V(1).Printf("Can't build operators for service %q on container %s: %v", serviceName, ctrID, err)
			} else {
				logger.V(1).Printf("Can't build operators for service %q file %q: %v", serviceName, serviceRecv.LogFilePath, err)
			}

			continue
		}

		if ctrID != "" {
			ctr, found := man.crRuntime.CachedContainer(ctrID)
			if !found {
				logger.V(1).Printf("Can't find container with id %q (related to service %q)", ctrID, serviceName)

				continue
			}

			err = man.containerRecv.handleContainerLogs(ctx, man.crRuntime, ctr, operators)
			if err != nil {
				logger.V(1).Printf("Can't handle logs for service %q on container %s (%s): %v", serviceName, ctr.ContainerName(), ctr.ID(), err)
			}
		} else {
			recvName := fmt.Sprintf("service-%q_file-%q", serviceName, serviceRecv.LogFilePath)
			recvConfig := config.OTLPReceiver{
				Include:   []string{serviceRecv.LogFilePath},
				Operators: serviceRecv.Operators,
			}

			man.pipeline.addReceiver(ctx, recvName, recvConfig, nil, logWarnings)
		}
	}
}

func (man *Manager) DiagnosticArchive(_ context.Context, writer types.ArchiveWriter) error {
	man.pipeline.l.Lock()

	receiversInfo := make(map[string]receiverDiagnosticInformation, len(man.pipeline.receivers))

	for _, rcvr := range man.pipeline.receivers {
		receiversInfo[rcvr.name] = rcvr.diagnosticInfo()
	}

	man.pipeline.l.Unlock()

	wServices := make(map[string][]discovery.ServiceLogReceiver, len(man.watchedServices))

	for serv, recvs := range man.watchedServices {
		wServices[serv.Name] = recvs
	}

	diagnosticInfo := diagnosticInformation{
		LogProcessedCount:      man.pipeline.logProcessedCount.Load(),
		LogThroughputPerMinute: man.pipeline.logThroughputMeter.Total(),
		ProcessingStatus:       man.streamAvailabilityStatusFn().String(),
		Receivers:              receiversInfo,
		ContainerReceivers:     man.containerRecv.diagnostic(),
		WatchedServices:        wServices,
		GlobalOperators:        man.config.GlobalOperators,
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
