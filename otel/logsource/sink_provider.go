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

	"github.com/bleemeo/glouton/facts"

	"go.opentelemetry.io/collector/consumer"
)

// SourceKind distinguishes how ReceiverManager found a ResolvedSource.
type SourceKind int

const (
	// SourceReceiver is a configured OpenTelemetry.Receivers entry: a file include, a
	// container_name/container_selectors match, and/or network participation, sharing one name/operators.
	SourceReceiver SourceKind = iota
	// SourceContainerLabel is a container matched by no receiver's container_name/container_selectors,
	// opted into shipping/metrics solely through its own glouton.* labels/annotations.
	SourceContainerLabel
)

// ResolvedSource is what ReceiverManager offers every registered
// SinkProvider, once per logical source, so each feature can independently
// decide whether it wants this source's records (already parsed: operators/
// log_format are resolved once by ReceiverManager, upstream of the fan-out)
// and where to send them.
type ResolvedSource struct {
	Kind SourceKind

	// Name is this source's default metrics item: the receiver's own config name for SourceReceiver, or
	// the container's own runtime name for SourceContainerLabel.
	Name string

	// ReceiverName is the OpenTelemetry.Receivers key, set only for SourceReceiver.
	ReceiverName string

	// Container is the single container behind a SourceContainerLabel source; nil for SourceReceiver,
	// even when its selectors matched several containers, since those are tailed individually but share
	// this one resolved source.
	Container facts.Container

	// SendLogs is the fully-resolved shipping decision: SourceReceiver's own send_logs if set, else
	// OpenTelemetry.SendLogs; SourceContainerLabel's glouton.send_logs, else glouton.log_enable=true
	// (back-compat), else OpenTelemetry.SendLogs.
	SendLogs bool

	// LogMetricsRule is the glouton.log_metrics label's value, set only for SourceContainerLabel (""
	// otherwise). A SourceReceiver's metrics instead come from its own "metrics:" field.
	LogMetricsRule string
}

// SinkProvider is implemented by each feature that can consume a ReceiverManager-owned source
// (otel/logprocessing for shipping, otel/logmetrics for counting). WantSource is called once per
// ResolvedSource; a false ok or nil sink means "not interested," and no tail is started if nobody wants
// it. Called with ReceiverManager's lock held: implementations must return quickly. ctx stays valid for
// the lifetime of any component WantSource builds, not just this call.
type SinkProvider interface {
	WantSource(ctx context.Context, src ResolvedSource) (sink consumer.Logs, ok bool)

	// ReleaseSource tells the provider container is gone (e.g. a Docker/Kubernetes restart assigns a
	// new ID even for "the same" service), so it can forget whatever it built for it. No-op if nothing
	// was built. Only called for a SourceContainerLabel container -- a SourceReceiver never goes away
	// individually. Called with ReceiverManager's lock held; must return quickly.
	ReleaseSource(ctx context.Context, container facts.Container)
}
