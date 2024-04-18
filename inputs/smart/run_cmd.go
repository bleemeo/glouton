// Copyright 2015-2023 Bleemeo
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

package smart

import (
	"context"
	"encoding/json"
	"github.com/bleemeo/glouton/types"
	"strings"
	"sync"
	"time"
	_ "unsafe" // using hack with go linkname to access private variable :)

	"github.com/influxdata/telegraf/config"
)

const maxBuckets = 6

type runCmdType func(timeout config.Duration, sudo bool, command string, args ...string) ([]byte, error)

//go:linkname runCmd github.com/influxdata/telegraf/plugins/inputs/smart.runCmd
var runCmd runCmdType //nolint:gochecknoglobals

type wrappedRunCmd struct {
	l                sync.Mutex
	cond             *sync.Cond
	originalRunCmd   runCmdType
	maxConcurrency   int
	currentExecCount int
	timeoutCount     int
	allExecCount     int
	buckets          map[string]bucketStats
	globalStats      bucketStats
}

type bucketStats struct {
	maxWaitDuration        time.Duration
	executionCount         int
	totalExecutionDuration time.Duration
	maxExecutionDuration   time.Duration
	fastestExecution       smartExecution
	slowestExecution       smartExecution
	lastWithError          smartExecution
}

type smartExecution struct {
	args              []string
	output            string
	executionAt       time.Time
	executionDuration time.Duration
	waitDuration      time.Duration
	errorStr          string
}

var globalRunCmd wrappedRunCmd //nolint:gochecknoglobals

func SetupGlobalWrapper() {
	globalRunCmd.l.Lock()
	cmd := globalRunCmd.originalRunCmd
	globalRunCmd.l.Unlock()

	if cmd == nil {
		cmd = runCmd
	}

	globalRunCmd.reset(cmd)

	globalRunCmd.l.Lock()
	runCmd = globalRunCmd.runCmd
	globalRunCmd.l.Unlock()
}

func (w *wrappedRunCmd) reset(cmd runCmdType) {
	w.l.Lock()
	defer w.l.Unlock()

	w.originalRunCmd = cmd
	w.maxConcurrency = 1
	w.cond = sync.NewCond(&w.l)
	w.buckets = make(map[string]bucketStats)
	w.globalStats = bucketStats{}
}

func (w *wrappedRunCmd) SetConcurrency(maxConcurrency int) {
	w.l.Lock()
	defer w.l.Unlock()

	if maxConcurrency <= 0 {
		// maxConcurrency must be at least 1
		maxConcurrency = 1
	}

	w.maxConcurrency = maxConcurrency
	w.cond.Broadcast()
}

func (w *wrappedRunCmd) runCmd(timeout config.Duration, sudo bool, command string, args ...string) ([]byte, error) {
	start := time.Now()

	w.l.Lock()
	originalRunCmd := w.originalRunCmd

	for w.currentExecCount >= w.maxConcurrency {
		w.cond.Wait()
	}

	w.currentExecCount++

	w.l.Unlock()

	execStart := time.Now()
	output, err := originalRunCmd(timeout, sudo, command, args...)
	end := time.Now()

	w.l.Lock()
	defer w.l.Unlock()

	if err != nil && strings.Contains(err.Error(), "timed out") {
		w.timeoutCount++
	}

	w.allExecCount++
	w.currentExecCount--

	w.cond.Signal()

	errStr := ""
	if err != nil {
		errStr = err.Error()
	}

	w.addStats(smartExecution{
		args:              args,
		output:            string(output),
		executionAt:       execStart,
		executionDuration: end.Sub(execStart),
		waitDuration:      execStart.Sub(start),
		errorStr:          errStr,
	})

	return output, err
}

func (w *wrappedRunCmd) addStats(run smartExecution) {
	// --scan execution are not added to stats
	if len(run.args) > 0 && run.args[0] == "--scan" { //nolint: goconst
		return
	}

	w.globalStats.addStats(run)

	// one bucket per hour
	bucketKey := run.executionAt.Format("2006-01-02T15")

	bucket := w.buckets[bucketKey]

	bucket.addStats(run)

	w.buckets[bucketKey] = bucket

	if len(w.buckets) > maxBuckets {
		// drop olest bucket
		smallestKey := ""
		for key := range w.buckets {
			if smallestKey == "" || key < smallestKey {
				smallestKey = key
			}
		}

		delete(w.buckets, smallestKey)
	}
}

func (w *wrappedRunCmd) diagnosticArchive(_ context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("input-smart.json")
	if err != nil {
		return err
	}

	w.l.Lock()
	defer w.l.Unlock()

	state := struct {
		ExecutionWithScanCount int
		TimeoutCount           int
		AllowedConcurrentExec  int
		CurrentExecRunning     int
		GlobalStats            bucketStats
		Buckets                map[string]bucketStats
	}{
		ExecutionWithScanCount: w.allExecCount,
		TimeoutCount:           w.timeoutCount,
		AllowedConcurrentExec:  w.maxConcurrency,
		CurrentExecRunning:     w.currentExecCount,
		GlobalStats:            w.globalStats,
		Buckets:                w.buckets,
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(state)
}

func (b *bucketStats) addStats(run smartExecution) {
	b.executionCount++

	b.totalExecutionDuration += run.executionDuration

	if b.maxWaitDuration < run.waitDuration {
		b.maxWaitDuration = run.waitDuration
	}

	if b.maxExecutionDuration < run.executionDuration {
		b.maxExecutionDuration = run.executionDuration
	}

	if run.executionDuration > b.slowestExecution.executionDuration {
		b.slowestExecution = run
	}

	if run.executionDuration < b.fastestExecution.executionDuration || b.fastestExecution.executionDuration == 0 {
		b.fastestExecution = run
	}

	if run.errorStr != "" {
		b.lastWithError = run
	}
}

func (b bucketStats) MarshalJSON() ([]byte, error) {
	state := struct {
		ExecutionCount         int
		TotalExecutionDuration string
		MaxWaitDuration        string
		MaxExecutionDuration   string
		FastestExecution       smartExecution
		SlowestExecution       smartExecution
		LastWithError          smartExecution
	}{
		ExecutionCount:         b.executionCount,
		TotalExecutionDuration: b.totalExecutionDuration.String(),
		MaxWaitDuration:        b.maxWaitDuration.String(),
		MaxExecutionDuration:   b.maxExecutionDuration.String(),
		FastestExecution:       b.fastestExecution,
		SlowestExecution:       b.slowestExecution,
		LastWithError:          b.lastWithError,
	}

	return json.Marshal(state)
}

func (e smartExecution) MarshalJSON() ([]byte, error) {
	state := struct {
		ExecutionAt       string
		WaitDuration      string
		ExecutionDuration string
		Args              string
		Error             string
		Output            string
	}{
		ExecutionAt:       e.executionAt.Format(time.RFC3339Nano),
		WaitDuration:      e.waitDuration.String(),
		ExecutionDuration: e.executionDuration.String(),
		Args:              strings.Join(e.args, " "),
		Error:             e.errorStr,
		Output:            e.output,
	}

	return json.Marshal(state)
}
