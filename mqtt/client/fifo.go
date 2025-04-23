// Copyright 2015-2025 Bleemeo
//
// bleemeo.com an infrastructure monitoring solution in the Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package client

import (
	"context"
	"errors"
	"sync"
)

var errOpDone = errors.New("fifo operation done")

// fifo is a First In First Out queue implementation.
// A fifo queue can be queried and updated by multiple goroutines simultaneously.
type fifo[T any] struct {
	size              int
	queue             []T
	readIdx, writeIdx int
	writeReadDiff     int
	l                 *sync.Mutex
	notEmpty, notFull *sync.Cond
	closed            bool

	zero T
}

// newFifo returns an initialized fifo queue.
func newFifo[T any](size int) *fifo[T] {
	l := new(sync.Mutex)

	return &fifo[T]{
		size:     size,
		queue:    make([]T, size),
		l:        l,
		notEmpty: sync.NewCond(l),
		notFull:  sync.NewCond(l),
	}
}

// Close marks the fifo as closed, thus unable to add or pop elements.
func (fifo *fifo[T]) Close() {
	fifo.l.Lock()
	fifo.closed = true
	fifo.l.Unlock()

	// Release goroutines eventually waiting
	// for the queue not to be full or empty.
	fifo.notFull.Broadcast()
	fifo.notEmpty.Broadcast()
}

// Len returns the number of elements contained in the fifo queue.
// The returned value is not affected by the closing of the queue.
func (fifo *fifo[T]) Len() int {
	fifo.l.Lock()
	defer fifo.l.Unlock()

	return fifo.writeReadDiff
}

// watchForDone waits for the given Context to expire.
// Then, if it was not canceled due to the normal finish of calling function,
// it broadcasts a signal on the given condition to release eventually waiting goroutines.
// watchForDone must be started in a new goroutine, for instance by using the go statement.
func (fifo *fifo[T]) watchForDone(ctx context.Context, cond *sync.Cond) {
	<-ctx.Done()

	if !errors.Is(context.Cause(ctx), errOpDone) {
		fifo.l.Lock()
		cond.Broadcast()
		fifo.l.Unlock()
	}
}

// Get returns the oldest element of the queue along with whether the queue is open or not.
// If the queue is empty, Get will wait until an element is added to return it.
// When an element is returned by Get, it is removed from the queue freeing up a slot.
// If the given Context is canceled during the execution of Get, it returns false.
// Note that if the Get method returns that the queue is not opened anymore,
// the value returned is the zero-value of the queue's type.
func (fifo *fifo[T]) Get(ctx context.Context) (v T, open bool) {
	subCtx, cancel := context.WithCancelCause(ctx)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() { //nolint:wsl
		defer wg.Done()

		fifo.watchForDone(subCtx, fifo.notEmpty)
	}()

	defer wg.Wait()
	// Deferring context cancel after WaitGroup.Wait to have it called before the WaitGroup.Wait
	defer cancel(errOpDone)

	fifo.l.Lock()
	defer fifo.l.Unlock()

	if fifo.closed || ctx.Err() != nil {
		return v, false
	}

	for fifo.writeReadDiff <= 0 {
		fifo.notEmpty.Wait()

		if fifo.closed || ctx.Err() != nil {
			return v, false
		}
	}

	if fifo.readIdx == fifo.size {
		fifo.readIdx = 0
	}

	fifo.writeReadDiff--
	v = fifo.queue[fifo.readIdx]
	fifo.queue[fifo.readIdx] = fifo.zero // remove the reference to the value we stored here
	fifo.readIdx++

	fifo.notFull.Signal()

	return v, true
}

// Put adds the given value to the queue.
// If the queue is closed, Put returns without doing anything.
// If the queue is full, Put waits until a slot is freed and
// only returns once the value has been added.
func (fifo *fifo[T]) Put(ctx context.Context, v T) {
	subCtx, cancel := context.WithCancelCause(ctx)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() { //nolint:wsl
		defer wg.Done()

		fifo.watchForDone(subCtx, fifo.notFull)
	}()

	defer wg.Wait()
	// Deferring context cancel after WaitGroup.Wait to have it called before the WaitGroup.Wait
	defer cancel(errOpDone)

	fifo.l.Lock()
	defer fifo.l.Unlock()

	if fifo.closed || ctx.Err() != nil {
		return
	}

	for fifo.writeReadDiff >= fifo.size {
		fifo.notFull.Wait()

		if fifo.closed || ctx.Err() != nil {
			return
		}
	}

	if fifo.writeIdx == fifo.size {
		fifo.writeIdx = 0
	}

	fifo.writeReadDiff++
	fifo.queue[fifo.writeIdx] = v
	fifo.writeIdx++

	fifo.notEmpty.Signal()
}

// PutNoWait tries to add the given value to the queue if a slot is free.
// If the queue is full, PutNoWait does nothing and returns false.
// If the queue is closed, PutNoWait also does nothing and returns false.
func (fifo *fifo[T]) PutNoWait(v T) (ok bool) {
	fifo.l.Lock()
	defer fifo.l.Unlock()

	if fifo.closed {
		return false
	}

	if fifo.writeReadDiff >= fifo.size {
		return false
	}

	if fifo.closed {
		return false
	}

	if fifo.writeIdx == fifo.size {
		fifo.writeIdx = 0
	}

	fifo.writeReadDiff++
	fifo.queue[fifo.writeIdx] = v
	fifo.writeIdx++

	fifo.notEmpty.Signal()

	return true
}
