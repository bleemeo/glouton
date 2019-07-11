package debouncer

import (
	"context"
	"sync"
	"time"
)

// Debouncer make sure target function is not called too often. Run() must be called to works.
type Debouncer struct {
	target func(context.Context)
	delay  time.Duration

	l       sync.Mutex
	trigger bool
	wakeC   chan interface{}
	lastRun time.Time
	timer   *time.Timer
}

// New create a Debouncer. Two call to target won't be called with less that delay between them.
func New(target func(context.Context), delay time.Duration) *Debouncer {
	return &Debouncer{
		target: target,
		delay:  delay,
		wakeC:  make(chan interface{}),
		timer:  time.NewTimer(delay),
	}
}

// Run perform the call to target() when trigger is called
func (dd *Debouncer) Run(ctx context.Context) {
	dd.run(ctx, false)
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			break
		case <-dd.wakeC:
			dd.run(ctx, false)
		case <-dd.timer.C:
			dd.run(ctx, true)
		}
	}
	if !dd.timer.Stop() {
		<-dd.timer.C
	}
}

// Trigger will run the target() either immediately or after a delay if previous target() was just run.
//
// Trigger will always return immediately, and most likely before target() was run.
func (dd *Debouncer) Trigger() {
	dd.l.Lock()
	defer dd.l.Unlock()
	dd.trigger = true
	select {
	case dd.wakeC <- nil:
	default:
	}
}

func (dd *Debouncer) run(ctx context.Context, fromTimer bool) {
	dd.l.Lock()
	defer dd.l.Unlock()

	discoveryAgo := time.Since(dd.lastRun)
	if dd.trigger && discoveryAgo < dd.delay {
		// Update timer to the new delay
		if !dd.timer.Stop() && !fromTimer {
			<-dd.timer.C
		}
		dd.timer.Reset(dd.delay - discoveryAgo)
	} else if fromTimer {
		dd.timer.Reset(dd.delay)
	}

	if dd.trigger && discoveryAgo >= dd.delay {
		dd.trigger = false
		dd.l.Unlock()
		dd.target(ctx)
		dd.l.Lock()
		dd.lastRun = time.Now()
	}
}