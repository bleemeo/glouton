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
	"errors"
	"sync"
	"time"

	"github.com/bleemeo/glouton/types"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/model/exemplar"
	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/metadata"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
)

var errNotImplemented = errors.New("not implemented")

type BufferAppender struct {
	l         sync.Mutex
	temp      []promql.Sample
	committed map[string][]promql.Sample
}

// NewBufferAppender return a new appender that store sample in-memory.
func NewBufferAppender() *BufferAppender {
	return &BufferAppender{}
}

func (a *BufferAppender) Append(_ storage.SeriesRef, l labels.Labels, t int64, v float64) (storage.SeriesRef, error) {
	a.l.Lock()
	defer a.l.Unlock()

	a.temp = append(a.temp, promql.Sample{T: t, F: v, Metric: l})

	return 0, nil
}

func (a *BufferAppender) Commit() error {
	a.l.Lock()
	defer a.l.Unlock()

	a.commit()

	return nil
}

func (a *BufferAppender) commit() {
	if a.committed == nil {
		a.committed = make(map[string][]promql.Sample)
	}

	for _, sample := range a.temp {
		name := sample.Metric.Get(types.LabelName)
		a.committed[name] = append(a.committed[name], sample)
	}

	a.rollback()
}

func (a *BufferAppender) Rollback() error {
	a.l.Lock()
	defer a.l.Unlock()

	a.rollback()

	return nil
}

func (a *BufferAppender) rollback() {
	a.temp = nil
}

func (a *BufferAppender) SetOptions(*storage.AppendOptions) {}

func (a *BufferAppender) FixSampleTimestamp(ts time.Time) {
	a.l.Lock()
	defer a.l.Unlock()

	for _, samples := range a.committed {
		for i := range samples {
			samples[i].T = ts.UnixMilli()
		}
	}
}

func (a *BufferAppender) AsMF() ([]*dto.MetricFamily, error) {
	a.l.Lock()
	defer a.l.Unlock()

	mfs := make([]*dto.MetricFamily, 0, len(a.committed))

	for _, samples := range a.committed {
		mf, err := SamplesToMetricFamily(samples, nil)
		if err != nil {
			return nil, err
		}

		mfs = append(mfs, mf)
	}

	return mfs, nil
}

func (a *BufferAppender) Reset() {
	a.l.Lock()
	defer a.l.Unlock()

	a.reset()
}

func (a *BufferAppender) reset() {
	a.temp = nil
	a.committed = nil
}

// CopyTo copy committed sample to given appender. It don't call Commit() on any Appender.
func (a *BufferAppender) CopyTo(app storage.Appender) error {
	a.l.Lock()
	defer a.l.Unlock()

	return a.copyTo(app)
}

// CommitCopyAndReset calls Commit, CopyTo and Reset atomically.
func (a *BufferAppender) CommitCopyAndReset(app storage.Appender) error {
	a.l.Lock()
	defer a.l.Unlock()

	a.commit()

	err := a.copyTo(app)
	if err != nil {
		return err
	}

	a.reset()

	return nil
}

func (a *BufferAppender) copyTo(app storage.Appender) error {
	for _, samples := range a.committed {
		for _, sample := range samples {
			_, err := app.Append(0, sample.Metric, sample.T, sample.F)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (a *BufferAppender) AppendExemplar(storage.SeriesRef, labels.Labels, exemplar.Exemplar) (storage.SeriesRef, error) {
	return 0, errNotImplemented
}

func (a *BufferAppender) AppendHistogram(storage.SeriesRef, labels.Labels, int64, *histogram.Histogram, *histogram.FloatHistogram) (storage.SeriesRef, error) {
	return 0, errNotImplemented
}

func (a *BufferAppender) AppendHistogramCTZeroSample(storage.SeriesRef, labels.Labels, int64, int64, *histogram.Histogram, *histogram.FloatHistogram) (storage.SeriesRef, error) {
	return 0, errNotImplemented
}

func (a *BufferAppender) UpdateMetadata(storage.SeriesRef, labels.Labels, metadata.Metadata) (storage.SeriesRef, error) {
	return 0, errNotImplemented
}

func (a *BufferAppender) AppendCTZeroSample(_ storage.SeriesRef, _ labels.Labels, _, _ int64) (storage.SeriesRef, error) {
	return 0, errNotImplemented
}
