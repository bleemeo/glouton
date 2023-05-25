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
	"sync"

	"github.com/google/uuid"
)

const (
	stateVersion       = 1
	discardFileForTest = "/this/path/does/not/exists"
)

const (
	KeyKubernetesCluster = "kubernetes_cluster_name"
)

var errVersionIncompatible = errors.New("state.json is incompatible with this glouton")

type persistedState struct {
	Version         int    `json:"version"`
	BleemeoAgentID  string `json:"agent_uuid"`
	BleemeoPassword string `json:"password"`
	TelemetryID     string `json:"telemery_id"`
	dirty           bool   `json:"-"`
}

// State is both state.json and state.cache.json.
type State struct {
	persistent persistedState
	cache      map[string]json.RawMessage

	l              sync.Mutex
	persistentPath string
	cachePath      string
}

func DefaultCachePath(persistentPath string) string {
	ext := filepath.Ext(persistentPath)

	return fmt.Sprintf("%s.cache%s", persistentPath[0:len(persistentPath)-len(ext)], ext)
}

// Load load state.json file.
func Load(persistentPath string, cachePath string) (*State, error) {
	state := State{
		persistentPath: persistentPath,
		cachePath:      cachePath,
		cache:          make(map[string]json.RawMessage),
	}

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

	f, err = os.Open(cachePath)
	if err == nil {
		decoder = json.NewDecoder(f)
		err = decoder.Decode(&state.cache)

		if err != nil {
			logger.V(1).Printf("unable to load state cache: %v", err)
		}

		f.Close()
	} else {
		logger.V(1).Printf("unable to load state cache: %v", err)
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

	return s.save()
}

// Save will write back the State to disk.
func (s *State) Save() error {
	s.l.Lock()
	defer s.l.Unlock()

	return s.save()
}

func (s *State) saveIfPossible() error {
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
	if s.persistentPath == discardFileForTest {
		return nil
	}

	if s.persistent.dirty {
		w, err := os.OpenFile(s.persistentPath+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
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

		err = os.Rename(s.persistentPath+".tmp", s.persistentPath)
		if err != nil {
			return err
		}

		s.persistent.dirty = false
	}

	w, err := os.OpenFile(s.cachePath+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
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

	err = os.Rename(s.cachePath+".tmp", s.cachePath)

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
		logger.V(2).Printf("failed to load telemery ID from V0 state: %v", err)
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
