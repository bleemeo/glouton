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
