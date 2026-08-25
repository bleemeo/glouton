// Copyright 2015-2026 Bleemeo
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

package logsource

import (
	"bytes"
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

var errStorageClientNotFound = errors.New("storage client not found")

// PersistConfig gives a PersistHost its own identity, so independent features never collide in the shared state cache or OTel
// component registry.
type PersistConfig struct {
	// StorageType is this host's component.Type, must be unique across features.
	StorageType string
	// CacheKey is the bleemeoTypes.State key this host's data is stored under.
	CacheKey string
	// ArchivePath is the file created by WriteToArchive in a diagnostic bundle.
	ArchivePath string
	// FullSnapshot, if true, persists every receiver's metadata on every SaveToState call, even untouched ones; if false, only
	// touched receivers are persisted.
	FullSnapshot bool
	// SaveThrottle, if non-zero, limits how often a Set() call pushes its receiver's dirty data into the host's in-memory map.
	SaveThrottle time.Duration
}

// PersistHost is a minimal component.Host backing every source's filelogreceiver storage with a client persisted into Glouton's
// state cache, so read offsets survive a restart.
type PersistHost struct {
	cfg PersistConfig

	l                   sync.Mutex
	extensions          map[component.ID]component.Component
	metadataPerReceiver map[string]map[string][]byte
	updatedKeys         map[string]struct{}
}

func NewPersistHost(state bleemeoTypes.State, cfg PersistConfig) (*PersistHost, error) {
	metadata, err := getFileMetadataFromCache(state, cfg.CacheKey)
	if err != nil {
		return nil, err
	}

	return &PersistHost{
		cfg:                 cfg,
		extensions:          make(map[component.ID]component.Component),
		metadataPerReceiver: metadata,
		updatedKeys:         make(map[string]struct{}),
	}, nil
}

func getFileMetadataFromCache(state bleemeoTypes.State, cacheKey string) (map[string]map[string][]byte, error) {
	var metadataMap map[string]map[string][]byte

	err := state.Get(cacheKey, &metadataMap)
	if err != nil {
		return nil, err
	}

	if metadataMap == nil { // it may not exist in the state cache yet
		metadataMap = make(map[string]map[string][]byte)
	}

	return metadataMap, nil
}

func saveFileMetadataToCache(state bleemeoTypes.State, cacheKey string, metadata map[string]map[string][]byte) {
	if err := state.Set(cacheKey, metadata); err != nil {
		logger.V(1).Printf("Failed to save log file metadata to cache (%s): %v", cacheKey, err)
	}
}

// NewPersistentExt registers (or re-attaches to) the persisted storage for name, a stable per-source identity, and returns its
// component.ID.
func (h *PersistHost) NewPersistentExt(name string) component.ID {
	h.l.Lock()
	defer h.l.Unlock()

	// Error is impossible here: the type is known-correct and name is unrestricted.
	id := component.MustNewIDWithName(h.cfg.StorageType, name)

	receiverMetadata, found := h.metadataPerReceiver[name]
	if !found {
		receiverMetadata = make(map[string][]byte)
	}

	if _, ok := h.extensions[id]; ok {
		logger.V(2).Printf("duplicate extensions with ID %v (name=%s)", id, name)
	}

	// Replacing the old client here (if any) doesn't lose its unflushed Set() calls: storageClient.set()
	// already writes through to h.metadataPerReceiver/h.updatedKeys synchronously on every call,
	// independent of this client's own SaveThrottle-gated saveMetadata(). SaveToState/getAllMetadata (the
	// actual restart-recovery path) reads those host-level maps directly, not any individual client's
	// private dirty/updatedKeys -- so they already reflect every Set() this duplicate name ever received,
	// regardless of which storageClient instance is current by the time a save happens.
	h.extensions[id] = persistExtension{
		client: &storageClient{
			name:        name,
			host:        h,
			dirty:       maps.Clone(receiverMetadata),
			updatedKeys: make(map[string]struct{}),
			lastSave:    time.Now(),
		},
	}

	return id
}

// RemovePersistentExt un-registers a single extension, previously returned by NewPersistentExt. Its
// metadata (last-known offset) is kept, since this is also called on a graceful, resumable shutdown
// (e.g. process restart): see RemovePersistentExtsAndForget for permanent removal.
func (h *PersistHost) RemovePersistentExt(id component.ID) {
	h.l.Lock()
	defer h.l.Unlock()

	delete(h.extensions, id)
}

// RemovePersistentExts un-registers several extensions at once, keeping their metadata (see RemovePersistentExt).
func (h *PersistHost) RemovePersistentExts(ids []component.ID) {
	h.l.Lock()
	defer h.l.Unlock()

	for _, id := range ids {
		delete(h.extensions, id)
	}
}

// RemovePersistentExtsAndForget un-registers several extensions and forgets their metadata, for a source
// that's permanently gone (e.g. a removed container), as opposed to a graceful/resumable shutdown.
// Without this, a removed source's last-known metadata (re-saved by its storageClient.Close call just
// before removal) would stay in metadataPerReceiver/updatedKeys and keep being rewritten into the state
// cache by every later SaveToState/getAllMetadata call, for as long as the process runs.
func (h *PersistHost) RemovePersistentExtsAndForget(ids []component.ID) {
	h.l.Lock()
	defer h.l.Unlock()

	for _, id := range ids {
		delete(h.extensions, id)
		delete(h.metadataPerReceiver, id.Name())
		delete(h.updatedKeys, id.Name())
	}
}

func (h *PersistHost) storeMetadata(recvName string, metadata map[string][]byte) {
	h.l.Lock()
	defer h.l.Unlock()

	h.metadataPerReceiver[recvName] = metadata
	h.updatedKeys[recvName] = struct{}{}
}

// SaveToState persists this host's current metadata into state, under CacheKey.
func (h *PersistHost) SaveToState(state bleemeoTypes.State) {
	saveFileMetadataToCache(state, h.cfg.CacheKey, h.getAllMetadata())
}

func (h *PersistHost) getAllMetadata() map[string]map[string][]byte {
	h.l.Lock()
	defer h.l.Unlock()

	if h.cfg.FullSnapshot {
		out := make(map[string]map[string][]byte, len(h.metadataPerReceiver))

		for key, val := range h.metadataPerReceiver {
			out[key] = maps.Clone(val)
		}

		return out
	}

	updatedData := make(map[string]map[string][]byte, len(h.updatedKeys))

	for key := range h.updatedKeys {
		// Assumes metadataPerReceiver[key] values aren't mutated elsewhere.
		updatedData[key] = maps.Clone(h.metadataPerReceiver[key])
	}

	return updatedData
}

// GetExtensions implements component.Host. Returns a clone, not h.extensions itself: callers (e.g. an
// OTel receiver adapter's Start()) index the result after this call returns and the lock is released,
// racing NewPersistentExt/RemovePersistentExt(s)/RemovePersistentExtsAndForget -- this PersistHost is
// shared between ReceiverManager and logprocessing.Manager, which write it from independent goroutines.
func (h *PersistHost) GetExtensions() map[component.ID]component.Component {
	h.l.Lock()
	defer h.l.Unlock()

	return maps.Clone(h.extensions)
}

// WriteToArchive writes the list of currently-registered extension IDs to ArchivePath in a diagnostic bundle.
func (h *PersistHost) WriteToArchive(writer types.ArchiveWriter) error {
	h.l.Lock()
	extensionIDs := slices.Collect(maps.Keys(h.extensions))
	h.l.Unlock()

	file, err := writer.Create(h.cfg.ArchivePath)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(extensionIDs)
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
	host *PersistHost

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
	cloned := bytes.Clone(value)
	s.dirty[key] = cloned
	s.updatedKeys[key] = struct{}{}

	s.host.l.Lock()

	m := s.host.metadataPerReceiver[s.name]
	if m == nil {
		m = make(map[string][]byte)
		s.host.metadataPerReceiver[s.name] = m
	}

	m[key] = cloned
	s.host.updatedKeys[s.name] = struct{}{}
	s.host.l.Unlock()

	if s.host.cfg.SaveThrottle == 0 || time.Since(s.lastSave) >= s.host.cfg.SaveThrottle {
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

	s.host.l.Lock()
	delete(s.host.metadataPerReceiver[s.name], key)
	s.host.l.Unlock()
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

	if s.host.cfg.FullSnapshot {
		s.host.storeMetadata(s.name, maps.Clone(s.dirty))

		return
	}

	updatedData := make(map[string][]byte, len(s.updatedKeys))
	// Discards values for files that no longer exist.
	for key := range s.updatedKeys {
		updatedData[key] = s.dirty[key]
	}

	s.host.storeMetadata(s.name, updatedData)
}
