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
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"

	"go.opentelemetry.io/collector/pdata/plog"
)

// TestBuildOperatorsFromKnownFormat smoke-tests resolving a known_log_formats entry by name into operator.Config values.
func TestBuildOperatorsFromKnownFormat(t *testing.T) {
	t.Parallel()

	knownFormats := map[string][]config.OTELOperator{
		"apache_access": {
			{
				"type":  "regex_parser",
				"regex": `^(?P<http_response_status_code>\d+)$`,
			},
		},
	}

	expanded, err := ExpandLogFormats(knownFormats)
	if err != nil {
		t.Fatal("ExpandLogFormats returned an error:", err)
	}

	ops, err := BuildOperators(expanded["apache_access"])
	if err != nil {
		t.Fatal("BuildOperators returned an error:", err)
	}

	if len(ops) != 1 {
		t.Fatalf("Expected exactly 1 built operator, got %d", len(ops))
	}
}

// TestWrapWithOperatorsEmptyIsExactPassthrough guards the zero-cost passthrough contract wrapWithOperators
// promises for the overwhelmingly common case: a from_listeners receiver with no operators/log_format set.
func TestWrapWithOperatorsEmptyIsExactPassthrough(t *testing.T) {
	t.Parallel()

	next, _ := recordingLogsConsumer()

	got, cleanup := wrapWithOperators(nil, NewTelemetrySettings(), next)

	cleanup()

	if got != next {
		t.Fatal("expected an exact passthrough of next when operators is empty")
	}
}

// TestWrapWithOperatorsNilNextIsExactPassthrough guards the nil-fanout case (no SinkProvider wants this
// source): wrapping must not turn a nil consumer into a non-nil one, or NetworkWants'/
// PlanSharedNetworkListeners' existing nil-Consumer skip would stop working.
func TestWrapWithOperatorsNilNextIsExactPassthrough(t *testing.T) {
	t.Parallel()

	ops, err := BuildOperators([]config.OTELOperator{{"type": "add", "field": "attributes.tag", "value": "x"}})
	if err != nil {
		t.Fatal("BuildOperators returned an error:", err)
	}

	got, cleanup := wrapWithOperators(ops, NewTelemetrySettings(), nil)

	cleanup()

	if got != nil {
		t.Fatalf("expected a nil passthrough when next is nil, got %v", got)
	}
}

// TestWrapWithOperatorsAppliesOperator checks the bridge end-to-end: a batch pushed through the wrapped
// consumer reaches next with the operator's transform applied.
func TestWrapWithOperatorsAppliesOperator(t *testing.T) {
	t.Parallel()

	ops, err := BuildOperators([]config.OTELOperator{{"type": "add", "field": "attributes.tag", "value": "net"}})
	if err != nil {
		t.Fatal("BuildOperators returned an error:", err)
	}

	next, received := recordingLogsConsumer()

	wrapped, cleanup := wrapWithOperators(ops, NewTelemetrySettings(), next)

	defer cleanup()

	if wrapped == next {
		t.Fatal("expected a wrapping consumer, not an exact passthrough, when operators is non-empty")
	}

	if err := wrapped.ConsumeLogs(t.Context(), makeLogs("network")); err != nil {
		t.Fatal("ConsumeLogs returned an error:", err)
	}

	got, ok := firstLogRecordTagValue(received())
	if !ok || got != "net" {
		t.Fatalf("expected the received record to carry tag=net, got %q (found=%v)", got, ok)
	}
}

// makeMultiResourceLogs builds one plog.Logs with n distinct (ResourceLogs x ScopeLogs) pairs, one log
// record each -- the unit runThroughOperators counts to know how many converted batches to wait for.
func makeMultiResourceLogs(n int) plog.Logs {
	ld := plog.NewLogs()

	for range n {
		rl := ld.ResourceLogs().AppendEmpty()
		rl.Resource().Attributes().PutStr("marker", "network")
		rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("line")
	}

	return ld
}

// TestWrapWithOperatorsManyResourceScopePairsDoesNotDeadlock guards against a regression where
// runThroughOperators called fromPdata.Batch(ld) to completion before draining OutChannel(): with
// wrapWithOperators' workerCount=1, Batch()'s buffer-1 workerChan plus the single worker blocking on the
// unbuffered OutChannel() meant Batch() itself permanently blocked trying to enqueue a 3rd
// (ResourceLogs x ScopeLogs) pair, since nothing was draining OutChannel() concurrently to unblock the
// worker. That wedged not just this call but every future ConsumeLogs on the same wrapped consumer,
// since the single worker goroutine never recovers. Uses a goroutine + timeout instead of calling
// ConsumeLogs directly so a regression fails this test instead of hanging `go test` forever.
func TestWrapWithOperatorsManyResourceScopePairsDoesNotDeadlock(t *testing.T) {
	t.Parallel()

	ops, err := BuildOperators([]config.OTELOperator{{"type": "add", "field": "attributes.tag", "value": "net"}})
	if err != nil {
		t.Fatal("BuildOperators returned an error:", err)
	}

	next, received := recordingLogsConsumer()

	wrapped, cleanup := wrapWithOperators(ops, NewTelemetrySettings(), next)

	defer cleanup()

	const pairs = 10

	done := make(chan error, 1)

	go func() {
		done <- wrapped.ConsumeLogs(t.Context(), makeMultiResourceLogs(pairs))
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal("ConsumeLogs returned an error:", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("ConsumeLogs deadlocked on %d (ResourceLogs x ScopeLogs) pairs", pairs)
	}

	if got := len(received()); got != pairs {
		t.Fatalf("expected %d converted batches to reach next, got %d", pairs, got)
	}
}
