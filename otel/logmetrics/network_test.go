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

package logmetrics

import (
	"testing"

	"github.com/bleemeo/glouton/config"
)

func TestNewNetworkSourceDisabled(t *testing.T) {
	t.Parallel()

	sink, _ := collectingSink()

	netCfg := config.LogMetricsNetworkReceiver{
		Counters: []config.LogCounter{{Metric: "pushed_errors_count", Regex: `\[error\]`}},
	}

	src, err := newNetworkSource(t.Context(), testTelemetrySettings(), netCfg, sink)
	if err != nil {
		t.Fatal("Expected no error when GRPC/HTTP are both disabled, got:", err)
	}

	if src != nil {
		t.Fatal("Expected a nil source when GRPC/HTTP are both disabled")
	}
}

func TestNewNetworkSourceNoCounters(t *testing.T) {
	t.Parallel()

	sink, _ := collectingSink()

	netCfg := config.LogMetricsNetworkReceiver{
		GRPC: config.EnableListener{Enable: true, Address: "127.0.0.1", Port: 0},
	}

	src, err := newNetworkSource(t.Context(), testTelemetrySettings(), netCfg, sink)
	if err != nil {
		t.Fatal("Expected no error when no counters are configured, got:", err)
	}

	if src != nil {
		t.Fatal("Expected a nil source when no counters are configured")
	}
}

// TestNewNetworkSourceGRPCStartsAndStops verifies the gRPC OTLP receiver
// actually binds and can be cleanly shut down. Verifying a real OTLP push end
// to end would need an OTLP log exporter client, not otherwise a dependency
// of this module -- left as a manual verification step (see the review reply).
func TestNewNetworkSourceGRPCStartsAndStops(t *testing.T) {
	t.Parallel()

	sink, _ := collectingSink()

	netCfg := config.LogMetricsNetworkReceiver{
		GRPC:     config.EnableListener{Enable: true, Address: "127.0.0.1", Port: 0},
		Counters: []config.LogCounter{{Metric: "pushed_errors_count", Regex: `\[error\]`}},
	}

	src, err := newNetworkSource(t.Context(), testTelemetrySettings(), netCfg, sink)
	if err != nil {
		t.Fatal("Failed to start network source:", err)
	}

	if src == nil {
		t.Fatal("Expected a non-nil source when GRPC is enabled with counters configured")
	}

	if err := src.stop(t.Context()); err != nil {
		t.Fatal("Failed to stop network source:", err)
	}
}

// TestNewNetworkSourceHTTPStartsAndStops is the regression test for two real
// bugs found in logsource.SetupOTLPNetworkReceiver's HTTP branch: an invalid
// "ip" transport (net.Listen only accepts tcp/tcp4/tcp6/unix/unixpacket) and
// building otlpreceiver.HTTPConfig from scratch, which dropped the factory's
// default LogsURLPath and made otlpreceiver panic on Start (net/http rejects
// an empty ServeMux pattern).
func TestNewNetworkSourceHTTPStartsAndStops(t *testing.T) {
	t.Parallel()

	sink, _ := collectingSink()

	netCfg := config.LogMetricsNetworkReceiver{
		HTTP:     config.EnableListener{Enable: true, Address: "127.0.0.1", Port: 0},
		Counters: []config.LogCounter{{Metric: "pushed_errors_count", Regex: `\[error\]`}},
	}

	src, err := newNetworkSource(t.Context(), testTelemetrySettings(), netCfg, sink)
	if err != nil {
		t.Fatal("Failed to start network source:", err)
	}

	if src == nil {
		t.Fatal("Expected a non-nil source when HTTP is enabled with counters configured")
	}

	if err := src.stop(t.Context()); err != nil {
		t.Fatal("Failed to stop network source:", err)
	}
}
