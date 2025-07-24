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

package smart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	_ "unsafe" // using hack with go linkname to access private variable :)

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/influxdata/telegraf/config"
)

const maxBuckets = 6

type runCmdType func(timeout config.Duration, sudo bool, command string, args ...string) ([]byte, error)

//go:linkname runCmd github.com/influxdata/telegraf/plugins/inputs/smart.runCmd
var runCmd runCmdType //nolint:gochecknoglobals

type wrappedRunCmd struct {
	l                     sync.Mutex
	cond                  *sync.Cond
	runner                Runner
	maxConcurrency        int
	currentExecCount      int
	timeoutCount          int
	allExecCount          int
	buckets               map[string]bucketStats
	globalStats           bucketStats
	latestResultPerDevice map[string]string
	firstScanOutput       string
	latestScanOutput      string
}

type Runner interface {
	Run(ctx context.Context, option gloutonexec.Option, name string, arg ...string) ([]byte, error)
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

func SetupGlobalWrapper(runner *gloutonexec.Runner) {
	globalRunCmd.reset(runner)

	globalRunCmd.l.Lock()
	runCmd = globalRunCmd.runCmd
	globalRunCmd.l.Unlock()
}

func (w *wrappedRunCmd) reset(runner Runner) {
	w.l.Lock()
	defer w.l.Unlock()

	w.runner = runner
	w.maxConcurrency = 1
	w.cond = sync.NewCond(&w.l)
	w.buckets = make(map[string]bucketStats)
	w.globalStats = bucketStats{}
	w.latestResultPerDevice = make(map[string]string)
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
	runner := w.runner

	for w.currentExecCount >= w.maxConcurrency {
		w.cond.Wait()
	}

	w.currentExecCount++

	w.l.Unlock()

	execStart := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout))
	defer cancel()

	output, err := runner.Run(ctx, gloutonexec.Option{RunAsRoot: sudo, RunOnHost: true, CombinedOutput: true, GraceDelay: 5 * time.Second}, command, args...)
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

	if err != nil {
		// If the error is due to an exit code != 0,
		// we only want to stop if it is code 1;
		// which means that the command line didn't parse.
		// For more information, see the EXIT STATUS section
		// of the manpage smartctl(8).
		if isExitErr, isCode1 := isExitCode1(err); isExitErr && !isCode1 {
			err = nil
		}
	}

	return output, err
}

func (w *wrappedRunCmd) addStats(run smartExecution) {
	if len(run.args) > 0 && run.args[0] == "--scan" { //nolint: goconst,nolintlint
		if w.firstScanOutput == "" {
			w.firstScanOutput = run.output
		}

		w.latestScanOutput = run.output

		return // --scan executions aren't added to stats
	}

	w.globalStats.addStats(run)

	// one bucket per hour
	bucketKey := run.executionAt.Format("2006-01-02T15")

	bucket := w.buckets[bucketKey]

	bucket.addStats(run)

	w.buckets[bucketKey] = bucket

	if len(w.buckets) > maxBuckets {
		// drop oldest bucket
		smallestKey := ""
		for key := range w.buckets {
			if smallestKey == "" || key < smallestKey {
				smallestKey = key
			}
		}

		delete(w.buckets, smallestKey)
	}

	device, found := findDeviceInArgs(run.args)
	if found {
		w.latestResultPerDevice[device] = run.output
	} else {
		logger.V(2).Printf("Can't find device in smartctl args %+v", run.args)
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
		LatestResultPerDevice  map[string]string
		FirstScanOutput        string
		LatestScanOutput       string
	}{
		ExecutionWithScanCount: w.allExecCount,
		TimeoutCount:           w.timeoutCount,
		AllowedConcurrentExec:  w.maxConcurrency,
		CurrentExecRunning:     w.currentExecCount,
		GlobalStats:            w.globalStats,
		Buckets:                w.buckets,
		LatestResultPerDevice:  w.latestResultPerDevice,
		FirstScanOutput:        w.firstScanOutput,
		LatestScanOutput:       w.latestScanOutput,
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

func (b *bucketStats) MarshalJSON() ([]byte, error) {
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
		Error             string `json:",omitempty"`
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

// findDeviceInArgs	looks for the device identifier in the given gather command args.
// It also returns whether it was successfully retrieved or not.
//
// Note that the way we search for the device is somewhat fragile,
// as it only relies on the fact that "--format=brief" precedes it.
// (Though, this behavior is tested in TestFindDeviceInArgs.)
// The code where the arguments list if built up is (currently)
// located in the smart.Smart.gatherDisk() method.
func findDeviceInArgs(args []string) (string, bool) {
	// "--format=brief" is the latest arg before the device identifier.
	// We can't just take the last arg, because it may be a compound name, e.g., "/dev/nvme0 -d nvme".
	formatArgPos := slices.Index(args, "--format=brief")
	if formatArgPos < 0 {
		return "", false
	}

	device := strings.Join(args[formatArgPos+1:], " ")

	return device, device != ""
}

// testExitError represents an *exec.ExitError,
// since we can't instantiate the latter ourselves.
type testExitError uint8

func (tee testExitError) ExitCode() int {
	return int(tee)
}

func (tee testExitError) Error() string {
	return fmt.Sprintf("exit status %d", uint8(tee))
}

// isExitCode1 returns whether the given error is an exec.ExitError,
// and if so, whether its status code is 1 or not.
func isExitCode1(err error) (isExitErr, isCode1 bool) {
	var errExitCode interface {
		ExitCode() int
	}

	if errors.As(err, &errExitCode) {
		return true, errExitCode.ExitCode() == 1
	}

	return false, false
}
