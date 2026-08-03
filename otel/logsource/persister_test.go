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
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/bleemeo/glouton/agent/state"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"

	"github.com/google/go-cmp/cmp"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/adapter"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension/xextension/storage"
)

const (
	testStorageType = "test_storage"
	testCacheKey    = "TestFileMetadata"
)

func touchedOnlyConfig() PersistConfig {
	return PersistConfig{StorageType: testStorageType, CacheKey: testCacheKey}
}

func fullSnapshotConfig() PersistConfig {
	return PersistConfig{StorageType: testStorageType, CacheKey: testCacheKey, FullSnapshot: true}
}

// memoryState is a real (JSON round-trip, not a no-op) bleemeoTypes.State fake.
type memoryState struct {
	l    sync.Mutex
	data map[string][]byte
}

func newMemoryState() *memoryState {
	return &memoryState{data: make(map[string][]byte)}
}

func (m *memoryState) Set(key string, object any) error {
	b, err := json.Marshal(object)
	if err != nil {
		return err
	}

	m.l.Lock()
	m.data[key] = b
	m.l.Unlock()

	return nil
}

func (m *memoryState) Get(key string, result any) error {
	m.l.Lock()
	b, found := m.data[key]
	m.l.Unlock()

	if !found {
		return nil
	}

	return json.Unmarshal(b, result)
}

func (m *memoryState) GetByPrefix(string, any) (map[string]any, error) { return map[string]any{}, nil }
func (m *memoryState) Delete(string) error                             { return nil }
func (m *memoryState) BleemeoCredentials() (string, string)            { return "", "" }
func (m *memoryState) SetBleemeoCredentials(string, string) error      { return nil }

// TestPersistHostTouchedOnlyEvictsUntouchedReceivers verifies that only touched receivers are persisted.
func TestPersistHostTouchedOnlyEvictsUntouchedReceivers(t *testing.T) { //nolint:maintidx
	t.Parallel()

	const (
		ext1 = "ext1"
		ext2 = "ext2"

		key1 = "key1"
		key2 = "key2"

		val1 = "val1"
		val2 = "val2"
	)

	ctx := t.Context()
	compID := component.MustNewID("unused")

	st, err := state.LoadReadOnly("not", "used")
	if err != nil {
		t.Fatal("Can't instantiate state:", err)
	}

	// - - - First run: starting with no existing metadata - - -
	{
		host, err := NewPersistHost(st, touchedOnlyConfig())
		if err != nil {
			t.Fatal("Can't instantiate persist host:", err)
		}

		extID := host.NewPersistentExt(ext1)

		client, err := adapter.GetStorageClient(ctx, host, &extID, compID)
		if err != nil {
			t.Fatal("Can't retrieve storage client:", err)
		}

		if err := client.Set(ctx, key1, []byte(val1)); err != nil {
			t.Fatal("Can't set value:", err)
		}

		v, err := client.Get(ctx, key1)
		if err != nil {
			t.Fatal("Can't retrieve value:", err)
		}

		if !bytes.Equal(v, []byte(val1)) {
			t.Fatalf("Unexpected value: want %q, got %q", val1, v)
		}

		if err := client.Set(ctx, key2, []byte(val2)); err != nil {
			t.Fatal("Can't set value:", err)
		}

		if err := client.Delete(ctx, key1); err != nil {
			t.Fatal("Can't delete value:", err)
		}

		v, err = client.Get(ctx, key1)
		if err != nil {
			t.Fatal("Can't retrieve value:", err)
		}

		if v != nil {
			t.Fatal("Deleted key should have a nil value, but:", v)
		}

		if err := client.Close(ctx); err != nil {
			t.Fatal("Can't close client:", err)
		}

		metadata := host.getAllMetadata()
		expectedMetadata := map[string]map[string][]byte{
			ext1: {key2: []byte(val2)},
		}

		if diff := cmp.Diff(expectedMetadata, metadata); diff != "" {
			t.Fatalf("Unexpected metadata (-want +got):\n%s\n", diff)
		}

		saveFileMetadataToCache(st, testCacheKey, metadata)
	}

	// - - - Second run: starting with pre-existing metadata - - -
	{
		host, err := NewPersistHost(st, touchedOnlyConfig())
		if err != nil {
			t.Fatal("Can't instantiate persist host:", err)
		}

		ext1ID := host.NewPersistentExt(ext1)
		ext2ID := host.NewPersistentExt(ext2)

		client1, err := adapter.GetStorageClient(ctx, host, &ext1ID, compID)
		if err != nil {
			t.Fatal("Can't retrieve storage client:", err)
		}

		client2, err := adapter.GetStorageClient(ctx, host, &ext2ID, compID)
		if err != nil {
			t.Fatal("Can't retrieve storage client:", err)
		}

		v, err := client1.Get(ctx, key1)
		if err != nil {
			t.Fatal("Can't retrieve value:", err)
		}

		if v != nil {
			t.Fatal("Non-existing key should have a nil value, but:", v)
		}

		v, err = client1.Get(ctx, key2)
		if err != nil {
			t.Fatal("Can't retrieve value:", err)
		}

		if !bytes.Equal(v, []byte(val2)) {
			t.Fatalf("Unexpected value: want %q, got %q", val2, v)
		}

		if err := client1.Set(ctx, key1, []byte(val1)); err != nil {
			t.Fatal("Can't set value:", err)
		}

		if err := client2.Set(ctx, key1, []byte(val1)); err != nil {
			t.Fatal("Can't set value:", err)
		}

		if err := client1.Close(ctx); err != nil {
			t.Fatal("Can't close client:", err)
		}

		if err := client2.Close(ctx); err != nil {
			t.Fatal("Can't close client:", err)
		}

		host.SaveToState(st)

		metadata, err := getFileMetadataFromCache(st, testCacheKey)
		if err != nil {
			t.Fatal("Can't get metadata from state:", err)
		}

		expectedMetadata := map[string]map[string][]byte{
			ext1: {key1: []byte(val1)},
			ext2: {key1: []byte(val1)},
		}

		if diff := cmp.Diff(expectedMetadata, metadata); diff != "" {
			t.Fatalf("Unexpected metadata (-want +got):\n%s\n", diff)
		}
	}

	// - - - Third run: starting with pre-existing metadata, again - - -
	{
		host, err := NewPersistHost(st, touchedOnlyConfig())
		if err != nil {
			t.Fatal("Can't instantiate persist host:", err)
		}

		extID := host.NewPersistentExt(ext1)

		client, err := adapter.GetStorageClient(ctx, host, &extID, compID)
		if err != nil {
			t.Fatal("Can't retrieve storage client:", err)
		}

		// Ensuring only values from runs prior than the last one have been discarded

		v, err := client.Get(ctx, key2)
		if err != nil {
			t.Fatal("Can't retrieve value:", err)
		}

		if v != nil {
			t.Fatal("Value should have been discarded, since it wasn't set in the last run")
		}

		v, err = client.Get(ctx, key1)
		if err != nil {
			t.Fatal("Can't retrieve value:", err)
		}

		if !bytes.Equal(v, []byte(val1)) {
			t.Fatalf("Unexpected value: want %q, got %q", val1, v)
		}

		if err := client.Close(ctx); err != nil {
			t.Fatal("Can't close client:", err)
		}

		// No values have been written this run, so none should be persisted

		metadata := host.getAllMetadata()
		expectedMetadata := map[string]map[string][]byte{
			ext1: {},
		}

		if diff := cmp.Diff(expectedMetadata, metadata); diff != "" {
			t.Fatalf("Unexpected metadata (-want +got):\n%s\n", diff)
		}
	}
}

// TestPersistHostFullSnapshotKeepsUntouchedReceivers verifies idle receivers' offsets aren't dropped on save.
func TestPersistHostFullSnapshotKeepsUntouchedReceivers(t *testing.T) {
	t.Parallel()

	st := newMemoryState()

	initial := map[string]map[string][]byte{
		"A": {"offset": []byte("A1")},
		"B": {"offset": []byte("B1")},
	}

	if err := st.Set(testCacheKey, initial); err != nil {
		t.Fatal("Failed to seed initial state:", err)
	}

	h, err := NewPersistHost(st, fullSnapshotConfig())
	if err != nil {
		t.Fatal("NewPersistHost failed:", err)
	}

	idA := h.NewPersistentExt("A")
	h.NewPersistentExt("B") // never touched afterwards

	extA, ok := h.extensions[idA].(persistExtension)
	if !ok {
		t.Fatal("Expected a persistExtension for A")
	}

	if err := extA.client.Set(t.Context(), "offset", []byte("A2")); err != nil {
		t.Fatal("Failed to update A's offset:", err)
	}

	h.SaveToState(st)

	var saved map[string]map[string][]byte

	if err := st.Get(testCacheKey, &saved); err != nil {
		t.Fatal("Failed to read back saved state:", err)
	}

	if got := string(saved["A"]["offset"]); got != "A2" {
		t.Errorf("Expected A's offset to be updated to %q, got %q", "A2", got)
	}

	if got, found := saved["B"]; !found || string(got["offset"]) != "B1" {
		t.Errorf("Expected B's untouched offset %q to survive the save, got %v (found=%v)", "B1", got, found)
	}
}

// TestStorageClientFullSnapshotCloseKeepsSnapshot verifies that closing a read-only client preserves its offsets.
func TestStorageClientFullSnapshotCloseKeepsSnapshot(t *testing.T) {
	t.Parallel()

	st := newMemoryState()

	initial := map[string]map[string][]byte{
		"A": {"offset": []byte("A1")},
	}

	if err := st.Set(testCacheKey, initial); err != nil {
		t.Fatal("Failed to seed initial state:", err)
	}

	h, err := NewPersistHost(st, fullSnapshotConfig())
	if err != nil {
		t.Fatal("NewPersistHost failed:", err)
	}

	idA := h.NewPersistentExt("A")

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

	h.SaveToState(st)

	var saved map[string]map[string][]byte

	if err := st.Get(testCacheKey, &saved); err != nil {
		t.Fatal("Failed to read back saved state:", err)
	}

	if got, found := saved["A"]; !found || string(got["offset"]) != "A1" {
		t.Errorf("Expected A's offset %q to survive an idle Close(), got %v (found=%v)", "A1", got, found)
	}
}

type stateMock struct {
	bleemeoTypes.State
}

func (stateMock) Get(string, any) error {
	return nil
}

func (stateMock) Set(string, any) error {
	return nil
}

// TestStorageClient exercises concurrent storageClient usage; run with -race.
func TestStorageClient(t *testing.T) {
	t.Parallel()

	h, err := NewPersistHost(stateMock{}, touchedOnlyConfig())
	if err != nil {
		t.Fatal("Can't instantiate persist host:", err)
	}

	extID := h.NewPersistentExt("test")
	ext := h.extensions[extID]

	persistExt, ok := ext.(persistExtension)
	if !ok {
		t.Fatalf("Unexpected extension type: %T", ext)
	}

	storageCl, err := persistExt.GetClient(t.Context(), component.KindReceiver, extID, "test")
	if err != nil {
		t.Fatal("Can't retrieve storage client:", err)
	}

	const storageKey = "test"

	makeFunc := func(i int) func() {
		if i%2 == 0 {
			return func() {
				value := []byte("Hello there")

				err := storageCl.Set(t.Context(), storageKey, value)
				if err != nil {
					t.Error("Can't set value:", err)
				}

				value[0] = 'W' // later use of the variable
			}
		}

		return func() {
			value, err := storageCl.Get(t.Context(), storageKey)
			if err != nil {
				t.Error("Can't get value:", err)
			}

			if value != nil {
				_ = value[0] // making use of the retrieved value
			}
		}
	}

	wg := new(sync.WaitGroup)

	for i := range 10 {
		fn := makeFunc(i)

		wg.Go(func() {
			for range 100 {
				fn()
			}
		})
	}

	wg.Wait()
}

// TestPersistHostConcurrent exercises concurrent multi-extension access with Set/Get and SaveToState; run with -race.
func TestPersistHostConcurrent(t *testing.T) {
	t.Parallel()

	const (
		numExtensions = 5
		numOps        = 100
		numSaveCalls  = 50
	)

	ctx := t.Context()
	compID := component.MustNewID("unused")

	h, err := NewPersistHost(stateMock{}, touchedOnlyConfig())
	if err != nil {
		t.Fatal("Can't instantiate persist host:", err)
	}

	clients := make([]storage.Client, numExtensions)

	for range 2 {
		for i := range numExtensions {
			name := fmt.Sprintf("ext%d", i)

			// Use one duplicated extension name
			if i == numExtensions-1 {
				name = fmt.Sprintf("ext%d", 0)
			}

			extID := h.NewPersistentExt(name)

			clients[i], err = adapter.GetStorageClient(ctx, h, &extID, compID)
			if err != nil {
				t.Fatal("Can't retrieve storage client:", err)
			}
		}

		wg := new(sync.WaitGroup)

		// Each extension performs concurrent Set/Get operations.
		for i, client := range clients {
			wg.Go(func() {
				for j := range numOps {
					key := fmt.Sprintf("key%d", j%10)
					value := fmt.Appendf(nil, "value-%d-%d", i, j)

					if j%2 == 0 {
						if err := client.Set(ctx, key, value); err != nil {
							t.Error("Can't set value:", err)
						}
					} else {
						v, err := client.Get(ctx, key)
						if err != nil {
							t.Error("Can't get value:", err)
						}

						if v != nil {
							_ = v[0] // use the retrieved value
						}
					}
				}
			})
		}

		// Simulate the periodic ticker: call SaveToState concurrently.
		wg.Go(func() {
			for range numSaveCalls {
				h.SaveToState(stateMock{})
			}
		})

		wg.Wait()

		// Shutdown (save state) and then re-run test
		for _, ext := range h.GetExtensions() {
			if p, ok := ext.(persistExtension); ok {
				p.client.saveMetadata()
			}
		}

		h.SaveToState(stateMock{})
	}
}
