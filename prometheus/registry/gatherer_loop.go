package registry

import (
	"context"
	"glouton/logger"
	"io"
	"time"
	"unsafe"
	_ "unsafe" // required for unsafe linkname import

	"github.com/go-kit/log"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/pkg/exemplar"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/pkg/pool"
	_ "github.com/prometheus/prometheus/scrape" // for unsafe linkname import
	"github.com/prometheus/prometheus/storage"
)

// scrapeLoop is an unsafe access to Prometheus unexported scrapeLoop.
// We use the Prometheus scrapeLoop reuse their scheduling and time alignement.
// To use their time alignement, we need to hijack the Appender.Append & Commit to
// get the aligned timestamp.
// After each scheduling, the callback is called.
type scrapeLoop struct {
	ptr    unsafe.Pointer
	cancel context.CancelFunc
}

func startScrapeLoop(ctx context.Context, interval time.Duration, timeout time.Duration, jitterSeed uint64, callback func(ctx context.Context, t0 time.Time)) scrapeLoop {
	ctx, cancel := context.WithCancel(ctx)

	// Note: if you got segfault after Prometheus upgrade,
	// double verify the number and order of arguments.
	result := newScrapeLoop(ctx, noopScraper{jitterSeed: jitterSeed}, logger.GoKitLoggerWrapper(logger.V(0)), nil,
		nil,
		func(l labels.Labels) labels.Labels { return l },
		func() storage.Appender {
			return &mockAppend{
				callback: callback,
				timeout:  timeout,
			}
		},
		nil,
		jitterSeed,
		false,
	)

	sl := scrapeLoop{
		ptr:    result,
		cancel: cancel,
	}

	errc := make(chan error)

	go func() {
		scraperLoopRun(sl.ptr, interval, timeout, errc)

		close(errc)
	}()

	go func() {
		for err := range errc {
			logger.Printf("error in scrapeLoop: %v", err)
		}
	}()

	return sl
}

func (sl scrapeLoop) stop() {
	sl.cancel()
	scraperLoopStop(sl.ptr)
}

// A scraper retrieves samples and accepts a status report at the end.
type scraper interface {
	scrape(ctx context.Context, w io.Writer) (string, error)
	Report(start time.Time, dur time.Duration, err error)
	offset(interval time.Duration, jitterSeed uint64) time.Duration
}

type noopScraper struct {
	jitterSeed uint64
}

func (r noopScraper) scrape(ctx context.Context, w io.Writer) (string, error) {
	return "text/plain;does not matter", nil
}

func (r noopScraper) Report(start time.Time, dur time.Duration, err error) {
}

func (r noopScraper) offset(interval time.Duration, jitterSeed uint64) time.Duration {
	now := time.Now().UnixNano()

	var (
		base   = int64(interval) - now%int64(interval)
		offset = r.jitterSeed % uint64(interval)
		next   = base + int64(offset)
	)

	if next > int64(interval) {
		next -= int64(interval)
	}

	return time.Duration(next)
}

type labelsMutator func(labels.Labels) labels.Labels

//go:linkname newScrapeLoop github.com/prometheus/prometheus/scrape.newScrapeLoop
func newScrapeLoop(ctx context.Context,
	sc scraper,
	l log.Logger,
	buffers *pool.Pool,
	sampleMutator labelsMutator,
	reportSampleMutator labelsMutator,
	appender func() storage.Appender,
	cache *struct{},
	jitterSeed uint64,
	honorTimestamps bool,
) unsafe.Pointer

//go:linkname scraperLoopRun github.com/prometheus/prometheus/scrape.(*scrapeLoop).run
func scraperLoopRun(ptr unsafe.Pointer, interval, timeout time.Duration, errc chan<- error)

//go:linkname scraperLoopStop github.com/prometheus/prometheus/scrape.(*scrapeLoop).stop
func scraperLoopStop(ptr unsafe.Pointer)

type mockAppend struct {
	callback func(ctx context.Context, t0 time.Time)
	timeout  time.Duration
	t0       time.Time
}

func (ap *mockAppend) Commit() error {
	if ap.t0.IsZero() {
		return errMissingAppend
	}

	ctx, cancel := context.WithTimeout(context.Background(), ap.timeout)
	defer cancel()

	ap.callback(ctx, ap.t0)

	return nil
}

func (ap *mockAppend) Rollback() error {
	return nil
}

func (ap *mockAppend) Append(ref uint64, l labels.Labels, t int64, v float64) (uint64, error) {
	ap.t0 = model.Time(t).Time()

	return 0, nil
}

func (ap *mockAppend) AppendExemplar(ref uint64, l labels.Labels, e exemplar.Exemplar) (uint64, error) {
	return 0, errNotImplemented
}
