package task

import (
	"agentgo/logger"
	"context"
	"errors"
	"sync"
)

// Runner is something that can be Run
type Runner interface {
	Run(context.Context) error
}

// RunCloser is something that can be Run and Close(d)
type RunCloser interface {
	Runner
	Close() error
}

// Registry contains running tasks. It allow to add/remove tasks
type Registry struct {
	ctx    context.Context
	cancel func()
	tasks  map[int]*taskInfo
	closed bool
	l      sync.Mutex
}

type taskInfo struct {
	Runner     Runner
	Name       string
	CancelFunc func()
	Running    bool
}

// NewRegistry create a new registry. All task running in this registry will terminate when ctx is cancelled
func NewRegistry(ctx context.Context) *Registry {
	subCtx, cancel := context.WithCancel(ctx)
	return &Registry{
		ctx:    subCtx,
		cancel: cancel,
		tasks:  make(map[int]*taskInfo),
	}
}

// Close stops and wait for all currently running tasks
func (r *Registry) Close() {
	r.close()
	r.cancel()
	for k := range r.tasks {
		r.removeTask(k, true)
	}
	r.l.Lock()
	defer r.l.Unlock()
	r.tasks = make(map[int]*taskInfo)
}

func (r *Registry) close() {
	r.l.Lock()
	defer r.l.Unlock()
	r.closed = true
}

// AddTask add and start a new task. It return an taskID that could be used in RemoveTask
func (r *Registry) AddTask(task Runner, shortName string) (int, error) {
	r.l.Lock()
	defer r.l.Unlock()

	if r.closed {
		return 0, errors.New("registry already closed")
	}

	id := 1
	_, ok := r.tasks[id]
	for ok {
		id++
		if id == 0 {
			panic("too many tasks in the registry. Unable to find new slot")
		}
		_, ok = r.tasks[id]
	}

	ctx, cancel := context.WithCancel(r.ctx)
	waitC := make(chan interface{})
	cancelWait := func() {
		cancel()
		<-waitC
	}
	go func() {
		defer close(waitC)
		err := task.Run(ctx)
		if err != nil {
			logger.Printf("Task %#v failed: %v", shortName, err)
		}
		r.l.Lock()
		defer r.l.Unlock()
		r.tasks[id].Running = false
	}()

	r.tasks[id] = &taskInfo{
		CancelFunc: cancelWait,
		Runner:     task,
		Name:       shortName,
		Running:    true,
	}

	return id, nil
}

// RemoveTask stop (and potentially close) and remove given task
func (r *Registry) RemoveTask(taskID int) {
	r.l.Lock()
	defer r.l.Unlock()
	if r.closed {
		return
	}
	r.removeTask(taskID, false)
}

// IsRunning return true if the taskID is still running.
func (r *Registry) IsRunning(taskID int) bool {
	r.l.Lock()
	defer r.l.Unlock()
	return r.tasks[taskID].Running
}

func (r *Registry) removeTask(taskID int, forClosing bool) {
	if task, ok := r.tasks[taskID]; ok {
		task.CancelFunc()
		if closer, ok := task.Runner.(RunCloser); ok {
			if err := closer.Close(); err != nil {
				logger.V(1).Printf("Failed to close task %#v: %v", task.Name, err)
			}
		}
	} else {
		logger.V(2).Printf("called RemoveTask with unexisting ID %d", taskID)
	}

	if !forClosing {
		delete(r.tasks, taskID)
	}
}
