package model

import (
	"context"
	"sync"

	"github.com/prometheus/prometheus/model/exemplar"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
)

type AppenableOfAppender struct {
	l   sync.Mutex
	app storage.Appender
}

type childrenAppender struct {
	parent *AppenableOfAppender
	ctx    context.Context
}

// NewFromAppender create an Appenable from an appender.
// The result appenders created from this Appendable are thread-safe, but call to Commit() or Rollback() will
// affect all appenders.
func NewFromAppender(app storage.Appender) *AppenableOfAppender {
	return &AppenableOfAppender{
		app: app,
	}
}

func (a *AppenableOfAppender) Appender(ctx context.Context) storage.Appender {
	return &childrenAppender{
		parent: a,
		ctx:    ctx,
	}
}

func (a *childrenAppender) Append(ref storage.SeriesRef, l labels.Labels, t int64, v float64) (storage.SeriesRef, error) {
	a.parent.l.Lock()
	defer a.parent.l.Unlock()

	return a.parent.app.Append(ref, l, t, v)
}

func (a *childrenAppender) Commit() error {
	a.parent.l.Lock()
	defer a.parent.l.Unlock()

	return a.parent.app.Commit()
}

func (a *childrenAppender) Rollback() error {
	a.parent.l.Lock()
	defer a.parent.l.Unlock()

	return a.parent.app.Rollback()
}

func (a *childrenAppender) AppendExemplar(ref storage.SeriesRef, l labels.Labels, e exemplar.Exemplar) (storage.SeriesRef, error) {
	a.parent.l.Lock()
	defer a.parent.l.Unlock()

	return a.parent.app.AppendExemplar(ref, l, e)
}
