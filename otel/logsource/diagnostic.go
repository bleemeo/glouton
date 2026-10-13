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
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/types"

	"github.com/bmatcuk/doublestar/v4"
)

const (
	sourcesJSONArchivePath = "log-processing/sources.json"
	sourcesTextArchivePath = "log-processing/sources.txt"
)

// SourceStatus is the outcome of a log source's or input's setup, as reported in the diagnostic archive.
type SourceStatus string

const (
	// SourceActive means it is read (for a source: at least one of its inputs is).
	SourceActive SourceStatus = "active"
	// SourceWaiting means it is set up, but there is nothing to read yet (e.g. its file doesn't
	// exist, or its service isn't running).
	SourceWaiting SourceStatus = "waiting"
	// SourceFailed means the config asks for it, but its config is invalid or its setup failed.
	SourceFailed SourceStatus = "failed"
	// SourceExcluded means the config makes it not read, so no input exists: an exclusion rule or
	// label, no opt-in, a global switch that is off, or it is read by another source.
	SourceExcluded SourceStatus = "excluded"
	// SourceUnused means it is explicitly configured, but neither shipping nor log-to-metric consumes
	// it, so no input exists.
	SourceUnused SourceStatus = "unused"
)

// InputKind is what an input of a source reads.
type InputKind string

const (
	InputFile      InputKind = "file"
	InputContainer InputKind = "container"
	InputListener  InputKind = "listener"
	InputJournal   InputKind = "journal"
)

// SourceDiagnostic describes one log source for the diagnostic archive: where it comes from, what it
// reads or is configured to read, and which features consume it.
type SourceDiagnostic struct {
	// Name identifies the source, prefixed by its origin (e.g. "receiver:apache", "service:nginx").
	Name   string
	Origin string
	Status SourceStatus
	// Reason explains a Status that isn't explained by the inputs' own status.
	Reason string `json:",omitempty"`
	// Errors are config errors; a source can have some and still be active.
	Errors    []string `json:",omitempty"`
	Container string   `json:",omitempty"`
	LogFormat string   `json:",omitempty"`
	LogFilter string   `json:",omitempty"`
	Inputs    []InputDiagnostic
	// Shipping is nil when the logs of this source aren't shipped.
	Shipping *ShippingDiagnostic `json:",omitempty"`
	// LogToMetrics is nil when no log-to-metric counter reads this source.
	LogToMetrics *LogToMetricsDiagnostic `json:",omitempty"`

	// key links a ReceiverManager-owned source to what its SinkProviders report about it; zero otherwise.
	key SourceKey
}

// InputDiagnostic is one file, container, listener or journal a source reads or is configured to read.
type InputDiagnostic struct {
	Kind InputKind
	// Name is the file path (or include pattern when nothing matches it), the listener name, or the
	// container match.
	Name   string
	Status SourceStatus
	Reason string `json:",omitempty"`
	// Mode is "direct" when Glouton reads the file itself, "sudo" when it goes through `sudo tail`.
	Mode        string `json:",omitempty"`
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

// Mode is how a file read by this kind of receiver is accessed, as shown in the diagnostic.
func (k ReceiverKind) Mode() string {
	switch k {
	case ReceiverFileLog:
		return "direct"
	case ReceiverExecLog:
		return "sudo"
	default:
		return string(k)
	}
}

// IncludeInputs describes the inputs of include patterns: one active input per file read, one failed
// input per file whose setup failed, and one waiting input per pattern none of these files match.
func IncludeInputs(include []string, watching map[string]ReceiverKind, failures map[string]string) []InputDiagnostic {
	inputs := make([]InputDiagnostic, 0, len(watching)+len(failures))

	for _, path := range slices.Sorted(maps.Keys(watching)) {
		inputs = append(inputs, InputDiagnostic{Kind: InputFile, Name: path, Status: SourceActive, Mode: watching[path].Mode()})
	}

	for _, path := range slices.Sorted(maps.Keys(failures)) {
		inputs = append(inputs, InputDiagnostic{Kind: InputFile, Name: path, Status: SourceFailed, Reason: failures[path]})
	}

	for _, pattern := range include {
		if slices.ContainsFunc(inputs, func(input InputDiagnostic) bool { return matchesInclude(pattern, input.Name) }) {
			continue
		}

		input := InputDiagnostic{Kind: InputFile, Name: pattern, Status: SourceWaiting, Reason: "file doesn't exist or can't be read"}
		if isGlob(pattern) {
			input.Reason = "no readable file matches"
		}

		inputs = append(inputs, input)
	}

	return inputs
}

// ConfiguredInputs describes inputs that aren't set up, all sharing their source's status and reason.
func ConfiguredInputs(kind InputKind, names []string, status SourceStatus, reason string) []InputDiagnostic {
	inputs := make([]InputDiagnostic, 0, len(names))

	for _, name := range names {
		inputs = append(inputs, InputDiagnostic{Kind: kind, Name: name, Status: status, Reason: reason})
	}

	return inputs
}

// StatusFromInputs is the status of a source that is set up: active if any input is, else failed if any
// input is, else waiting.
func StatusFromInputs(inputs []InputDiagnostic) SourceStatus {
	switch {
	case slices.ContainsFunc(inputs, func(i InputDiagnostic) bool { return i.Status == SourceActive }):
		return SourceActive
	case slices.ContainsFunc(inputs, func(i InputDiagnostic) bool { return i.Status == SourceFailed }):
		return SourceFailed
	default:
		return SourceWaiting
	}
}

func isGlob(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[{")
}

func matchesInclude(pattern, path string) bool {
	if !isGlob(pattern) {
		return pattern == path
	}

	// The pattern was already validated when resolving it.
	matches, _ := doublestar.PathMatch(pattern, path)

	return matches
}

// WriteSourcesDiagnostic attaches shipping and log-to-metric info to the ReceiverManager-owned sources they
// belong to, then writes every source to the archive, as JSON and as human-readable tables.
func WriteSourcesDiagnostic(
	archive types.ArchiveWriter,
	sources []SourceDiagnostic,
	shipping map[SourceKey]ShippingDiagnostic,
	logToMetrics map[SourceKey]LogToMetricsDiagnostic,
) error {
	sources = slices.Clone(sources)

	for i := range sources {
		if len(sources[i].Inputs) == 0 {
			// Every source shows up in the inputs table.
			sources[i].Inputs = []InputDiagnostic{{Kind: "-", Name: "-", Status: sources[i].Status, Reason: sources[i].Reason}}
		}

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

	// Alphabetical on status puts the active ones first.
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

	fmt.Fprintf(file, "Details in %s.\n\nINPUTS: one line per file, container or listener a source reads or is configured to read.\n\n", sourcesJSONArchivePath)

	if err := writeInputsTable(file, sources); err != nil {
		return err
	}

	fmt.Fprint(file, "\nSOURCES: counters are per source, shared by all its inputs.\n\n")

	return writeSourcesTable(file, sources)
}

func writeInputsTable(file io.Writer, sources []SourceDiagnostic) error {
	type row struct {
		source string
		input  InputDiagnostic
	}

	var rows []row

	for _, src := range sources {
		for _, input := range src.Inputs {
			rows = append(rows, row{source: src.Name, input: input})
		}
	}

	slices.SortStableFunc(rows, func(a, b row) int {
		return cmp.Or(cmp.Compare(a.input.Status, b.input.Status), cmp.Compare(a.source, b.source), cmp.Compare(a.input.Name, b.input.Name))
	})

	w := tabwriter.NewWriter(file, 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, "SOURCE\tSTATUS\tTYPE\tINPUT\tMODE\tREASON")

	for _, r := range rows {
		name := r.input.Name
		if r.input.ContainerID != "" {
			name += " (container " + r.input.ContainerID + ")"
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", r.source, r.input.Status, r.input.Kind, name, dashIfEmpty(r.input.Mode), r.input.Reason)
	}

	return w.Flush()
}

func writeSourcesTable(file io.Writer, sources []SourceDiagnostic) error {
	w := tabwriter.NewWriter(file, 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, "SOURCE\tSTATUS\tSHIPPED\tPER_MIN\tMETRICS\tREASON")

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

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", src.Name, src.Status, shipped, perMin, metrics, reason)
	}

	return w.Flush()
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}

	return s
}

// SetNetworkListenerStates records the outcome of starting each planned network listener, by name: ""
// when it runs, else the error. A listener missing from states was not planned, because it isn't defined.
func (rm *ReceiverManager) SetNetworkListenerStates(states map[string]string) {
	rm.l.Lock()
	defer rm.l.Unlock()

	rm.listenerStates = states
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

	containerMatch := describeContainerMatch(containerName, containerSelectors)
	diag.Container = containerMatch

	// What's configured, for a receiver whose inputs aren't set up.
	configured := func(status SourceStatus, reason string) []InputDiagnostic {
		inputs := ConfiguredInputs(InputFile, include, status, reason)
		inputs = append(inputs, ConfiguredInputs(InputListener, fromListeners, status, reason)...)

		if containerMatch != "" {
			inputs = append(inputs, ConfiguredInputs(InputContainer, []string{containerMatch}, status, reason)...)
		}

		return inputs
	}

	ms, found := rm.receivers[name]
	if !found {
		diag.Status = SourceWaiting
		diag.Reason = "not resolved yet"

		if _, err := decodeReceiverFields(raw); err != nil {
			diag.Status = SourceFailed
			diag.Reason = ""
			diag.Errors = []string{fmt.Sprintf("decoding config: %v", err)}
		}

		diag.Inputs = configured(diag.Status, diag.Reason)

		return diag
	}

	diag.LogFormat = ms.fields.LogFormat

	ms.l.Lock()
	defer ms.l.Unlock()

	if ms.configErr != "" {
		diag.Errors = []string{ms.configErr}
	}

	if ms.fanout == nil {
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

		diag.Inputs = configured(diag.Status, diag.Reason)

		return diag
	}

	includeWatching := make(map[string]ReceiverKind, len(ms.recvs))
	for file := range ms.recvs {
		includeWatching[file] = ms.watching[file]
	}

	diag.Inputs = IncludeInputs(include, includeWatching, ms.includeErrs)
	diag.Inputs = append(diag.Inputs, rm.listenerInputs(fromListeners)...)

	if containerMatch != "" {
		diag.Inputs = append(diag.Inputs, ms.containerInputs(containerMatch)...)
	}

	diag.Status = StatusFromInputs(diag.Inputs)

	return diag
}

// listenerInputs describes the from_listeners entries of a receiver that has a consumer. Callers must hold rm.l.
func (rm *ReceiverManager) listenerInputs(fromListeners []string) []InputDiagnostic {
	inputs := make([]InputDiagnostic, 0, len(fromListeners))

	for _, name := range fromListeners {
		input := InputDiagnostic{Kind: InputListener, Name: name}
		startErr, planned := rm.listenerStates[name]

		switch {
		case rm.listenerStates == nil:
			input.Status = SourceWaiting
			input.Reason = "network listeners not started yet"
		case !planned:
			input.Status = SourceFailed
			input.Reason = "not defined in opentelemetry.listeners"
		case startErr != "":
			input.Status = SourceFailed
			input.Reason = "failed to start: " + startErr
		default:
			input.Status = SourceActive
		}

		inputs = append(inputs, input)
	}

	return inputs
}

// containerInputs describes the container tails of a receiver matching containers. Callers must hold ms.l.
func (ms *managedSource) containerInputs(containerMatch string) []InputDiagnostic {
	inputs := make([]InputDiagnostic, 0, len(ms.containerLogFile)+len(ms.containerErrs))

	for _, ctrID := range slices.Sorted(maps.Keys(ms.containerLogFile)) {
		file := ms.containerLogFile[ctrID]
		inputs = append(inputs, InputDiagnostic{Kind: InputContainer, Name: file, Status: SourceActive, Mode: ms.watching[file].Mode(), ContainerID: ctrID})
	}

	for _, ctrID := range slices.Sorted(maps.Keys(ms.containerErrs)) {
		inputs = append(inputs, InputDiagnostic{Kind: InputContainer, Name: containerMatch, Status: SourceFailed, Reason: ms.containerErrs[ctrID], ContainerID: ctrID})
	}

	if len(inputs) == 0 {
		inputs = append(inputs, InputDiagnostic{Kind: InputContainer, Name: containerMatch, Status: SourceWaiting, Reason: "no container matches"})
	}

	return inputs
}

// containerLabelDiagnostic describes a container handled by the glouton.* label fallback. Callers must hold rm.l.
func (rm *ReceiverManager) containerLabelDiagnostic(ms *managedSource) SourceDiagnostic {
	diag := SourceDiagnostic{
		Name:      "container:" + ms.name,
		Origin:    "container_label",
		LogFormat: ms.labels.LogFormat,
		LogFilter: ms.labels.LogFilter,
	}

	input := InputDiagnostic{Kind: InputContainer, Name: ms.name}

	if ms.container != nil {
		diag.Container = ms.container.ContainerName() + " (" + ms.container.ID() + ")"
		diag.key = ContainerLabelSourceKey(ms.container.ID())
		input.Name = ms.container.LogPath()
		input.ContainerID = ms.container.ID()
	}

	ms.l.Lock()
	defer ms.l.Unlock()

	if ms.configErr != "" {
		diag.Errors = []string{ms.configErr}
	}

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
	}

	if diag.Status != "" {
		input.Status = diag.Status
		input.Reason = diag.Reason
		diag.Inputs = []InputDiagnostic{input}

		return diag
	}

	if file, tailed := ms.containerLogFile[input.ContainerID]; tailed {
		input.Name = file
		input.Status = SourceActive
		input.Mode = ms.watching[file].Mode()
	} else if err, failed := ms.containerErrs[input.ContainerID]; failed {
		input.Status = SourceFailed
		input.Reason = err
	} else {
		input.Status = SourceWaiting
		input.Reason = "no log file for this container yet"
	}

	diag.Inputs = []InputDiagnostic{input}
	diag.Status = input.Status

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
