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
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension/xextension/storage"
)

const storageType = "glouton_log_metadata_storage"

var errStorageClientNotFound = errors.New("storage client not found")

type persistHost struct {
	l                   sync.Mutex
	extensions          map[component.ID]component.Component
	metadataPerReceiver map[string]map[string][]byte
	updatedKeys         map[string]struct{}
}

func newPersistHost(state bleemeoTypes.State) (*persistHost, error) {
	metadata, err := getFileMetadataFromCache(state)
	if err != nil {
		return nil, err
	}

	return &persistHost{
		extensions:          make(map[component.ID]component.Component),
		metadataPerReceiver: metadata,
		updatedKeys:         make(map[string]struct{}),
	}, nil
}

func (h *persistHost) newPersistentExt(name string) component.ID {
	h.l.Lock()
	defer h.l.Unlock()

	// We don't have to care about handling any error,
	// since the type is known to be correct (otherwise TestPersistHost would have failed),
	// and the name doesn't have any format restriction.
	id := component.MustNewIDWithName(storageType, name)

	receiverMetadata, found := h.metadataPerReceiver[name]
	if !found {
		receiverMetadata = make(map[string][]byte)
	}

	if _, ok := h.extensions[id]; ok {
		logger.V(2).Printf("duplicate extensions with ID %v (name=%s)", id, name)
	}

	h.extensions[id] = persistExtension{
		client: &storageClient{
			name:        name,
			host:        h,
			dirty:       receiverMetadata,
			updatedKeys: make(map[string]struct{}),
			lastSave:    time.Now(),
		},
	}

	return id
}

func (h *persistHost) storeMetadata(recvName string, metadata map[string][]byte) {
	h.l.Lock()
	defer h.l.Unlock()

	h.metadataPerReceiver[recvName] = metadata
	h.updatedKeys[recvName] = struct{}{}
}

func (h *persistHost) getAllMetadata() map[string]map[string][]byte {
	h.l.Lock()
	defer h.l.Unlock()

	updatedData := make(map[string]map[string][]byte, len(h.updatedKeys))

	for key := range h.updatedKeys {
		// We assume the []byte in value of `h.metadataPerReceiver[key]` isn't mutated.
		// If it was mutated, we also need to copy it.
		// Also be careful to only reads key that are in updatedKeys, `persistHost` don't
		// own `h.metadataPerReceiver[key]` for key that started an `persistExtension` and
		// which didn't called `storeMetadata`.
		updatedData[key] = maps.Clone(h.metadataPerReceiver[key])
	}

	return updatedData
}

func (h *persistHost) GetExtensions() map[component.ID]component.Component {
	h.l.Lock()
	defer h.l.Unlock()

	return h.extensions
}

func (h *persistHost) writeToArchive(writer types.ArchiveWriter) error {
	h.l.Lock()
	extensionIDs := slices.Collect(maps.Keys(h.extensions))
	h.l.Unlock()

	file, err := writer.Create("log-processing/persister.json")
	if err != nil {
		return err
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	if err := enc.Encode(extensionIDs); err != nil {
		return err
	}

	return nil
}

type persistExtension struct {
	client *storageClient
}

func (e persistExtension) Start(context.Context, component.Host) error {
	return nil
}

func (e persistExtension) Shutdown(ctx context.Context) error {
	return e.client.Close(ctx)
}

func (e persistExtension) GetClient(_ context.Context, kind component.Kind, _ component.ID, _ string) (storage.Client, error) {
	if kind == component.KindReceiver {
		return e.client, nil
	}

	return nil, fmt.Errorf("%w for kind %q", errStorageClientNotFound, kind)
}

type storageClient struct {
	name string
	host *persistHost

	l           sync.Mutex
	dirty       map[string][]byte
	updatedKeys map[string]struct{}
	lastSave    time.Time
}

func (s *storageClient) Get(_ context.Context, key string) ([]byte, error) {
	s.l.Lock()
	defer s.l.Unlock()

	return s.dirty[key], nil
}

func (s *storageClient) Set(_ context.Context, key string, value []byte) error {
	s.l.Lock()
	defer s.l.Unlock()

	s.set(key, value)

	return nil
}

func (s *storageClient) set(key string, value []byte) {
	s.dirty[key] = value
	s.updatedKeys[key] = struct{}{}

	if time.Since(s.lastSave) >= saveFileSizesToCachePeriod {
		s.saveMetadata()
	}
}

func (s *storageClient) Delete(_ context.Context, key string) error {
	s.l.Lock()
	defer s.l.Unlock()

	s.delete(key)

	return nil
}

func (s *storageClient) delete(key string) {
	delete(s.dirty, key)
	delete(s.updatedKeys, key)
}

func (s *storageClient) Batch(_ context.Context, ops ...*storage.Operation) error {
	s.l.Lock()
	defer s.l.Unlock()

	for _, op := range ops {
		switch op.Type {
		case storage.Get:
			op.Value = s.dirty[op.Key]
		case storage.Set:
			s.set(op.Key, op.Value)
		case storage.Delete:
			s.delete(op.Key)
		}
	}

	return nil
}

// Close is called by the storageClient's related filelogreceiver when it shuts down.
func (s *storageClient) Close(_ context.Context) error {
	s.l.Lock()
	defer s.l.Unlock()

	s.saveMetadata()

	return nil
}

func (s *storageClient) saveMetadata() {
	s.lastSave = time.Now()

	updatedData := make(map[string][]byte, len(s.updatedKeys))
	// Only saving the values that have been updated during this run,
	// so as to discard the ones that correspond to files that no longer exist.
	for key := range s.updatedKeys {
		updatedData[key] = s.dirty[key]
	}

	s.host.storeMetadata(s.name, updatedData)
}
