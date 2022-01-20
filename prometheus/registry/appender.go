package registry

import (
	"context"
	"errors"
	"glouton/types"
	"time"

	"github.com/prometheus/prometheus/model/exemplar"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
)

var errNotImplemented = errors.New("not implemented")

type Appendable struct {
	reg *Registry
	ttl time.Duration
}

type appender struct {
	reg    *Registry
	ttl    time.Duration
	buffer []types.MetricPoint
	ctx    context.Context
}

// Appender returns a prometheus appender that push points to the Registry.
func (app Appendable) Appender(ctx context.Context) storage.Appender {
	return &appender{reg: app.reg, ttl: app.ttl, ctx: ctx}
}

func (a *appender) Append(ref storage.SeriesRef, l labels.Labels, t int64, v float64) (storage.SeriesRef, error) {
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

	a.buffer = append(a.buffer, newPoint)

	return 0, nil
}

func (a *appender) Commit() error {
	a.reg.pushPoint(a.ctx, a.buffer, a.ttl, types.MetricFormatPrometheus)

	a.buffer = a.buffer[:0]

	return nil
}

func (a *appender) Rollback() error {
	a.buffer = a.buffer[:0]

	return nil
}

func (a *appender) AppendExemplar(ref storage.SeriesRef, l labels.Labels, e exemplar.Exemplar) (storage.SeriesRef, error) {
	return 0, errNotImplemented
}

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
