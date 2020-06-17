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
	"reflect"
)

// Mock is useful for tests.
type Mock struct {
	data map[string]interface{}
}

// NewMock create a new state mock.
func NewMock() *Mock {
	return &Mock{
		data: map[string]interface{}{},
	}
}

// Set updates an object.
// No json or anything there, just stupid objects.
func (s *Mock) Set(key string, object interface{}) error {
	s.data[key] = object

	return nil
}

// Delete an key from state.
func (s *Mock) Delete(key string) error {
	if _, ok := s.data[key]; !ok {
		return nil
	}

	delete(s.data, key)

	return nil
}

// Get returns an object.
func (s *Mock) Get(key string, result interface{}) error {
	val, ok := s.data[key]
	if ok {
		reflect.ValueOf(result).Elem().Set(reflect.ValueOf(val))
	}

	return nil
}
