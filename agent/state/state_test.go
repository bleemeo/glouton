// Copyright 2015-2023 Bleemeo
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
	"os"
	"testing"

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

	state, _ := Load("not_found", "not_found")

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

	persistentFile, errP := os.CreateTemp("", "persistent")
	cacheFile, errC := os.CreateTemp("", "cache")

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

	persisted = bytes.Trim(persisted, "\n")
	expected := `{"version":1,"agent_uuid":"98a28d20-eb60-4304-aa05-1e1ffe633bee","password":"theSecretPassword","telemetry_id":"78946"}`

	if diff := cmp.Diff(expected, string(persisted)); diff != "" {
		t.Fatal("Unexpected persisted state (-want +got)\n", diff)
	}
}
