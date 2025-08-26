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
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/bleemeo/glouton/logger"

	"github.com/go-viper/mapstructure/v2"
	"github.com/google/uuid"
)

const stateVersion = 1

const (
	KeyKubernetesCluster = "kubernetes_cluster_name"
	KeyAgentBroken       = "AgentBroken"
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

	// When multiple locks are acquired,
	// it must be in the order of declaration.
	l                      sync.RWMutex
	backgroundLock         sync.Mutex
	backgroundWriterWG     sync.WaitGroup
	backgroundWriteTrigger chan bool
	persistentPath         string
	cachePath              string
	isInMemory             bool
	closed                 bool
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
// File could be omitted by using empty string but you should probably omit both or none or state versioning might
// cause trouble.
// This function is mostly present for test that need a state mock.
// SaveTo will use a file and remove the fact that state is only in-memory.
func LoadReadOnly(persistentPath string, cachePath string) (*State, error) {
	return load(true, persistentPath, cachePath)
}

// load loads state.json file.
func load(readOnly bool, persistentPath string, cachePath string) (*State, error) {
	state := State{
		persistentPath:         persistentPath,
		cachePath:              cachePath,
		cache:                  make(map[string]json.RawMessage),
		backgroundWriteTrigger: make(chan bool, 1),
		isInMemory:             readOnly,
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

			return state.withBackgroundWriter(), nil
		} else if err != nil {
			return nil, err
		}

		decoder := json.NewDecoder(f)
		err = decoder.Decode(&state.persistent)

		_ = f.Close()

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

			_ = f.Close()
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

	return state.withBackgroundWriter(), nil
}

func (s *State) withBackgroundWriter() *State {
	s.backgroundWriterWG.Add(1)

	go func() {
		defer s.backgroundWriterWG.Done()

		s.backgroundWriter()
	}()

	return s
}

// IsEmpty return true is the state is empty. This usually only happen when the state file does not exists.
func (s *State) IsEmpty() bool {
	s.l.RLock()
	defer s.l.RUnlock()

	return len(s.cache) == 0
}

// FileSizes return the size of state files persistent & cache.
func (s *State) FileSizes() (int, int) {
	s.l.RLock()
	defer s.l.RUnlock()

	sizePersistent := 0
	sizeCache := 0

	st, err := os.Stat(s.persistentPath)
	if err == nil {
		sizePersistent = int(st.Size())
	}

	st, err = os.Stat(s.cachePath)
	if err == nil {
		sizeCache = int(st.Size())
	}

	return sizePersistent, sizeCache
}

// Close wait for any background write.
func (s *State) Close() {
	s.l.RLock()
	s.backgroundLock.Lock()

	close(s.backgroundWriteTrigger)
	// We need to explicitly mark the channel as closed,
	// otherwise we wouldn't know if we can still write to it.
	s.closed = true

	s.backgroundLock.Unlock()
	s.l.RUnlock()

	s.backgroundWriterWG.Wait()
}

// KeepOnlyPersistent will delete everything from state but persistent information.
func (s *State) KeepOnlyPersistent() {
	s.l.Lock()
	defer s.l.Unlock()

	s.cache = make(map[string]json.RawMessage)

	s.saveCacheIfFileNotDeleted()
}

// SaveTo will write back the State to specified filename and following auto-save will use the same file.
func (s *State) SaveTo(persistentPath string, cachePath string) error {
	s.l.Lock()
	defer s.l.Unlock()

	s.persistentPath = persistentPath
	s.cachePath = cachePath
	s.isInMemory = false

	if err := s.savePersistent(); err != nil {
		return err
	}

	s.triggerCacheWrite(true, false)

	return nil
}

func (s *State) saveCacheIfFileNotDeleted() {
	if s.isInMemory {
		return
	}

	s.triggerCacheWrite(false, true)
}

func (s *State) savePersistent() error {
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
			_ = w.Close()

			return err
		}

		_ = w.Sync()
		_ = w.Close()

		err = os.Rename(s.persistentPath+tmpExt, s.persistentPath)
		if err != nil {
			return err
		}

		s.persistent.dirty = false
	}

	return nil
}

// Caller of triggerCacheWrite must hold the writer lock (s.l.Lock).
// If triggerCacheWrite is called while the state is closed, the message will be dropped.
//
// If `blocking` is set to true, triggerCacheWrite will wait for the channel
// to accept the message before returning. When set to false, the message may be dropped.
//
// If `onlyIfFileExists` is set to true, the cache will only be written if the file already exists.
// When set to false, the file will be written without any pre-existence check.
func (s *State) triggerCacheWrite(blocking, onlyIfFileExists bool) {
	s.backgroundLock.Lock()
	defer s.backgroundLock.Unlock()

	if s.closed {
		return
	}

	if blocking {
		s.backgroundWriteTrigger <- onlyIfFileExists
	} else {
		select {
		case s.backgroundWriteTrigger <- onlyIfFileExists:
		default:
		}
	}
}

func (s *State) backgroundWriter() {
	for onlyIfFileExists := range s.backgroundWriteTrigger {
		if err := s.writeCache(onlyIfFileExists); err != nil {
			logger.V(1).Printf("writing cache.json failed: %v", err)
		}
	}
}

func (s *State) serializeCache() ([]byte, error) {
	buffer := bytes.NewBuffer(nil)
	if err := s.saveCacheTo(buffer); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func (s *State) writeCache(onlyIfFileExists bool) error {
	if onlyIfFileExists {
		_, err := os.Stat(s.cachePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// This case happens when the state.cache.json is deleted at runtime.
				// While this is not a regular use case, it is checked in order to prevent
				// recreating the state on shutdown.
				return nil
			}

			return err
		}
	}

	s.l.RLock()

	if s.isInMemory {
		s.l.RUnlock()

		return nil
	}

	data, err := s.serializeCache()
	if err != nil {
		s.l.RUnlock()

		return err
	}

	cachePath := s.cachePath
	tmpCachePath := cachePath + tmpExt

	s.l.RUnlock()

	w, err := os.OpenFile(tmpCachePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}

	_, err = w.Write(data)
	if err != nil {
		_ = w.Close()

		return err
	}

	_ = w.Close()

	err = os.Rename(tmpCachePath, cachePath)

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
func (s *State) Set(key string, object any) error {
	s.l.Lock()
	defer s.l.Unlock()

	modified, err := s.set(key, object)
	if err != nil {
		return err
	}

	if !modified {
		return nil
	}

	s.saveCacheIfFileNotDeleted()

	return nil
}

func (s *State) set(key string, object any) (bool, error) {
	buffer, err := json.Marshal(object)
	if err != nil {
		return false, err
	}

	oldValue := s.cache[key]
	s.cache[key] = json.RawMessage(buffer)

	modified := !bytes.Equal(oldValue, s.cache[key])

	return modified, nil
}

// Delete an key from state.
func (s *State) Delete(key string) error {
	s.l.Lock()
	defer s.l.Unlock()

	if _, ok := s.cache[key]; !ok {
		return nil
	}

	delete(s.cache, key)

	s.saveCacheIfFileNotDeleted()

	return nil
}

// Get return an object.
func (s *State) Get(key string, result any) error {
	s.l.RLock()
	defer s.l.RUnlock()

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
	s.l.RLock()
	defer s.l.RUnlock()

	return s.persistent.BleemeoAgentID, s.persistent.BleemeoPassword
}

// TelemetryID return a stable ID for the telemetry.
func (s *State) TelemetryID() string {
	s.l.RLock()
	telemetryID := s.persistent.TelemetryID
	s.l.RUnlock()

	if telemetryID != "" {
		return telemetryID
	}

	// telemetryID is not yet set, generate one
	telemetryID = s.generateTelemetryID()

	return telemetryID
}

func (s *State) generateTelemetryID() string {
	s.l.Lock()
	defer s.l.Unlock()

	saveNeeded := false

	// We must re-check that TelemetryID is still unset to avoid any concurrent read-then-set
	if s.persistent.TelemetryID == "" {
		s.persistent.TelemetryID = uuid.New().String()
		s.persistent.dirty = true
		saveNeeded = true
	}

	telemetryID := s.persistent.TelemetryID

	if saveNeeded {
		_ = s.savePersistent()
	}

	return telemetryID
}

// SetBleemeoCredentials sets the Bleemeo agent_uuid and password.
func (s *State) SetBleemeoCredentials(agentUUID string, password string) error {
	s.l.Lock()
	defer s.l.Unlock()

	s.persistent.BleemeoAgentID = agentUUID
	s.persistent.BleemeoPassword = password
	s.persistent.dirty = true

	return s.savePersistent()
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
	s.l.RLock()
	defer s.l.RUnlock()

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
