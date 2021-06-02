// Copyright 2015-2021 Bleemeo
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
	"glouton/types"
	"time"

	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/storage"
)

type appender struct {
	store *Store
}

//Appender returns a prometheus appender wrapping the in memory store.
func (s *Store) Appender() storage.Appender {
	return appender{store: s}
}

func (a appender) Add(l labels.Labels, t int64, v float64) (uint64, error) {
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

	a.store.PushPoints([]types.MetricPoint{newPoint})

	return 0, nil
}

func (a appender) AddFast(ref uint64, t int64, v float64) error {
	return errNotImplemented
}

func (a appender) Commit() error {
	return errNotImplemented
}

func (a appender) Rollback() error {
	return errNotImplemented
}
