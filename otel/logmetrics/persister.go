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

package logmetrics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension/xextension/storage"
)

// This is a copy of otel/logprocessing's persister.go, kept independent on
// purpose: a distinct storage type and state cache key avoid
// any collision with log shipping's own persisted offsets.
const (
	storageType          = "glouton_log_metrics_storage"
	fileMetadataCacheKey = "LogMetricsFileMetadata"
)

var errStorageClientNotFound = errors.New("storage client not found")

func getFileMetadataFromCache(state bleemeoTypes.State) (map[string]map[string][]byte, error) {
	var metadataMap map[string]map[string][]byte

	err := state.Get(fileMetadataCacheKey, &metadataMap)
	if err != nil {
		return nil, err
	}

	if metadataMap == nil { // it may not exist in the state cache yet
		metadataMap = make(map[string]map[string][]byte)
	}

	return metadataMap, nil
}

func saveFileMetadataToCache(state bleemeoTypes.State, metadata map[string]map[string][]byte) {
	if err := state.Set(fileMetadataCacheKey, metadata); err != nil {
		logger.V(1).Printf("logmetrics: failed to save log file metadata to cache: %v", err)
	}
}

// persistHost is a minimal component.Host (just GetExtensions()) backing every
// source's filelogreceiver StorageID with a storageClient persisted into
// Glouton's state cache, so read offsets survive a Glouton restart.
type persistHost struct {
	l                   sync.Mutex
	extensions          map[component.ID]component.Component
	metadataPerReceiver map[string]map[string][]byte
}

func newPersistHost(state bleemeoTypes.State) (*persistHost, error) {
	metadata, err := getFileMetadataFromCache(state)
	if err != nil {
		return nil, err
	}

	return &persistHost{
		extensions:          make(map[component.ID]component.Component),
		metadataPerReceiver: metadata,
	}, nil
}

// newPersistentExt registers (or re-attaches to) the persisted storage for name
// (a stable per-source identity: a joined path list for static sources, a
// container ID for container sources) and returns its component.ID.
func (h *persistHost) newPersistentExt(name string) component.ID {
	h.l.Lock()
	defer h.l.Unlock()

	id := component.MustNewIDWithName(storageType, name)

	receiverMetadata, found := h.metadataPerReceiver[name]
	if !found {
		receiverMetadata = make(map[string][]byte)
	}

	h.extensions[id] = persistExtension{
		client: &storageClient{
			name:  name,
			host:  h,
			dirty: maps.Clone(receiverMetadata),
		},
	}

	return id
}

func (h *persistHost) removePersistentExt(id component.ID) {
	h.l.Lock()
	defer h.l.Unlock()

	delete(h.extensions, id)
}

func (h *persistHost) storeMetadata(recvName string, metadata map[string][]byte) {
	h.l.Lock()
	defer h.l.Unlock()

	h.metadataPerReceiver[recvName] = metadata
}

func (h *persistHost) saveToState(state bleemeoTypes.State) {
	saveFileMetadataToCache(state, h.getAllMetadata())
}

// getAllMetadata returns a full snapshot of every receiver's metadata, not
// just the ones touched since this Manager started: state.Set overwrites the
// whole cache key, so persisting anything less than the full picture would
// silently evict untouched receivers (e.g. an idle source) from the cache.
func (h *persistHost) getAllMetadata() map[string]map[string][]byte {
	h.l.Lock()
	defer h.l.Unlock()

	out := make(map[string]map[string][]byte, len(h.metadataPerReceiver))

	for key, val := range h.metadataPerReceiver {
		out[key] = maps.Clone(val)
	}

	return out
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

	file, err := writer.Create("log-to-metrics/persister.json")
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
	host *persistHost

	l     sync.Mutex
	dirty map[string][]byte
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

	s.host.l.Lock()

	m := s.host.metadataPerReceiver[s.name]
	if m == nil {
		m = make(map[string][]byte)
		s.host.metadataPerReceiver[s.name] = m
	}

	m[key] = cloned
	s.host.l.Unlock()
}

func (s *storageClient) Delete(_ context.Context, key string) error {
	s.l.Lock()
	defer s.l.Unlock()

	s.delete(key)

	return nil
}

func (s *storageClient) delete(key string) {
	delete(s.dirty, key)

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

// Close is called by filelogreceiver when it shuts down.
func (s *storageClient) Close(_ context.Context) error {
	s.l.Lock()
	defer s.l.Unlock()

	s.saveMetadata()

	return nil
}

func (s *storageClient) saveMetadata() {
	s.host.storeMetadata(s.name, maps.Clone(s.dirty))
}
