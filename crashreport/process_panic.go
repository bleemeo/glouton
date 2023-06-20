package crashreport

import (
	"time"

	"github.com/getsentry/sentry-go"
)

// ProcessPanic logs panics to Sentry.
// It should be deferred at the beginning of every new goroutine.
func ProcessPanic() {
	if err := recover(); err != nil {
		sentry.CurrentHub().Recover(err)
		sentry.Flush(time.Second * 5)
		panic(err)
	}
}
