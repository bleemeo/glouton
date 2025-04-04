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

package store

import (
	"context"
	"time"

	"github.com/bleemeo/glouton/types"

	"github.com/prometheus/prometheus/model/exemplar"
	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/metadata"
	"github.com/prometheus/prometheus/storage"
)

type appender struct {
	store *Store
	ctx   context.Context //nolint:containedctx
}

// Appender returns a prometheus appender wrapping the in memory store.
func (s *Store) Appender(ctx context.Context) storage.Appender {
	return appender{store: s, ctx: ctx}
}

func (a appender) Append(_ storage.SeriesRef, l labels.Labels, t int64, v float64) (storage.SeriesRef, error) {
	labelsMap := make(map[string]string)

	for _, lblv := range l {
		labelsMap[lblv.Name] = lblv.Value
	}

	newPoint := types.MetricPoint{
		Point: types.Point{
			Time:  time.Unix(0, t*1e6),
			Value: v,
		},
		Labels:      labelsMap,
		Annotations: types.MetricAnnotations{},
	}

	a.store.PushPoints(a.ctx, []types.MetricPoint{newPoint})

	return 0, nil
}

func (a appender) Commit() error {
	return nil
}

func (a appender) Rollback() error {
	return errNotImplemented
}

func (a appender) SetOptions(*storage.AppendOptions) {}

func (a appender) AppendExemplar(storage.SeriesRef, labels.Labels, exemplar.Exemplar) (storage.SeriesRef, error) {
	return 0, errNotImplemented
}

func (a appender) AppendHistogram(storage.SeriesRef, labels.Labels, int64, *histogram.Histogram, *histogram.FloatHistogram) (storage.SeriesRef, error) {
	return 0, errNotImplemented
}

func (a appender) AppendHistogramCTZeroSample(storage.SeriesRef, labels.Labels, int64, int64, *histogram.Histogram, *histogram.FloatHistogram) (storage.SeriesRef, error) {
	return 0, errNotImplemented
}

func (a appender) UpdateMetadata(storage.SeriesRef, labels.Labels, metadata.Metadata) (storage.SeriesRef, error) {
	return 0, errNotImplemented
}

func (a appender) AppendCTZeroSample(_ storage.SeriesRef, _ labels.Labels, _, _ int64) (storage.SeriesRef, error) {
	return 0, errNotImplemented
}
