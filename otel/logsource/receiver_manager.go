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

package logsource

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"sync"
	"time"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/hostrootsymlink"

	"github.com/go-viper/mapstructure/v2"
	"github.com/google/uuid"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
)

// Persistence identity, exported so otel/logprocessing shares the exact same values instead of hand-copying
// them (which could otherwise silently drift and split log-file read offsets across a restart).
const (
	PersistStorageType      = "glouton_log_metadata_storage"
	LogFileMetadataCacheKey = "LogFileMetadata"
	LogFileSizesCacheKey    = "LogFileSizes"
)

const (
	// persistArchivePath keeps otel/logprocessing's pre-rewrite name (see Persister()).
	persistArchivePath = "log-processing/persister.json"

	// saveThrottle limits how often a file's read offset is saved, matching otel/logprocessing's policy.
	saveThrottle = time.Minute
)

var errNoContainerLogFile = errors.New("no log file found for container")

// ReceiverManager owns log tails and fans records to registered SinkProviders. Use NewReceiverManager; all exported methods are thread-safe.
type ReceiverManager struct {
	cfg           config.OpenTelemetry
	hostroot      string
	state         bleemeoTypes.State
	commandRunner CommandRunner
	statFile      StatFileFunc
	telemetry     component.TelemetrySettings

	knownLogFormats map[string][]config.OTELOperator

	persister     *PersistHost
	lastFileSizes map[string]int64

	l                  sync.Mutex
	providers          []SinkProvider
	receivers          map[string]*managedSource // by OpenTelemetry.Receivers key
	byContainer        map[string]*managedSource // by container ID, SourceContainerLabel only
	externalSizerFuncs []func() []FileSizer
}

// NewReceiverManager builds a ReceiverManager for cfg. Register every SinkProvider first, then call
// RescanReceivers/UpdateContainers to resolve sources and start tailing.
func NewReceiverManager(cfg config.OpenTelemetry, hostroot string, state bleemeoTypes.State, commandRunner CommandRunner) (*ReceiverManager, error) {
	knownLogFormats, err := ExpandLogFormats(cfg.KnownLogFormats)
	if err != nil {
		logger.V(1).Printf("logsource: failed to expand known log formats, log_format won't be usable: %v", err)
	}

	persister, err := NewPersistHost(state, PersistConfig{
		StorageType:  PersistStorageType,
		CacheKey:     LogFileMetadataCacheKey,
		ArchivePath:  persistArchivePath,
		SaveThrottle: saveThrottle,
	})
	if err != nil {
		return nil, fmt.Errorf("creating persist host: %w", err)
	}

	return &ReceiverManager{
		cfg:             cfg,
		hostroot:        hostroot,
		state:           state,
		commandRunner:   commandRunner,
		statFile:        StatFile,
		telemetry:       NewTelemetrySettings(),
		knownLogFormats: knownLogFormats,
		persister:       persister,
		lastFileSizes:   GetLastFileSizesFromCache(state, LogFileSizesCacheKey),
		receivers:       make(map[string]*managedSource),
		byContainer:     make(map[string]*managedSource),
	}, nil
}

// RegisterSinkProvider registers p as a candidate consumer of every source resolved from now on. Must
// be called before the first RescanReceivers/UpdateContainers/NetworkWants call, since WantSource is
// only asked once per source.
func (rm *ReceiverManager) RegisterSinkProvider(p SinkProvider) {
	rm.l.Lock()
	defer rm.l.Unlock()

	rm.providers = append(rm.providers, p)
}

// Persister returns the shared *PersistHost every resolved source's read offset is persisted through,
// so callers like otel/logprocessing persist through this same instance instead of building their own
// and silently overwriting these offsets.
func (rm *ReceiverManager) Persister() *PersistHost {
	return rm.persister
}

// DiagnosticArchive writes the persisted read-offset/registered-extension state to a diagnostic bundle.
// otel/logprocessing's Manager shares this exact *PersistHost (see Persister()) and already writes it
// through its own DiagnosticArchive when it exists -- agent.go only wires this one in when that manager
// doesn't exist (log shipping/Bleemeo disabled), so the same file is never written twice into one archive.
// Without either, a bundle taken while log-to-metric receivers are active would have no read-offset/
// extension state at all, even though that state is exactly what's needed to debug a stuck tail.
func (rm *ReceiverManager) DiagnosticArchive(_ context.Context, writer types.ArchiveWriter) error {
	return rm.persister.WriteToArchive(writer)
}

// RegisterExternalSizer folds FileSizers that ReceiverManager doesn't own into SaveState's
// "LogFileSizes" snapshot. fn is called fresh on every SaveState, so it can reflect receivers
// added/removed since registration.
func (rm *ReceiverManager) RegisterExternalSizer(fn func() []FileSizer) {
	rm.l.Lock()
	defer rm.l.Unlock()

	rm.externalSizerFuncs = append(rm.externalSizerFuncs, fn)
}

// managedSource is one resolved fan-out point (a configured receiver, or a label-opted-in container):
// its physical tail(s) and the consumer they feed into (nil if no SinkProvider wants it).
//
// Its start-new/stop-unwanted tail lifecycle bookkeeping (watching/sizeFnByFile/recvs/extIDs,
// containerRecvs/containerExtIDs/containerLogFile) independently parallels otel/logprocessing's own
// logReceiver (receiver.go) and containerReceiver (containers.go) -- they track a different shape
// (component.Component for a filter/batch/export chain, vs. plain receiver.Logs here) so they haven't been
// unified, but a fix to one's tail-start/stop or offset-forget logic likely applies to the others too.
type managedSource struct {
	name string
	kind SourceKind

	// fanout is nil if no SinkProvider wants this source.
	fanout consumer.Logs
	// container is set only for a SourceContainerLabel managedSource, so a later removal can notify
	// every provider via releaseProviders.
	container facts.Container
	// labels is the glouton.* labels resolved when fanout was last (re)built, set only for a
	// SourceContainerLabel managedSource. Compared against each scan's freshly-parsed labels so a live
	// label/annotation edit (e.g. glouton.log_metrics) triggers a rebuild instead of being silently ignored.
	labels containerLabels

	// operators applies to every tail under this source, before fan-out. Callers prepend
	// BuildContainerEnvelopeOperator() per container tail, since a mixed receiver only wants it there.
	operators []operator.Config
	// extraRaw is the raw passthrough into the fileconsumer config, nil for SourceContainerLabel.
	extraRaw map[string]any

	l sync.Mutex
	// watching/sizeFnByFile: cover both include-pattern and container-derived file tails.
	watching     map[string]ReceiverKind
	sizeFnByFile map[string]func() (int64, error)
	// recvs/extIDs: include-pattern file tails, keyed by file so one can be stopped (e.g. it stopped
	// matching any include pattern, or was rotated away) without touching the others.
	recvs  map[string][]receiver.Logs
	extIDs map[string][]component.ID

	// containerRecvs/containerExtIDs/containerLogFile: container-derived tails, keyed by container ID
	// so one can be stopped without touching the others.
	containerRecvs   map[string][]receiver.Logs
	containerExtIDs  map[string][]component.ID
	containerLogFile map[string]string
}

func newManagedSource(name string, kind SourceKind, operators []operator.Config, extraRaw map[string]any) *managedSource {
	return &managedSource{
		name:             name,
		kind:             kind,
		operators:        operators,
		extraRaw:         extraRaw,
		watching:         make(map[string]ReceiverKind),
		sizeFnByFile:     make(map[string]func() (int64, error)),
		recvs:            make(map[string][]receiver.Logs),
		extIDs:           make(map[string][]component.ID),
		containerRecvs:   make(map[string][]receiver.Logs),
		containerExtIDs:  make(map[string][]component.ID),
		containerLogFile: make(map[string]string),
	}
}

// SizesByFile implements FileSizer, for the cross-restart file-size cache. A single file's stat error
// (e.g. a permission flip after logrotate, or sudoStatFile's timeout under load) only skips that file --
// it must not discard every other file's already-successfully-read size for this managedSource, which a
// caller merging sizes from several FileSizer instances (see SaveLastFileSizesToCache) would otherwise
// drop entirely on any single error.
func (ms *managedSource) SizesByFile() (map[string]int64, error) {
	ms.l.Lock()
	defer ms.l.Unlock()

	sizes := make(map[string]int64, len(ms.sizeFnByFile))

	for logFile, sizeFn := range ms.sizeFnByFile {
		size, err := sizeFn()
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				logger.V(1).Printf("Can't get size of file %q (ignoring it): %v", logFile, err)
			}

			continue
		}

		sizes[logFile] = size
	}

	return sizes, nil
}

// receiverFields is the subset of a raw LogReceiver's keys ReceiverManager needs beyond
// config.LogReceiverSelectors: send_logs, log_format and operators.
type receiverFields struct {
	Include   []string
	SendLogs  *bool                 `mapstructure:"send_logs"`
	LogFormat string                `mapstructure:"log_format"`
	Operators []config.OTELOperator `mapstructure:"operators"`
}

func decodeReceiverFields(raw config.LogReceiver) (receiverFields, error) {
	var fields receiverFields

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{Result: &fields})
	if err != nil {
		return receiverFields{}, fmt.Errorf("creating decoder: %w", err)
	}

	if err := decoder.Decode(raw); err != nil {
		return receiverFields{}, err
	}

	return fields, nil
}

// askProviders asks every SinkProvider if it wants src, fanning out answers. Callers must hold rm.l.
func (rm *ReceiverManager) askProviders(ctx context.Context, src ResolvedSource) consumer.Logs {
	sinks := make([]consumer.Logs, 0, len(rm.providers))

	for _, p := range rm.providers {
		if sink, ok := p.WantSource(ctx, src); ok && sink != nil {
			sinks = append(sinks, sink)
		}
	}

	return FanoutLogs(sinks...)
}

// releaseProviders tells every SinkProvider to clean up for this container. Callers must hold rm.l.
func (rm *ReceiverManager) releaseProviders(ctx context.Context, container facts.Container) {
	for _, p := range rm.providers {
		p.ReleaseSource(ctx, container)
	}
}

// ensureReceiverSource returns or resolves the managedSource for a receiver. Callers must hold rm.l.
func (rm *ReceiverManager) ensureReceiverSource(ctx context.Context, name string, raw config.LogReceiver) (*managedSource, receiverFields, error) {
	fields, err := decodeReceiverFields(raw)
	if err != nil {
		return nil, receiverFields{}, fmt.Errorf("decoding config: %w", err)
	}

	if ms, found := rm.receivers[name]; found {
		return ms, fields, nil
	}

	sendLogs := rm.cfg.SendLogs
	if fields.SendLogs != nil {
		sendLogs = *fields.SendLogs
	}

	operators := rm.buildReceiverOperators(name, fields)

	ms := newManagedSource(name, SourceReceiver, operators, raw)
	ms.fanout = rm.askProviders(ctx, ResolvedSource{
		Kind:         SourceReceiver,
		Name:         name,
		ReceiverName: name,
		SendLogs:     sendLogs,
	})

	rm.receivers[name] = ms

	return ms, fields, nil
}

// buildReceiverOperators resolves a receiver's operators/log_format into stanza operator.Config,
// warning (not failing) on error.
func (rm *ReceiverManager) buildReceiverOperators(name string, fields receiverFields) []operator.Config {
	rawOps, err := ExpandOperators(fields.Operators, rm.knownLogFormats, false)
	if err != nil {
		logger.V(1).Printf("logsource: receiver %q: failed to expand operators: %v", name, err)

		rawOps = nil
	}

	operators, err := BuildOperators(rawOps)
	if err != nil {
		logger.V(1).Printf("logsource: receiver %q: failed to build operators: %v", name, err)

		operators = nil
	}

	if fields.LogFormat == "" {
		return operators
	}

	formatRawOps, found := rm.knownLogFormats[fields.LogFormat]
	if !found {
		logger.V(1).Printf("logsource: receiver %q requires an unknown log format %q", name, fields.LogFormat)

		return operators
	}

	formatOps, err := BuildOperators(formatRawOps)
	if err != nil {
		logger.V(1).Printf("logsource: receiver %q: failed to build log format %q: %v", name, fields.LogFormat, err)

		return operators
	}

	return append(operators, formatOps...)
}

// RescanReceivers resolves receivers and starts tails for new include patterns; idempotent.
func (rm *ReceiverManager) RescanReceivers(ctx context.Context) error {
	rm.l.Lock()
	defer rm.l.Unlock()

	var errs error

	for name, raw := range rm.cfg.Receivers {
		ms, fields, err := rm.ensureReceiverSource(ctx, name, raw)
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("receiver %q: %w", name, err))

			continue
		}

		if len(fields.Include) == 0 || ms.fanout == nil {
			continue
		}

		files := rm.resolveIncludeGlobs(name, fields.Include)

		if err := rm.startIncludeFiles(ctx, ms, name, files); err != nil {
			errs = errors.Join(errs, fmt.Errorf("receiver %q: %w", name, err))
		}

		wanted := make(map[string]bool, len(files))
		for _, f := range files {
			wanted[f] = true
		}

		rm.stopUnwantedIncludeFiles(ctx, ms, wanted)
	}

	return errs
}

// resolveIncludeGlobs expands patterns into hostroot-stripped, symlink-resolved file paths (needed
// for e.g. Kubernetes' /var/log/containers/* -> /var/log/pods/* symlinks).
func (rm *ReceiverManager) resolveIncludeGlobs(name string, patterns []string) []string {
	return ResolveIncludeGlobs(rm.hostroot, patterns, func(msg string) {
		logger.V(1).Printf("logsource: receiver %q: %s", name, msg)
	})
}

// startIncludeFiles starts receivers for new files in ms; idempotent. Files are set up one at a time (not
// batched into a single SetupLogReceiverFactories call) so each file's receiver/extension can be tracked
// and later stopped independently in stopUnwantedIncludeFiles, without touching the others.
func (rm *ReceiverManager) startIncludeFiles(ctx context.Context, ms *managedSource, name string, files []string) error {
	ms.l.Lock()
	defer ms.l.Unlock()

	var errs error

	for _, f := range files {
		if _, ok := ms.watching[f]; ok {
			continue
		}

		if err := rm.startIncludeFile(ctx, ms, name, f); err != nil {
			errs = errors.Join(errs, fmt.Errorf("file %q: %w", f, err))
		}
	}

	return errs
}

// startIncludeFile starts a single include-pattern file's receiver under ms. Callers must hold ms.l.
func (rm *ReceiverManager) startIncludeFile(ctx context.Context, ms *managedSource, name, file string) error {
	var newExtIDs []component.ID

	makeStorageFn := func(logFile string) *component.ID {
		id := rm.persister.NewPersistentExt(name + "/" + logFile)
		newExtIDs = append(newExtIDs, id)

		return &id
	}

	factories, readFiles, execFiles, sizeFns, err := SetupLogReceiverFactories(
		[]string{file}, rm.hostroot, ms.operators, rm.lastFileSizes, rm.commandRunner, makeStorageFn, rm.statFile, nil, ms.extraRaw,
	)
	if err != nil {
		rm.persister.RemovePersistentExts(newExtIDs)

		return fmt.Errorf("setting up receiver factories: %w", err)
	}

	if len(factories) == 0 {
		rm.persister.RemovePersistentExts(newExtIDs)

		return nil
	}

	newRecvs, err := rm.createAndStartReceivers(ctx, factories, ms.fanout)
	if err != nil {
		rm.persister.RemovePersistentExts(newExtIDs)

		return err
	}

	ms.recvs[file] = append(ms.recvs[file], newRecvs...)
	ms.extIDs[file] = append(ms.extIDs[file], newExtIDs...)
	maps.Copy(ms.sizeFnByFile, sizeFns)

	switch {
	case len(readFiles) == 1:
		ms.watching[file] = ReceiverFileLog
	case len(execFiles) == 1:
		ms.watching[file] = ReceiverExecLog
	}

	return nil
}

// stopUnwantedIncludeFiles stops every include-pattern tail under ms whose file isn't in wanted (e.g. it
// stopped matching any include pattern, or was rotated/deleted away), forgetting its persisted offset too:
// symmetric to stopUnwantedContainerTails.
func (rm *ReceiverManager) stopUnwantedIncludeFiles(ctx context.Context, ms *managedSource, wanted map[string]bool) {
	ms.l.Lock()
	defer ms.l.Unlock()

	for file, recvs := range ms.recvs {
		if wanted[file] {
			continue
		}

		shutdownReceivers(ctx, recvs)
		rm.persister.RemovePersistentExtsAndForget(ms.extIDs[file])

		delete(ms.recvs, file)
		delete(ms.extIDs, file)
		delete(ms.watching, file)
		delete(ms.sizeFnByFile, file)
	}
}

func (rm *ReceiverManager) createAndStartReceivers(
	ctx context.Context,
	factories map[receiver.Factory]component.Config,
	sink consumer.Logs,
) ([]receiver.Logs, error) {
	newRecvs := make([]receiver.Logs, 0, len(factories))

	for factory, recvCfg := range factories {
		recv, err := factory.CreateLogs(
			ctx,
			receiver.Settings{
				ID:                component.NewIDWithName(factory.Type(), uuid.NewString()),
				TelemetrySettings: rm.telemetry,
			},
			recvCfg,
			sink,
		)
		if err != nil {
			shutdownReceivers(ctx, newRecvs)

			return nil, fmt.Errorf("build receiver: %w", err)
		}

		if err := recv.Start(ctx, rm.persister); err != nil {
			shutdownReceivers(ctx, newRecvs)

			return nil, fmt.Errorf("start receiver: %w", err)
		}

		newRecvs = append(newRecvs, recv)
	}

	return newRecvs, nil
}

func shutdownReceivers(ctx context.Context, recvs []receiver.Logs) {
	for _, recv := range recvs {
		_ = recv.Shutdown(ctx)
	}
}

// containerMatcher is a configured receiver's container-selection fields,
// precomputed once per UpdateContainers call.
type containerMatcher struct {
	name               string
	raw                config.LogReceiver
	containerName      string
	containerSelectors map[string]string
}

// UpdateContainers matches containers to receivers by name/selectors, falling back to glouton.* labels.
func (rm *ReceiverManager) UpdateContainers(ctx context.Context, containers []facts.Container) {
	rm.l.Lock()
	defer rm.l.Unlock()

	matchers := rm.containerMatchers()

	currentByReceiver := make(map[string]map[string]bool, len(matchers))
	claimed := make(map[string]bool, len(containers))

	for _, ctr := range containers {
		if ctr.LogPath() == "" || IsContainerConfigExcluded(rm.cfg, ctr) {
			continue
		}

		for _, m := range matchers {
			if !MatchesContainerRule(ctr, m.containerName, m.containerSelectors) {
				continue
			}

			claimed[ctr.ID()] = true

			if currentByReceiver[m.name] == nil {
				currentByReceiver[m.name] = make(map[string]bool)
			}

			currentByReceiver[m.name][ctr.ID()] = true

			ms, _, err := rm.ensureReceiverSource(ctx, m.name, m.raw)
			if err != nil {
				logger.V(1).Printf("logsource: receiver %q: %v", m.name, err)

				continue
			}

			operators := append([]operator.Config{BuildContainerEnvelopeOperator()}, ms.operators...)

			if err := rm.startContainerTail(ctx, ms, ctr, operators, m.name); err != nil {
				logger.V(1).Printf("logsource: receiver %q: container %s (%s): %v", m.name, ctr.ContainerName(), ctr.ID(), err)
			}
		}
	}

	for _, ms := range rm.receivers {
		rm.stopUnwantedContainerTails(ctx, ms, currentByReceiver[ms.name])
	}

	rm.updateLabelContainers(ctx, containers, claimed)
}

// containerMatchers returns container-selection fields from all receivers. Callers must hold rm.l.
func (rm *ReceiverManager) containerMatchers() []containerMatcher {
	var matchers []containerMatcher

	for name, raw := range rm.cfg.Receivers {
		_, containerName, containerSelectors, _, err := config.LogReceiverSelectors(raw)
		if err != nil {
			logger.V(1).Printf("logsource: receiver %q: %v", name, err)

			continue
		}

		if containerName == "" && len(containerSelectors) == 0 {
			continue
		}

		matchers = append(matchers, containerMatcher{
			name: name, raw: raw, containerName: containerName, containerSelectors: containerSelectors,
		})
	}

	return matchers
}

// updateLabelContainers resolves glouton.* label fallbacks for unclaimed containers. Callers must hold rm.l.
func (rm *ReceiverManager) updateLabelContainers(ctx context.Context, containers []facts.Container, claimed map[string]bool) {
	current := make(map[string]bool, len(containers))

	for _, ctr := range containers {
		if ctr.LogPath() == "" || claimed[ctr.ID()] || IsContainerExcluded(rm.cfg, ctr) {
			continue
		}

		labels := parseContainerLabels(ctr)

		ms, found := rm.byContainer[ctr.ID()]
		if found && !ms.labels.equal(labels) {
			// The container's glouton.* labels/annotations changed since last scan (e.g. a live edit,
			// not a recreation): tear down and rebuild so the new send_logs/log_metrics/log_format take
			// effect, instead of silently keeping the stale fanout/operators forever.
			rm.shutdownSource(ctx, ms, false)
			rm.releaseProviders(ctx, ms.container)
			delete(rm.byContainer, ctr.ID())

			found = false
		}

		if !found {
			rawOps := resolveContainerLogFormat(ctr.ContainerName(), labels.LogFormat, rm.cfg.ContainerFormat, rm.knownLogFormats)

			operators, err := BuildOperators(rawOps)
			if err != nil {
				logger.V(1).Printf("logsource: container %s (%s): failed to build log format operators: %v", ctr.ContainerName(), ctr.ID(), err)

				operators = nil
			}

			ms = newManagedSource(
				ctr.ContainerName(), SourceContainerLabel,
				append([]operator.Config{BuildContainerEnvelopeOperator()}, operators...), nil,
			)
			// Default here is auto_discovery.container_and_service_enable, not OpenTelemetry.SendLogs,
			// since this container was never explicitly configured. Only affects SendLogs, not LogMetricsRule.
			ms.container = ctr
			ms.labels = labels
			ms.fanout = rm.askProviders(ctx, ResolvedSource{
				Kind:           SourceContainerLabel,
				Name:           ctr.ContainerName(),
				Container:      ctr,
				SendLogs:       labels.resolveSendLogs(rm.cfg.AutoDiscovery.ContainerAndServiceEnable),
				LogMetricsRule: labels.LogMetrics,
			})

			rm.byContainer[ctr.ID()] = ms
		}

		current[ctr.ID()] = true

		if err := rm.startContainerTail(ctx, ms, ctr, ms.operators, ""); err != nil {
			logger.V(1).Printf("logsource: container %s (%s): %v", ctr.ContainerName(), ctr.ID(), err)
		}
	}

	for id, ms := range rm.byContainer {
		if current[id] {
			continue
		}

		// The container is gone for good (not just a restart): forget its offset too.
		rm.shutdownSource(ctx, ms, true)
		rm.releaseProviders(ctx, ms.container)
		delete(rm.byContainer, id)
	}
}

// containerPersistName builds a persisted-offset identity; namespace is "" for label-detected, or a receiver name for independent offsets.
func containerPersistName(namespace, containerID, logFile string) string {
	if namespace == "" {
		return "container/" + containerID + "/" + logFile
	}

	return "container/" + namespace + "/" + containerID + "/" + logFile
}

// startContainerTail starts a tail for ctr under ms if wanted and not already running.
func (rm *ReceiverManager) startContainerTail(
	ctx context.Context,
	ms *managedSource,
	ctr facts.Container,
	operators []operator.Config,
	persistNamespace string,
) error {
	ms.l.Lock()
	defer ms.l.Unlock()

	if _, already := ms.containerLogFile[ctr.ID()]; already {
		return nil
	}

	if ms.fanout == nil {
		return nil
	}

	logFilePath := ctr.LogPath()
	if logFilePath == "" {
		return errNoContainerLogFile
	}

	// Resolve hostroot symlinks (e.g. Kubernetes' /var/log/containers/* -> /var/log/pods/*), or the
	// symlink target would be read ignoring hostroot.
	realFile := logFilePath
	if rm.hostroot != "/" {
		realFile = hostrootsymlink.EvalSymlinks(rm.hostroot, realFile)
	}

	attributes := BuildContainerAttributes(ctx, ctr)

	var newExtIDs []component.ID

	makeStorageFn := func(logFile string) *component.ID {
		id := rm.persister.NewPersistentExt(containerPersistName(persistNamespace, ctr.ID(), logFile))
		newExtIDs = append(newExtIDs, id)

		return &id
	}

	factories, readFiles, execFiles, sizeFns, err := SetupLogReceiverFactories(
		[]string{realFile}, rm.hostroot, operators, rm.lastFileSizes, rm.commandRunner, makeStorageFn, rm.statFile, attributes.AsMap(), nil,
	)
	if err != nil {
		rm.persister.RemovePersistentExts(newExtIDs)

		return fmt.Errorf("setting up receiver factories: %w", err)
	}

	if len(factories) != 1 {
		rm.persister.RemovePersistentExts(newExtIDs)

		return errNoContainerLogFile
	}

	recvs, err := rm.createAndStartReceivers(ctx, factories, ms.fanout)
	if err != nil {
		rm.persister.RemovePersistentExts(newExtIDs)

		return err
	}

	ms.containerRecvs[ctr.ID()] = recvs
	ms.containerExtIDs[ctr.ID()] = newExtIDs
	ms.containerLogFile[ctr.ID()] = realFile
	maps.Copy(ms.sizeFnByFile, sizeFns)

	switch {
	case len(readFiles) == 1:
		ms.watching[realFile] = ReceiverFileLog
	case len(execFiles) == 1:
		ms.watching[realFile] = ReceiverExecLog
	}

	return nil
}

// stopUnwantedContainerTails stops every container tail under ms whose
// container ID isn't in wanted.
func (rm *ReceiverManager) stopUnwantedContainerTails(ctx context.Context, ms *managedSource, wanted map[string]bool) {
	ms.l.Lock()
	defer ms.l.Unlock()

	for id, recvs := range ms.containerRecvs {
		if wanted[id] {
			continue
		}

		shutdownReceivers(ctx, recvs)
		// The container is gone for good (not just a restart): forget its offset too.
		rm.persister.RemovePersistentExtsAndForget(ms.containerExtIDs[id])

		logFile := ms.containerLogFile[id]
		delete(ms.watching, logFile)
		delete(ms.sizeFnByFile, logFile)
		delete(ms.containerRecvs, id)
		delete(ms.containerExtIDs, id)
		delete(ms.containerLogFile, id)
	}
}

// shutdownSource stops every physical tail (include-file and container-derived) under ms. forget must be
// false for a graceful/resumable shutdown (e.g. process restart), where the offset must survive so the next
// run resumes from it, and true only when ms is gone for good (e.g. its container was removed).
func (rm *ReceiverManager) shutdownSource(ctx context.Context, ms *managedSource, forget bool) {
	ms.l.Lock()
	defer ms.l.Unlock()

	removeExts := rm.persister.RemovePersistentExts
	if forget {
		removeExts = rm.persister.RemovePersistentExtsAndForget
	}

	for _, recvs := range ms.recvs {
		shutdownReceivers(ctx, recvs)
	}

	for _, extIDs := range ms.extIDs {
		removeExts(extIDs)
	}

	for id, recvs := range ms.containerRecvs {
		shutdownReceivers(ctx, recvs)
		removeExts(ms.containerExtIDs[id])
	}
}

// Shutdown stops every physical tail this ReceiverManager owns, preserving every offset for the next restart.
func (rm *ReceiverManager) Shutdown(ctx context.Context) {
	rm.l.Lock()
	defer rm.l.Unlock()

	for _, ms := range rm.receivers {
		rm.shutdownSource(ctx, ms, false)
	}

	for _, ms := range rm.byContainer {
		rm.shutdownSource(ctx, ms, false)
	}
}

// SaveState persists every source's read offset and refreshes the lastFileSizes cache. Call it
// periodically and at shutdown.
func (rm *ReceiverManager) SaveState() {
	rm.persister.SaveToState(rm.state)

	rm.l.Lock()
	sizers := make([]FileSizer, 0, len(rm.receivers)+len(rm.byContainer))

	for _, ms := range rm.receivers {
		sizers = append(sizers, ms)
	}

	for _, ms := range rm.byContainer {
		sizers = append(sizers, ms)
	}

	externalFuncs := slices.Clone(rm.externalSizerFuncs)
	rm.l.Unlock()

	// Called outside rm.l: an external sizer locks its own state and may be slow (it stats every
	// watched file).
	for _, fn := range externalFuncs {
		sizers = append(sizers, fn()...)
	}

	SaveLastFileSizesToCache(rm.state, LogFileSizesCacheKey, sizers)
}

// NetworkWants returns one NetworkWant per configured receiver with network participation, its
// Consumer already the fan-out of every SinkProvider that wants it.
func (rm *ReceiverManager) NetworkWants(ctx context.Context) []NetworkWant {
	rm.l.Lock()
	defer rm.l.Unlock()

	wants := make([]NetworkWant, 0, len(rm.cfg.Receivers))

	for name, raw := range rm.cfg.Receivers {
		_, _, _, fromListeners, err := config.LogReceiverSelectors(raw)
		if err != nil {
			continue
		}

		if len(fromListeners) == 0 {
			continue
		}

		ms, _, err := rm.ensureReceiverSource(ctx, name, raw)
		if err != nil {
			logger.V(1).Printf("logsource: receiver %q: %v", name, err)

			continue
		}

		wants = append(wants, NetworkWant{Consumer: ms.fanout, Receivers: fromListeners})
	}

	return wants
}
