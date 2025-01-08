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

	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/cenkalti/backoff/v4"
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
	go i.processWatcher(ctx)

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
		RunAsRoot: i.runAsRoot,
	}

	i.Logger().Debug("starting command", zap.Strings("argv", i.argv))

	i.stdout, i.stderr, i.cmdWait, err = i.commandRunner.StartWithPipes(ctx, runOpts, i.argv[0], i.argv[1:]...)
	if err != nil {
		return err
	}

	return nil
}

func (i *Input) processWatcher(ctx context.Context) {
	defer i.wg.Done()

	var wg sync.WaitGroup

	buffer := make([]byte, len(i.buffer))

	for ctx.Err() == nil {
		startTime := time.Now()

		wg.Add(1)

		go func() {
			defer wg.Done()
			i.processStdout(ctx)
		}()

		i.processStderr(buffer)

		processDuration := time.Since(startTime)
		if processDuration > time.Minute {
			i.backoff.Reset()
		}

		wg.Wait()

		i.stderr.Close()
		i.stdout.Close()

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
	scan := bufio.NewScanner(i.stderr)
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
	i.l.Lock()
	scan := bufio.NewScanner(i.stdout)
	i.l.Unlock()

	scan.Split(i.splitFunc)
	scan.Buffer(i.buffer, len(i.buffer))

	for scan.Scan() {
		line := scan.Bytes()
		if len(line) == 0 {
			continue
		}

		if err := i.sendEntry(ctx, line); err != nil {
			i.Logger().Error("failed to process exec input", zap.Error(err))
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
