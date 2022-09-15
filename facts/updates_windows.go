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

package facts

import (
	"context"
	"glouton/logger"
	"os"
	"strconv"
	"strings"
	"time"
)

func (uf updateFacter) freshness() time.Time {
	return time.Time{}
}

func (uf updateFacter) pendingUpdates(ctx context.Context) (pendingUpdates int, pendingSecurityUpdates int) {
	pendingUpdates = -1
	pendingSecurityUpdates = -1

	// Glouton runs under the LocalService account, so we do not have enough permissions to call into
	// the COM object, instead we read a file that a scheduled task is updating periodically.
	content, err := os.ReadFile(`C:\ProgramData\glouton\windowsupdate.txt`)
	if err != nil {
		logger.V(2).Printf("Couldn't retrieve Windows Update information: %v", err)
	}

	splits := strings.Split(string(content), "\r\n")

	// get rid of the trailing newline, if any
	if splits[len(splits)-1] == "" {
		splits = splits[:len(splits)-1]
	}

	if len(splits) != 2 {
		logger.V(2).Printf("Couldn't retrieve Windows Update information: got %d values, expected 2 in %v", len(splits), splits)

		return
	}

	parsedPendingUpdates, err1 := strconv.Atoi(splits[0])
	parsedPendingSecurityUpdates, err2 := strconv.Atoi(splits[1])

	if err1 != nil || err2 != nil {
		logger.V(2).Printf("Couldn't retrieve Windows Update information: cannot parse the file with content %v", splits)

		return
	}

	return parsedPendingUpdates, parsedPendingSecurityUpdates
}
