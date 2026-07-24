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

// PersistConfig gives a PersistHost its own identity, so two independent
// features (log shipping, log-to-metric) never collide in Glouton's shared
// state cache or OTel component registry, while sharing this implementation.
type PersistConfig struct {
	// StorageType is this host's component.Type, must be unique across features.
	StorageType string
	// CacheKey is the bleemeoTypes.State key this host's data is stored under.
	CacheKey string
	// ArchivePath is the file created by WriteToArchive in a diagnostic bundle.
	ArchivePath string
	// FullSnapshot, if true, persists every receiver's metadata on every
	// SaveToState call, even ones untouched since this PersistHost was built
	// (so an idle-but-still-running source doesn't lose its offset just for
	// not having produced anything yet this run). If false, only receivers
	// touched via Set/Delete/Batch since this PersistHost was built are
	// persisted, so a removed source's stale offset falls out of the cache
	// instead of being kept forever.
	FullSnapshot bool
	// SaveThrottle, if non-zero, limits how often a single Set() call pushes
	// its receiver's dirty data up into the host's in-memory map (a cheap
	// operation, but proportional to that receiver's key count). Zero means
	// push on every call.
	SaveThrottle time.Duration
}

// PersistHost is a minimal component.Host (just GetExtensions()) backing every
// source's filelogreceiver StorageID with a storageClient persisted into
// Glouton's state cache, so read offsets survive a Glouton restart.
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

// NewPersistentExt registers (or re-attaches to) the persisted storage for name
// (a stable per-source identity: e.g. a joined path list for static sources, a
// container ID for container sources) and returns its component.ID.
func (h *PersistHost) NewPersistentExt(name string) component.ID {
	h.l.Lock()
	defer h.l.Unlock()

	// We don't have to care about handling any error,
	// since the type is known to be correct (otherwise TestPersistHost would have failed),
	// and the name has no format restriction.
	id := component.MustNewIDWithName(h.cfg.StorageType, name)

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
			dirty:       maps.Clone(receiverMetadata),
			updatedKeys: make(map[string]struct{}),
			lastSave:    time.Now(),
		},
	}

	return id
}

// RemovePersistentExt un-registers a single extension, previously returned by NewPersistentExt.
func (h *PersistHost) RemovePersistentExt(id component.ID) {
	h.l.Lock()
	defer h.l.Unlock()

	delete(h.extensions, id)
}

// RemovePersistentExts un-registers several extensions at once.
func (h *PersistHost) RemovePersistentExts(ids []component.ID) {
	h.l.Lock()
	defer h.l.Unlock()

	for _, id := range ids {
		delete(h.extensions, id)
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
		// We assume the []byte values in `h.metadataPerReceiver[key]` aren't mutated.
		// If they were mutated, we'd also need to deep-copy them.
		// `h.metadataPerReceiver[key]` is always accessed under h.l: written by set/delete
		// and storeMetadata, and read here. Only keys in updatedKeys are read, as those
		// are the ones that have been explicitly stored via set or storeMetadata.
		updatedData[key] = maps.Clone(h.metadataPerReceiver[key])
	}

	return updatedData
}

// GetExtensions implements component.Host.
func (h *PersistHost) GetExtensions() map[component.ID]component.Component {
	h.l.Lock()
	defer h.l.Unlock()

	return h.extensions
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
	// Only saving the values that have been updated during this run,
	// so as to discard the ones that correspond to files that no longer exist.
	for key := range s.updatedKeys {
		updatedData[key] = s.dirty[key]
	}

	s.host.storeMetadata(s.name, updatedData)
}
