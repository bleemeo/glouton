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
	"sync"
	"testing"

	"github.com/bleemeo/glouton/config"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
)

// recordingLogsConsumer returns a consumer.Logs that records every batch, safe for concurrent access.
func recordingLogsConsumer() (consumer.Logs, func() []plog.Logs) {
	var l sync.Mutex

	var received []plog.Logs

	sink, err := consumer.NewLogs(func(_ context.Context, ld plog.Logs) error {
		l.Lock()
		defer l.Unlock()

		received = append(received, ld)

		return nil
	})
	if err != nil {
		panic(err)
	}

	return sink, func() []plog.Logs {
		l.Lock()
		defer l.Unlock()

		return append([]plog.Logs(nil), received...)
	}
}

func makeLogs(resourceAttr string) plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("marker", resourceAttr)
	rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("line")

	return ld
}

func TestFanoutLogsNoSinks(t *testing.T) {
	t.Parallel()

	if got := FanoutLogs(); got != nil {
		t.Fatalf("Expected a nil consumer with no sinks, got %v", got)
	}

	if got := FanoutLogs(nil, nil); got != nil {
		t.Fatalf("Expected a nil consumer when every sink is nil, got %v", got)
	}
}

func TestFanoutLogsSingleSinkPassthrough(t *testing.T) {
	t.Parallel()

	sink, received := recordingLogsConsumer()

	fanout := FanoutLogs(nil, sink)
	if fanout == nil {
		t.Fatal("Expected a non-nil consumer")
	}

	ld := makeLogs("only")

	if err := fanout.ConsumeLogs(t.Context(), ld); err != nil {
		t.Fatal("ConsumeLogs returned an error:", err)
	}

	got := received()
	if len(got) != 1 {
		t.Fatalf("Expected exactly 1 received batch, got %d", len(got))
	}
}

// TestFanoutLogsIsolatesMutations checks that each sink after the first gets its own copy, since a sink may mutate its batch in place.
func TestFanoutLogsIsolatesMutations(t *testing.T) {
	t.Parallel()

	var mutatingCalls int

	mutating, err := consumer.NewLogs(func(_ context.Context, ld plog.Logs) error {
		mutatingCalls++

		for _, rl := range ld.ResourceLogs().All() {
			rl.Resource().Attributes().PutStr("touched_by", "mutating")
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	recording, received := recordingLogsConsumer()

	fanout := FanoutLogs(mutating, recording)

	ld := makeLogs("original")

	if err := fanout.ConsumeLogs(t.Context(), ld); err != nil {
		t.Fatal("ConsumeLogs returned an error:", err)
	}

	if mutatingCalls != 1 {
		t.Fatalf("Expected the mutating sink to be called exactly once, got %d", mutatingCalls)
	}

	got := received()
	if len(got) != 1 {
		t.Fatalf("Expected exactly 1 received batch, got %d", len(got))
	}

	rl := got[0].ResourceLogs().At(0)

	if _, found := rl.Resource().Attributes().Get("touched_by"); found {
		t.Fatal("The second sink's batch must not carry the first sink's in-place mutation")
	}

	marker, found := rl.Resource().Attributes().Get("marker")
	if !found || marker.AsString() != "original" {
		t.Fatalf("Expected the second sink's batch to still carry the original data, got %v (found=%v)", marker, found)
	}
}

func TestPlanSharedNetworkReceiversOmitsUnreferencedReceivers(t *testing.T) {
	t.Parallel()

	receivers := map[string]config.NetworkReceiver{
		"otlp":   {Protocols: config.NetworkProtocols{GRPC: &config.NetworkEndpoint{Endpoint: "127.0.0.1:4317"}}},
		"unused": {Protocols: config.NetworkProtocols{GRPC: &config.NetworkEndpoint{Endpoint: "127.0.0.1:5317"}}},
	}

	consumerA, _ := recordingLogsConsumer()

	planned := PlanSharedNetworkReceivers(receivers, []NetworkWant{
		{Consumer: consumerA, Receivers: []string{"otlp"}},
	})

	if len(planned) != 1 {
		t.Fatalf("Expected exactly 1 planned receiver, got %d", len(planned))
	}

	if planned[0].Name != "otlp" {
		t.Fatalf("Expected the planned receiver to be for %q, got %q", "otlp", planned[0].Name)
	}

	if planned[0].Protocols.GRPC == nil || planned[0].Protocols.HTTP != nil {
		t.Fatalf("Expected only GRPC configured, got %+v", planned[0].Protocols)
	}
}

// TestPlanSharedNetworkReceiversOmitsUnknownReceiverName checks that a receiver name undefined in log.network.receivers is skipped, not turned into a protocol-less PlannedReceiver.
func TestPlanSharedNetworkReceiversOmitsUnknownReceiverName(t *testing.T) {
	t.Parallel()

	receivers := map[string]config.NetworkReceiver{
		"custom1": {Protocols: config.NetworkProtocols{GRPC: &config.NetworkEndpoint{Endpoint: "127.0.0.1:4317"}}},
	}

	consumerA, _ := recordingLogsConsumer()

	planned := PlanSharedNetworkReceivers(receivers, []NetworkWant{
		{Consumer: consumerA, Receivers: []string{"otlp"}}, // not in receivers
	})

	if len(planned) != 0 {
		t.Fatalf("Expected no planned receiver for an unknown name, got %+v", planned)
	}
}

func TestPlanSharedNetworkReceiversSplitsByName(t *testing.T) {
	t.Parallel()

	receivers := map[string]config.NetworkReceiver{
		"shipping": {Protocols: config.NetworkProtocols{GRPC: &config.NetworkEndpoint{Endpoint: "127.0.0.1:4317"}}},
		"metrics":  {Protocols: config.NetworkProtocols{HTTP: &config.NetworkEndpoint{Endpoint: "127.0.0.1:4418"}}},
	}

	shippingConsumer, shippingReceived := recordingLogsConsumer()
	metricsConsumer, metricsReceived := recordingLogsConsumer()

	planned := PlanSharedNetworkReceivers(receivers, []NetworkWant{
		{Consumer: shippingConsumer, Receivers: []string{"shipping"}},
		{Consumer: metricsConsumer, Receivers: []string{"metrics"}},
	})

	if len(planned) != 2 {
		t.Fatalf("Expected 2 independent planned receivers, got %d", len(planned))
	}

	// Deterministic order: PlanSharedNetworkReceivers sorts by name.
	if planned[0].Name != "metrics" || planned[1].Name != "shipping" {
		t.Fatalf("Unexpected planned receiver names/order: %v", []string{planned[0].Name, planned[1].Name})
	}

	if err := planned[0].Sink.ConsumeLogs(t.Context(), makeLogs("m")); err != nil {
		t.Fatal(err)
	}

	if err := planned[1].Sink.ConsumeLogs(t.Context(), makeLogs("s")); err != nil {
		t.Fatal(err)
	}

	if len(metricsReceived()) != 1 || len(shippingReceived()) != 1 {
		t.Fatal("Expected each feature's consumer to receive only its own receiver's batch, not the other's")
	}
}

// TestPlanSharedNetworkReceiversIsolatesSharedNetworkMutations checks that sharing one receiver's sink between two consumers doesn't leak in-place mutations, regardless of order.
func TestPlanSharedNetworkReceiversIsolatesSharedNetworkMutations(t *testing.T) {
	t.Parallel()

	receivers := map[string]config.NetworkReceiver{
		"otlp": {Protocols: config.NetworkProtocols{GRPC: &config.NetworkEndpoint{Endpoint: "127.0.0.1:4317"}}},
	}

	for _, order := range [][2]string{{"shipping", "metrics"}, {"metrics", "shipping"}} {
		var mutatingCalls int

		mutating, err := consumer.NewLogs(func(_ context.Context, ld plog.Logs) error {
			mutatingCalls++

			for _, rl := range ld.ResourceLogs().All() {
				rl.Resource().Attributes().PutStr("touched_by", "shipping")
			}

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}

		recording, received := recordingLogsConsumer()

		wants := map[string]NetworkWant{
			"shipping": {Consumer: mutating, Receivers: []string{"otlp"}},
			"metrics":  {Consumer: recording, Receivers: []string{"otlp"}},
		}

		planned := PlanSharedNetworkReceivers(receivers, []NetworkWant{wants[order[0]], wants[order[1]]})
		if len(planned) != 1 {
			t.Fatalf("order %v: expected exactly 1 planned (shared) receiver, got %d", order, len(planned))
		}

		if err := planned[0].Sink.ConsumeLogs(t.Context(), makeLogs("original")); err != nil {
			t.Fatal(err)
		}

		if mutatingCalls != 1 {
			t.Fatalf("order %v: expected the mutating (shipping) consumer to be called exactly once, got %d", order, mutatingCalls)
		}

		got := received()
		if len(got) != 1 {
			t.Fatalf("order %v: expected exactly 1 batch received by the metrics consumer, got %d", order, len(got))
		}

		if _, found := got[0].ResourceLogs().At(0).Resource().Attributes().Get("touched_by"); found {
			t.Fatalf("order %v: the metrics consumer's batch must not carry the shipping consumer's in-place mutation", order)
		}
	}
}

func TestSetupOTLPNetworkReceiverGRPCStartsAndStops(t *testing.T) {
	t.Parallel()

	sink, _ := recordingLogsConsumer()

	protocols := config.NetworkProtocols{GRPC: &config.NetworkEndpoint{Endpoint: "127.0.0.1:0"}}

	recv, err := SetupOTLPNetworkReceiver(t.Context(), NewTelemetrySettings(), protocols, sink, "test-grpc-receiver")
	if err != nil {
		t.Fatal("Failed to start OTLP receiver:", err)
	}

	if err := recv.Shutdown(t.Context()); err != nil {
		t.Fatal("Failed to stop OTLP receiver:", err)
	}
}

// TestSetupOTLPNetworkReceiverHTTPStartsAndStops is a regression test for otlpreceiver panicking on Start due to an invalid transport and missing LogsURLPath.
func TestSetupOTLPNetworkReceiverHTTPStartsAndStops(t *testing.T) {
	t.Parallel()

	sink, _ := recordingLogsConsumer()

	protocols := config.NetworkProtocols{HTTP: &config.NetworkEndpoint{Endpoint: "127.0.0.1:0"}}

	recv, err := SetupOTLPNetworkReceiver(t.Context(), NewTelemetrySettings(), protocols, sink, "test-http-receiver")
	if err != nil {
		t.Fatal("Failed to start OTLP receiver:", err)
	}

	if err := recv.Shutdown(t.Context()); err != nil {
		t.Fatal("Failed to stop OTLP receiver:", err)
	}
}

func TestSetupOTLPNetworkReceiverNoProtocolErrors(t *testing.T) {
	t.Parallel()

	sink, _ := recordingLogsConsumer()

	// Without Validate(), Start silently no-ops instead of erroring when no protocol is set.
	_, err := SetupOTLPNetworkReceiver(t.Context(), NewTelemetrySettings(), config.NetworkProtocols{}, sink, "test-no-protocol")
	if err == nil {
		t.Fatal("Expected an error when no protocol is configured, got none")
	}
}
