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

package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestDefaultCachePath(t *testing.T) {
	tests := []struct {
		persistentPath string
		want           string
	}{
		{
			persistentPath: "state.json",
			want:           "state.cache.json",
		},
		{
			persistentPath: "state.dat",
			want:           "state.cache.dat",
		},
		{
			persistentPath: "state",
			want:           "state.cache",
		},
		{
			persistentPath: "/var/lib/glouton/state.json",
			want:           "/var/lib/glouton/state.cache.json",
		},
		{
			persistentPath: "C:\\ProgramData\\glouton\\state.json",
			want:           "C:\\ProgramData\\glouton\\state.cache.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.persistentPath, func(t *testing.T) {
			if got := DefaultCachePath(tt.persistentPath); got != tt.want {
				t.Errorf("DefaultCachePath() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestBackwardCompatibleV0 check that state version 1 can be read by older Glouton.
// It only test that agent_uuid & password are kept, everything else will be reset.
func TestBackwardCompatibleV0(t *testing.T) {
	const (
		agentUUID = "221812ce-41b4-4881-9154-78d74063d4f4"
		password  = "secret!"
	)

	writer := bytes.NewBuffer(nil)

	state, _ := load(true, "not_found", "not_found")

	_ = state.SetBleemeoCredentials(agentUUID, password)
	_ = state.savePersistentTo(writer)

	var stateV0 map[string]json.RawMessage

	decoder := json.NewDecoder(bytes.NewReader(writer.Bytes()))

	err := decoder.Decode(&stateV0)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		key  string
		want string
	}{
		{key: "agent_uuid", want: agentUUID},
		{key: "password", want: password},
	}

	for _, tt := range tests {
		var got string

		buffer, ok := stateV0[tt.key]
		if !ok {
			t.Fatalf("missing key %s", tt.key)
		}

		err := json.Unmarshal(buffer, &got)
		if err != nil {
			t.Fatalf("Unmarshal failed on key %s: %v", tt.key, err)
		}

		if got != tt.want {
			t.Fatalf("key %s = %s, want %s", tt.key, got, tt.want)
		}
	}
}

func TestLoad(t *testing.T) {
	type args struct {
		persistentPath string
		cachePath      string
	}

	type fakeFactType struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}

	type fakeBleemeoCache struct {
		Version int
		Facts   []fakeFactType
	}

	tests := []struct {
		name           string
		args           args
		wantPersistent persistedState
		wantCache      map[string]json.RawMessage
		wantKeys       map[string]interface{}
		wantErr        bool
	}{
		{
			name: "load from v0",
			args: args{
				persistentPath: "testdata/state-v0.json",
				cachePath:      "testdata/state-v0.cache.json", // does not exists
			},
			wantPersistent: persistedState{
				dirty:           true,
				Version:         stateVersion,
				BleemeoAgentID:  "98a28d20-eb60-4304-aa05-1e1ffe633bee",
				BleemeoPassword: "theSecretPassword",
				TelemetryID:     "78946",
			},
			wantKeys: map[string]interface{}{
				"CacheBleemeoConnector": fakeBleemeoCache{
					Version: 6,
					Facts:   []fakeFactType{{ID: "e3f3ef05-b112-440e-b7c0-44768901c99c", Key: "cpu_model_name"}},
				},
			},
		},
		{
			name: "load from v1",
			args: args{
				persistentPath: "testdata/state-v1.json",
				cachePath:      "testdata/state-v1.cache.json",
			},
			wantPersistent: persistedState{
				dirty:           true,
				Version:         stateVersion,
				BleemeoAgentID:  "98a28d20-eb60-4304-aa05-1e1ffe633bee",
				BleemeoPassword: "theSecretPassword",
				TelemetryID:     "78946",
			},
			wantKeys: map[string]interface{}{
				"CacheBleemeoConnector": fakeBleemeoCache{
					Version: 7,
					Facts:   []fakeFactType{{ID: "e3f3ef05-b112-440e-b7c0-44768901c99c", Key: "cpu_model_name_v2"}},
				},
			},
		},
		{
			name: "load from v0 downgrade",
			args: args{
				persistentPath: "testdata/state-v0.json",
				cachePath:      "testdata/state-v1.cache.json",
			},
			wantPersistent: persistedState{
				dirty:           true,
				Version:         stateVersion,
				BleemeoAgentID:  "98a28d20-eb60-4304-aa05-1e1ffe633bee",
				BleemeoPassword: "theSecretPassword",
				TelemetryID:     "78946",
			},
			wantKeys: map[string]interface{}{
				"CacheBleemeoConnector": fakeBleemeoCache{
					Version: 6,
					Facts:   []fakeFactType{{ID: "e3f3ef05-b112-440e-b7c0-44768901c99c", Key: "cpu_model_name"}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, err := Load(tt.args.persistentPath, tt.args.cachePath)
			if (err != nil) != tt.wantErr {
				t.Errorf("Load() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			if diff := cmp.Diff(tt.wantPersistent, state.persistent, cmpopts.IgnoreUnexported(persistedState{})); diff != "" {
				t.Errorf("persistent mismatch (-want +got)\n%s", diff)
			}

			if diff := cmp.Diff(tt.wantCache, state.cache); tt.wantCache != nil && diff != "" {
				t.Errorf("cache mismatch (-want +got)\n%s", diff)
			}

			for k, v := range tt.wantKeys {
				var got interface{}

				switch v.(type) {
				case fakeBleemeoCache:
					var tmp fakeBleemeoCache

					if err := state.Get(k, &tmp); err != nil {
						t.Error(err)
					}

					got = tmp
				default:
					t.Errorf("Unsupported type for key %s", k)
				}

				if diff := cmp.Diff(v, got); diff != "" {
					t.Errorf("Key %s mismatch (-want +got)\n%s", k, diff)
				}
			}
		})
	}
}

func TestTelemetryFieldMigration(t *testing.T) {
	state, err := Load("testdata/state-v1.json", "testdata/state-v1.cache.json")
	if err != nil {
		t.Fatal("Failed to load state:", err)
	}

	persistentFile, errP := os.CreateTemp(t.TempDir(), "persistent")
	cacheFile, errC := os.CreateTemp(t.TempDir(), "cache")

	if errP != nil || errC != nil {
		t.Skip("Failed to setup test:\n", errors.Join(errP, errC))
	}

	persistentPath, cachePath := persistentFile.Name(), cacheFile.Name()

	_, _ = persistentFile.Close(), cacheFile.Close()

	defer func() {
		_ = os.Remove(persistentPath)
		_ = os.Remove(cachePath)
	}()

	err = state.SaveTo(persistentPath, cachePath)
	if err != nil {
		t.Fatal("Failed to save state:", err)
	}

	persisted, err := os.ReadFile(persistentPath)
	if err != nil {
		t.Fatal("Failed to read persisted state:", err)
	}

	expected, err := os.ReadFile("testdata/state-v1-migrate-telemetry.json")
	if err != nil {
		t.Fatal("Failed to read test data:", err)
	}

	if diff := cmp.Diff(expected, persisted); diff != "" {
		t.Fatal("Unexpected persisted state (-want +got)\n", diff)
	}
}

func TestGetByPrefix(t *testing.T) {
	type a struct {
		A float32 `json:"a"`
	}

	state := State{
		cache: map[string]json.RawMessage{
			"a:b:c1": []byte(`[1, 2, 3]`),
			"a:b:c2": []byte(`[7]`),
			"a:bc:d": []byte(`{"a": 2.5}`),
			"a:":     []byte(`{"b:": "c"}`),
		},
	}

	testCases := []struct {
		prefix     string
		resultType any
		expected   map[string]any
	}{
		{
			prefix:     "a:b:",
			resultType: []int{},
			expected: map[string]any{
				"a:b:c1": []int{1, 2, 3},
				"a:b:c2": []int{7},
			},
		},
		{
			prefix:     "a:b:c2",
			resultType: []int{},
			expected: map[string]any{
				"a:b:c2": []int{7},
			},
		},
		{
			prefix:     "a:bc",
			resultType: a{},
			expected: map[string]any{
				"a:bc:d": a{2.5},
			},
		},
		{
			prefix:     "a:",
			resultType: map[string]any{},
			expected: map[string]any{
				// a{} can also be represented as a map:
				"a:bc:d": map[string]any{
					"a": 2.5,
				},
				"a:": map[string]any{
					"b:": "c",
				},
			},
		},
	}

	for i, tc := range testCases {
		t.Run(fmt.Sprint("n°", i+1), func(t *testing.T) {
			t.Parallel()

			result, err := state.GetByPrefix(tc.prefix, tc.resultType)
			if err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(tc.expected, result); diff != "" {
				t.Errorf("Unexpected result of GetByPrefix(%q, %T): (-want +got)\n%v", tc.prefix, tc.resultType, diff)
			}
		})
	}
}

// TestBackgroundWriter test that state.cache.json is wrote (by the background thread).
func TestBackgroundWriter(t *testing.T) {
	const (
		cacheKeyString  = "this-should-be-stored-in-cache-file"
		cacheKeyString2 = "another-marker-value"
	)

	tmpdir := t.TempDir()

	persistentPath := filepath.Join(tmpdir, "state.json")
	cachePath := filepath.Join(tmpdir, "state.cache.json")

	state, err := Load(persistentPath, cachePath)
	if err != nil {
		t.Fatal(err)
	}

	// Need to call first SaveTo() to create state the first time
	if err := state.SaveTo(persistentPath, cachePath); err != nil {
		t.Fatal(err)
	}

	// Wait for state.cache.json to be written a first time by call to SaveTo
	deadline := time.Now().Add(10 * time.Second)
	fileCreated := false

	for time.Now().Before(deadline) {
		if _, err := os.Stat(cachePath); err == nil {
			fileCreated = true

			break
		}
	}

	if !fileCreated {
		t.Fatalf("cache file not created")
	}

	// This will cause a persistent writes
	id := state.TelemetryID()

	data, err := os.ReadFile(persistentPath)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(data, []byte(id)) {
		t.Errorf("TelemetryID isn't stored in persistent state")
	}

	// This cause a *background* write to state.cache.json
	_ = state.Set("key1", cacheKeyString)

	deadline = time.Now().Add(10 * time.Second)
	valueFound := false

	for time.Now().Before(deadline) {
		data, err := os.ReadFile(cachePath)
		if err != nil {
			t.Fatal(err)
		}

		if bytes.Contains(data, []byte(cacheKeyString)) {
			valueFound = true

			break
		}

		time.Sleep(250 * time.Millisecond)
	}

	if !valueFound {
		t.Errorf("cacheKeyString isn't stored in cache state")
	}

	_ = state.Set("key2", cacheKeyString2)
	// Close ensure the background write finish
	state.Close()

	data, err = os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(data, []byte(cacheKeyString)) {
		t.Errorf("cacheKeyString isn't stored in cache state")
	}

	if !bytes.Contains(data, []byte(cacheKeyString2)) {
		t.Errorf("cacheKeyString2 isn't stored in cache state")
	}
}

// TestBackgroundWriterNoCreateWait is similar to TestBackgroundWriter but ensure that even if
// we don't wait for state.cache.json creation, it will eventually be created.
func TestBackgroundWriterNoCreateWait(t *testing.T) {
	const (
		cacheKeyString  = "this-should-be-stored-in-cache-file"
		cacheKeyString2 = "another-marker-value"
	)

	tmpdir := t.TempDir()

	persistentPath := filepath.Join(tmpdir, "state.json")
	cachePath := filepath.Join(tmpdir, "state.cache.json")

	state, err := Load(persistentPath, cachePath)
	if err != nil {
		t.Fatal(err)
	}

	// Need to call first SaveTo() to create state the first time
	if err := state.SaveTo(persistentPath, cachePath); err != nil {
		t.Fatal(err)
	}

	id := state.TelemetryID()
	_ = state.Set("key1", cacheKeyString)

	// persistent is always created.
	data, err := os.ReadFile(persistentPath)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(data, []byte(id)) {
		t.Errorf("TelemetryID isn't stored in persistent state")
	}

	// At this point, state.cache.json might not be created
	_, err = os.Stat(cachePath)
	if errors.Is(err, os.ErrNotExist) {
		t.Log("cache file don't exists, test will fully test that modification done before cache.json created aren't lost")
	} else {
		t.Log("cache file already exists, test might not fully test that modification done before cache.json created aren't lost")
	}

	// Need to wait for state.cache.json to be written at least once
	deadline := time.Now().Add(10 * time.Second)
	fileCreated := false

	for time.Now().Before(deadline) {
		if _, err := os.Stat(cachePath); err == nil {
			fileCreated = true

			break
		}
	}

	if !fileCreated {
		t.Fatalf("cache file not created")
	}

	// Cache is allowed to not contains the first key, because the background thread might just write
	// the empty state requested in SaveTo() and could submitting write asked by Set().
	// But any additional change to cache will fix the issue
	_ = state.Set("key2", cacheKeyString2)
	state.Close()

	data, err = os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(data, []byte(cacheKeyString)) {
		t.Errorf("cacheKeyString isn't stored in cache state")
	}

	if !bytes.Contains(data, []byte(cacheKeyString2)) {
		t.Errorf("cacheKeyString2 isn't stored in cache state")
	}
}

// TestSkipCacheWriteIfRemoved make sure that state.cache.json isn't created by Glouton
// if it was deleted.
func TestSkipCacheWriteIfRemoved(t *testing.T) {
	const (
		cacheKeyString  = "this-should-be-stored-in-cache-file"
		cacheKeyString2 = "another-marker-value"
		agentID         = "agent-id"
		password1       = "password1"
		password2       = "password2"
	)

	tmpdir := t.TempDir()

	persistentPath := filepath.Join(tmpdir, "state.json")
	cachePath := filepath.Join(tmpdir, "state.cache.json")

	state, err := Load(persistentPath, cachePath)
	if err != nil {
		t.Fatal(err)
	}

	// Need to call first SaveTo() to create state the first time
	if err := state.SaveTo(persistentPath, cachePath); err != nil {
		t.Fatal(err)
	}

	// Wait for state.cache.json to be written a first time by call to SaveTo
	deadline := time.Now().Add(10 * time.Second)
	fileCreated := false

	for time.Now().Before(deadline) {
		if _, err := os.Stat(cachePath); err == nil {
			fileCreated = true

			break
		}
	}

	if !fileCreated {
		t.Fatalf("cache file not created")
	}

	_ = state.SetBleemeoCredentials(agentID, password1)
	_ = state.Set("key1", cacheKeyString)

	data, err := os.ReadFile(persistentPath)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(data, []byte(password1)) {
		t.Errorf("password1 isn't stored in persistent state")
	}

	deadline = time.Now().Add(10 * time.Second)
	valueFound := false

	for time.Now().Before(deadline) {
		data, err := os.ReadFile(cachePath)
		if err != nil {
			t.Fatal(err)
		}

		if bytes.Contains(data, []byte(cacheKeyString)) {
			valueFound = true

			break
		}

		time.Sleep(250 * time.Millisecond)
	}

	if !valueFound {
		t.Errorf("cacheKeyString isn't stored in cache state")
	}

	// user now remove the state.cache.json
	_ = os.Remove(cachePath)

	_ = state.SetBleemeoCredentials(agentID, password2)
	_ = state.Set("key2", cacheKeyString2)

	// persistent get updated, but state.cache.json will not be created at all
	data, err = os.ReadFile(persistentPath)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(data, []byte(password2)) {
		t.Errorf("password1 isn't stored in persistent state")
	}

	deadline = time.Now().Add(3 * time.Second)
	fileCreated = false

	for time.Now().Before(deadline) {
		if _, err := os.Stat(cachePath); err == nil {
			fileCreated = true

			break
		}
	}

	if fileCreated {
		t.Fatalf("cache file IS created")
	}

	state.Close()

	if _, err := os.Stat(cachePath); err == nil {
		t.Fatalf("cache file IS created")
	}
}
