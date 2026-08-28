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
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/logsource"
	"github.com/bleemeo/glouton/types"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

type logSource struct {
	container   facts.Container
	serviceID   *discovery.NameInstance
	logFilePath string

	operators []config.OTELOperator
	filters   config.OTELFilters
}

type Manager struct {
	config                     config.OpenTelemetry
	knownLogFormats            map[string][]config.OTELOperator
	state                      bleemeoTypes.State
	streamAvailabilityStatusFn func() bleemeoTypes.LogsAvailability
	// containerFilter is cfg.ContainerFilter, pre-validated at New() (invalid entries stripped with a warning).
	containerFilter map[string]string

	receiverManager *logsource.ReceiverManager
	persister       *logsource.PersistHost
	pipeline        *pipelineContext
	containerRecv   *containerReceiver

	l                 sync.Mutex
	skippedSource     []sourceDiagnostic
	watchedServices   map[discovery.NameInstance]sourceDiagnostic
	watchedContainers map[string]sourceDiagnostic // map key: container ID
	// serviceReceivers only contains services that don't run in a container
	serviceReceivers map[discovery.NameInstance][]*logReceiver
	// fanoutSinks tracks, for diagnostics only, sources opted into via WantSource; the tail itself is owned by logsource.
	fanoutSinks map[string]*fanoutSink
}

// New builds the log-shipping pipeline and registers it as a logsource.SinkProvider on receiverManager,
// which decides per-source whether to ship it via WantSource (see sink_provider.go).
func New(
	ctx context.Context,
	cfg config.OpenTelemetry,
	hostroot string,
	state bleemeoTypes.State,
	commandRunner CommandRunner,
	facter *facts.FactProvider,
	pushLogs func(context.Context, []byte) error,
	streamAvailabilityStatusFn func() bleemeoTypes.LogsAvailability,
	addWarnings func(...error),
	receiverManager *logsource.ReceiverManager,
) (*Manager, error) {
	// Expand known log formats, allowing one level of cross-referencing.
	knownLogFormats, err := logsource.ExpandLogFormats(cfg.KnownLogFormats)
	if err != nil {
		addWarnings(err)

		return nil, fmt.Errorf("can't expand known log formats: %w", err)
	}

	// Reuse receiverManager's own PersistHost; two separate instances would overwrite each other's saved offsets.
	persister := receiverManager.Persister()

	pipelineOpts := pipelineOptions{
		batcherTimeout:           10 * time.Second,
		logsAvailabilityCacheTTL: 5 * time.Second,
	}

	pipeline, err := makePipeline(
		ctx,
		cfg,
		hostroot,
		commandRunner,
		facter,
		pushLogs,
		streamAvailabilityStatusFn,
		persister,
		addWarnings,
		knownLogFormats,
		logsource.GetLastFileSizesFromCache(state, logsource.LogFileSizesCacheKey),
		pipelineOpts,
	)
	if err != nil {
		return nil, fmt.Errorf("building pipeline: %w", err)
	}

	containerRecv := newContainerReceiver(pipeline)

	processingManager := &Manager{
		config:                     cfg,
		knownLogFormats:            knownLogFormats,
		state:                      state,
		streamAvailabilityStatusFn: streamAvailabilityStatusFn,
		containerFilter:            validateContainerFilters(cfg.ContainerFilter, cfg.KnownLogFilters),
		receiverManager:            receiverManager,
		persister:                  persister,
		pipeline:                   pipeline,
		containerRecv:              containerRecv,
		watchedServices:            make(map[discovery.NameInstance]sourceDiagnostic),
		watchedContainers:          make(map[string]sourceDiagnostic),
		serviceReceivers:           make(map[discovery.NameInstance][]*logReceiver),
		fanoutSinks:                make(map[string]*fanoutSink),
	}

	receiverManager.RegisterSinkProvider(processingManager)

	// Fold this package's file sizes into receiverManager's shared save instead of running an independent saver of the same state key.
	receiverManager.RegisterExternalSizer(func() []logsource.FileSizer {
		processingManager.pipeline.l.Lock()
		defer processingManager.pipeline.l.Unlock()

		return mergeLastFileSizes(processingManager.pipeline.receivers, processingManager.containerRecv)
	})

	go processingManager.handleProcessingLifecycle(ctx)

	return processingManager, nil
}

// handleProcessingLifecycle periodically saves file sizes to the state cache,
// and shutdowns all the started components when the given context expires.
func (man *Manager) handleProcessingLifecycle(ctx context.Context) {
	defer crashreport.ProcessPanic()

	recvUpdateTicker := time.NewTicker(receiversUpdatePeriod)
	defer recvUpdateTicker.Stop()

	// File sizes are saved via receiverManager's own periodic SaveState (see New's RegisterExternalSizer), not a ticker here.
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
		}
	}

	// ctx has expired, shutting everything down

	// man.serviceReceivers is guarded by man.l, which a concurrent discovery cycle writes to
	// (removeOldSources deletes from it, setupProcessingForSource appends to it), so iterating it under
	// man.pipeline.l alone is a data race. Both locks are taken in the man.l -> man.pipeline.l order used
	// by every other path needing the pair (updateServiceReceivers, DiagnosticArchive,
	// setupProcessingForSource); reversing it here would risk a deadlock against them.
	man.l.Lock()
	man.pipeline.l.Lock()

	for _, receivers := range man.serviceReceivers {
		stopReceivers(receivers, man.persister.RemovePersistentExts)
	}

	man.containerRecv.stop()

	man.pipeline.shutdownAll()

	man.pipeline.l.Unlock()
	man.l.Unlock()

	// Must run outside both: SaveState locks man.pipeline.l via the external sizer, and takes
	// ReceiverManager's lock, from under which WantSource takes man.l -- sync.Mutex isn't reentrant.
	man.receiverManager.SaveState()
}

func (man *Manager) updateServiceReceivers(ctx context.Context) error {
	man.pipeline.l.Lock()
	defer man.pipeline.l.Unlock()

	errGrp := new(errgroup.Group)

	for _, receivers := range man.serviceReceivers {
		for _, recv := range receivers {
			// update() can run in parallel here since it only reads the lock-protected fields.
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

// ServiceTailedContainerIDs returns the IDs of containers this package currently tails through a
// discovered service's own log receiver. Passed to logsource.ReceiverManager.UpdateContainers so a
// container's glouton.*-label source is rebuilt when that status changes, which re-asks WantSource --
// see sink_provider.go for why the shipping decision itself has to stay there.
//
// containerRecv carries its own lock and is the authority on what's really tailed, so man.l is
// deliberately not taken (see isTailing on why a failed setup must not count).
func (man *Manager) ServiceTailedContainerIDs() map[string]bool {
	return man.containerRecv.tailedContainerIDs()
}

func (man *Manager) HandleLogsFromDynamicSources(ctx context.Context, services []discovery.Service, containers []facts.Container) {
	// Computed without holding man.l: receiverManager's askProviders (itself called under its own lock,
	// from RescanReceivers/UpdateContainers) calls back into man.l via WantSource/wrapWithFilter. Calling
	// ContainerIDsShippedByReceivers (which takes receiverManager's lock) while man.l is held would nest
	// the two locks in the opposite order from that existing call path, on two different goroutines --
	// a deadlock waiting to happen.
	var shippedByReceivers map[string]bool

	if man.receiverManager != nil {
		shippedByReceivers = man.receiverManager.ContainerIDsShippedByReceivers(containers)
	}

	man.l.Lock()
	defer man.l.Unlock()

	man.removeOldSources(ctx, services, containers)

	logSources := man.processLogSources(services, containers, shippedByReceivers)

	// Every logSource comes from a discovered service now, so serviceID is always set below
	// (label-only containers go through logsource.ReceiverManager/WantSource instead, see sink_provider.go).
	for _, logSource := range logSources {
		err := man.setupProcessingForSource(ctx, logSource)
		if err == nil {
			continue
		}

		if diag, found := man.watchedServices[*logSource.serviceID]; found {
			diag.SetupError = err.Error()
			man.watchedServices[*logSource.serviceID] = diag
		}

		if logSource.container != nil {
			if diag, found := man.watchedContainers[logSource.container.ID()]; found {
				diag.SetupError = err.Error()
				man.watchedContainers[logSource.container.ID()] = diag
			}

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
	}
}

// processLogSources resolves log sources for discovered services; everything else goes through logsource.ReceiverManager/WantSource.
// shippedByReceivers marks containers already matched by one of receiverManager's own
// container_name/container_selectors receivers (see HandleLogsFromDynamicSources for why it's computed
// by the caller instead of here): claiming one of them too would tail its log file twice. Explicit
// config wins over service auto-discovery.
func (man *Manager) processLogSources(services []discovery.Service, containers []facts.Container, shippedByReceivers map[string]bool) []logSource {
	man.skippedSource = make([]sourceDiagnostic, 0, len(man.skippedSource))

	containersByID := make(map[string]facts.Container, len(containers))

	for _, ctr := range containers {
		containersByID[ctr.ID()] = ctr
	}

	var logSources []logSource

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

		if service.ContainerID != "" {
			if ctr, found := containersByID[service.ContainerID]; found && logsource.IsContainerExcluded(man.config, ctr) {
				logger.V(2).Printf("Ignoring logs of service %q, because its container is excluded", service.Name)

				man.skippedSource = append(man.skippedSource, sourceDiagnostic{
					IsFromService: true,
					ServiceKey:    key,
					ContainerID:   service.ContainerID,
					ContainerName: service.ContainerName,
					SkipReason:    "Container excluded (glouton.log_enable=false or container_exclude)",
				})

				continue
			}

			if shippedByReceivers[service.ContainerID] {
				logger.V(2).Printf("Ignoring logs of service %q, because its container is already matched by an explicit receiver", service.Name)

				man.skippedSource = append(man.skippedSource, sourceDiagnostic{
					IsFromService: true,
					ServiceKey:    key,
					ContainerID:   service.ContainerID,
					ContainerName: service.ContainerName,
					SkipReason:    "Container already matched by an explicit log.opentelemetry.receivers entry",
				})

				continue
			}
		}

		var ctr facts.Container

		if service.ContainerID != "" {
			var found bool

			ctr, found = containersByID[service.ContainerID]
			if !found {
				logger.V(1).Printf("Can't find container with id %q (related to service %q); ignoring it", service.ContainerID, service.Name)

				man.skippedSource = append(man.skippedSource, sourceDiagnostic{
					IsFromService: true,
					ServiceKey:    key,
					ContainerID:   service.ContainerID,
					ContainerName: service.ContainerName,
					SkipReason:    "Can't find container",
				})

				continue
			}

			man.watchedContainers[service.ContainerID] = sourceDiagnostic{
				IsFromService: true,
				ServiceKey:    key,
				ContainerID:   service.ContainerID,
				ContainerName: service.ContainerName,
				SkipReason:    "IsFromService, look at entry in WatchedServices",
			}
		}

		for _, serviceLogProcessing := range service.LogProcessing {
			format := man.knownLogFormats[serviceLogProcessing.Format]
			filters := man.config.KnownLogFilters[serviceLogProcessing.Filter]

			// A container's own glouton.log_format/glouton.log_filter (or its container_format/
			// container_filter mapping) is explicit user config and outranks what auto-discovery inferred
			// from the service type. This tail is the container's only one -- WantSource declines its
			// glouton.*-label source precisely because this path already ships it -- so without honouring
			// them here those labels would silently stop having any effect on a containerised service.
			if ctr != nil {
				if containerFormat := man.resolveContainerFormat(ctr); containerFormat != nil {
					format = containerFormat
				}

				if containerFilters := man.resolveContainerFilter(ctr); containerFilters != nil {
					filters = containerFilters
				}
			}

			logSource := logSource{
				serviceID:   &key,
				logFilePath: serviceLogProcessing.FilePath, // ignored if in a container
				container:   ctr,                           // possibly nil if not in a container
				operators:   append(operatorsForServiceName(service.Name), format...),
				filters:     filters,
			}

			logSources = append(logSources, logSource)
		}

		man.watchedServices[key] = sourceDiagnostic{
			IsFromService:   true,
			ServiceKey:      key,
			ServiceLogPaths: flattenLogPaths(service.LogProcessing),
			ContainerID:     service.ContainerID,
			ContainerName:   service.ContainerName,
		}
	}

	return logSources
}

func (man *Manager) setupProcessingForSource(ctx context.Context, logSource logSource) error {
	rawOps, err := logsource.ExpandOperators(logSource.operators, man.knownLogFormats, false)
	if err != nil {
		return fmt.Errorf("expanding operators: %w", err)
	}

	operators, err := logsource.BuildOperators(rawOps)
	if err != nil {
		return fmt.Errorf("building operators: %w", err)
	}

	if logSource.container != nil {
		logPath, err := man.containerRecv.handleContainerLogs(ctx, logSource.container, operators, logSource.filters)

		if diag, found := man.watchedContainers[logSource.container.ID()]; found {
			if diag.IsFromService {
				if diagServ, found := man.watchedServices[diag.ServiceKey]; found {
					diagServ.ContainerLogPath = logPath
					man.watchedServices[diag.ServiceKey] = diagServ
				}
			} else {
				diag.ContainerLogPath = logPath
				man.watchedContainers[logSource.container.ID()] = diag
			}
		}

		if err != nil {
			return err
		}
	} else {
		recvName := fmt.Sprintf("service_%s-%q_%s", logSource.serviceID.Name, logSource.serviceID.Instance, uuid.NewString())
		recvConfig := config.LogReceiver{
			"include":   []string{logSource.logFilePath},
			"operators": logSource.operators,
			"filters":   logSource.filters,
		}

		recv, warn, err := newLogReceiver(recvName, recvConfig, true, man.pipeline.getInput(), nil, logsource.StatFile)
		if err != nil {
			return err
		}

		if warn != nil {
			logWarnings(errorf("A warning occurred while setting up log receiver for service %s / %s: %w", logSource.serviceID.Name, logSource.serviceID.Instance, warn))
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
	latestServices := make(map[discovery.NameInstance]struct{}, len(services))
	latestContainers := make(map[string]struct{}, len(containers)) // map key: container ID

	for _, service := range services {
		if service.LogProcessing != nil {
			latestServices[discovery.NameInstance{Name: service.Name, Instance: service.Instance}] = struct{}{}
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

	containerAlsoGone := make(map[string]bool, len(noLongerExistingContainers))
	for _, ctrID := range noLongerExistingContainers {
		containerAlsoGone[ctrID] = true
	}

	for _, service := range noLongerExistingServices {
		receivers, found := man.serviceReceivers[service]
		if found {
			// The service is gone for good (not just a restart): forget its offset too.
			stopReceivers(receivers, man.persister.RemovePersistentExtsAndForget)
			delete(man.serviceReceivers, service)
		}

		// A container-hosted service has no serviceReceivers entry: its tail lives in containerRecv, and
		// the container branch below only stops it once the *container* disappears. Left running, it keeps
		// applying the vanished service's operators/filters forever, and it stops the container's
		// glouton.*-label source from being reconsidered (ServiceTailedContainerIDs still reports it).
		// The offset is kept, since the container itself is still there: this only drops the reason to
		// tail it, so whoever picks it up next resumes rather than skipping to the end of the file.
		//
		// containerAlsoGone is checked to skip this teardown entirely when the container itself is also
		// disappearing this cycle (the ordinary "container removed" case): stopWatchingForContainers
		// unconditionally clears its registeredExtensions/containers bookkeeping for ctrID regardless of
		// forget, so a forget=false call here first would leave the forget=true call below (which runs the
		// real, permanent offset-forget) nothing to forget from.
		if diag, watched := man.watchedServices[service]; watched && diag.ContainerID != "" && !containerAlsoGone[diag.ContainerID] {
			man.containerRecv.stopWatchingForContainers(ctx, []string{diag.ContainerID}, false)
			delete(man.watchedContainers, diag.ContainerID)
		}

		delete(man.watchedServices, service)
	}

	if len(noLongerExistingContainers) > 0 {
		// The containers themselves are gone for good (not just restarted): forget their offsets too.
		man.containerRecv.stopWatchingForContainers(ctx, noLongerExistingContainers, true)

		for _, ctrID := range noLongerExistingContainers {
			delete(man.watchedContainers, ctrID)
		}
	}
}

func (man *Manager) DiagnosticArchive(_ context.Context, writer types.ArchiveWriter) error {
	man.l.Lock()
	defer man.l.Unlock()

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

	skippedSource := slices.Clone(man.skippedSource)
	watchedContainers := maps.Clone(man.watchedContainers)
	watchedServices := make(map[string]sourceDiagnostic, len(man.watchedServices))

	for serv, row := range man.watchedServices {
		key := serv.Name + "/" + serv.Instance
		watchedServices[key] = row
	}

	pipelineStartedComponentsCount := len(man.pipeline.startedComponents)
	perServiceStartedComponentCount := make(map[string]int)

	for srvID, logsReceivers := range man.serviceReceivers {
		key := srvID.Name + "/" + srvID.Instance
		total := 0

		for _, lr := range logsReceivers {
			lr.l.Lock()
			total += len(lr.startedComponents)
			lr.l.Unlock()
		}

		perServiceStartedComponentCount[key] = total
	}

	man.pipeline.l.Unlock()

	diagnosticInfo := diagnosticInformation{
		summary: diagnosticSummary{
			LogProcessedCount:               man.pipeline.logProcessedCount.Load(),
			LogThroughputPerMinute:          man.pipeline.logThroughputMeter.Total(),
			ProcessingStatus:                man.streamAvailabilityStatusFn().String(),
			ContainerStartedComponents:      man.containerRecv.StartedComponentKeys(),
			PipelineStartedComponentsCount:  pipelineStartedComponentsCount,
			PerServiceStartedComponentCount: perServiceStartedComponentCount,
		},
		receivers: diagnosticReceiver{
			Receivers:          receiversInfo,
			ContainerReceivers: man.containerRecv.diagnostic(),
			WatchedServices:    wServices,
			FanoutSources:      man.fanoutSourceDiagnosticsLocked(),
		},
		receiversSetup: diagnosticReceiverSetup{
			SkippedSource:     skippedSource,
			WatchedServices:   watchedServices,
			WatchedContainers: watchedContainers,
		},
		KnownLogFormats: man.knownLogFormats,
		KnownLogFilters: man.config.KnownLogFilters,
	}

	if man.pipeline.journaldCounter != nil {
		diagnosticInfo.receivers.JournaldReceiver = &journaldReceiverDiagnosticInformation{
			LogProcessedCount:      man.pipeline.journaldCounter.Load(),
			LogThroughputPerMinute: man.pipeline.journaldThroughputMeter.Total(),
		}
	}

	return diagnosticInfo.writeToArchive(writer)
}

func operatorsForServiceName(serviceName string) []config.OTELOperator {
	return []config.OTELOperator{
		{
			"type":  "add",
			"field": "resource['service.name']",
			"value": serviceName,
		},
	}
}
