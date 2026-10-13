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

package facts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"testing"

	"github.com/bleemeo/glouton/utils/gloutonexec"
)

func TestLinuxAutoUpgradeIsEnabled(t *testing.T) {
	t.Parallel()

	const (
		unitLine  = "glouton-auto-upgrade.timer enabled vendor-preset\n"
		timerLine = "Fri 2026-10-02 11:42:31 CEST 20h - - glouton-auto-upgrade.timer glouton-auto-upgrade.service\n"
	)

	// A real exit status 1, as returned by "systemctl list-unit-files" when nothing matches.
	exitErr := exec.CommandContext(context.Background(), "sh", "-c", "exit 1").Run()

	if _, ok := errors.AsType[*exec.ExitError](exitErr); !ok {
		t.Fatalf("expected an ExitError, got %v", exitErr)
	}

	errNoSystemctl := fmt.Errorf(`exec: "systemctl": %w`, exec.ErrNotFound)
	errBoom := io.ErrUnexpectedEOF

	tests := []struct {
		name         string
		unitFiles    []byte
		unitFilesErr error
		timers       []byte
		timersErr    error
		want         bool
		wantErr      error
		wantAnyErr   bool
	}{
		{name: "enabled", unitFiles: []byte(unitLine), timers: []byte(timerLine), want: true},
		{name: "unit present but disabled", unitFiles: []byte("glouton-auto-upgrade.timer disabled enabled\n"), timers: []byte("0 timers listed.\n")},
		{name: "unit absent", unitFilesErr: exitErr, wantErr: errAutoUpgradeNotSupported},
		{name: "unit absent, exit 0 and empty output", wantErr: errAutoUpgradeNotSupported},
		{name: "no systemctl", unitFilesErr: errNoSystemctl, wantErr: errAutoUpgradeNotSupported},
		{name: "container", unitFilesErr: gloutonexec.ErrExecutionSkipped, wantErr: errAutoUpgradeNotSupported},
		{name: "unexpected failure", unitFilesErr: errBoom, wantAnyErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			run := func(_ context.Context, _ gloutonexec.Option, _ string, args ...string) ([]byte, error) {
				if args[0] == "list-unit-files" {
					return tt.unitFiles, tt.unitFilesErr
				}

				return tt.timers, tt.timersErr
			}

			got, err := linuxAutoUpgradeIsEnabled(context.Background(), run)

			switch {
			case tt.wantAnyErr:
				if err == nil || errors.Is(err, errAutoUpgradeNotSupported) {
					t.Fatalf("expected a real error, got %v", err)
				}
			case !errors.Is(err, tt.wantErr):
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
