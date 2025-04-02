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
	"fmt"
	"math"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

func TestSize(t *testing.T) {
	t.Parallel()

	for size := 1; size < 1e6; size *= 10 {
		t.Run(fmt.Sprintf("%d sized fifo queue", size), func(t *testing.T) {
			queue := newFifo[int](size)
			ctx, cancel := context.WithCancel(t.Context())

			length := queue.Len()
			if length != 0 {
				cancel()
				t.Fatalf("Unexpected queue length: got %d, want 0.", length)
			}

			for i := range size - 1 {
				queue.Put(ctx, i)
			}

			length = queue.Len()
			if length != size-1 {
				cancel()
				t.Fatalf("Unexpected queue length: got %d, want %d.", length, size-1)
			}

			added := queue.PutNoWait(1)
			if !added {
				cancel()
				t.Fatalf("Should have added item n°%d in %d-sized queue.", size, size)
			}

			length = queue.Len()
			if length != size {
				cancel()
				t.Fatalf("Unexpected queue length: got %d, want %d.", length, size)
			}

			added = queue.PutNoWait(1)
			if added {
				cancel()
				t.Fatalf("Should not have added item n°%d in %d-sized queue.", size+1, size)
			}

			length = queue.Len()
			if length != size {
				cancel()
				t.Fatalf("Unexpected queue length: got %d, want %d.", length, size)
			}

			queue.Close()

			length = queue.Len()
			if length != size {
				cancel()
				t.Fatalf("Unexpected queue length: got %d, want %d.", length, size)
			}

			cancel()
		})
	}
}

func doesTimeout[T any](duration time.Duration, fn func(), queue *fifo[T]) (timedOut bool) {
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
		queue.Close()
		<-done

		return true
	}
}

func TestMethods(t *testing.T) {
	t.Run("Put", func(t *testing.T) {
		queue := newFifo[string](2)
		ctx, cancel := context.WithCancel(t.Context())

		if doesTimeout(time.Millisecond, func() { queue.Put(ctx, "a") }, queue) {
			cancel()
			t.Fatal("Should not have waited to put element in queue.")
		}

		if doesTimeout(time.Millisecond, func() { queue.Put(ctx, "b") }, queue) {
			cancel()
			t.Fatal("Should not have waited to put element in queue.")
		}

		if !doesTimeout(5*time.Millisecond, func() { queue.Put(ctx, "c") }, queue) {
			cancel()
			t.Fatal("Should have waited to put element in queue.")
		}

		cancel()
	})

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		queue := newFifo[string](2)
		ctx, cancel := context.WithCancel(t.Context())

		getFn := func(expectedValue string, expectedOk bool) func() {
			return func() {
				v, ok := queue.Get(ctx)
				if ok != expectedOk {
					t.Errorf("unexpected ok status: got %t, want %t", ok, expectedOk)
				}

				if v != expectedValue {
					t.Errorf("unexpected value: got \"%v\", want \"%v\"", v, expectedValue)
				}
			}
		}

		queue.Put(ctx, "a")

		if doesTimeout(5*time.Millisecond, getFn("a", true), queue) {
			cancel()
			t.Fatalf("Should not have waited to get element from queue.")
		}

		if !doesTimeout(5*time.Millisecond, getFn("", false), queue) {
			cancel()
			t.Fatalf("Should not have return without value or closing.")
		}

		cancel()
	})

	t.Run("PutNoWait", func(t *testing.T) {
		t.Parallel()

		queue := newFifo[string](2)

		putFn := func(v string, expected bool) func() {
			t.Helper()

			return func() {
				ok := queue.PutNoWait(v)
				if ok != expected {
					if expected {
						t.Error("Should have inserted the value.")
					} else {
						t.Error("Should not have insert the value.")
					}
				}
			}
		}

		if doesTimeout(time.Millisecond, putFn("a", true), queue) {
			t.Fatal("Should not have waited to put element in queue.")
		}

		if doesTimeout(time.Millisecond, putFn("b", true), queue) {
			t.Fatal("Should not have waited to put element in queue.")
		}

		if doesTimeout(time.Millisecond, putFn("c", false), queue) {
			t.Fatal("Should not have waited after failed to put element in queue.")
		}
	})
}

func TestClose(t *testing.T) {
	t.Parallel()

	t.Run("ThroughMethod", func(t *testing.T) {
		queue := newFifo[float64](2)
		ctx, cancel := context.WithCancel(t.Context())

		queue.Put(ctx, math.Pi)

		v, open := queue.Get(ctx)
		if v != math.Pi {
			cancel()
			t.Fatal("Unexpected value from Get")
		}

		if !open {
			cancel()
			t.Fatal("Queue unexpectedly closed")
		}

		queue.Close()

		_, open = queue.Get(ctx)
		if open {
			cancel()
			t.Fatal("Queue should have been closed")
		}

		queue.Put(ctx, 4.9)

		length := queue.Len()
		if length != 0 {
			cancel()
			t.Fatal("Element should not have been put in queue")
		}

		cancel()
	})

	t.Run("ThroughContext", func(t *testing.T) {
		queue := newFifo[float64](2)
		ctx, cancel := context.WithCancel(t.Context())

		queue.Put(ctx, math.Pi)

		v, open := queue.Get(ctx)
		if v != math.Pi {
			cancel()
			t.Fatal("Unexpected value from Get")
		}

		if !open {
			cancel()
			t.Fatal("Queue unexpectedly closed")
		}

		cancel()

		_, open = queue.Get(ctx)
		if open {
			t.Fatal("Should not be able to Get after context cancel")
		}

		queue.Put(ctx, 4.9)

		length := queue.Len()
		if length != 0 {
			t.Fatal("Element should not have been put in queue")
		}
	})
}

var errUnexpectedQueueStatus = errors.New("unexpected queue status")

// This test should be run with the -race flag.
func TestRacing(t *testing.T) {
	t.Parallel()

	t.Run("MultiWriter", func(t *testing.T) {
		queue := newFifo[time.Time](1000)
		ctx, cancel := context.WithCancel(t.Context())
		g, errCtx := errgroup.WithContext(ctx)

		for range 10 {
			g.Go(func() error {
				for range 1000 {
					queue.Put(errCtx, time.Now())
				}

				return nil
			})
		}

		g.Go(func() error {
			var prev time.Time

			for range 10000 {
				ts, ok := queue.Get(errCtx)
				if !ok {
					return errUnexpectedQueueStatus
				}

				if ts.Before(prev) {
					return fmt.Errorf("unordered values: got %v, expected more than %v", ts, prev) //nolint:err113
				}
			}

			return nil
		})

		if err := g.Wait(); err != nil {
			cancel()
			t.Fatalf("Error while interacting with fifo: %v", err)
		}

		length := queue.Len()
		if length != 0 {
			cancel()
			t.Fatalf("Queue expected to be empty, but has a length of %d.", length)
		}

		cancel()
	})

	t.Run("MultiReader", func(t *testing.T) {
		queue := newFifo[time.Time](1000)
		ctx, cancel := context.WithCancel(t.Context())
		g, errCtx := errgroup.WithContext(ctx)

		g.Go(func() error {
			for range 10000 {
				queue.Put(errCtx, time.Now())
			}

			return nil
		})

		for range 10 {
			g.Go(func() error {
				var prev time.Time

				for range 1000 {
					ts, ok := queue.Get(errCtx)
					if !ok {
						return errUnexpectedQueueStatus
					}

					if ts.Before(prev) {
						return fmt.Errorf("unordered values: got %v, expected more than %v", ts, prev) //nolint:err113
					}
				}

				return nil
			})
		}

		if err := g.Wait(); err != nil {
			cancel()
			t.Fatalf("Error while interacting with fifo: %v", err)
		}

		length := queue.Len()
		if length != 0 {
			cancel()
			t.Fatalf("Queue expected to be empty, but has a length of %d.", length)
		}

		cancel()
	})
}
