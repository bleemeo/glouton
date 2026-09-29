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
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/types"
)

const (
	sourcesJSONArchivePath = "log-processing/sources.json"
	sourcesTextArchivePath = "log-processing/sources.txt"
)

// SourceStatus is the outcome of a log source's setup, as reported in the diagnostic archive.
type SourceStatus string

const (
	// SourceActive means at least one file/container/listener of this source is read.
	SourceActive SourceStatus = "active"
	// SourceWaiting means the source is set up, but there is nothing to read yet (e.g. its file doesn't
	// exist, or its service isn't running).
	SourceWaiting SourceStatus = "waiting"
	// SourceFailed means the config asks for this source, but its config is invalid or its setup failed.
	SourceFailed SourceStatus = "failed"
	// SourceExcluded means the config makes this source not read, so no input exists: an exclusion
	// rule or label, no opt-in, a global switch that is off, or the source is read by another one.
	SourceExcluded SourceStatus = "excluded"
	// SourceUnused means the source is explicitly configured, but neither shipping nor log-to-metric
	// consumes it, so no input exists.
	SourceUnused SourceStatus = "unused"
)

// SourceDiagnostic describes one log source for the diagnostic archive: where it comes from, whether it
// is read, which files, and which features consume it.
type SourceDiagnostic struct {
	// Name identifies the source, prefixed by its origin (e.g. "receiver:apache", "service:nginx").
	Name   string
	Origin string
	Status SourceStatus
	// Reason explains any Status other than SourceActive.
	Reason string `json:",omitempty"`
	// Errors are setup or config errors; a source can have some and still be partially active.
	Errors    []string `json:",omitempty"`
	Container string   `json:",omitempty"`
	Include   []string `json:",omitempty"`
	Listeners []string `json:",omitempty"`
	LogFormat string   `json:",omitempty"`
	LogFilter string   `json:",omitempty"`
	Files     []FileDiagnostic
	// Shipping is nil when the logs of this source aren't shipped.
	Shipping *ShippingDiagnostic `json:",omitempty"`
	// LogToMetrics is nil when no log-to-metric counter reads this source.
	LogToMetrics *LogToMetricsDiagnostic `json:",omitempty"`

	// key links a ReceiverManager-owned source to what its SinkProviders report about it; zero otherwise.
	key SourceKey
}

// FileDiagnostic is one file actually read by a source.
type FileDiagnostic struct {
	Path string
	// Input is "direct" when Glouton reads the file itself, "sudo" when it goes through `sudo tail`.
	Input       string
	ContainerID string `json:",omitempty"`
}

// ShippingDiagnostic holds the counters of a shipped source. They count records that went through
// the source's own filters, before the global filters and the back-pressure.
type ShippingDiagnostic struct {
	LogCount               int64
	LogThroughputPerMinute int
}

// LogToMetricsDiagnostic lists the log-to-metric counters reading a source.
type LogToMetricsDiagnostic struct {
	Rule    string `json:",omitempty"`
	Metrics []string
}

// SourceKey identifies a ReceiverManager-owned source across SinkProviders.
type SourceKey struct {
	Kind SourceKind
	// Name is the receiver name for SourceReceiver, the container ID for SourceContainerLabel.
	Name string
}

// ReceiverSourceKey is the SourceKey of the configured receiver name.
func ReceiverSourceKey(name string) SourceKey {
	return SourceKey{Kind: SourceReceiver, Name: name}
}

// ContainerLabelSourceKey is the SourceKey of the container opted in by its labels.
func ContainerLabelSourceKey(containerID string) SourceKey {
	return SourceKey{Kind: SourceContainerLabel, Name: containerID}
}

// Input is how a file read by this kind of receiver is accessed, as shown in the diagnostic.
func (k ReceiverKind) Input() string {
	switch k {
	case ReceiverFileLog:
		return "direct"
	case ReceiverExecLog:
		return "sudo"
	default:
		return string(k)
	}
}

// filesFromWatching converts a file -> ReceiverKind map into sorted FileDiagnostic.
func filesFromWatching(watching map[string]ReceiverKind, containerID string) []FileDiagnostic {
	files := make([]FileDiagnostic, 0, len(watching))

	for path, kind := range watching {
		files = append(files, FileDiagnostic{Path: path, Input: kind.Input(), ContainerID: containerID})
	}

	slices.SortFunc(files, func(a, b FileDiagnostic) int { return cmp.Compare(a.Path, b.Path) })

	return files
}

// WriteSourcesDiagnostic attaches shipping and log-to-metric info to the ReceiverManager-owned sources they
// belong to, then writes every source to the archive, as JSON and as a human-readable table.
func WriteSourcesDiagnostic(
	archive types.ArchiveWriter,
	sources []SourceDiagnostic,
	shipping map[SourceKey]ShippingDiagnostic,
	logToMetrics map[SourceKey]LogToMetricsDiagnostic,
) error {
	sources = slices.Clone(sources)

	for i := range sources {
		if sources[i].key.Name == "" {
			continue
		}

		if ship, ok := shipping[sources[i].key]; ok {
			sources[i].Shipping = &ship
		}

		if metrics, ok := logToMetrics[sources[i].key]; ok {
			sources[i].LogToMetrics = &metrics
		}
	}

	// Alphabetical on status puts the active sources first.
	slices.SortStableFunc(sources, func(a, b SourceDiagnostic) int {
		return cmp.Or(cmp.Compare(a.Status, b.Status), cmp.Compare(a.Name, b.Name))
	})

	file, err := archive.Create(sourcesJSONArchivePath)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	if err := enc.Encode(sources); err != nil {
		return err
	}

	file, err = archive.Create(sourcesTextArchivePath)
	if err != nil {
		return err
	}

	fmt.Fprintf(file, "One line per file read (or per source, when it reads none). Details in %s.\n\n", sourcesJSONArchivePath)

	w := tabwriter.NewWriter(file, 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, "SOURCE\tSTATUS\tFILE\tINPUT\tSHIPPED\tPER_MIN\tMETRICS\tREASON")

	for _, src := range sources {
		shipped, perMin := "-", "-"
		if src.Shipping != nil {
			shipped = strconv.FormatInt(src.Shipping.LogCount, 10)
			perMin = strconv.Itoa(src.Shipping.LogThroughputPerMinute)
		}

		metrics := "-"
		if src.LogToMetrics != nil {
			metrics = strconv.Itoa(len(src.LogToMetrics.Metrics))
		}

		reason := strings.Join(append([]string{src.Reason}, src.Errors...), "; ")
		reason = strings.TrimPrefix(reason, "; ")

		if len(src.Files) == 0 {
			fmt.Fprintf(w, "%s\t%s\t%s\t-\t%s\t%s\t%s\t%s\n", src.Name, src.Status, src.wantedFiles(), shipped, perMin, metrics, reason)

			continue
		}

		for _, f := range src.Files {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", src.Name, src.Status, f.Path, f.Input, shipped, perMin, metrics, reason)
		}
	}

	return w.Flush()
}

// wantedFiles describes what a source reading no file is configured to read.
func (src SourceDiagnostic) wantedFiles() string {
	switch {
	case len(src.Include) > 0:
		return strings.Join(src.Include, ",")
	case len(src.Listeners) > 0:
		return "listeners:" + strings.Join(src.Listeners, ",")
	case src.Container != "":
		return "container:" + src.Container
	default:
		return "-"
	}
}

// DiagnosticSources describes every configured receiver and every container known to the label fallback.
// Shipping and LogToMetrics are left for WriteSourcesDiagnostic to fill from the SinkProviders.
func (rm *ReceiverManager) DiagnosticSources() []SourceDiagnostic {
	rm.l.Lock()
	defer rm.l.Unlock()

	sources := make([]SourceDiagnostic, 0, len(rm.cfg.Receivers)+len(rm.byContainer))

	for name, raw := range rm.cfg.Receivers {
		sources = append(sources, rm.receiverDiagnostic(name, raw))
	}

	for _, ms := range rm.byContainer {
		sources = append(sources, rm.containerLabelDiagnostic(ms))
	}

	return sources
}

// receiverDiagnostic describes one configured receiver. Callers must hold rm.l.
func (rm *ReceiverManager) receiverDiagnostic(name string, raw config.LogReceiver) SourceDiagnostic {
	diag := SourceDiagnostic{
		Name:   "receiver:" + name,
		Origin: "receiver",
		key:    ReceiverSourceKey(name),
	}

	include, containerName, containerSelectors, fromListeners, err := config.LogReceiverSelectors(raw)
	if err != nil {
		diag.Status = SourceFailed
		diag.Errors = []string{fmt.Sprintf("decoding config: %v", err)}

		return diag
	}

	diag.Include = include
	diag.Listeners = fromListeners
	diag.Container = describeContainerMatch(containerName, containerSelectors)

	ms, found := rm.receivers[name]
	if !found {
		diag.Status = SourceWaiting
		diag.Reason = "not resolved yet"

		if _, err := decodeReceiverFields(raw); err != nil {
			diag.Status = SourceFailed
			diag.Reason = ""
			diag.Errors = []string{fmt.Sprintf("decoding config: %v", err)}
		}

		return diag
	}

	diag.LogFormat = ms.fields.LogFormat

	ms.l.Lock()
	defer ms.l.Unlock()

	diag.Files = ms.filesDiagnostic()
	diag.Errors = ms.errorsDiagnostic()
	setupFailed := ms.includeErr != "" || len(ms.containerErrs) > 0

	switch {
	case ms.fanout == nil:
		diag.Status = SourceUnused

		sendLogs := rm.cfg.ReceiversDefaultSendLogs
		if ms.fields.SendLogs != nil {
			sendLogs = *ms.fields.SendLogs
		}

		if _, hasMetrics := raw["metrics"]; !sendLogs && !hasMetrics {
			diag.Reason = "send_logs is false and no metrics are configured"
		} else {
			diag.Reason = "no feature accepted this source (log shipping unavailable, or its filters/metrics failed to build: see Glouton logs)"
		}
	case len(diag.Files) > 0 || len(diag.Listeners) > 0:
		diag.Status = SourceActive
	case setupFailed:
		diag.Status = SourceFailed
	case len(diag.Include) > 0:
		diag.Status = SourceWaiting
		diag.Reason = "no readable file matches include"
	default:
		diag.Status = SourceWaiting
		diag.Reason = "no container matches"
	}

	return diag
}

// containerLabelDiagnostic describes a container handled by the glouton.* label fallback. Callers must hold rm.l.
func (rm *ReceiverManager) containerLabelDiagnostic(ms *managedSource) SourceDiagnostic {
	diag := SourceDiagnostic{
		Name:      "container:" + ms.name,
		Origin:    "container_label",
		LogFormat: ms.labels.LogFormat,
		LogFilter: ms.labels.LogFilter,
	}

	if ms.container != nil {
		diag.Container = ms.container.ContainerName() + " (" + ms.container.ID() + ")"
		diag.key = ContainerLabelSourceKey(ms.container.ID())
	}

	ms.l.Lock()
	defer ms.l.Unlock()

	diag.Files = ms.filesDiagnostic()
	diag.Errors = ms.errorsDiagnostic()

	switch {
	case ms.fanout == nil && ms.serviceTailed && ms.labels.LogMetrics == "":
		diag.Status = SourceExcluded
		diag.Reason = "already read through its discovered service"
	case ms.fanout == nil && ms.labels.LogMetrics != "":
		diag.Status = SourceFailed
		diag.Reason = fmt.Sprintf("glouton.log_metrics rule %q is unknown or invalid: see Glouton logs", ms.labels.LogMetrics)
	case ms.fanout == nil && ms.labels.SendLogs != nil && !*ms.labels.SendLogs:
		diag.Status = SourceExcluded
		diag.Reason = "glouton.send_logs is false"
	case ms.fanout == nil && !ms.labels.resolveSendLogs(rm.cfg.AutoDiscovery.ContainerAndServiceEnable):
		diag.Status = SourceExcluded
		diag.Reason = "not opted in: no glouton.log_enable/glouton.send_logs/glouton.log_metrics label," +
			" and log.opentelemetry.auto_discovery.container_and_service_enable is false"
	case ms.fanout == nil:
		diag.Status = SourceUnused
		diag.Reason = "no feature accepted this container (log shipping unavailable, or its filters failed to build: see Glouton logs)"
	case len(diag.Files) > 0:
		diag.Status = SourceActive
	case len(ms.containerErrs) > 0:
		diag.Status = SourceFailed
	default:
		diag.Status = SourceWaiting
		diag.Reason = "no log file for this container yet"
	}

	return diag
}

func describeContainerMatch(containerName string, containerSelectors map[string]string) string {
	parts := make([]string, 0, 1+len(containerSelectors))

	if containerName != "" {
		parts = append(parts, "name="+containerName)
	}

	for _, k := range slices.Sorted(maps.Keys(containerSelectors)) {
		parts = append(parts, k+"="+containerSelectors[k])
	}

	return strings.Join(parts, ",")
}

// filesDiagnostic lists the files this source reads. Callers must hold ms.l.
func (ms *managedSource) filesDiagnostic() []FileDiagnostic {
	files := filesFromWatching(ms.watching, "")

	for i := range files {
		for ctrID, logFile := range ms.containerLogFile {
			if logFile == files[i].Path {
				files[i].ContainerID = ctrID

				break
			}
		}
	}

	return files
}

// errorsDiagnostic lists this source's config and last setup errors. Callers must hold ms.l.
func (ms *managedSource) errorsDiagnostic() []string {
	var errs []string

	if ms.configErr != "" {
		errs = append(errs, ms.configErr)
	}

	if ms.includeErr != "" {
		errs = append(errs, ms.includeErr)
	}

	for _, ctrID := range slices.Sorted(maps.Keys(ms.containerErrs)) {
		errs = append(errs, fmt.Sprintf("container %s: %s", ctrID, ms.containerErrs[ctrID]))
	}

	return errs
}

func (ms *managedSource) setIncludeErr(err error) {
	ms.l.Lock()
	defer ms.l.Unlock()

	ms.includeErr = ""
	if err != nil {
		ms.includeErr = err.Error()
	}
}

func (ms *managedSource) resetContainerErrs() {
	ms.l.Lock()
	defer ms.l.Unlock()

	ms.containerErrs = nil
}

func (ms *managedSource) setContainerErr(ctr facts.Container, err error) {
	ms.l.Lock()
	defer ms.l.Unlock()

	if ms.containerErrs == nil {
		ms.containerErrs = make(map[string]string)
	}

	ms.containerErrs[ctr.ID()] = err.Error()
}
