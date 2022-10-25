// Copyright 2015-2022 Bleemeo
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

package config

import (
	"errors"

	"github.com/fatih/structs"
	"gopkg.in/yaml.v3"
)

var errNotSupported = errors.New("structs provider does not support this method")

// provider implements a structs provider.
type provider struct {
	s   interface{}
	tag string
}

// Provider returns a provider that takes a  takes a struct and a struct tag
// and uses structs to parse and provide it to koanf.
func structsProvider(s interface{}, tag string) *provider {
	return &provider{s: s, tag: tag}
}

// Read reads the struct and returns a nested config map.
func (s *provider) Read() (map[string]interface{}, error) {
	ns := structs.New(s.s)
	ns.TagName = s.tag

	// Map() returns a map[string]interface{}, but the underlying types are still known,
	// for example m["network_interface_blacklist"] has type []string.
	// The config loaded from files doesn't know these types, and to merge maps coming
	// from the files and the struct, the types need to be equal, so we convert all slices
	// to []interface{} by marshaling and unmarshaling the map.
	m := ns.Map()

	marshaled, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}

	var out map[string]interface{}
	if err := yaml.Unmarshal(marshaled, &out); err != nil {
		return nil, err
	}

	return out, err
}

// ReadBytes is not supported by the structs provider.
func (s *provider) ReadBytes() ([]byte, error) {
	return nil, errNotSupported
}

// Watch is not supported by the structs provider.
func (s *provider) Watch(cb func(event interface{}, err error)) error {
	return errNotSupported
}
