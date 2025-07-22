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

package execlog

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/cenkalti/backoff/v5"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/helper"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/trim"
	"go.uber.org/zap"
)

type Input struct {
	helper.InputOperator

	commandRunner Runner
	runAsRoot     bool
	buffer        []byte
	argv          []string
	splitFunc     bufio.SplitFunc
	trimFunc      trim.Func
	backoff       backoff.BackOff
	cancel        context.CancelFunc
	wg            sync.WaitGroup

	l       sync.Mutex
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	cmdWait func() error
}

func (i *Input) Start(_ operator.Persister) error {
	ctx, cancel := context.WithCancel(context.Background())
	i.cancel = cancel

	if err := i.startProcess(ctx); err != nil {
		return err
	}

	i.wg.Add(1)

	go func() {
		defer crashreport.ProcessPanic()
		defer i.wg.Done()

		i.processWatcher(ctx)
	}()

	return nil
}

func (i *Input) Stop() error {
	if i.cancel != nil {
		i.cancel()
	}

	i.wg.Wait()

	return nil
}

func (i *Input) startProcess(ctx context.Context) error {
	var err error

	i.l.Lock()
	defer i.l.Unlock()

	runOpts := gloutonexec.Option{
		RunAsRoot:  i.runAsRoot,
		GraceDelay: 1 * time.Second,
	}

	i.Logger().Debug("starting command", zap.Strings("argv", i.argv))

	i.stdout, i.stderr, i.cmdWait, err = i.commandRunner.StartWithPipes(ctx, runOpts, i.argv[0], i.argv[1:]...)
	if err != nil {
		return err
	}

	return nil
}

func (i *Input) processWatcher(ctx context.Context) {
	// Allowing a small buffer to avoid getting the goroutines stuck on writing to the channel if ctx.Done occurs before.
	stdoutDone := make(chan struct{}, 1)
	stderrDone := make(chan struct{}, 1)

	buffer := make([]byte, len(i.buffer))

	for ctx.Err() == nil {
		startTime := time.Now()

		// We need to start both i.processStdout and i.processStderr in new goroutines,
		// because they can be stuck indefinitely scanning the pipe.
		// The only way to make them quit is to close the pipe,
		// which obviously can't be done from the same goroutine.
		go func() {
			defer crashreport.ProcessPanic()

			i.processStdout(ctx)

			stdoutDone <- struct{}{}
		}()

		go func() {
			defer crashreport.ProcessPanic()

			i.processStderr(buffer)

			stderrDone <- struct{}{}
		}()

		waitForBoth(ctx, stdoutDone, stderrDone)

		processDuration := time.Since(startTime)
		if processDuration > time.Minute {
			i.backoff.Reset()
		}

		_ = i.stderr.Close()
		_ = i.stdout.Close()

		next := i.backoff.NextBackOff()

		select {
		case <-ctx.Done():
			return
		case <-time.After(next):
		}

		if err := i.startProcess(ctx); err != nil {
			i.Logger().Warn("startProcess failed", zap.Error(err))
		}
	}
}

func (i *Input) processStderr(buffer []byte) {
	i.l.Lock()
	scan := bufio.NewScanner(i.stderr)
	i.l.Unlock()

	scan.Split(i.splitFunc)
	scan.Buffer(buffer, len(buffer))

	for scan.Scan() {
		line := scan.Text()
		if len(line) == 0 {
			continue
		}

		i.Logger().Warn("process send stderr message", zap.String("stderr", line))
	}

	if err := scan.Err(); err != nil {
		i.Logger().Debug("reading stderr failed", zap.Error(err))
	}

	err := i.cmdWait()
	i.Logger().Debug("process terminated", zap.Error(err))
}

func (i *Input) processStdout(ctx context.Context) {
	scan := bufio.NewScanner(i.stdout)
	scan.Split(i.splitFunc)
	scan.Buffer(i.buffer, len(i.buffer))

	for scan.Scan() {
		line := scan.Bytes()
		if len(line) == 0 {
			continue
		}

		if err := i.sendEntry(ctx, line); err != nil {
			i.Logger().Error("failed to send exec entry", zap.Error(err))
		}
	}

	if err := scan.Err(); err != nil {
		i.Logger().Error("failed to process exec input", zap.Error(err))
	}
}

// sendEntry sends an entry to the next operator in the pipeline.
func (i *Input) sendEntry(ctx context.Context, bytes []byte) error {
	bytes = i.trimFunc(bytes)
	if len(bytes) == 0 {
		return nil
	}

	entry, err := i.NewEntry(string(bytes))
	if err != nil {
		return fmt.Errorf("failed to create entry: %w", err)
	}

	return i.Write(ctx, entry)
}

// waitForBoth blocks until both c1 and c2 send values, or ctx expires.
func waitForBoth(ctx context.Context, c1, c2 <-chan struct{}) {
	var total int8

	for {
		select {
		case <-ctx.Done():
			return
		case <-c1:
			total++
		case <-c2:
			total++
		}

		if total >= 2 {
			return
		}
	}
}
