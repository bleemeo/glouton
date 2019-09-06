// Copyright 2015-2019 Bleemeo
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

package task

import (
	"agentgo/logger"
	"context"
	"errors"
	"sync"
)

// Runner is something that can be Run
type Runner func(context.Context) error

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

	l       sync.Mutex
	Running bool
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
	ti := &taskInfo{
		CancelFunc: cancelWait,
		Runner:     task,
		Name:       shortName,
		Running:    true,
	}
	go func() {
		defer close(waitC)
		err := task(ctx)
		if err != nil {
			logger.Printf("Task %#v failed: %v", shortName, err)
		}
		ti.l.Lock()
		defer ti.l.Unlock()
		ti.Running = false
	}()

	r.tasks[id] = ti

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
	task, ok := r.tasks[taskID]
	if !ok {
		return false
	}
	task.l.Lock()
	defer task.l.Unlock()
	return task.Running
}

func (r *Registry) removeTask(taskID int, forClosing bool) {
	if task, ok := r.tasks[taskID]; ok {
		task.CancelFunc()
	} else {
		logger.V(2).Printf("called RemoveTask with unexisting ID %d", taskID)
	}

	if !forClosing {
		delete(r.tasks, taskID)
	}
}
