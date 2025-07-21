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

//go:build !race

package smart

import (
	"testing"
	"time"

	"github.com/bleemeo/glouton/utils/gloutonexec"
	"github.com/influxdata/telegraf/config"
)

// TestTimeoutDetection make sure that our hack detect timeout correctly.
// We can't run this test with -race, because telegraf code had a race-condition.
func TestTimeoutDetection(t *testing.T) {
	// all our test must use setMockRunCmd, or it's possible to use runCmd from another test
	testUsingGlobalRunCmd.Lock()
	defer testUsingGlobalRunCmd.Unlock()

	realRunner := gloutonexec.New("/")

	SetupGlobalWrapper(realRunner)

	wrapper := &wrappedRunCmd{}
	wrapper.reset(realRunner)

	_, err := wrapper.runCmd(config.Duration(50*time.Millisecond), false, "sleep", "30")
	if err == nil {
		t.Errorf("error is nil, want TimeoutError")
	}

	if wrapper.timeoutCount != 1 {
		t.Errorf("timeoutCount = %d, want 1", wrapper.timeoutCount)
	}
}
