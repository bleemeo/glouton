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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/utils/hostrootsymlink"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/uuid"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
)

// Persistence identity, deliberately identical to otel/logprocessing's
// current (live, production) values: once otel/logprocessing is rewired onto
// ReceiverManager, an already-deployed agent's read offsets must survive the
// switch unchanged.
const (
	persistStorageType      = "glouton_log_metadata_storage"
	logFileMetadataCacheKey = "LogFileMetadata"
	logFileSizesCacheKey    = "LogFileSizes"
	persistArchivePath      = "log-receivers/persister.json"

	// saveThrottle limits how often a single file's read offset is pushed
	// into the persister's in-memory map, matching otel/logprocessing's
	// current (live) save policy.
	saveThrottle = time.Minute
)

var errNoContainerLogFile = errors.New("no log file found for container")

// ReceiverManager owns exactly one physical file/container tail per
// configured log.opentelemetry.receivers entry (plus one per container
// opted in solely via glouton.* labels), and fans each one's parsed records
// out to whichever registered SinkProvider wants them (see RegisterSinkProvider
// and FanoutLogs). It replaces the previously-independent receiver/tailing/
// container-watching runtimes of otel/logprocessing and otel/logmetrics.
//
// A receiver's log_format/operators are resolved once, upstream of the
// fan-out point, so every SinkProvider sees identically-parsed records --
// this is deliberate (see the package's design notes), not an oversight.
//
// The zero value isn't usable; construct with NewReceiverManager. All
// exported methods are safe for concurrent use.
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

	l           sync.Mutex
	providers   []SinkProvider
	receivers   map[string]*managedSource // by OpenTelemetry.Receivers key
	byContainer map[string]*managedSource // by container ID, SourceContainerLabel only
}

// NewReceiverManager builds a ReceiverManager for cfg. It doesn't start
// anything yet -- register every SinkProvider first (RegisterSinkProvider),
// then call RescanReceivers/UpdateContainers to actually resolve sources and
// start tailing.
func NewReceiverManager(cfg config.OpenTelemetry, hostroot string, state bleemeoTypes.State, commandRunner CommandRunner) (*ReceiverManager, error) {
	knownLogFormats, err := ExpandLogFormats(cfg.KnownLogFormats)
	if err != nil {
		logger.V(1).Printf("logsource: failed to expand known log formats, log_format won't be usable: %v", err)
	}

	persister, err := NewPersistHost(state, PersistConfig{
		StorageType:  persistStorageType,
		CacheKey:     logFileMetadataCacheKey,
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
		lastFileSizes:   GetLastFileSizesFromCache(state, logFileSizesCacheKey),
		receivers:       make(map[string]*managedSource),
		byContainer:     make(map[string]*managedSource),
	}, nil
}

// RegisterSinkProvider registers p as a candidate consumer of every source
// ReceiverManager resolves from now on. Must be called before the first
// RescanReceivers/UpdateContainers/NetworkWants call: a source resolved
// before p registers never asks it (WantSource is only called once, the
// first time a source is seen).
func (rm *ReceiverManager) RegisterSinkProvider(p SinkProvider) {
	rm.l.Lock()
	defer rm.l.Unlock()

	rm.providers = append(rm.providers, p)
}

// managedSource is one resolved fan-out point (one configured receiver, or
// one container opted in via labels): the physical tail(s) feeding it, and
// the (possibly nil, if no SinkProvider wants it) consumer they feed into.
type managedSource struct {
	name string
	kind SourceKind

	// fanout is nil if no registered SinkProvider wants this source; no
	// physical tail is ever started in that case.
	fanout consumer.Logs

	// operators applies to every physical tail under this source, before the
	// fan-out point. It never includes the container envelope operator
	// itself -- callers prepend BuildContainerEnvelopeOperator() per
	// container tail, since a mixed receiver (include + container_selectors)
	// only wants it on the container-derived tails.
	operators []operator.Config
	// extraRaw is the raw passthrough into the real fileconsumer config, nil
	// for a SourceContainerLabel source (no receiver entry to paste one into).
	extraRaw map[string]any

	l sync.Mutex
	// watching/sizeFnByFile/recvs/extIDs: include-pattern file tails, flat
	// (never removed individually -- only whole-source shutdown does).
	watching     map[string]ReceiverKind
	sizeFnByFile map[string]func() (int64, error)
	recvs        []receiver.Logs
	extIDs       []component.ID

	// containerRecvs/containerExtIDs/containerLogFile: container-derived
	// tails, keyed by container ID so one can be stopped without touching
	// the others (container disappears, or a receiver's selector no longer
	// matches it).
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
		containerRecvs:   make(map[string][]receiver.Logs),
		containerExtIDs:  make(map[string][]component.ID),
		containerLogFile: make(map[string]string),
	}
}

// SizesByFile implements FileSizer, for the cross-restart "have we ever seen
// this file" cache (see GetLastFileSizesFromCache/SaveLastFileSizesToCache).
func (ms *managedSource) SizesByFile() (map[string]int64, error) {
	ms.l.Lock()
	defer ms.l.Unlock()

	sizes := make(map[string]int64, len(ms.sizeFnByFile))

	for logFile, sizeFn := range ms.sizeFnByFile {
		size, err := sizeFn()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}

			return nil, err
		}

		sizes[logFile] = size
	}

	return sizes, nil
}

// receiverFields is the subset of a raw LogReceiver's keys ReceiverManager
// itself needs beyond config.LogReceiverSelectors (which only covers
// include/container_name/container_selectors/network): send_logs, log_format
// and operators, following the same narrow-decode pattern as
// decodeRawReceiverConfig.
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

// askProviders asks every registered SinkProvider whether it wants src,
// returning the fan-out of every non-nil answer (nil if none do -- see
// FanoutLogs). Callers must hold rm.l. ctx is forwarded from whichever
// caller resolved this source (RescanReceivers/UpdateContainers/
// NetworkWants) -- it's the long-lived context any component a provider
// builds must be tied to, not a short-request-scoped one.
func (rm *ReceiverManager) askProviders(ctx context.Context, src ResolvedSource) consumer.Logs {
	sinks := make([]consumer.Logs, 0, len(rm.providers))

	for _, p := range rm.providers {
		if sink, ok := p.WantSource(ctx, src); ok && sink != nil {
			sinks = append(sinks, sink)
		}
	}

	return FanoutLogs(sinks...)
}

// ensureReceiverSource returns the managedSource for a configured receiver,
// resolving it (deciding once whether any SinkProvider wants it, and
// building its shared operators) the first time it's seen. Callers must hold
// rm.l.
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

// buildReceiverOperators resolves a receiver's operators/log_format fields
// into stanza operator.Config, warning (not failing) on an expansion/build
// error -- the receiver still starts, just without that parsing step.
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

// RescanReceivers (re)resolves every configured receiver -- including
// deciding, the first time each is seen, whether any SinkProvider wants it --
// and starts a file tail for any newly-matching include pattern. Safe (and
// expected) to call repeatedly: an already-running tail is left untouched, so
// the caller can wire this to the same periodic tick otel/logprocessing used
// to run its own receivers on.
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
	}

	return errs
}

// resolveIncludeGlobs expands patterns into actual, hostroot-stripped,
// symlink-resolved file paths -- based on otel/logprocessing's richer
// version (logReceiver.update), which (unlike otel/logmetrics's) resolves
// hostroot symlinks, needed for e.g. Kubernetes' /var/log/containers/*
// -> /var/log/pods/* symlinks.
func (rm *ReceiverManager) resolveIncludeGlobs(name string, patterns []string) []string {
	hasHostRoot := len(rm.hostroot) > len(string(os.PathSeparator))

	seen := make(map[string]bool)

	var files []string

	for _, pattern := range patterns {
		matching, err := doublestar.FilepathGlob(
			filepath.Join(rm.hostroot, pattern),
			doublestar.WithFilesOnly(),
			doublestar.WithFailOnIOErrors(),
		)
		if err != nil {
			if errors.Is(err, doublestar.ErrBadPattern) {
				logger.V(1).Printf("logsource: receiver %q: file %q: %v", name, pattern, err)

				continue
			}

			if errors.Is(err, fs.ErrPermission) {
				if hasHostRoot {
					logger.V(1).Printf("logsource: receiver %q: resolving file %q: %v (ignoring it)", name, pattern, err)

					continue
				}

				if strings.Contains(pattern, "*") {
					logger.V(1).Printf(
						"logsource: receiver %q: resolving file pattern %q: %v (ignoring it; Glouton can read a protected log "+
							"file via sudo tail, but only for an explicit path, not a glob pattern)", name, pattern, err,
					)

					continue
				}

				matching = []string{pattern} // still a chance via sudo tail
			} else {
				logger.V(1).Printf("logsource: receiver %q: file %q: %v", name, pattern, err)

				continue
			}
		} else if hasHostRoot {
			for i, logFile := range matching {
				matching[i] = strings.TrimPrefix(logFile, rm.hostroot)
			}
		}

		for _, file := range matching {
			realFile := file
			if rm.hostroot != "/" {
				realFile = hostrootsymlink.EvalSymlinks(rm.hostroot, realFile)
			}

			if !seen[realFile] {
				seen[realFile] = true

				files = append(files, realFile)
			}
		}
	}

	return files
}

// startIncludeFiles starts a receiver for each of files not already being
// watched by ms. Already-running tails are left untouched.
func (rm *ReceiverManager) startIncludeFiles(ctx context.Context, ms *managedSource, name string, files []string) error {
	ms.l.Lock()
	defer ms.l.Unlock()

	var newFiles []string

	for _, f := range files {
		if _, ok := ms.watching[f]; !ok {
			newFiles = append(newFiles, f)
		}
	}

	if len(newFiles) == 0 {
		return nil
	}

	var newExtIDs []component.ID

	makeStorageFn := func(logFile string) *component.ID {
		id := rm.persister.NewPersistentExt(name + "/" + logFile)
		newExtIDs = append(newExtIDs, id)

		return &id
	}

	factories, readFiles, execFiles, sizeFns, err := SetupLogReceiverFactories(
		newFiles, rm.hostroot, ms.operators, rm.lastFileSizes, rm.commandRunner, makeStorageFn, rm.statFile, nil, ms.extraRaw,
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

	ms.recvs = append(ms.recvs, newRecvs...)
	ms.extIDs = append(ms.extIDs, newExtIDs...)
	maps.Copy(ms.sizeFnByFile, sizeFns)

	for _, f := range readFiles {
		ms.watching[f] = ReceiverFileLog
	}

	for _, f := range execFiles {
		ms.watching[f] = ReceiverExecLog
	}

	return nil
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

// UpdateContainers is the single entry point for reacting to container
// add/remove/change: it resolves, for every live (non-excluded) container,
// which configured receivers' container_name/container_selectors match it
// (starting/stopping per-container tails accordingly), and falls back to
// glouton.* container-label detection for any container matched by no
// receiver at all. It replaces otel/logmetrics's former independent
// 1-minute container poll: call it from the same discovery-triggered path
// otel/logprocessing already reacts to, so metrics reacts to container
// changes exactly as fast as shipping does.
func (rm *ReceiverManager) UpdateContainers(ctx context.Context, containers []facts.Container) {
	rm.l.Lock()
	defer rm.l.Unlock()

	matchers := rm.containerMatchers()

	currentByReceiver := make(map[string]map[string]bool, len(matchers))
	claimed := make(map[string]bool, len(containers))

	for _, ctr := range containers {
		if ctr.LogPath() == "" || IsContainerExcluded(rm.cfg, ctr) {
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

// containerMatchers returns the container-selection fields of every
// configured receiver that watches containers at all. Callers must hold rm.l.
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

// updateLabelContainers resolves the glouton.* label fallback for every
// live, non-excluded container claimed by no receiver, starting/stopping
// per-container sources as they appear, change ownership, or disappear.
// Callers must hold rm.l.
func (rm *ReceiverManager) updateLabelContainers(ctx context.Context, containers []facts.Container, claimed map[string]bool) {
	current := make(map[string]bool, len(containers))

	for _, ctr := range containers {
		if ctr.LogPath() == "" || claimed[ctr.ID()] || IsContainerExcluded(rm.cfg, ctr) {
			continue
		}

		labels := parseContainerLabels(ctr)

		ms, found := rm.byContainer[ctr.ID()]
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
			// The fallback default here is auto_discovery.container_and_service_enable,
			// not OpenTelemetry.SendLogs: SendLogs is the default for receivers
			// (sources the user already explicitly configured), whereas a
			// container reached ONLY through this label-fallback path was never
			// explicitly configured at all -- auto_discovery is the toggle that
			// decides whether such untouched containers ship by default, exactly
			// as it does today. Metrics (LogMetricsRule) are unaffected either
			// way: this only decides SendLogs.
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

		rm.shutdownSource(ctx, ms)
		delete(rm.byContainer, id)
	}
}

// containerPersistName builds a stable persisted-offset identity for a
// container's tail. namespace is "" for a label-detected container (matching
// otel/logprocessing's current key exactly, for offset continuity across the
// migration -- today, a container is always reached through exactly one
// path), or a receiver name when reached through an explicit
// container_name/container_selectors match (namespaced so the same
// container matched by two different receivers, per design, gets two
// independent read offsets).
func containerPersistName(namespace, containerID, logFile string) string {
	if namespace == "" {
		return "container/" + containerID + "/" + logFile
	}

	return "container/" + namespace + "/" + containerID + "/" + logFile
}

// startContainerTail starts ctr's log tail under ms, unless it's already
// running or ms.fanout is nil (nobody wants this source: don't waste a file
// handle on it).
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

	// Resolve hostroot symlinks (e.g. Kubernetes' /var/log/containers/* ->
	// /var/log/pods/*): without this, Glouton would try to read
	// "<hostroot>/var/log/pods/..." by following the symlink from inside its
	// own mount namespace, ignoring hostroot.
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
		rm.persister.RemovePersistentExts(ms.containerExtIDs[id])

		logFile := ms.containerLogFile[id]
		delete(ms.watching, logFile)
		delete(ms.sizeFnByFile, logFile)
		delete(ms.containerRecvs, id)
		delete(ms.containerExtIDs, id)
		delete(ms.containerLogFile, id)
	}
}

// shutdownSource stops every physical tail (include-file and container-derived)
// under ms.
func (rm *ReceiverManager) shutdownSource(ctx context.Context, ms *managedSource) {
	ms.l.Lock()
	defer ms.l.Unlock()

	shutdownReceivers(ctx, ms.recvs)
	rm.persister.RemovePersistentExts(ms.extIDs)

	for id, recvs := range ms.containerRecvs {
		shutdownReceivers(ctx, recvs)
		rm.persister.RemovePersistentExts(ms.containerExtIDs[id])
	}
}

// Shutdown stops every physical tail this ReceiverManager owns.
func (rm *ReceiverManager) Shutdown(ctx context.Context) {
	rm.l.Lock()
	defer rm.l.Unlock()

	for _, ms := range rm.receivers {
		rm.shutdownSource(ctx, ms)
	}

	for _, ms := range rm.byContainer {
		rm.shutdownSource(ctx, ms)
	}
}

// SaveState persists every source's read offset (so a restart resumes
// tailing where it left off) and refreshes the coarser cross-restart
// lastFileSizes cache. Call it periodically and at shutdown.
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
	rm.l.Unlock()

	SaveLastFileSizesToCache(rm.state, logFileSizesCacheKey, sizers)
}

// NetworkWants returns one NetworkWant per configured receiver with a
// network: participation, its Consumer already the fan-out of every
// SinkProvider that wants it -- so the caller (see PlanSharedNetworkReceivers)
// only ever has one want per receiver to plan, regardless of how many
// features consume it.
func (rm *ReceiverManager) NetworkWants(ctx context.Context) []NetworkWant {
	rm.l.Lock()
	defer rm.l.Unlock()

	wants := make([]NetworkWant, 0, len(rm.cfg.Receivers))

	for name, raw := range rm.cfg.Receivers {
		_, _, _, network, err := config.LogReceiverSelectors(raw)
		if err != nil {
			continue
		}

		receiverNames := config.ResolveNetworkReceivers(network.Enable, network.Receivers)
		if len(receiverNames) == 0 {
			continue
		}

		ms, _, err := rm.ensureReceiverSource(ctx, name, raw)
		if err != nil {
			logger.V(1).Printf("logsource: receiver %q: %v", name, err)

			continue
		}

		wants = append(wants, NetworkWant{Consumer: ms.fanout, Receivers: receiverNames})
	}

	return wants
}
