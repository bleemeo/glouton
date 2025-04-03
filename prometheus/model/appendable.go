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

package model

import (
	"context"
	"sync"

	"github.com/prometheus/prometheus/model/exemplar"
	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/metadata"
	"github.com/prometheus/prometheus/storage"
)

type AppenableOfAppender struct {
	l   sync.Mutex
	app storage.Appender
}

type childrenAppender struct {
	parent *AppenableOfAppender
	ctx    context.Context //nolint:containedctx
}

// NewFromAppender create an Appenable from an appender.
// The result appenders created from this Appendable are thread-safe, but call to Commit() or Rollback() will
// affect all appenders.
func NewFromAppender(app storage.Appender) *AppenableOfAppender {
	return &AppenableOfAppender{
		app: app,
	}
}

func (a *AppenableOfAppender) Appender(ctx context.Context) storage.Appender {
	return &childrenAppender{
		parent: a,
		ctx:    ctx,
	}
}

func (a *childrenAppender) Append(ref storage.SeriesRef, l labels.Labels, t int64, v float64) (storage.SeriesRef, error) {
	a.parent.l.Lock()
	defer a.parent.l.Unlock()

	return a.parent.app.Append(ref, l, t, v)
}

func (a *childrenAppender) Commit() error {
	a.parent.l.Lock()
	defer a.parent.l.Unlock()

	return a.parent.app.Commit()
}

func (a *childrenAppender) Rollback() error {
	a.parent.l.Lock()
	defer a.parent.l.Unlock()

	return a.parent.app.Rollback()
}

func (a *childrenAppender) SetOptions(opts *storage.AppendOptions) {
	a.parent.l.Lock()
	defer a.parent.l.Unlock()

	a.parent.app.SetOptions(opts)
}

func (a *childrenAppender) AppendExemplar(ref storage.SeriesRef, l labels.Labels, e exemplar.Exemplar) (storage.SeriesRef, error) {
	a.parent.l.Lock()
	defer a.parent.l.Unlock()

	return a.parent.app.AppendExemplar(ref, l, e)
}

func (a *childrenAppender) AppendHistogram(ref storage.SeriesRef, l labels.Labels, t int64, h *histogram.Histogram, fh *histogram.FloatHistogram) (storage.SeriesRef, error) {
	a.parent.l.Lock()
	defer a.parent.l.Unlock()

	return a.parent.app.AppendHistogram(ref, l, t, h, fh)
}

func (a *childrenAppender) AppendHistogramCTZeroSample(ref storage.SeriesRef, l labels.Labels, t, ct int64, h *histogram.Histogram, fh *histogram.FloatHistogram) (storage.SeriesRef, error) {
	a.parent.l.Lock()
	defer a.parent.l.Unlock()

	return a.parent.app.AppendHistogramCTZeroSample(ref, l, t, ct, h, fh)
}

func (a *childrenAppender) UpdateMetadata(ref storage.SeriesRef, l labels.Labels, m metadata.Metadata) (storage.SeriesRef, error) {
	a.parent.l.Lock()
	defer a.parent.l.Unlock()

	return a.parent.app.UpdateMetadata(ref, l, m)
}

func (a *childrenAppender) AppendCTZeroSample(ref storage.SeriesRef, l labels.Labels, t, ct int64) (storage.SeriesRef, error) {
	a.parent.l.Lock()
	defer a.parent.l.Unlock()

	return a.parent.app.AppendCTZeroSample(ref, l, t, ct)
}
