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
	"context"
	"sync"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension/xextension/storage"
)

type persistHost struct {
	state      bleemeoTypes.State
	extensions map[component.ID]component.Component
}

func (h *persistHost) newPersistentExt(ctx context.Context, name string) (component.ID, error) {
	id := component.NewIDWithName(component.MustNewType("glouton_log_metadata_storage"), name)
	h.extensions[id] = persistExtension{
		client: &storageClient{
			prefix:      name,
			state:       h.state,
			updatedKeys: make(map[string]struct{}),
		},
	}

	return id, h.extensions[id].Start(ctx, h)
}

func (h *persistHost) GetExtensions() map[component.ID]component.Component {
	return h.extensions
}

type persistExtension struct {
	client *storageClient
}

func (e persistExtension) Start(context.Context, component.Host) error {
	return e.client.load()
}

func (e persistExtension) Shutdown(ctx context.Context) error {
	return e.client.Close(ctx)
}

func (e persistExtension) GetClient(_ context.Context, kind component.Kind, _ component.ID, _ string) (storage.Client, error) {
	if kind == component.KindReceiver {
		return e.client, nil
	}

	return nil, nil //nolint:nilnil
}

type storageClient struct {
	prefix string
	state  bleemeoTypes.State

	l           sync.Mutex
	dirty       map[string][]byte
	updatedKeys map[string]struct{}
	sets        int
}

func (s *storageClient) load() error {
	s.l.Lock()
	defer s.l.Unlock()

	m, err := getFileMetadataFromCache(s.state)
	if err != nil {
		return err
	}

	s.dirty = m

	return nil
}

func (s *storageClient) Get(_ context.Context, key string) ([]byte, error) {
	key = s.prefix + "." + key

	logger.V(1).Printf("=> storageClient.Get(ctx, %q)", key) // TODO: remove

	s.l.Lock()
	defer s.l.Unlock()

	return s.dirty[key], nil
}

func (s *storageClient) Set(_ context.Context, key string, value []byte) error {
	key = s.prefix + "." + key

	// logger.V(1).Printf("=> storageClient.Set(ctx, %q, %s)", key, string(value)) // TODO: remove

	s.l.Lock()
	defer s.l.Unlock()

	s.dirty[key] = value
	s.updatedKeys[key] = struct{}{}
	s.sets++ // TODO: remove

	return nil
}

func (s *storageClient) Delete(_ context.Context, key string) error {
	key = s.prefix + "." + key

	logger.V(1).Printf("=> storageClient.Delete(ctx, %q)", key) // TODO: remove

	s.l.Lock()
	defer s.l.Unlock()

	delete(s.dirty, key)

	return nil
}

func (s *storageClient) Batch(ctx context.Context, ops ...*storage.Operation) error {
	logger.V(1).Printf("=> storageClient.Batch(ctx, %v)", ops) // TODO: remove

	for _, op := range ops {
		switch op.Type {
		case storage.Get:
			value, err := s.Get(ctx, op.Key)
			if err != nil {
				return err
			}

			op.Value = value
		case storage.Set:
			return s.Set(ctx, op.Key, op.Value)
		case storage.Delete:
			return s.Delete(ctx, op.Key)
		}
	}

	return nil
}

func (s *storageClient) Close(_ context.Context) error {
	s.l.Lock()
	defer s.l.Unlock()

	logger.V(1).Printf("=> storageClient.Close(ctx) -> did %d calls to Set()", s.sets) // TODO: remove

	updatedData := make(map[string][]byte, len(s.updatedKeys))
	// Only keeping the values that have been updated during this run,
	// so as to discard the ones that correspond to files that no longer exist.
	for key := range s.updatedKeys {
		updatedData[key] = s.dirty[key]
	}

	return saveFileMetadataToCache(s.state, updatedData)
}
