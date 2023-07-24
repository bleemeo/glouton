package store

import (
	"glouton/types"
	"testing"
	"time"
)

type mapStore map[uint64][]types.Point

func (mStore *mapStore) pushPoint(id uint64, point types.Point) error {
	(*mStore)[id] = append((*mStore)[id], point)

	return nil
}

const pointsPerMetric = 360

func BenchmarkPointsWriting(b *testing.B) {
	t0 := time.Now()
	points := newEncodedPoints()
	//points := make(mapStore)

	for i := 0; i < b.N; i++ {
		m := uint64(i)
		for p := 0; p < pointsPerMetric; p++ {
			err := points.pushPoint(m, types.Point{
				Time:  t0.Add(time.Duration(p*10) * time.Second),
				Value: float64(p),
			})
			if err != nil {
				b.Fatalf("Metric %d / Point %d: %v", m, p, err)
			}
		}
	}
}
