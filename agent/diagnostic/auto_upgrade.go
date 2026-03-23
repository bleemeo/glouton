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

package diagnostic

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"
	"github.com/bleemeo/glouton/version"
)

type DiagnosticAutoUpgrade struct {
	runner *gloutonexec.Runner
}

func NewDiagnosticAutoUpgrade(runner *gloutonexec.Runner) DiagnosticAutoUpgrade {
	return DiagnosticAutoUpgrade{
		runner: runner,
	}
}

func (d DiagnosticAutoUpgrade) DiagnosticAutoupgrade(ctx context.Context, archive types.ArchiveWriter) error {
	if version.IsWindows() {
		return d.diagnosticAutoupgradeWindows(ctx, archive)
	}

	if version.IsFreeBSD() {
		return d.diagnosticAutoupgradeFreeBSD(ctx, archive)
	}

	if version.IsLinux() {
		return d.diagnosticAutoupgradeLinux(ctx, archive)
	}

	return nil
}

func (d DiagnosticAutoUpgrade) diagnosticAutoupgradeLinux(ctx context.Context, archive types.ArchiveWriter) error {
	if err := d.diagnosticAutoupgradeLinuxJournalctl(ctx, archive); err != nil {
		return err
	}

	if err := d.diagnosticAutoupgradeLinuxShowTimer(ctx, archive); err != nil {
		return err
	}

	return nil
}

func (d DiagnosticAutoUpgrade) diagnosticAutoupgradeLinuxJournalctl(ctx context.Context, archive types.ArchiveWriter) error {
	out, cmdErr := d.runner.Run(ctx, gloutonexec.Option{SkipInContainer: true}, "journalctl", "-u", "glouton-auto-upgrade", "--since", "24 hours ago")
	if cmdErr != nil && errors.Is(cmdErr, gloutonexec.ErrExecutionSkipped) {
		// The auto upgrade is not supported on containers, skip producing the diagnostic file
		return nil
	}

	file, err := archive.Create("auto-upgrade-troubleshooting/journalctl.txt")
	if err != nil {
		return err
	}

	if cmdErr != nil {
		fmt.Fprintf(
			file,
			"Unable to get glouton-auto-upgrade logs: %s\n", cmdErr.Error(),
		)

		return nil
	}

	_, err = file.Write(out)
	if err != nil {
		return err
	}

	return nil
}

func (d DiagnosticAutoUpgrade) diagnosticAutoupgradeLinuxShowTimer(ctx context.Context, archive types.ArchiveWriter) error {
	out, cmdErr := d.runner.Run(ctx, gloutonexec.Option{SkipInContainer: true}, "systemctl", "list-timers", "glouton-auto-upgrade.timer")
	if cmdErr != nil && errors.Is(cmdErr, gloutonexec.ErrExecutionSkipped) {
		// The auto upgrade is not supported on containers, skip producing the diagnostic file
		return nil
	}

	file, err := archive.Create("auto-upgrade-troubleshooting/list-timers.txt")
	if err != nil {
		return err
	}

	if cmdErr != nil {
		fmt.Fprintf(
			file,
			"Unable to list-timers: %s\n", cmdErr.Error(),
		)

		return nil
	}

	_, err = file.Write(out)
	if err != nil {
		return err
	}

	return nil
}

func (d DiagnosticAutoUpgrade) diagnosticAutoupgradeWindows(ctx context.Context, archive types.ArchiveWriter) error {
	_ = ctx

	if err := d.diagnosticFileContent(archive, "auto-upgrade-troubleshooting/auto_update-marker.txt", `C:\ProgramData\glouton\auto_update`); err != nil {
		return err
	}

	if err := d.diagnosticFileContent(archive, "auto-upgrade-troubleshooting/auto_update.txt", `C:\ProgramData\glouton\auto_update.txt`); err != nil {
		return err
	}

	if err := d.diagnosticFileContent(archive, "auto-upgrade-troubleshooting/msiexec-log.txt", `C:\ProgramData\glouton\msiexec-log.txt`); err != nil {
		return err
	}

	if err := d.diagnosticAutoupgradeWindowsShowTimer(ctx, archive); err != nil {
		return err
	}

	return nil
}

func (d DiagnosticAutoUpgrade) diagnosticAutoupgradeWindowsShowTimer(ctx context.Context, archive types.ArchiveWriter) error {
	out, cmdErr := d.runner.Run(ctx, gloutonexec.Option{SkipInContainer: true}, "schtasks", "/Query", "/TN", `Bleemeo\Glouton\Auto Update`, "/V", "/FO", "list")
	if cmdErr != nil && errors.Is(cmdErr, gloutonexec.ErrExecutionSkipped) {
		// The auto upgrade is not supported on containers, skip producing the diagnostic file
		return nil
	}

	file, err := archive.Create("auto-upgrade-troubleshooting/list-timers.txt")
	if err != nil {
		return err
	}

	if cmdErr != nil {
		fmt.Fprintf(
			file,
			"Unable to list-timers: %s\n", cmdErr.Error(),
		)

		return nil
	}

	_, err = file.Write(out)
	if err != nil {
		return err
	}

	return nil
}

func (d DiagnosticAutoUpgrade) diagnosticAutoupgradeFreeBSD(ctx context.Context, archive types.ArchiveWriter) error {
	_ = ctx

	if err := d.diagnosticFileContent(archive, "auto-upgrade-troubleshooting/auto-upgrade.txt", "/var/lib/glouton/auto-upgrade.log"); err != nil {
		return err
	}

	return nil
}

func (d DiagnosticAutoUpgrade) diagnosticFileContent(archive types.ArchiveWriter, archiveDestinationPath string, sourcePath string) error {
	file, err := archive.Create(archiveDestinationPath)
	if err != nil {
		return err
	}

	st, err := os.Stat(sourcePath)
	if err != nil {
		fmt.Fprintf(
			file,
			"## Unable to Stat() file: %s\n", err.Error(),
		)

		return nil
	}

	fmt.Fprintf(file, "## File mtime=%s and size=%d\n", st.ModTime().String(), st.Size())

	contentBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		fmt.Fprintf(
			file,
			"## Unable to ReadFile: %s\n", err.Error(),
		)

		return nil
	}

	_, err = file.Write(contentBytes)
	if err != nil {
		return err
	}

	return nil
}
