package registry

import (
	"context"
	"errors"
	"glouton/types"
	"time"

	"github.com/prometheus/prometheus/pkg/exemplar"
	"github.com/prometheus/prometheus/pkg/labels"
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

func (a *appender) Append(ref uint64, l labels.Labels, t int64, v float64) (uint64, error) {
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

func (a *appender) AppendExemplar(ref uint64, l labels.Labels, e exemplar.Exemplar) (uint64, error) {
	return 0, errNotImplemented
}
