package crashreport

import (
	"context"
	"glouton/logger"
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
	if diagnostic == nil {
		return // No diagnostic generation will be possible.
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	diagnosticPath := filepath.Join(stateDir, panicDiagnosticArchive)

	diagnosticArchive, err := os.Create(diagnosticPath)
	if err != nil {
		logger.V(1).Println("Failed to create diagnostic archive:", err)

		return
	}

	tarWriter := newTarWriter(diagnosticArchive)

	err = generateDiagnostic(ctx, tarWriter)
	if err != nil {
		logger.V(1).Println("Failed to generate diagnostic archive:", err)

		return
	}

	logger.V(1).Println("Generated diagnostic at", diagnosticPath)
}
