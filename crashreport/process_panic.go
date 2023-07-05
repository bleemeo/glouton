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

package crashreport

import (
	"context"
	"fmt"
	"glouton/logger"
	"glouton/utils/archivewriter"
	"os"
	"path/filepath"
	"time"

	"github.com/getsentry/sentry-go"
)

// ProcessPanic logs panics to Sentry.
// It should be deferred at the beginning of every new goroutine.
func ProcessPanic() {
	if err := recover(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Glouton crashed at", time.Now().Format(time.RFC3339))

		sentry.CurrentHub().Recover(err)
		sentry.Flush(time.Second * 5)

		tryToGenerateDiagnostic(time.Second * 10)

		logger.Printf("Glouton crashed: %v", err)

		panic(err)
	}
}

func tryToGenerateDiagnostic(timeout time.Duration) {
	lock.Lock()
	stateDir := dir
	diagnosticFn := diagnostic
	lock.Unlock()

	if diagnosticFn == nil {
		return // No diagnostic generation will be possible.
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	diagnosticPath := filepath.Join(stateDir, panicDiagnosticArchive)

	diagnosticArchive, err := os.Create(diagnosticPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to create diagnostic archive:", err)

		return
	}

	tarWriter := archivewriter.NewTarWriter(diagnosticArchive)
	defer tarWriter.Close()

	err = generateDiagnostic(ctx, tarWriter, diagnosticFn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to generate diagnostic archive:", err)

		return
	}
}
