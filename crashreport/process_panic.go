package crashreport

import (
	"context"
	"fmt"
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
		sentry.CurrentHub().Recover(err)
		sentry.Flush(time.Second * 5)

		tryToGenerateDiagnostic(time.Second * 10)

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
