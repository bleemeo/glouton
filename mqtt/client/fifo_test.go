package client

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSize(t *testing.T) {
	t.Parallel()

	for size := 1; size < 1e7; size *= 10 {
		t.Run(fmt.Sprintf("%d sized fifo queue", size), func(t *testing.T) {
			queue := newFifo[int](size)

			length := queue.len()
			if length != 0 {
				t.Fatalf("Unexpected queue length: got %d, want 0.", length)
			}

			for i := 0; i < size-1; i++ {
				queue.put(i)
			}

			length = queue.len()
			if length != size-1 {
				t.Fatalf("Unexpected queue length: got %d, want %d.", length, size-1)
			}

			added := queue.putNoWait(1)
			if !added {
				t.Fatalf("Should have added item n°%d in %d-sized queue.", size, size)
			}

			length = queue.len()
			if length != size {
				t.Fatalf("Unexpected queue length: got %d, want %d.", length, size)
			}

			added = queue.putNoWait(1)
			if added {
				t.Fatalf("Should not have added item n°%d in %d-sized queue.", size+1, size)
			}

			length = queue.len()
			if length != size {
				t.Fatalf("Unexpected queue length: got %d, want %d.", length, size)
			}

			queue.close()

			length = queue.len()
			if length != size {
				t.Fatalf("Unexpected queue length: got %d, want %d.", length, size)
			}
		})
	}
}

func doesTimeout(duration time.Duration, fn func()) (timedOut bool) {
	done := make(chan struct{})
	timer := time.NewTimer(duration)

	go func() {
		fn()
		close(done)
	}()

	select {
	case <-done:
		return false
	case <-timer.C:
		return true
	}
}

func TestBlocking(t *testing.T) {
	t.Run("put", func(t *testing.T) {
		t.Parallel()

		queue := newFifo[string](2)

		if doesTimeout(time.Millisecond, func() { queue.put("a") }) {
			t.Fatal("Should not have waited to put element in queue.")
		}

		if doesTimeout(time.Millisecond, func() { queue.put("b") }) {
			t.Fatal("Should not have waited to put element in queue.")
		}

		if !doesTimeout(5*time.Millisecond, func() { queue.put("c") }) {
			t.Fatal("Should have waited to put element in queue.")
		}
	})

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		queue := newFifo[string](2)

		getFn := func(expected string) func() {
			t.Helper()

			return func() {
				v, ok := queue.get()
				if !ok {
					t.Fatalf("Queue unexpectedly closed.")
				}
				if v != expected {
					t.Fatalf("Unexpected value: got %q, want %q.", v, expected)
				}
			}
		}

		if !doesTimeout(5*time.Millisecond, getFn("_")) {
			t.Fatal("Should have waited to get element from queue.")
		}

		queue.put("_") // Release previous get

		time.Sleep(time.Millisecond) // Guarantee that previous get is completed

		queue.put("a")

		if doesTimeout(5*time.Millisecond, getFn("a")) {
			t.Fatal("Should not have waited to get element from queue.")
		}
	})

	t.Run("putNoWait", func(t *testing.T) {
		t.Parallel()

		queue := newFifo[string](2)

		putFn := func(v string, expected bool) func() {
			t.Helper()

			return func() {
				ok := queue.putNoWait(v)
				if ok != expected {
					if expected {
						t.Error("Should have inserted the value.")
					} else {
						t.Error("Should not have insert the value.")
					}
				}
			}
		}

		if doesTimeout(time.Millisecond, putFn("a", true)) {
			t.Fatal("Should not have waited to put element in queue.")
		}

		if doesTimeout(time.Millisecond, putFn("b", true)) {
			t.Fatal("Should not have waited to put element in queue.")
		}

		if doesTimeout(time.Millisecond, putFn("c", false)) {
			t.Fatal("Should not have waited after failed to put element in queue.")
		}
	})
}

func TestClose(t *testing.T) {
	t.Parallel()

	queue := newFifo[float64](2)

	queue.put(math.Pi)

	v, open := queue.get()
	if v != math.Pi {
		t.Fatal("Unexpected value from Get")
	}

	if !open {
		t.Fatal("Queue unexpectedly closed")
	}

	queue.close()

	_, open = queue.get()
	if open {
		t.Fatal("Queue should have been closed")
	}

	queue.put(4.9)

	length := queue.len()
	if length != 0 {
		t.Fatal("Element should not have been put in queue")
	}
}

// This test should be run with the -race flag.
func TestRacing(t *testing.T) {
	t.Parallel()

	var wg sync.WaitGroup

	queue := newFifo[time.Time](1000)

	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() { //nolint:wsl
			defer wg.Done()

			for i := 0; i < 1000; i++ {
				queue.put(time.Now())
			}
		}()
	}

	var totalErrs int32

	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() { //nolint:wsl
			defer wg.Done()

			var (
				prev time.Time
				errs int32
			)

			for i := 0; i < 1000; i++ {
				ts, ok := queue.get()
				if !ok {
					t.Error("Unexpectedly closed queue")

					return
				}

				if ts.Before(prev) {
					errs++
				}

				prev = ts
			}

			atomic.AddInt32(&totalErrs, errs)
		}()
	}

	wg.Wait()

	length := queue.len()
	if length != 0 {
		t.Fatalf("Queue expected to be empty, but has a length of %d.", length)
	}

	inaccuraciesPerc := float64(totalErrs) / 10000 * 100
	if inaccuraciesPerc > 1 {
		t.Fatalf("Unexacceptable amount of inaccuracies: %.2f%%\n", inaccuraciesPerc)
	}
}
