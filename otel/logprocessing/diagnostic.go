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
	"cmp"
	"encoding/json"
	"slices"
	"time"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/otel/logsource"
	"github.com/bleemeo/glouton/types"
)

const shippingArchivePath = "log-processing/shipping.json"

// shippingDiagnostic is the content of shippingArchivePath.
type shippingDiagnostic struct {
	Enabled bool
	// Reason tells why shipping isn't enabled.
	Reason       string                                   `json:",omitempty"`
	BackPressure *bleemeoTypes.LogsBackPressureDiagnostic `json:",omitempty"`

	ShippedLogCount               int64
	ShippedLogThroughputPerMinute int
	// BackPressureRefusedLogCount counts records refused while shipping buffers; their input retries
	// them, so a record can be counted several times.
	BackPressureRefusedLogCount   int64
	BackPressureDiscardedLogCount int64
	// ExportFailedLogCount counts records lost because their export failed.
	ExportFailedLogCount int64
	LastExportError      string     `json:",omitempty"`
	LastExportErrorAt    *time.Time `json:",omitempty"`
}

// WriteShippingDiagnostic writes the state of log shipping. man is nil when shipping isn't running,
// disabledReason then telling why.
func WriteShippingDiagnostic(
	archive types.ArchiveWriter,
	man *Manager,
	disabledReason string,
	backPressure *bleemeoTypes.LogsBackPressureDiagnostic,
) error {
	diag := shippingDiagnostic{
		Enabled:      man != nil,
		BackPressure: backPressure,
	}

	if man == nil {
		diag.Reason = disabledReason
	} else {
		diag.ShippedLogCount = man.pipeline.logProcessedCount.Load()
		diag.ShippedLogThroughputPerMinute = man.pipeline.logThroughputMeter.Total()
		diag.BackPressureRefusedLogCount = man.pipeline.backPressureRefusedCount.Load()
		diag.BackPressureDiscardedLogCount = man.pipeline.backPressureDiscardedCount.Load()
		diag.ExportFailedLogCount = man.pipeline.exportFailedCount.Load()

		if lastErr := man.pipeline.lastExportErr.Load(); lastErr != nil {
			diag.LastExportError = lastErr.Err
			diag.LastExportErrorAt = &lastErr.At
		}
	}

	file, err := archive.Create(shippingArchivePath)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(diag)
}

// DiagnosticSources describes the sources this package reads itself (discovered services, journald,
// syslog, auditd), and the shipping counters of the ReceiverManager-owned sources it ships.
func (man *Manager) DiagnosticSources() ([]logsource.SourceDiagnostic, map[logsource.SourceKey]logsource.ShippingDiagnostic) {
	man.l.Lock()
	defer man.l.Unlock()

	man.pipeline.l.Lock()
	defer man.pipeline.l.Unlock()

	containers := man.containerRecv.diagnostic()
	sources := make([]logsource.SourceDiagnostic, 0, len(man.watchedServices)+len(man.skippedSource)+4)

	for key, diag := range man.watchedServices {
		sources = append(sources, man.watchedServiceDiagnostics(key, diag, containers)...)
	}

	for _, skipped := range man.skippedSource {
		sources = append(sources, logsource.SourceDiagnostic{
			Name:      serviceSourceName(skipped.ServiceKey),
			Origin:    "service",
			Status:    skipped.SkipStatus,
			Reason:    skipped.SkipReason,
			Container: describeContainer(skipped.ContainerName, skipped.ContainerID),
		})
	}

	sources = append(sources, man.autoDiscoveryDiagnostics()...)

	return sources, man.fanoutShippingDiagnosticsLocked()
}

// watchedServiceDiagnostics describes a watched service: one source per log file, or a single one when
// it runs in a container. Callers must hold man.l and man.pipeline.l.
func (man *Manager) watchedServiceDiagnostics(
	key discovery.NameInstance,
	diag sourceDiagnostic,
	containers map[string]containerDiagnosticInformation,
) []logsource.SourceDiagnostic {
	base := logsource.SourceDiagnostic{
		Name:      serviceSourceName(key),
		Origin:    "service",
		Container: describeContainer(diag.ContainerName, diag.ContainerID),
	}

	var configErrs []string

	if diag.LogProcessingError != "" {
		configErrs = []string{diag.LogProcessingError}
	}

	if diag.ContainerID != "" {
		src := base
		src.Errors = configErrs

		if len(diag.LogProcessing) > 0 {
			src.LogFormat = diag.LogProcessing[0].Format
			src.LogFilter = diag.LogProcessing[0].Filter
		}

		ctr, tailing := containers[diag.ContainerID]
		setupErr, failed := diag.SetupErrors[""]

		switch {
		case tailing:
			src.Status = logsource.SourceActive
			src.Files = []logsource.FileDiagnostic{{Path: ctr.LogFileRealPath, Input: ctr.ReceiverKind.Input(), ContainerID: diag.ContainerID}}
			src.Shipping = &logsource.ShippingDiagnostic{LogCount: ctr.LogProcessedCount, LogThroughputPerMinute: ctr.LogThroughputPerMinute}
		case failed:
			src.Status = logsource.SourceFailed
			src.Errors = append(src.Errors, setupErr)
		default:
			src.Status = logsource.SourceWaiting
			src.Reason = "container has no log file yet"
		}

		return []logsource.SourceDiagnostic{src}
	}

	if len(diag.LogProcessing) == 0 {
		src := base
		src.Status = logsource.SourceFailed
		src.Errors = configErrs

		return []logsource.SourceDiagnostic{src}
	}

	sources := make([]logsource.SourceDiagnostic, 0, len(diag.LogProcessing))

	for _, logProcessing := range diag.LogProcessing {
		src := base
		src.Include = []string{logProcessing.FilePath}
		src.LogFormat = logProcessing.Format
		src.LogFilter = logProcessing.Filter
		src.Errors = slices.Clone(configErrs)

		var recv *logReceiver

		for _, r := range man.serviceReceivers[key] {
			if slices.Equal(r.include, src.Include) {
				recv = r

				break
			}
		}

		setupErr, failed := diag.SetupErrors[logProcessing.FilePath]

		switch {
		case recv != nil:
			fillFromReceiver(&src, recv)
		case failed:
			src.Status = logsource.SourceFailed
			src.Errors = append(src.Errors, setupErr)
		default:
			src.Status = logsource.SourceWaiting
			src.Reason = "log receiver not set up yet"
		}

		sources = append(sources, src)
	}

	return sources
}

// autoDiscoveryDiagnostics describes journald, syslog and auditd. Callers must hold man.pipeline.l.
func (man *Manager) autoDiscoveryDiagnostics() []logsource.SourceDiagnostic {
	autoDiscovery := man.config.AutoDiscovery

	journald := logsource.SourceDiagnostic{Name: "journald", Origin: "journald"}

	switch {
	case !autoDiscovery.JournaldEnable:
		journald.Status = logsource.SourceExcluded
		journald.Reason = "log.opentelemetry.auto_discovery.journald_enable is false"
	case man.pipeline.journaldCounter == nil:
		journald.Status = logsource.SourceFailed
		journald.Reason = "journald receiver failed to start: see Glouton logs"
	default:
		journald.Status = logsource.SourceActive
		journald.Shipping = &logsource.ShippingDiagnostic{
			LogCount:               man.pipeline.journaldCounter.Load(),
			LogThroughputPerMinute: man.pipeline.journaldThroughputMeter.Total(),
		}
	}

	sources := []logsource.SourceDiagnostic{journald}

	receiversByName := make(map[string]*logReceiver, len(man.pipeline.receivers))
	for _, r := range man.pipeline.receivers {
		receiversByName[r.name] = r
	}

	for _, feature := range []struct {
		origin    string
		enabled   bool
		setting   string
		receivers []string
	}{
		{"syslog", autoDiscovery.SyslogEnable, "syslog_enable", []string{"syslog", "syslog-auth"}},
		{"auditd", autoDiscovery.AuditdEnable, "auditd_enable", []string{"auditd"}},
	} {
		if !feature.enabled {
			sources = append(sources, logsource.SourceDiagnostic{
				Name:   feature.origin,
				Origin: feature.origin,
				Status: logsource.SourceExcluded,
				Reason: "log.opentelemetry.auto_discovery." + feature.setting + " is false",
			})

			continue
		}

		for _, name := range feature.receivers {
			src := logsource.SourceDiagnostic{Name: name, Origin: feature.origin}

			if recv, found := receiversByName[name]; found {
				src.Include = recv.include
				fillFromReceiver(&src, recv)
			} else {
				src.Status = logsource.SourceFailed
				src.Reason = feature.origin + " receiver failed to start: see Glouton logs"
			}

			sources = append(sources, src)
		}
	}

	return sources
}

// fillFromReceiver sets the files, counters and status of src from recv, which ships its logs.
func fillFromReceiver(src *logsource.SourceDiagnostic, recv *logReceiver) {
	info := recv.diagnosticInfo()

	for _, path := range info.FileLogReceiverPaths {
		src.Files = append(src.Files, logsource.FileDiagnostic{Path: path, Input: logsource.ReceiverFileLog.Input()})
	}

	for _, path := range info.ExecLogReceiverPaths {
		src.Files = append(src.Files, logsource.FileDiagnostic{Path: path, Input: logsource.ReceiverExecLog.Input()})
	}

	slices.SortFunc(src.Files, func(a, b logsource.FileDiagnostic) int { return cmp.Compare(a.Path, b.Path) })

	src.Shipping = &logsource.ShippingDiagnostic{LogCount: info.LogProcessedCount, LogThroughputPerMinute: info.LogThroughputPerMinute}

	if len(src.Files) > 0 {
		src.Status = logsource.SourceActive
	} else {
		src.Status = logsource.SourceWaiting
		src.Reason = "no readable file matches include"
	}
}

// ExcludedServiceSources describes the services with an explicit log config (log_files or log_format)
// while the discovered services' logs aren't read at all, reason telling why.
func ExcludedServiceSources(services []discovery.Service, reason string) []logsource.SourceDiagnostic {
	var sources []logsource.SourceDiagnostic

	for _, service := range services {
		if len(service.Config.LogFiles) == 0 && service.Config.LogFormat == "" {
			continue
		}

		src := logsource.SourceDiagnostic{
			Name:      serviceSourceName(discovery.NameInstance{Name: service.Name, Instance: service.Instance}),
			Origin:    "service",
			Status:    logsource.SourceExcluded,
			Reason:    reason,
			Container: describeContainer(service.ContainerName, service.ContainerID),
			LogFormat: service.Config.LogFormat,
			LogFilter: service.Config.LogFilter,
		}

		for _, logFile := range service.Config.LogFiles {
			src.Include = append(src.Include, logFile.FilePath)
		}

		sources = append(sources, src)
	}

	return sources
}

func serviceSourceName(key discovery.NameInstance) string {
	if key.Instance == "" {
		return "service:" + key.Name
	}

	return "service:" + key.Name + "/" + key.Instance
}

func describeContainer(name, id string) string {
	if id == "" {
		return name
	}

	return name + " (" + id + ")"
}
