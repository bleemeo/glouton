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

// Package tsdb embeds Prometheus' on-disk TSDB so Glouton can persist
// metrics locally without running a separate process.
package tsdb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	prom_tsdb "github.com/prometheus/prometheus/tsdb"
)

var (
	errPathRequired = errors.New("tsdb: path is required")
	errStoreClosed  = errors.New("tsdb: store is closed")
)

// Store wraps a prometheus/tsdb.DB so it can be used as a Glouton
// types.PointPusher and as a storage.Queryable for the local API.
type Store struct {
	db        *prom_tsdb.DB
	path      string
	retention time.Duration

	l      sync.Mutex
	closed bool
}

// Options configures a Store.
type Options struct {
	// Path is the directory where TSDB blocks are stored.
	// It must exist and be writable.
	Path string
	// Retention controls how long data is kept on disk.
	// A zero value falls back to Prometheus' default (15d).
	Retention time.Duration
}

// Open opens (or creates) a TSDB at opts.Path. Open is expensive (it
// replays the WAL) so callers should keep the returned Store across
// agent reloads.
func Open(opts Options) (*Store, error) {
	if opts.Path == "" {
		return nil, errPathRequired
	}

	if err := os.MkdirAll(opts.Path, 0o750); err != nil {
		return nil, fmt.Errorf("tsdb: create dir: %w", err)
	}

	tsdbOpts := prom_tsdb.DefaultOptions()
	if opts.Retention > 0 {
		tsdbOpts.RetentionDuration = int64(opts.Retention / time.Millisecond)
	}

	db, err := prom_tsdb.Open(opts.Path, newSlogger(), prometheus.NewRegistry(), tsdbOpts, nil)
	if err != nil {
		return nil, fmt.Errorf("tsdb: open %s: %w", opts.Path, err)
	}

	retention := opts.Retention
	if retention <= 0 {
		retention = time.Duration(prom_tsdb.DefaultOptions().RetentionDuration) * time.Millisecond
	}

	return &Store{db: db, path: opts.Path, retention: retention}, nil
}

// Path returns the on-disk location of the TSDB.
func (s *Store) Path() string { return s.path }

// Retention returns the configured retention duration.
func (s *Store) Retention() time.Duration { return s.retention }

// OldestPointMs returns the timestamp (in ms since epoch) of the
// oldest point that can still be queried, looking at on-disk blocks
// first and falling back to the head block. Returns 0 if the store is
// empty or closed.
func (s *Store) OldestPointMs() int64 {
	s.l.Lock()
	closed := s.closed
	s.l.Unlock()

	if closed {
		return 0
	}

	if blocks := s.db.Blocks(); len(blocks) > 0 {
		return blocks[0].MinTime()
	}

	head := s.db.Head()
	minTime := head.MinTime()

	// An empty head returns math.MaxInt64; treat that as "no data".
	if minTime == int64(^uint64(0)>>1) {
		return 0
	}

	return minTime
}

// PushPoints implements types.PointPusher. It writes the given points
// into a single appender and commits. Points with NaN values are
// silently skipped (Prometheus' staleness convention is encoded
// elsewhere; we don't want to commit unmappable values here).
func (s *Store) PushPoints(ctx context.Context, points []types.MetricPoint) {
	if len(points) == 0 {
		return
	}

	s.l.Lock()
	defer s.l.Unlock()

	if s.closed {
		return
	}

	app := s.db.Appender(ctx)

	for _, p := range points {
		if math.IsNaN(p.Value) {
			continue
		}

		lbls := labelsFromMap(p.Labels)
		if _, err := app.Append(0, lbls, p.Time.UnixMilli(), p.Value); err != nil {
			logger.V(2).Printf("tsdb: append %s: %v", lbls.String(), err)
		}
	}

	if err := app.Commit(); err != nil {
		logger.V(1).Printf("tsdb: commit: %v", err)
	}
}

// Querier returns a storage.Querier for the [mint, maxt] window
// (milliseconds since epoch).
func (s *Store) Querier(mint, maxt int64) (storage.Querier, error) {
	s.l.Lock()
	closed := s.closed
	s.l.Unlock()

	if closed {
		return nil, errStoreClosed
	}

	return s.db.Querier(mint, maxt)
}

// DiagnosticArchive writes a short status file for support archives.
func (s *Store) DiagnosticArchive(_ context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("tsdb.txt")
	if err != nil {
		return err
	}

	s.l.Lock()
	closed := s.closed
	s.l.Unlock()

	fmt.Fprintf(file, "Path: %s\n", s.path)
	fmt.Fprintf(file, "Closed: %v\n", closed)

	if !closed {
		head := s.db.Head()
		minTime := time.UnixMilli(head.MinTime()).UTC()
		maxTime := time.UnixMilli(head.MaxTime()).UTC()

		fmt.Fprintf(file, "Head min time: %s\n", minTime.Format(time.RFC3339))
		fmt.Fprintf(file, "Head max time: %s\n", maxTime.Format(time.RFC3339))
		fmt.Fprintf(file, "Head series: %d\n", head.NumSeries())
		fmt.Fprintf(file, "Blocks: %d\n", len(s.db.Blocks()))
	}

	return nil
}

// Close flushes the head block and releases the on-disk lock. After
// Close, Querier and PushPoints become no-ops.
func (s *Store) Close() error {
	s.l.Lock()
	defer s.l.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true

	return s.db.Close()
}

// newSlogger routes prometheus/tsdb's slog output through Glouton's
// logger, keeping the level at warn+ so normal operation stays quiet.
func newSlogger() *slog.Logger {
	return slog.New(&gloutonSlogHandler{minLevel: slog.LevelWarn})
}

type gloutonSlogHandler struct {
	minLevel slog.Level
	attrs    []slog.Attr
	group    string
}

func (h *gloutonSlogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.minLevel
}

func (h *gloutonSlogHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder

	b.WriteString("tsdb: ")
	b.WriteString(r.Message)

	for _, a := range h.attrs {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value.Any())
	}

	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value.Any())

		return true
	})

	switch {
	case r.Level >= slog.LevelError:
		logger.V(0).Println(b.String())
	default:
		logger.V(1).Println(b.String())
	}

	return nil
}

func (h *gloutonSlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	merged = append(merged, h.attrs...)
	merged = append(merged, attrs...)

	return &gloutonSlogHandler{minLevel: h.minLevel, attrs: merged, group: h.group}
}

func (h *gloutonSlogHandler) WithGroup(name string) slog.Handler {
	return &gloutonSlogHandler{minLevel: h.minLevel, attrs: h.attrs, group: name}
}

// labelsFromMap converts a Glouton MetricPoint label map into a
// sorted Prometheus labels.Labels (Prometheus' Append requires sorted
// labels for correctness).
func labelsFromMap(m map[string]string) labels.Labels {
	if len(m) == 0 {
		return labels.EmptyLabels()
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	b := labels.NewScratchBuilder(len(keys))
	for _, k := range keys {
		b.Add(k, m[k])
	}

	return b.Labels()
}
