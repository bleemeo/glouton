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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"
	"github.com/bleemeo/glouton/version"
	"golang.org/x/text/encoding/unicode"
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
	if err := d.commonDiagnostic(ctx, archive); err != nil {
		return err
	}

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

func (d DiagnosticAutoUpgrade) commonDiagnostic(ctx context.Context, archive types.ArchiveWriter) error {
	if err := d.commonDiagnosticGloutonBinary(ctx, archive); err != nil {
		return err
	}

	return nil
}

func (d DiagnosticAutoUpgrade) commonDiagnosticGloutonBinary(ctx context.Context, archive types.ArchiveWriter) error {
	_ = ctx

	type DiagInfo struct {
		GloutonPath string
		Errors      []string
		FileModtime time.Time
		FileSize    int64
		FileMode    string
		FileStatSys string
		FileSHA256  string
	}

	var (
		result DiagInfo
		err    error
	)

	result.GloutonPath, err = os.Executable()
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
	}

	if result.GloutonPath != "" {
		st, err := os.Stat(result.GloutonPath)
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		}

		result.FileModtime = st.ModTime()
		result.FileSize = st.Size()
		result.FileMode = fmt.Sprintf("%v", st.Mode())
		result.FileStatSys = fmt.Sprintf("%v", st.Sys())

		hasher := sha256.New()

		f, err := os.Open(result.GloutonPath)
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		} else {
			defer f.Close()

			_, err = io.Copy(hasher, f)
			if err != nil {
				result.Errors = append(result.Errors, err.Error())
			}

			result.FileSHA256 = hex.EncodeToString(hasher.Sum(nil))
		}
	}

	file, err := archive.Create("auto-upgrade-troubleshooting/glouton-binary-info.json")
	if err != nil {
		return err
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(result)
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

	if err := d.diagnosticFileContent(archive, "auto-upgrade-troubleshooting/deprecated/auto_update-marker.txt", `C:\ProgramData\glouton\auto_update`, true); err != nil {
		return err
	}

	// This is the deprecated log files
	if err := d.diagnosticFileContent(archive, "auto-upgrade-troubleshooting/deprecated/auto_update.txt", `C:\ProgramData\glouton\auto_update.txt`, true); err != nil {
		return err
	}

	if err := d.diagnosticFileContent(archive, "auto-upgrade-troubleshooting/deprecated/msiexec-log.txt", `C:\ProgramData\glouton\msiexec-log.txt`, true); err != nil {
		return err
	}

	if err := d.diagnosticFileContent(archive, "auto-upgrade-troubleshooting/logs/auto_upgrade.log", `C:\ProgramData\glouton\logs\auto_upgrade.log`, true); err != nil {
		return err
	}

	if err := d.diagnosticFileContent(archive, "auto-upgrade-troubleshooting/logs/auto_upgrade_old.log", `C:\ProgramData\glouton\logs\auto_upgrade_old.log`, true); err != nil {
		return err
	}

	if err := d.diagnosticFileContent(archive, "auto-upgrade-troubleshooting/logs/msiexec_log.txt", `C:\ProgramData\glouton\logs\msiexec.log`, true); err != nil {
		return err
	}

	if err := d.diagnosticFileContent(archive, "auto-upgrade-troubleshooting/logs/msiexec_log_old.txt", `C:\ProgramData\glouton\logs\msiexec_old.log`, true); err != nil {
		return err
	}

	if err := d.diagnosticFileContent(archive, "auto-upgrade-troubleshooting/logs/last_post_install.log", `C:\ProgramData\glouton\logs\last_post_install.log`, true); err != nil {
		return err
	}

	if err := d.diagnosticFileContent(archive, "auto-upgrade-troubleshooting/logs/last_pre_uninstall.log", `C:\ProgramData\glouton\logs\last_pre_uninstall.log`, true); err != nil {
		return err
	}

	if err := d.diagnosticWindowsRegistry(ctx, archive); err != nil {
		return err
	}

	return nil
}

func (d DiagnosticAutoUpgrade) diagnosticAutoupgradeFreeBSD(ctx context.Context, archive types.ArchiveWriter) error {
	_ = ctx

	if err := d.diagnosticFileContent(archive, "auto-upgrade-troubleshooting/auto-upgrade.txt", "/var/lib/glouton/auto-upgrade.log", false); err != nil {
		return err
	}

	return nil
}

func (d DiagnosticAutoUpgrade) diagnosticFileContent(archive types.ArchiveWriter, archiveDestinationPath string, sourcePath string, tryUTF16 bool) error {
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

	if tryUTF16 {
		// In Windows, we might get UTF8 or UTF-16 (if UTF-32 is possible, we ignore it).
		// In UTF16 the BOM is always present. In UTF8, we will assume it could be present or not.
		// UTF16 BOM is 0xFFFE or 0xFEFF. UTF8 BOM is 0xEF 0xBB 0xBF
		// If no BOM is found, assume UTF8
		if len(contentBytes) > 0 {
			// poor-man UTF16 BOM detection. (those bytes are invalid in UTF8, so if present we assume it UTF16)
			if contentBytes[0] == 0xFF || contentBytes[1] == 0xFE {
				decoder := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder()

				utf8Bytes, err := decoder.Bytes(contentBytes)
				if err != nil {
					fmt.Fprintf(file, "## Failed to decode from UTF16: %s\n", err.Error())
				} else {
					contentBytes = utf8Bytes
				}
			}
		}
	}

	_, err = file.Write(contentBytes)
	if err != nil {
		return err
	}

	return nil
}
