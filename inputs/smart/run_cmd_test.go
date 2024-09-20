// Copyright 2015-2024 Bleemeo
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
	"math"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"
	_ "unsafe"

	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/version"

	"github.com/google/go-cmp/cmp"
	"github.com/influxdata/telegraf/config"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/smart"
)

var testUsingGlobalRunCmd sync.Mutex //nolint:gochecknoglobals

// TestAccessPrivateField make sure that our hack to hijack runCmd (private global of telegraf) works
// (our function is called) and don't cause build error / panic.
func TestAccessPrivateField(t *testing.T) {
	var (
		l         sync.Mutex
		callsArgs [][]string
	)

	fakeResponse := `smartctl 7.3 2022-02-28 r5338 [x86_64-linux-6.1.0-9-amd64] (local build)
Copyright (C) 2002-22, Bruce Allen, Christian Franke, www.smartmontools.org

=== START OF INFORMATION SECTION ===
Model Number:                       CT123456SSD8
Serial Number:                      123456789ABC
Firmware Version:                   1010200
PCI Vendor/Subsystem ID:            0x0007
IEEE OUI Identifier:                0x000042
Controller ID:                      1
NVMe Version:                       1.4
Number of Namespaces:               1
Namespace 1 Size/Capacity:          1,000,204,886,016 [1.00 TB]
Namespace 1 Formatted LBA Size:     512
Namespace 1 IEEE EUI-64:            123456 789abcdef0
Local Time is:                      Wed Jul  5 08:34:03 2023 UTC

=== START OF SMART DATA SECTION ===
SMART overall-health self-assessment test result: PASSED

SMART/Health Information (NVMe Log 0x02)
Critical Warning:                   0x00
Temperature:                        36 Celsius
Available Spare:                    100%
Available Spare Threshold:          5%
Percentage Used:                    0%
Data Units Read:                    47,484 [24.3 GB]
Data Units Written:                 66,576 [34.0 GB]
Host Read Commands:                 605,051
Host Write Commands:                1,713,726
Controller Busy Time:               16
Power Cycles:                       13
Power On Hours:                     1,690
Unsafe Shutdowns:                   10
Media and Data Integrity Errors:    0
Error Information Log Entries:      11
Warning  Comp. Temperature Time:    0
Critical Comp. Temperature Time:    0
Temperature Sensor 1:               36 Celsius
Temperature Sensor 2:               43 Celsius
Temperature Sensor 8:               36 Celsius
`

	runCmd := func(_ config.Duration, _ bool, _ string, args ...string) ([]byte, error) {
		l.Lock()
		defer l.Unlock()

		callsArgs = append(callsArgs, args)

		if args[0] == "--scan" {
			return []byte("/dev/nvme0 -d nvme # /dev/nvme0, NVMe device"), nil
		}

		if args[len(args)-1] == "/dev/nvme0" {
			return []byte(fakeResponse), nil
		}

		return nil, nil
	}

	testUsingGlobalRunCmd.Lock()
	defer testUsingGlobalRunCmd.Unlock()

	SetupGlobalWrapper()

	trueOriginalCmd := globalRunCmd.originalRunCmd
	globalRunCmd.originalRunCmd = runCmd

	defer func() {
		globalRunCmd.originalRunCmd = trueOriginalCmd
	}()

	input, ok := telegraf_inputs.Inputs["smart"]
	if !ok {
		t.Fatal("smart input not found in telegraf_inputs.Inputs")
	}

	smartInput, ok := input().(*smart.Smart)
	if !ok {
		t.Fatal("unexpected input type")
	}

	acc := &internal.StoreAccumulator{}
	_ = smartInput.Gather(acc)

	// The exact option doesn't really matter. Only that we get called.
	expectedCalls := [][]string{
		{"--scan"},
		{"--scan", "--device=nvme"},
		{"--info", "--health", "--attributes", "--tolerance=verypermissive", "-n", "standby", "--format=brief", "/dev/nvme0"},
	}

	if diff := cmp.Diff(expectedCalls, callsArgs); diff != "" {
		t.Errorf("calls mismatch (-want +got)\n%s", diff)
	}

	// We only check that we have measurement and just check one metric.
	if len(acc.Measurement) == 0 {
		t.Errorf("got no Measurement, want some")
	}

	var foundTempC bool

	for _, m := range acc.Measurement {
		if m.Name != "smart_device" {
			continue
		}

		tempC, ok := m.Fields["temp_c"].(int64)
		if !ok {
			t.Errorf("temp_c isn't a int64: %v (%T)", m.Fields["temp_c"], m.Fields["temp_c"])
		}

		if tempC != 36 {
			t.Errorf("temp_c = %d, want 36", tempC)
		}

		foundTempC = true
	}

	if !foundTempC {
		t.Error("temp_c not found in metrics")
	}
}

func TestLimitedConcurrentcy(t *testing.T) {
	var (
		l           sync.Mutex
		execution   int
		exceedLimit bool
		maxDuration time.Duration
	)

	const maxConcurrency = 5

	runCmd := func(_ config.Duration, _ bool, _ string, _ ...string) ([]byte, error) {
		l.Lock()
		execution++

		if execution > maxConcurrency {
			exceedLimit = true
		}
		l.Unlock()

		time.Sleep(time.Second)

		l.Lock()
		execution--
		l.Unlock()

		return nil, nil
	}

	wrapper := &wrappedRunCmd{}
	wrapper.reset(runCmd)
	wrapper.maxConcurrency = maxConcurrency

	for _, withWait := range []bool{false, true} {
		var wg sync.WaitGroup

		// The first test will only run maxConcurrency execution (so no wait), then the double (so with wait)
		runCmdCount := maxConcurrency
		if withWait {
			runCmdCount = 2 * maxConcurrency
		}

		// First test maxConcurrency call, all should be fast
		for range runCmdCount {
			wg.Add(1)

			go func() {
				defer wg.Done()

				start := time.Now()
				_, _ = wrapper.runCmd(config.Duration(0), false, "not used")

				duration := time.Since(start)

				l.Lock()
				defer l.Unlock()

				if duration > maxDuration {
					maxDuration = duration
				}
			}()
		}

		wg.Wait()

		wantedMaxDuration := 1.0
		if withWait {
			wantedMaxDuration = 2.0
		}

		if math.Abs(maxDuration.Seconds()-wantedMaxDuration) > 0.2 {
			t.Errorf("maxDuration = %s, want %f second", maxDuration, wantedMaxDuration)
		}

		if exceedLimit {
			t.Errorf("exceedLimit is true, too many concurrent execution happened")
		}
	}
}

func Test_addStats(t *testing.T) {
	wrapper := &wrappedRunCmd{}
	wrapper.reset(func(_ config.Duration, _ bool, _ string, _ ...string) ([]byte, error) {
		return nil, nil
	})

	currentTime := time.Now()

	const outputValue = "something in output"

	for range 5000 {
		currentTime = currentTime.Add(time.Minute)
		wrapper.addStats(smartExecution{
			args:              []string{"ok", "ok"},
			output:            outputValue,
			executionAt:       currentTime,
			executionDuration: time.Second,
			waitDuration:      time.Millisecond,
			errorStr:          "err",
		})
	}

	if len(wrapper.buckets) != maxBuckets {
		t.Errorf("len(wrapper.buckets) = %d, want %d", len(wrapper.buckets), maxBuckets)
	}

	if wrapper.globalStats.executionCount != 5000 {
		t.Errorf("globalStats.executionCount = %d, want 5000", wrapper.globalStats.executionCount)
	}

	if wrapper.globalStats.fastestExecution.output != outputValue {
		t.Error("globalStats.fastestExecution is unset")
	}

	if wrapper.globalStats.slowestExecution.output != outputValue {
		t.Error("globalStats.slowestExecution is unset")
	}

	if wrapper.globalStats.lastWithError.output != outputValue {
		t.Error("globalStats.lastWithError is unset")
	}
}

func TestIsExitCode1(t *testing.T) {
	if version.IsWindows() {
		t.Skip("This test only makes sense on Unix-like systems.")
	}

	t.Parallel()

	testCases := []struct {
		err         error
		expectCode1 bool
	}{
		{
			err:         nil, // successful command
			expectCode1: false,
		},
		{
			err:         exec.Command("false").Run(),
			expectCode1: true,
		},
		{
			err:         exec.Command("sh", "-c", "exit 8").Run(),
			expectCode1: false,
		},
	}

	for i, tc := range testCases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()

			if isCode1 := isExitCode1(tc.err); isCode1 != tc.expectCode1 {
				t.Errorf("isExitCode1(%q) = %t, want %t", tc.err, isCode1, tc.expectCode1)
			}
		})
	}
}
