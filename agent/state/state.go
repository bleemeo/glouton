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
	"encoding/json"
	"errors"
	"fmt"
	"glouton/logger"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/mitchellh/mapstructure"
)

const stateVersion = 1

const (
	KeyKubernetesCluster = "kubernetes_cluster_name"
	tmpExt               = ".tmp"
)

var errVersionIncompatible = errors.New("state.json is incompatible with this glouton")

type persistedState struct {
	Version         int    `json:"version"`
	BleemeoAgentID  string `json:"agent_uuid"`
	BleemeoPassword string `json:"password"`
	TelemetryID     string `json:"telemetry_id"`
	// TelemeryID is being migrated to TelemetryID.
	TelemeryID string `json:"telemery_id,omitempty"`
	dirty      bool   `json:"-"`
}

// State is both state.json and state.cache.json.
type State struct {
	persistent persistedState
	cache      map[string]json.RawMessage

	l              sync.Mutex
	persistentPath string
	cachePath      string
	isInMemory     bool
}

func DefaultCachePath(persistentPath string) string {
	ext := filepath.Ext(persistentPath)

	return fmt.Sprintf("%s.cache%s", persistentPath[0:len(persistentPath)-len(ext)], ext)
}

// Load loads state.json file.
func Load(persistentPath string, cachePath string) (*State, error) {
	return load(false, persistentPath, cachePath)
}

// LoadReadOnly create a state that don't write file. It only read file initially and then work from memory.
// File could be omitted by using empty string but you should probably omit both or none or state versionning might
// cause trouble.
// This function is mostly present for test that need a state mock.
// SaveTo will use a file and remove the fact that state is only in-memory.
func LoadReadOnly(persistentPath string, cachePath string) (*State, error) {
	return load(true, persistentPath, cachePath)
}

// load loads state.json file.
func load(readOnly bool, persistentPath string, cachePath string) (*State, error) {
	state := State{
		persistentPath: persistentPath,
		cachePath:      cachePath,
		cache:          make(map[string]json.RawMessage),
		isInMemory:     readOnly,
	}

	if readOnly {
		state.persistentPath = ""
		state.cachePath = ""
	}

	if persistentPath != "" {
		f, err := os.Open(persistentPath)
		if err != nil && os.IsNotExist(err) {
			state.persistent.Version = stateVersion
			state.persistent.dirty = true

			return &state, nil
		} else if err != nil {
			return nil, err
		}

		decoder := json.NewDecoder(f)
		err = decoder.Decode(&state.persistent)

		f.Close()

		if err != nil {
			return nil, err
		}

		if state.persistent.TelemetryID == "" {
			// Migrate old 'telemery' field to new telemetry field
			state.persistent.TelemetryID = state.persistent.TelemeryID
			state.persistent.TelemeryID = "" // so that it is omitted during the next marshaling

			state.persistent.dirty = true
		}
	} else {
		state.persistent.Version = stateVersion
	}

	if cachePath != "" {
		f, err := os.Open(cachePath)
		if err == nil {
			decoder := json.NewDecoder(f)
			err = decoder.Decode(&state.cache)

			if err != nil {
				logger.V(1).Printf("unable to load state cache: %v", err)
			}

			f.Close()
		} else {
			logger.V(1).Printf("unable to load state cache: %v", err)
		}
	}

	upgradeCount := 0
	for state.persistent.Version != stateVersion {
		if upgradeCount > stateVersion+1 {
			return nil, fmt.Errorf("%w: too many try to upgrade state version", errVersionIncompatible)
		}

		upgradeCount++

		if err := state.loadFromV0(); err != nil {
			return nil, err
		}
	}

	return &state, nil
}

// IsEmpty return true is the state is empty. This usually only happen when the state file does not exists.
func (s *State) IsEmpty() bool {
	s.l.Lock()
	defer s.l.Unlock()

	return len(s.cache) == 0
}

// KeepOnlyPersistent will delete everything from state but persistent information.
func (s *State) KeepOnlyPersistent() {
	s.l.Lock()
	defer s.l.Unlock()

	s.cache = make(map[string]json.RawMessage)

	if err := s.saveIfPossible(); err != nil {
		logger.Printf("Unable to save state.json: %v", err)
	}
}

// SaveTo will write back the State to specified filename and following Save() will use the same file.
//
// Note that Save() will use the new filename even if this function fail.
func (s *State) SaveTo(persistentPath string, cachePath string) error {
	s.l.Lock()
	defer s.l.Unlock()

	s.persistentPath = persistentPath
	s.cachePath = cachePath
	s.isInMemory = false

	return s.save()
}

// Save will write back the State to disk.
func (s *State) Save() error {
	s.l.Lock()
	defer s.l.Unlock()

	return s.save()
}

func (s *State) saveIfPossible() error {
	if s.isInMemory {
		return nil
	}

	file, err := os.Open(s.cachePath)
	if errors.Is(err, os.ErrNotExist) {
		// This case happens when the state.cache.json is deleted at runtime.
		// While this is not a regular use case it is checked in order to prevent
		// recreating the state on shutdown.
		return nil
	}

	if err != nil {
		return err
	}

	file.Close()

	return s.save()
}

func (s *State) save() error {
	if s.isInMemory {
		return nil
	}

	if s.persistent.dirty {
		w, err := os.OpenFile(s.persistentPath+tmpExt, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}

		err = s.savePersistentTo(w)
		if err != nil {
			w.Close()

			return err
		}

		_ = w.Sync()
		w.Close()

		err = os.Rename(s.persistentPath+tmpExt, s.persistentPath)
		if err != nil {
			return err
		}

		s.persistent.dirty = false
	}

	w, err := os.OpenFile(s.cachePath+tmpExt, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}

	err = s.saveCacheTo(w)
	if err != nil {
		w.Close()

		return err
	}

	_ = w.Sync()
	w.Close()

	err = os.Rename(s.cachePath+tmpExt, s.cachePath)

	return err
}

func (s *State) saveCacheTo(w io.Writer) error {
	encoder := json.NewEncoder(w)

	return encoder.Encode(s.cache)
}

func (s *State) savePersistentTo(w io.Writer) error {
	encoder := json.NewEncoder(w)

	err := encoder.Encode(s.persistent)
	if err != nil {
		return err
	}

	return nil
}

// Set save an object.
func (s *State) Set(key string, object interface{}) error {
	s.l.Lock()
	defer s.l.Unlock()

	buffer, err := json.Marshal(object)
	if err != nil {
		return err
	}

	s.cache[key] = json.RawMessage(buffer)

	err = s.saveIfPossible()
	if err != nil {
		logger.Printf("Unable to save state.json: %v", err)
	}

	return nil
}

// Delete an key from state.
func (s *State) Delete(key string) error {
	s.l.Lock()
	defer s.l.Unlock()

	if _, ok := s.cache[key]; !ok {
		return nil
	}

	delete(s.cache, key)

	if err := s.saveIfPossible(); err != nil {
		logger.Printf("Unable to save state.json: %v", err)
	}

	return nil
}

// Get return an object.
func (s *State) Get(key string, result interface{}) error {
	s.l.Lock()
	defer s.l.Unlock()

	buffer, ok := s.cache[key]
	if !ok {
		return nil
	}

	err := json.Unmarshal(buffer, &result)

	return err
}

// BleemeoCredentials return the Bleemeo agent_uuid and password.
// They may be empty if unset.
func (s *State) BleemeoCredentials() (string, string) {
	s.l.Lock()
	defer s.l.Unlock()

	return s.persistent.BleemeoAgentID, s.persistent.BleemeoPassword
}

// TelemetryID return a stable ID for the telemetry.
func (s *State) TelemetryID() string {
	s.l.Lock()
	defer s.l.Unlock()

	if s.persistent.TelemetryID == "" {
		s.persistent.TelemetryID = uuid.New().String()
		s.persistent.dirty = true

		_ = s.saveIfPossible()
	}

	return s.persistent.TelemetryID
}

// SetBleemeoCredentials sets the Bleemeo agent_uuid and password.
func (s *State) SetBleemeoCredentials(agentUUID string, password string) error {
	s.l.Lock()
	defer s.l.Unlock()

	s.persistent.BleemeoAgentID = agentUUID
	s.persistent.BleemeoPassword = password
	s.persistent.dirty = true

	return s.saveIfPossible()
}

func (s *State) loadFromV0() error {
	f, err := os.Open(s.persistentPath)
	if err != nil {
		return err
	}

	defer f.Close()

	decoder := json.NewDecoder(f)
	err = decoder.Decode(&s.cache)

	if err != nil {
		return err
	}

	if err := s.Get("agent_uuid", &s.persistent.BleemeoAgentID); err != nil {
		return err
	}

	if err := s.Get("password", &s.persistent.BleemeoPassword); err != nil {
		return err
	}

	var tmp struct{ ID string }

	if err := s.Get("Telemetry", &tmp); err != nil {
		logger.V(2).Printf("failed to load telemetry ID from V0 state: %v", err)
	} else {
		s.persistent.TelemetryID = tmp.ID
	}

	delete(s.cache, "agent_uuid")
	delete(s.cache, "password")
	delete(s.cache, "Telemetry")

	s.persistent.Version = 1
	s.persistent.dirty = true

	return nil
}

// GetByPrefix returns all the objects starting by the given key prefix,
// and which can be represented as the given resultType.
// The resultType must be of the type the objects are expected to be.
//
// Note that it only searches at the root level of the cache,
// and resultType must be a map, a struct or a slice.
func (s *State) GetByPrefix(keyPrefix string, resultType any) (map[string]any, error) {
	s.l.Lock()
	defer s.l.Unlock()

	resultTyp := reflect.TypeOf(resultType)
	result := make(map[string]any)

	for key, value := range s.cache {
		if strings.HasPrefix(key, keyPrefix) {
			// We could have used the resultType to receive the value,
			// but as it is passed as an interface{}, the json unmarshaler
			// would have redefined it as a map[string]interface{}.
			// Thus, we expect any-thing and then
			// decode it into a 'resultTyp' variable.
			var output any

			err := json.Unmarshal(value, &output)
			if err != nil {
				return nil, err // Really unexpected
			}

			// We allocate a new variable of the expected type, to prevent
			// modifying previous values of types that are passed by reference (e.g.: slices)
			resultTypeAlloc := reflect.New(resultTyp).Elem().Interface()

			err = mapstructure.Decode(output, &resultTypeAlloc)
			if err != nil {
				continue
			}

			result[key] = resultTypeAlloc
		}
	}

	return result, nil
}
