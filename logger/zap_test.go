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
	"fmt"
	"testing"
	"time"
)

// TestZapWrapperDebounceCacheBounded reproduces (and guards against) the memory
// leak diagnosed from on_demand_20260623-122247: the log de-duplication cache
// (zapWrapper.m) is keyed by the full message, so a flood of *unique* messages
// adds one entry per message and is only purged every 10 minutes. In production
// a Postgres container logged faster than it could be parsed; each parse-error
// carried a unique "entry.timestamp", so the cache grew to millions of entries
// and retained gigabytes (it was 61% of the live heap in the profile).
//
// The cache must stay bounded regardless of how many unique messages arrive.
func TestZapWrapperDebounceCacheBounded(t *testing.T) {
	// The cache is populated independently of the log level; lower it only to
	// keep the test from spamming the console.
	cfg.l.Lock()
	oldLevel := cfg.level
	cfg.l.Unlock()

	SetLevel(0)

	defer SetLevel(oldLevel)

	z := &zapWrapper{lastPurge: time.Now(), m: make(map[string]time.Time)}

	const flood = 50_000

	for i := range flood {
		// Unique per line, like the stanza parse-errors in the incident.
		_, _ = z.Write([]byte(fmt.Sprintf("error\tFailed to process entry\t{\"entry.timestamp\": %d}\n", i)))
	}

	z.l.Lock()
	got := len(z.m)
	z.l.Unlock()

	// Before the fix the cache held one entry per unique message (== flood),
	// i.e. it grew without bound. The cap must keep it at logDebounceMaxEntries.
	if got > logDebounceMaxEntries {
		t.Fatalf("debounce cache holds %d entries after %d unique messages; it must stay bounded "+
			"(<= %d) — otherwise it retains GBs under a log flood", got, flood, logDebounceMaxEntries)
	}
}
