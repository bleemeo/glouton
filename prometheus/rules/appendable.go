package rules

import (
	"context"
	"errors"
	"sync"

	"github.com/prometheus/prometheus/model/exemplar"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
)

type dynamicAppendable struct {
	app storage.Appendable
	l   sync.Mutex
}

type errAppender struct{}

var errNotAvailable = errors.New("Appender not available")

func (a *dynamicAppendable) Appender(ctx context.Context) storage.Appender {
	a.l.Lock()
	defer a.l.Unlock()

	if a.app == nil {
		return errAppender{}
	}

	return a.app.Appender(ctx)
}

func (a *dynamicAppendable) SetAppendable(app storage.Appendable) {
	a.l.Lock()
	defer a.l.Unlock()

	a.app = app
}

func (a errAppender) Append(ref storage.SeriesRef, l labels.Labels, t int64, v float64) (storage.SeriesRef, error) {
	return 0, errNotAvailable
}

func (a errAppender) Commit() error {
	return errNotAvailable
}

func (a errAppender) Rollback() error {
	return errNotAvailable
}

func (a errAppender) AppendExemplar(ref storage.SeriesRef, l labels.Labels, e exemplar.Exemplar) (storage.SeriesRef, error) {
	return 0, errNotAvailable
}
