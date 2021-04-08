// Copyright 2015-2019 Bleemeo
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
	"glouton/logger"
	"os"
	"sync"
)

// State is state.json.
type State struct {
	data map[string]json.RawMessage

	l    sync.Mutex
	path string
}

// Load load state.json file.
func Load(path string) (*State, error) {
	state := State{
		path: path,
		data: make(map[string]json.RawMessage),
	}

	f, err := os.Open(path)
	if err != nil && os.IsNotExist(err) {
		return &state, nil
	} else if err != nil {
		return nil, err
	}

	defer f.Close()

	decoder := json.NewDecoder(f)
	err = decoder.Decode(&state.data)

	return &state, err
}

// IsEmpty return true is the state is empty. This usually only happen when the state file does not exists.
func (s *State) IsEmpty() bool {
	s.l.Lock()
	defer s.l.Unlock()

	return len(s.data) == 0
}

// SaveTo will write back the State to specified filename and following Save() will use the same file.
//
// Note that Save() will use the new filename even if this function fail.
func (s *State) SaveTo(filename string) error {
	s.l.Lock()
	defer s.l.Unlock()

	s.path = filename

	return s.save()
}

// Save will write back the State to disk.
func (s *State) Save() error {
	s.l.Lock()
	defer s.l.Unlock()

	return s.save()
}

func (s *State) saveIfPossible() error {
	_, err := os.Open(s.path)
	if err == os.ErrNotExist {
		// This case happens when the state.json is deleted at runtime.
		// While this is not a regular use case it is checked in order to prevent
		// recreating the state on shutdown.
		return nil
	}

	if err != nil {
		return err
	}

	return s.save()
}

func (s *State) save() error {
	err := s.saveTo(s.path + ".tmp")
	if err != nil {
		return err
	}

	err = os.Rename(s.path+".tmp", s.path)

	return err
}

func (s *State) saveTo(path string) error {

	w, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}

	defer w.Close()

	encoder := json.NewEncoder(w)

	err = encoder.Encode(s.data)
	if err != nil {
		return err
	}

	_ = w.Sync()

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

	s.data[key] = json.RawMessage(buffer)

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

	if _, ok := s.data[key]; !ok {
		return nil
	}

	delete(s.data, key)

	err := s.saveIfPossible()
	if err != nil {
		logger.Printf("Unable to save state.json: %v", err)
	}

	return nil
}

// Get return an object.
func (s *State) Get(key string, result interface{}) error {
	s.l.Lock()
	defer s.l.Unlock()

	buffer, ok := s.data[key]
	if !ok {
		return nil
	}

	err := json.Unmarshal(buffer, &result)

	return err
}
