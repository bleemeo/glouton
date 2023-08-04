// Copyright 2015-2023 Bleemeo
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

	"github.com/prometheus/prometheus/tsdb/chunkenc"
)

type encodedPointsMeta struct {
	count            int
	oldest, youngest time.Time
}

// timeBounds returns the oldest and youngest timestamps
// of the point collection associated with this meta.
func (meta encodedPointsMeta) timeBounds() (oldest time.Time, youngest time.Time) {
	return meta.oldest, meta.youngest
}

type raw struct {
	chunk    *chunkenc.XORChunk
	appender chunkenc.Appender
}

type encodedPoints struct {
	metas map[uint64]encodedPointsMeta
	raws  map[uint64]raw
}

func newEncodedPoints() *encodedPoints {
	return &encodedPoints{
		metas: make(map[uint64]encodedPointsMeta),
		raws:  make(map[uint64]raw),
	}
}

// count returns the number of points associated with the given metric ID.
// If the metric is not referenced, 0 is returned.
func (epts *encodedPoints) count(metricID uint64) int {
	if meta, ok := epts.metas[metricID]; ok {
		return meta.count
	}

	return 0
}

// getPoint returns the point at the given index from
// the collection associated with the given metric ID.
// If an error occurs, a zero-value Point is returned.
func (epts *encodedPoints) getPoint(metricID uint64, idx int) types.Point {
	rawPts, exists := epts.raws[metricID]
	if !exists {
		return types.Point{}
	}

	for i, it := 0, rawPts.chunk.Iterator(nil); it.Next(); i++ {
		if i != idx {
			continue
		}

		t, v := it.At()

		return types.Point{
			Time:  time.UnixMilli(t),
			Value: v,
		}
	}

	return types.Point{}
}

// getPoints returns all the points associated with the given metric ID,
// or any error that occurs during the retrieving operation.
func (epts *encodedPoints) getPoints(metricID uint64) ([]types.Point, error) {
	rawPts, exist := epts.raws[metricID]
	if !exist {
		return []types.Point{}, nil
	}

	points := make([]types.Point, 0, rawPts.chunk.NumSamples())

	it := rawPts.chunk.Iterator(nil)
	for it.Next() {
		t, v := it.At()

		points = append(points, types.Point{
			Time:  time.UnixMilli(t),
			Value: v,
		})
	}

	return points, it.Err()
}

// pushPoint appends the given point to the collection associated
// with the given metric ID. It returns any error encountered.
func (epts *encodedPoints) pushPoint(metricID uint64, point types.Point) error {
	rawPts, exist := epts.raws[metricID]
	if !exist { // Path only taken on instance's first push
		rawPts.chunk = chunkenc.NewXORChunk()

		appender, err := rawPts.chunk.Appender()
		if err != nil {
			return err
		}

		rawPts.appender = appender
	}

	rawPts.appender.Append(point.Time.UnixMilli(), point.Value)

	epts.raws[metricID] = rawPts

	meta := epts.metas[metricID]
	meta.count++

	if meta.oldest.IsZero() || point.Time.Before(meta.oldest) {
		meta.oldest = point.Time
	}

	if meta.youngest.IsZero() || point.Time.After(meta.youngest) {
		meta.youngest = point.Time
	}

	epts.metas[metricID] = meta

	return nil
}

// setPoints overrides the point collection associated to the given metric ID
// with the new given collection. It returns any error encountered.
func (epts *encodedPoints) setPoints(metricID uint64, points []types.Point) error {
	epts.dropPoints(metricID)

	if len(points) == 0 {
		return nil
	}

	chunk := chunkenc.NewXORChunk()

	app, err := chunk.Appender()
	if err != nil {
		return err
	}

	for _, p := range points {
		app.Append(p.Time.UnixMilli(), p.Value)
	}

	epts.raws[metricID] = raw{chunk, app}

	epts.metas[metricID] = encodedPointsMeta{
		count:    len(points),
		oldest:   points[0].Time,
		youngest: points[len(points)-1].Time,
	}

	return nil
}

// dropPoints drops the point collection associated with the given metric ID.
// If the metric ID is not referenced, dropPoints does nothing.
func (epts *encodedPoints) dropPoints(metricID uint64) {
	delete(epts.metas, metricID)
	delete(epts.raws, metricID)
}
