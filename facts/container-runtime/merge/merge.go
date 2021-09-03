// Package merge will merge multiple container runtime.
package merge

import (
	"context"
	"errors"
	"glouton/facts"
	crTypes "glouton/facts/container-runtime/types"
	"glouton/logger"
	"glouton/types"
	"strings"
	"sync"
	"time"
)

var errNoRuntimeAvailable = errors.New("no container-runtime available")

// Runtime provide container-runtime which merge multiple container runtime.
// It assume that container ID are unique across all runtimes.
// Runtimes could NOT be changed after creation.
type Runtime struct {
	Runtimes []crTypes.RuntimeInterface

	l           sync.Mutex
	idToRuntime map[string]int
	notifyC     chan facts.ContainerEvent
}

func (r *Runtime) getRuntime(containerID string) crTypes.RuntimeInterface {
	r.l.Lock()
	defer r.l.Unlock()

	idx, ok := r.idToRuntime[containerID]

	if !ok || len(r.Runtimes) <= idx {
		return nil
	}

	return r.Runtimes[idx]
}

// LastUpdate return the most recent date of update.
func (r *Runtime) LastUpdate() time.Time {
	var max time.Time

	for _, cr := range r.Runtimes {
		t := cr.LastUpdate()
		if max.Before(t) {
			max = t
		}
	}

	return max
}

// CachedContainer call function on container runtimes.
func (r *Runtime) CachedContainer(containerID string) (c facts.Container, found bool) {
	cr := r.getRuntime(containerID)

	if cr == nil {
		logger.V(2).Printf("CachedContainer: can't route container %s to a container runtime", containerID)

		return nil, false
	}

	return cr.CachedContainer(containerID)
}

// ContainerLastKill call function on container runtimes.
func (r *Runtime) ContainerLastKill(containerID string) time.Time {
	cr := r.getRuntime(containerID)

	if cr == nil {
		logger.V(2).Printf("ContainerLastKill: can't route container %s to a container runtime", containerID)

		return time.Time{}
	}

	return cr.ContainerLastKill(containerID)
}

// Exec call function on container runtimes.
func (r *Runtime) Exec(ctx context.Context, containerID string, cmd []string) ([]byte, error) {
	cr := r.getRuntime(containerID)

	if cr == nil {
		logger.V(2).Printf("Exec: can't route container %s to a container runtime", containerID)

		return nil, errNoRuntimeAvailable
	}

	return cr.Exec(ctx, containerID, cmd)
}

// Containers call function on container runtimes.
func (r *Runtime) Containers(ctx context.Context, maxAge time.Duration, includeIgnored bool) (containers []facts.Container, globalErr error) {
	var errs MultiErrors

	idToRuntime := make(map[string]int)

	for i, cr := range r.Runtimes {
		list, err := cr.Containers(ctx, maxAge, true)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		for _, c := range list {
			idToRuntime[c.ID()] = i

			if includeIgnored || !facts.ContainerIgnored(c) {
				containers = append(containers, c)
			}
		}
	}

	if len(containers) == 0 {
		if errs != nil {
			return nil, errs
		}

		return nil, nil
	}

	r.l.Lock()

	r.idToRuntime = idToRuntime

	r.l.Unlock()

	return containers, nil
}

// Events call function on container runtimes.
func (r *Runtime) Events() <-chan facts.ContainerEvent {
	r.l.Lock()
	defer r.l.Unlock()

	if r.notifyC == nil {
		r.notifyC = make(chan facts.ContainerEvent)
	}

	return r.notifyC
}

// IsRuntimeRunning call function on container runtimes.
func (r *Runtime) IsRuntimeRunning(ctx context.Context) bool {
	atLeastOne := false

	for i, cr := range r.Runtimes {
		ok := cr.IsRuntimeRunning(ctx)

		logger.V(2).Printf("IsRuntimeRunning: runtime %d: isRunning=%v", i, ok)

		if ok {
			atLeastOne = ok
		}
	}

	return atLeastOne
}

// ProcessWithCache call function on container runtimes.
func (r *Runtime) ProcessWithCache() facts.ContainerRuntimeProcessQuerier {
	queriers := make([]facts.ContainerRuntimeProcessQuerier, len(r.Runtimes))

	for i, cr := range r.Runtimes {
		queriers[i] = cr.ProcessWithCache()
	}

	return mergeProcessQuerier{
		r:        r,
		queriers: queriers,
	}
}

// Run call function on container runtimes.
func (r *Runtime) Run(ctx context.Context) error {
	var (
		wg        sync.WaitGroup
		l         sync.Mutex
		globalErr error
	)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// ensure r.notifyC is created
	r.Events()

	wg.Add(len(r.Runtimes) * 2)

	for i, cr := range r.Runtimes {
		i := i
		cr := cr

		go func() {
			defer wg.Done()

			err := cr.Run(ctx)

			l.Lock()

			if err != nil && globalErr == nil {
				cancel()

				globalErr = err
			}

			l.Unlock()
		}()

		go func() {
			defer wg.Done()

			ch := cr.Events()

			for ctx.Err() == nil {
				if ch == nil {
					select {
					case <-ctx.Done():
					case <-time.After(10 * time.Second):
						ch = cr.Events()
					}

					continue
				}

				select {
				case ev, ok := <-ch:
					if !ok {
						ch = nil

						continue
					}

					r.l.Lock()

					if r.idToRuntime == nil {
						r.idToRuntime = make(map[string]int)

						r.idToRuntime[ev.ContainerID] = i
					}

					r.l.Unlock()

					select {
					case r.notifyC <- ev:
					case <-ctx.Done():
					}
				case <-ctx.Done():
				}
			}
		}()
	}

	wg.Wait()

	return globalErr
}

// RuntimeFact call function on container runtimes.
func (r *Runtime) RuntimeFact(ctx context.Context, currentFact map[string]string) map[string]string {
	runtimes := make([]string, 0)
	newFacts := make(map[string]string)

	for _, cr := range r.Runtimes {
		facts := cr.RuntimeFact(ctx, currentFact)

		if name := facts["container_runtime"]; name != "" {
			runtimes = append(runtimes, name)
		}

		for k, v := range facts {
			newFacts[k] = v
		}
	}

	if len(runtimes) > 0 {
		newFacts["container_runtime"] = strings.Join(runtimes, ",")
	}

	return newFacts
}

func (r *Runtime) Metrics(ctx context.Context) ([]types.MetricPoint, error) {
	points := make([]types.MetricPoint, 0)

	var errors MultiErrors

	for _, runtime := range r.Runtimes {
		runtimePoints, err := runtime.Metrics(ctx)
		if err != nil {
			errors = append(errors, err)
		}

		points = append(points, runtimePoints...)
	}

	return points, errors
}

type mergeProcessQuerier struct {
	r        *Runtime
	queriers []facts.ContainerRuntimeProcessQuerier
}

func (m mergeProcessQuerier) Processes(ctx context.Context) (result []facts.Process, err error) {
	pids := make(map[int]bool)

	for _, q := range m.queriers {
		procs, err := q.Processes(ctx)
		if err != nil {
			return nil, err
		}

		for _, p := range procs {
			if !pids[p.PID] {
				result = append(result, p)
				pids[p.PID] = true
			}
		}
	}

	return result, nil
}

func (m mergeProcessQuerier) ContainerFromCGroup(ctx context.Context, cgroupData string) (facts.Container, error) {
	var errs MultiErrors

	for i, q := range m.queriers {
		cont, err := q.ContainerFromCGroup(ctx, cgroupData)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		if cont != nil {
			m.r.l.Lock()
			defer m.r.l.Unlock()

			if m.r.idToRuntime == nil {
				m.r.idToRuntime = make(map[string]int)
			}

			m.r.idToRuntime[cont.ID()] = i

			return cont, nil
		}
	}

	if errs != nil {
		return nil, errs
	}

	return nil, nil
}

func (m mergeProcessQuerier) ContainerFromPID(ctx context.Context, parentContainerID string, pid int) (facts.Container, error) {
	var errs MultiErrors

	for i, q := range m.queriers {
		cont, err := q.ContainerFromPID(ctx, parentContainerID, pid)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		if cont != nil {
			m.r.l.Lock()
			defer m.r.l.Unlock()

			if m.r.idToRuntime == nil {
				m.r.idToRuntime = make(map[string]int)
			}

			m.r.idToRuntime[cont.ID()] = i

			return cont, nil
		}
	}

	if errs != nil {
		return nil, errs
	}

	return nil, nil
}

// MultiErrors is a type containing multiple errors. It implements the error interface.
type MultiErrors []error

func (errs MultiErrors) Error() string {
	list := make([]string, len(errs))

	for i, err := range errs {
		list[i] = err.Error()
	}

	return strings.Join(list, ", ")
}

func (errs MultiErrors) Is(target error) bool {
	for _, err := range errs {
		if errors.Is(err, target) {
			return true
		}
	}

	return false
}
