// Copyright 2015-2022 Bleemeo
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
	"glouton/types"

	"github.com/prometheus/prometheus/model/exemplar"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
)

var errNotImplemented = errors.New("not implemented")

type BufferAppender struct {
	temp      []promql.Sample
	Committed map[string][]promql.Sample
}

// NewBufferAppender return a new appender that store sample in-memory. It is not thread-safe.
func NewBufferAppender() *BufferAppender {
	return &BufferAppender{}
}

func (a *BufferAppender) Append(ref storage.SeriesRef, l labels.Labels, t int64, v float64) (storage.SeriesRef, error) {
	a.temp = append(a.temp, promql.Sample{Point: promql.Point{T: t, V: v}, Metric: l})

	return 0, nil
}

func (a *BufferAppender) Commit() error {
	if a.Committed == nil {
		a.Committed = make(map[string][]promql.Sample)
	}

	for _, sample := range a.temp {
		name := sample.Metric.Get(types.LabelName)
		a.Committed[name] = append(a.Committed[name], sample)
	}

	_ = a.Rollback()

	return nil
}

func (a *BufferAppender) Rollback() error {
	a.temp = nil

	return nil
}

func (a *BufferAppender) AppendExemplar(ref storage.SeriesRef, l labels.Labels, e exemplar.Exemplar) (storage.SeriesRef, error) {
	return 0, errNotImplemented
}
