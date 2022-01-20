package registry

import (
	"errors"
	"glouton/types"

	"github.com/prometheus/prometheus/model/exemplar"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
)

var errNotImplemented = errors.New("not implemented")

type bufferAppender struct {
	temp      []promql.Sample
	committed map[string][]promql.Sample
}

func newBufferAppender() *bufferAppender {
	return &bufferAppender{}
}

func (a *bufferAppender) Append(ref storage.SeriesRef, l labels.Labels, t int64, v float64) (storage.SeriesRef, error) {
	a.temp = append(a.temp, promql.Sample{Point: promql.Point{T: t, V: v}, Metric: l})

	return 0, nil
}

func (a *bufferAppender) Commit() error {
	if a.committed == nil {
		a.committed = make(map[string][]promql.Sample)
	}

	for _, sample := range a.temp {
		name := sample.Metric.Get(types.LabelName)
		a.committed[name] = append(a.committed[name], sample)
	}

	_ = a.Rollback()

	return nil
}

func (a *bufferAppender) Rollback() error {
	a.temp = nil

	return nil
}

func (a *bufferAppender) AppendExemplar(ref storage.SeriesRef, l labels.Labels, e exemplar.Exemplar) (storage.SeriesRef, error) {
	return 0, errNotImplemented
}
