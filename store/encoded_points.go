package store

import (
	"glouton/types"
	"time"

	"github.com/prometheus/prometheus/tsdb/chunkenc"
)

type encodedPointsMeta struct {
	count            int
	youngest, oldest time.Time
}

// timeBounds returns the youngest and oldest timestamps
// of the point collection associated with this meta.
func (meta encodedPointsMeta) timeBounds() (time.Time, time.Time) {
	return meta.youngest, meta.oldest
}

type encodedPoints struct {
	xorChunkPool chunkenc.Pool

	metas map[uint64]encodedPointsMeta
	raws  map[uint64][]byte
}

func newEncodedPoints() *encodedPoints {
	return &encodedPoints{
		xorChunkPool: chunkenc.NewPool(),
		metas:        make(map[uint64]encodedPointsMeta),
		raws:         make(map[uint64][]byte),
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
func (epts *encodedPoints) getPoint(metricID uint64, idx int) types.Point {
	rawPoint, exists := epts.raws[metricID]
	if !exists {
		return types.Point{}
	}

	chunk, err := epts.xorChunkPool.Get(chunkenc.EncXOR, rawPoint)
	if err != nil {
		return types.Point{}
	}

	defer epts.xorChunkPool.Put(chunk) //nolint:errcheck

	for i, it := 0, chunk.Iterator(nil); it.Next(); i++ {
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
	rawPoints, exist := epts.raws[metricID]
	if !exist {
		return []types.Point{}, nil
	}

	chunk, err := epts.xorChunkPool.Get(chunkenc.EncXOR, rawPoints)
	if err != nil {
		return nil, err
	}

	defer epts.xorChunkPool.Put(chunk) //nolint:errcheck

	points := make([]types.Point, 0, chunk.NumSamples())

	it := chunk.Iterator(nil)
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
	chunk := chunkenc.NewXORChunk()

	app, err := chunk.Appender()
	if err != nil {
		return err
	}

	if _, exist := epts.raws[metricID]; exist {
		points, err := epts.getPoints(metricID)
		if err != nil {
			return err
		}

		for _, p := range points {
			app.Append(p.Time.UnixMilli(), p.Value)
		}
	}

	app.Append(point.Time.UnixMilli(), point.Value)

	chunk.Compact()

	epts.raws[metricID] = chunk.Bytes()

	meta := epts.metas[metricID]
	meta.count++

	if point.Time.Before(meta.youngest) || meta.youngest.IsZero() {
		meta.youngest = point.Time
	}

	if point.Time.After(meta.oldest) || meta.oldest.IsZero() {
		meta.oldest = point.Time
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

	epts.raws[metricID] = chunk.Bytes()
	meta := epts.metas[metricID]
	meta.count = len(points)

	if points[0].Time.Before(meta.youngest) || meta.youngest.IsZero() {
		meta.youngest = points[0].Time
	}

	if points[len(points)-1].Time.After(meta.oldest) || meta.oldest.IsZero() {
		meta.oldest = points[len(points)-1].Time
	}

	epts.metas[metricID] = meta

	return nil
}

// dropPoints drops the point collection associated with the given metric ID.
// If the metric ID is not referenced, dropPoints does nothing.
func (epts *encodedPoints) dropPoints(metricID uint64) {
	delete(epts.metas, metricID)
	delete(epts.raws, metricID)
}
