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

package logger

import (
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// countingWriteSyncer counts how many times it is written to, standing in for
// zapWrapper so the test can assert on sampling behavior without depending on
// glouton's own V()-level output.
type countingWriteSyncer struct {
	mu sync.Mutex
	n  int
}

func (c *countingWriteSyncer) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()

	return len(p), nil
}

func (c *countingWriteSyncer) Sync() error { return nil }

func (c *countingWriteSyncer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.n
}

// TestZapLoggerSamplesRepeatedMessageTemplate reproduces the incident from
// on_demand_20260623-122247: a Postgres container logged parse errors faster
// than they could be handled, each carrying a unique "entry.timestamp" field.
// The old cache was keyed by the full rendered line, so it never deduped this
// flood and grew without bound. zap's sampler keys on the message template
// (the Entry's Message, before field substitution), so a flood of unique-field
// lines sharing one template must stay bounded, however many lines are logged.
func TestZapLoggerSamplesRepeatedMessageTemplate(t *testing.T) {
	writer := &countingWriteSyncer{}

	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(zap.NewProductionEncoderConfig()),
		writer,
		zap.DebugLevel,
	)

	const (
		tick       = 30 * time.Second
		first      = 10
		thereafter = 100
	)

	logger := zap.New(zapcore.NewSamplerWithOptions(core, tick, first, thereafter))

	const flood = 50_000

	for i := range flood {
		// Same message template, unique field per line — like the stanza
		// parse-errors in the incident.
		logger.Error("Failed to process entry", zap.Int("entry.timestamp", i))
	}

	got := writer.count()

	// Within one tick: first occurrences logged in full, then 1 in thereafter.
	want := first + (flood-first)/thereafter
	if got != want {
		t.Fatalf("sampler wrote %d entries for %d identical-template messages, want %d; "+
			"sampling must key on the message template so a flood of unique-field lines stays bounded", got, flood, want)
	}
}
