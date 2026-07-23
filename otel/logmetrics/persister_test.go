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
)

// TestPersistHostSaveKeepsUntouchedReceivers is the regression test for the
// save/overwrite bug: a periodic saveToState must not drop a receiver's
// previously-persisted offset just because that receiver hasn't been written
// to yet during this process's lifetime (e.g. an idle source).
func TestPersistHostSaveKeepsUntouchedReceivers(t *testing.T) {
	t.Parallel()

	state := newMemoryState()

	initial := map[string]map[string][]byte{
		"A": {"offset": []byte("A1")},
		"B": {"offset": []byte("B1")},
	}

	if err := state.Set(fileMetadataCacheKey, initial); err != nil {
		t.Fatal("Failed to seed initial state:", err)
	}

	h, err := newPersistHost(state)
	if err != nil {
		t.Fatal("newPersistHost failed:", err)
	}

	idA := h.newPersistentExt("A")
	h.newPersistentExt("B") // never touched afterwards

	extA, ok := h.extensions[idA].(persistExtension)
	if !ok {
		t.Fatal("Expected a persistExtension for A")
	}

	if err := extA.client.Set(t.Context(), "offset", []byte("A2")); err != nil {
		t.Fatal("Failed to update A's offset:", err)
	}

	h.saveToState(state)

	var saved map[string]map[string][]byte

	if err := state.Get(fileMetadataCacheKey, &saved); err != nil {
		t.Fatal("Failed to read back saved state:", err)
	}

	if got := string(saved["A"]["offset"]); got != "A2" {
		t.Errorf("Expected A's offset to be updated to %q, got %q", "A2", got)
	}

	if got, found := saved["B"]; !found || string(got["offset"]) != "B1" {
		t.Errorf("Expected B's untouched offset %q to survive the save, got %v (found=%v)", "B1", got, found)
	}
}

// TestStorageClientCloseKeepsFullSnapshot is the regression test for the same
// bug one level down: closing a storage client that was only ever Get() from
// (never Set()) must not erase its previously-known offsets from the host.
func TestStorageClientCloseKeepsFullSnapshot(t *testing.T) {
	t.Parallel()

	state := newMemoryState()

	initial := map[string]map[string][]byte{
		"A": {"offset": []byte("A1")},
	}

	if err := state.Set(fileMetadataCacheKey, initial); err != nil {
		t.Fatal("Failed to seed initial state:", err)
	}

	h, err := newPersistHost(state)
	if err != nil {
		t.Fatal("newPersistHost failed:", err)
	}

	idA := h.newPersistentExt("A")

	extA, ok := h.extensions[idA].(persistExtension)
	if !ok {
		t.Fatal("Expected a persistExtension for A")
	}

	// Read-only access this run: never call Set before Close.
	if _, err := extA.client.Get(t.Context(), "offset"); err != nil {
		t.Fatal("Get failed:", err)
	}

	if err := extA.client.Close(t.Context()); err != nil {
		t.Fatal("Close failed:", err)
	}

	h.saveToState(state)

	var saved map[string]map[string][]byte

	if err := state.Get(fileMetadataCacheKey, &saved); err != nil {
		t.Fatal("Failed to read back saved state:", err)
	}

	if got, found := saved["A"]; !found || string(got["offset"]) != "A1" {
		t.Errorf("Expected A's offset %q to survive an idle Close(), got %v (found=%v)", "A1", got, found)
	}
}
