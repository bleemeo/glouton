// Copyright 2015-2025 Bleemeo
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

package logprocessing

import (
	"bytes"
	"log"
	"sync"
	"testing"

	"github.com/bleemeo/glouton/agent/state"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"

	"github.com/google/go-cmp/cmp"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/adapter"
	"go.opentelemetry.io/collector/component"
)

func TestPersistHost(t *testing.T) { //nolint:maintidx
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
		host, err := newPersistHost(st)
		if err != nil {
			t.Fatal("Can't instantiate persist host:", err)
		}

		extID := host.newPersistentExt(ext1)

		client, err := adapter.GetStorageClient(ctx, host, &extID, compID)
		if err != nil {
			t.Fatal("Can't retrieve storage client:", err)
		}

		err = client.Set(ctx, key1, []byte(val1))
		if err != nil {
			t.Fatal("Can't set value:", err)
		}

		v, err := client.Get(ctx, key1)
		if err != nil {
			t.Fatal("Can't retrieve value:", err)
		}

		if !bytes.Equal(v, []byte(val1)) {
			t.Fatalf("Unexpected value: want %q, got %q", val1, v)
		}

		err = client.Set(ctx, key2, []byte(val2))
		if err != nil {
			t.Fatal("Can't set value:", err)
		}

		err = client.Delete(ctx, key1)
		if err != nil {
			t.Fatal("Can't delete value:", err)
		}

		v, err = client.Get(ctx, key1)
		if err != nil {
			t.Fatal("Can't retrieve value:", err)
		}

		if v != nil {
			t.Fatal("Deleted key should have a nil value, but:", v)
		}

		err = client.Close(ctx)
		if err != nil {
			t.Fatal("Can't close client:", err)
		}

		metadata := host.getAllMetadata()
		expectedMetadata := map[string]map[string][]byte{
			ext1: {
				key2: []byte(val2),
			},
		}

		if diff := cmp.Diff(expectedMetadata, metadata); diff != "" {
			log.Fatalf("Unexpected metadata (-want +got):\n%s\n", diff)
		}

		// Writing it to the state for the second phase
		saveFileMetadataToCache(st, metadata)
	}

	// - - - Second run: starting with pre-existing metadata - - -
	{
		host, err := newPersistHost(st)
		if err != nil {
			t.Fatal("Can't instantiate persist host:", err)
		}

		ext1ID := host.newPersistentExt(ext1)
		ext2ID := host.newPersistentExt(ext2)

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

		// Retrieving a value from the last run (through the state)

		v, err = client1.Get(ctx, key2)
		if err != nil {
			t.Fatal("Can't retrieve value:", err)
		}

		if !bytes.Equal(v, []byte(val2)) {
			t.Fatalf("Unexpected value: want %q, got %q", val2, v)
		}

		// Checking for conflicts between clients

		err = client1.Set(ctx, key1, []byte(val1))
		if err != nil {
			t.Fatal("Can't set value:", err)
		}

		err = client2.Set(ctx, key1, []byte(val1))
		if err != nil {
			t.Fatal("Can't set value:", err)
		}

		err = client1.Close(ctx)
		if err != nil {
			t.Fatal("Can't close client:", err)
		}

		err = client2.Close(ctx)
		if err != nil {
			t.Fatal("Can't close client:", err)
		}

		saveFileMetadataToCache(st, host.getAllMetadata())

		metadata, err := getFileMetadataFromCache(st)
		if err != nil {
			t.Fatal("Can't get metadata from state:", err)
		}

		expectedMetadata := map[string]map[string][]byte{
			ext1: {
				key1: []byte(val1),
			},
			ext2: {
				key1: []byte(val1),
			},
		}

		if diff := cmp.Diff(expectedMetadata, metadata); diff != "" {
			log.Fatalf("Unexpected metadata (-want +got):\n%s\n", diff)
		}
	}

	// - - - Third run: starting with pre-existing metadata, again - - -
	{
		host, err := newPersistHost(st)
		if err != nil {
			t.Fatal("Can't instantiate persist host:", err)
		}

		extID := host.newPersistentExt(ext1)

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

		// But the value written the very last run should still be present

		v, err = client.Get(ctx, key1)
		if err != nil {
			t.Fatal("Can't retrieve value:", err)
		}

		if !bytes.Equal(v, []byte(val1)) {
			t.Fatalf("Unexpected value: want %q, got %q", val1, v)
		}

		err = client.Close(ctx)
		if err != nil {
			t.Fatal("Can't close client:", err)
		}

		// No values have been written this run, so none should be persisted

		metadata := host.getAllMetadata()
		expectedMetadata := map[string]map[string][]byte{
			ext1: {},
		}

		if diff := cmp.Diff(expectedMetadata, metadata); diff != "" {
			log.Fatalf("Unexpected metadata (-want +got):\n%s\n", diff)
		}
	}
}

type stateMock struct {
	bleemeoTypes.State
}

func (stateMock) Get(string, any) error {
	return nil
}

// TestStorageClient tests concurrent usage of the storageClient,
// and should be executed with the -race flag.
func TestStorageClient(t *testing.T) {
	t.Parallel()

	h, err := newPersistHost(stateMock{})
	if err != nil {
		t.Fatal("Can't instantiate persist host:", err)
	}

	extID := h.newPersistentExt("test")
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
