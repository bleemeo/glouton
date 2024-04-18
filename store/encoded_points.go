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
	"github.com/bleemeo/glouton/types"
	"time"

	"github.com/prometheus/prometheus/tsdb/chunkenc"
)

type pointsData struct {
	chunk    *chunkenc.XORChunk
	appender chunkenc.Appender

	oldest, youngest time.Time
}

// count returns the number of points present in this collection.
func (data pointsData) count() int {
	return data.chunk.NumSamples()
}

// timeBounds returns the oldest and youngest timestamps
// of this point collection.
func (data pointsData) timeBounds() (oldest time.Time, youngest time.Time) {
	return data.oldest, data.youngest
}

type encodedPoints struct {
	pointsPerMetric map[uint64]pointsData
}

func newEncodedPoints() *encodedPoints {
	return &encodedPoints{
		pointsPerMetric: make(map[uint64]pointsData),
	}
}

// count returns the number of points associated with the given metric ID.
// If the metric is not referenced, 0 is returned.
func (epts *encodedPoints) count(metricID uint64) int {
	if data, ok := epts.pointsPerMetric[metricID]; ok {
		return data.count()
	}

	return 0
}

// getPoint returns the point at the given index from
// the collection associated with the given metric ID.
// If an error occurs, a zero-value Point is returned.
func (epts *encodedPoints) getPoint(metricID uint64, idx int) types.Point {
	data, exists := epts.pointsPerMetric[metricID]
	if !exists {
		return types.Point{}
	}

	for i, it := 0, data.chunk.Iterator(nil); it.Next() == chunkenc.ValFloat; i++ {
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
	data, exist := epts.pointsPerMetric[metricID]
	if !exist {
		return []types.Point{}, nil
	}

	points := make([]types.Point, 0, data.chunk.NumSamples())

	it := data.chunk.Iterator(nil)
	for it.Next() == chunkenc.ValFloat {
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
	data, exist := epts.pointsPerMetric[metricID]
	if !exist { // Path only taken on instance's first push
		data.chunk = chunkenc.NewXORChunk()

		app, err := data.chunk.Appender()
		if err != nil {
			return err
		}

		data.appender = app
	}

	data.appender.Append(point.Time.UnixMilli(), point.Value)

	if data.oldest.IsZero() || point.Time.Before(data.oldest) {
		data.oldest = point.Time
	}

	if data.youngest.IsZero() || point.Time.After(data.youngest) {
		data.youngest = point.Time
	}

	epts.pointsPerMetric[metricID] = data

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

	var oldest, youngest time.Time

	for _, p := range points {
		app.Append(p.Time.UnixMilli(), p.Value)

		if oldest.IsZero() || p.Time.Before(oldest) {
			oldest = p.Time
		}

		if youngest.IsZero() || p.Time.After(youngest) {
			youngest = p.Time
		}
	}

	epts.pointsPerMetric[metricID] = pointsData{
		chunk:    chunk,
		appender: app,
		oldest:   oldest,
		youngest: youngest,
	}

	return nil
}

// dropPoints drops the point collection associated with the given metric ID.
// If the metric ID is not referenced, dropPoints does nothing.
func (epts *encodedPoints) dropPoints(metricID uint64) {
	delete(epts.pointsPerMetric, metricID)
}
