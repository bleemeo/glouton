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

package logger

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestSlog(t *testing.T) {
	// No parallel for this test since we modify the logger config
	logBuf := new(bytes.Buffer)

	err := setLogger(func() error {
		cfg.writer = logBuf
		cfg.level = 2

		return nil
	})
	if err != nil {
		t.Fatal("Failed to set logger:", err)
	}

	slogger := NewSlog()

	slogger.Debug("ignored")
	slogger.Info("info msg")
	slogger.Warn("warn msg", "n", 7)
	slogger.WithGroup("grp").Error("error msg", "err", io.ErrUnexpectedEOF)

	result := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	// Removing timestamps from the start of the lines so they're easier to compare
	for i, line := range result {
		_, err := time.Parse(timeLayout, line[:len(timeLayout)])
		if err != nil {
			t.Fatalf("Failed to parse timestamp in line %q: %v", i, err)
		}

		result[i] = line[len(timeLayout):]
	}

	expectedWithoutTimestamps := []string{
		" info msg",
		" warn msg n=7",
		" error msg grp.err=unexpected EOF",
	}

	if diff := cmp.Diff(expectedWithoutTimestamps, result); diff != "" {
		t.Fatalf("Unexpected logs (-want +got):\n%s", diff)
	}
}
