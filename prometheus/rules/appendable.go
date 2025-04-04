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

package rules

import (
	"context"
	"errors"
	"sync"

	"github.com/prometheus/prometheus/model/exemplar"
	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/metadata"
	"github.com/prometheus/prometheus/storage"
)

type dynamicAppendable struct {
	app storage.Appendable
	l   sync.Mutex
}

type errAppender struct{}

var errNotAvailable = errors.New("Appender not available")

func (a *dynamicAppendable) Appender(ctx context.Context) storage.Appender {
	a.l.Lock()
	defer a.l.Unlock()

	if a.app == nil {
		return errAppender{}
	}

	return a.app.Appender(ctx)
}

func (a *dynamicAppendable) SetAppendable(app storage.Appendable) {
	a.l.Lock()
	defer a.l.Unlock()

	a.app = app
}

func (a errAppender) Append(storage.SeriesRef, labels.Labels, int64, float64) (storage.SeriesRef, error) {
	return 0, errNotAvailable
}

func (a errAppender) Commit() error {
	return errNotAvailable
}

func (a errAppender) Rollback() error {
	return errNotAvailable
}

func (a errAppender) SetOptions(*storage.AppendOptions) {}

func (a errAppender) AppendExemplar(storage.SeriesRef, labels.Labels, exemplar.Exemplar) (storage.SeriesRef, error) {
	return 0, errNotAvailable
}

func (a errAppender) AppendHistogram(storage.SeriesRef, labels.Labels, int64, *histogram.Histogram, *histogram.FloatHistogram) (storage.SeriesRef, error) {
	return 0, errNotAvailable
}

func (a errAppender) AppendHistogramCTZeroSample(storage.SeriesRef, labels.Labels, int64, int64, *histogram.Histogram, *histogram.FloatHistogram) (storage.SeriesRef, error) {
	return 0, errNotAvailable
}

func (a errAppender) UpdateMetadata(storage.SeriesRef, labels.Labels, metadata.Metadata) (storage.SeriesRef, error) {
	return 0, errNotAvailable
}

func (a errAppender) AppendCTZeroSample(_ storage.SeriesRef, _ labels.Labels, _, _ int64) (storage.SeriesRef, error) {
	return 0, errNotAvailable
}
