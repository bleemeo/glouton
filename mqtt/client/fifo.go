package client

import (
	"sync"
	"sync/atomic"
)

// fifo is a First In First Out queue implementation.
// A fifo queue can be queried and updated by multiple goroutines simultaneously.
type fifo[T any] struct {
	size              int
	queue             []T
	readIdx, writeIdx int
	writeReadDiff     int64
	l                 *sync.Mutex
	notEmpty, notFull *sync.Cond
	// any value > 0 means closed
	closed uint32
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

// close marks the fifo as closed, thus unable to get or put elements.
func (fifo *fifo[T]) close() {
	atomic.StoreUint32(&fifo.closed, 1)
}

// isClosed reports whether the fifo queue is closed.
func (fifo *fifo[T]) isClosed() bool {
	return atomic.LoadUint32(&fifo.closed) != 0
}

// len returns the number of elements contained in the fifo queue.
func (fifo *fifo[T]) len() int {
	return int(atomic.LoadInt64(&fifo.writeReadDiff))
}

// get returns the oldest element of the queue along with whether the queue is open or not.
// If the queue is empty, get will wait until an element is added to return it.
// When an element is returned by get, it is removed from the queue freeing up a slot.
// Note that if the get method returns that the queue is not opened anymore,
// the value returned is the zero-value of the queue's type.
func (fifo *fifo[T]) get() (v T, open bool) {
	fifo.l.Lock()
	defer fifo.l.Unlock()

	if fifo.isClosed() {
		return v, false
	}

	if fifo.readIdx == fifo.size {
		fifo.readIdx = 0
	}

	for fifo.writeReadDiff <= 0 {
		fifo.notEmpty.Wait()
	}

	if fifo.isClosed() {
		return v, false
	}

	fifo.writeReadDiff--
	v = fifo.queue[fifo.readIdx]
	fifo.readIdx++

	fifo.notFull.Signal()

	return v, true
}

// put adds the given value to the queue.
// If the queue is closed, put returns without doing nothing.
// If the queue is full, put waits until a slot is freed and
// only returns once the value has been added.
func (fifo *fifo[T]) put(v T) {
	fifo.l.Lock()
	defer fifo.l.Unlock()

	if fifo.isClosed() {
		return
	}

	if fifo.writeIdx == fifo.size {
		fifo.writeIdx = 0
	}

	for fifo.writeReadDiff >= int64(fifo.size) {
		fifo.notFull.Wait()
	}

	if fifo.isClosed() {
		return
	}

	fifo.writeReadDiff++
	fifo.queue[fifo.writeIdx] = v
	fifo.writeIdx++

	fifo.notEmpty.Signal()
}

// putNoWait tries to add the given value to the queue if a slot is free.
// If the queue is full, putNoWait does nothing and returns false.
func (fifo *fifo[T]) putNoWait(v T) (ok bool) {
	fifo.l.Lock()
	defer fifo.l.Unlock()

	if fifo.isClosed() {
		return false
	}

	if fifo.writeIdx == fifo.size {
		fifo.writeIdx = 0
	}

	if fifo.writeReadDiff >= int64(fifo.size) {
		return false
	}

	if fifo.isClosed() {
		return false
	}

	fifo.writeReadDiff++
	fifo.queue[fifo.writeIdx] = v
	fifo.writeIdx++

	fifo.notEmpty.Signal()

	return true
}
