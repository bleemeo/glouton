// Copyright 2015-2026 Bleemeo
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
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/utils/gloutonexec"
	"github.com/bleemeo/glouton/version"

	"github.com/google/go-cmp/cmp"
	"github.com/shirou/gopsutil/v4/process"
)

func TestDecodeAPTCachePolicy(t *testing.T) {
	cases := []struct {
		name          string
		in            string
		installed     bool
		hasRepository bool
	}{
		{
			// Glouton installed and the Bleemeo repository configured.
			name: "installed_with_repository",
			in: `glouton:
  Installed: 25.08.14.140000
  Candidate: 25.08.14.140000
  Version table:
 *** 25.08.14.140000 500
        500 https://packages.bleemeo.com/bleemeo-agent/ubuntu noble/main amd64 Packages
        100 /var/lib/dpkg/status
`,
			installed:     true,
			hasRepository: true,
		},
		{
			// Output of "apt-cache policy" without a package.
			name: "package_files_with_repository",
			in: `Package files:
 100 /var/lib/dpkg/status
     release a=now
 500 http://ports.ubuntu.com/ubuntu-ports noble/main arm64 Packages
     release v=24.04,o=Ubuntu,a=noble,n=noble,l=Ubuntu,c=main,b=arm64
     origin ports.ubuntu.com
Pinned packages:
`,
			installed:     false,
			hasRepository: true,
		},
		{
			// The same with an empty /var/lib/apt/lists.
			name: "package_files_without_repository",
			in: `Package files:
 100 /var/lib/dpkg/status
     release a=now
Pinned packages:
`,
			installed:     false,
			hasRepository: false,
		},
		{
			// After do-release-upgrade commented out the source.
			name: "installed_without_repository",
			in: `glouton:
  Installed: 25.08.14.140000
  Candidate: 25.08.14.140000
  Version table:
 *** 25.08.14.140000 100
        100 /var/lib/dpkg/status
`,
			installed:     true,
			hasRepository: false,
		},
		{
			// A newer version is available.
			name: "installed_with_several_versions",
			in: `glouton:
  Installed: 25.08.14.140000
  Candidate: 25.09.02.100000
  Version table:
     25.09.02.100000 500
        500 https://packages.bleemeo.com/bleemeo-agent/ubuntu noble/main amd64 Packages
 *** 25.08.14.140000 100
        100 /var/lib/dpkg/status
`,
			installed:     true,
			hasRepository: true,
		},
		{
			// The transitional bleemeo-agent package is known but not installed.
			name: "known_but_not_installed",
			in: `bleemeo-agent:
  Installed: (none)
  Candidate: 25.08.14.140000
  Version table:
     25.08.14.140000 500
        500 https://packages.bleemeo.com/bleemeo-agent/ubuntu noble/main amd64 Packages
`,
			installed:     false,
			hasRepository: true,
		},
		{
			// apt-cache prints nothing for an unknown package.
			name:          "unknown_package",
			in:            "",
			installed:     false,
			hasRepository: false,
		},
		{
			// Labels are translated, source lines aren't.
			name: "localized_output",
			in: `glouton:
  Installé : 25.08.14.140000
  Candidat : 25.08.14.140000
 Table de version :
 *** 25.08.14.140000 500
        500 https://packages.bleemeo.com/bleemeo-agent/ubuntu noble/main amd64 Packages
        100 /var/lib/dpkg/status
`,
			installed:     true,
			hasRepository: true,
		},
		{
			// A local mirror is a repository.
			name: "local_mirror",
			in: `glouton:
  Installed: 25.08.14.140000
  Candidate: 25.08.14.140000
  Version table:
 *** 25.08.14.140000 500
        500 file:/srv/mirror/bleemeo noble/main amd64 Packages
        100 /var/lib/dpkg/status
`,
			installed:     true,
			hasRepository: true,
		},
		{
			// A single-digit version must not be mistaken for a pin priority.
			name: "numeric_version",
			in: `glouton:
  Installed: 5
  Candidate: 5
  Version table:
 *** 5 100
        100 /var/lib/dpkg/status
`,
			installed:     true,
			hasRepository: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			installed, hasRepository := decodeAPTCachePolicy([]byte(tc.in))

			if installed != tc.installed {
				t.Errorf("installed = %v, want %v", installed, tc.installed)
			}

			if hasRepository != tc.hasRepository {
				t.Errorf("hasRepository = %v, want %v", hasRepository, tc.hasRepository)
			}
		})
	}
}

func TestDecodeProcLocks(t *testing.T) {
	// Captured on Ubuntu 24.04 with lock-frontend (inode 1425030) and lock (inode
	// 1425029) held. The "->" line is a waiter. The last two lines are a read lock and a
	// byte-range lock, which are ignored.
	const procLocks = `6: POSIX  ADVISORY  WRITE 8 00:47:1425029 0 EOF
24: POSIX  ADVISORY  WRITE 7 00:47:1425030 0 EOF
24: -> POSIX  ADVISORY  WRITE 9 00:47:1425030 0 EOF
29: FLOCK  ADVISORY  WRITE 10 00:47:1379269 0 EOF
30: OFDLCK ADVISORY  READ  -1 00:47:1379270 0 EOF
31: POSIX  ADVISORY  READ  11 08:01:12345 0 EOF
32: POSIX  ADVISORY  WRITE 12 08:01:67890 1073741826 1073742335
`

	want := map[uint64]int{
		1425029: 8,
		1425030: 7,
	}

	if diff := cmp.Diff(want, decodeProcLocks([]byte(procLocks))); diff != "" {
		t.Errorf("decodeProcLocks mismatch (-want +got):\n%s", diff)
	}
}

// TestDecodeProcLocksSharedInode checks that two locks on the same inode on different
// devices give a single entry, the first one.
func TestDecodeProcLocksSharedInode(t *testing.T) {
	const procLocks = `1: POSIX  ADVISORY  WRITE 42 00:47:900 0 EOF
2: POSIX  ADVISORY  WRITE 43 08:01:900 0 EOF
`

	want := map[uint64]int{900: 42}

	if diff := cmp.Diff(want, decodeProcLocks([]byte(procLocks))); diff != "" {
		t.Errorf("decodeProcLocks mismatch (-want +got):\n%s", diff)
	}
}

func TestDecodeProcLocksInvalid(t *testing.T) {
	const procLocks = `
not a lock line
1: POSIX
2: POSIX  ADVISORY  WRITE notapid 00:47:1425030 0 EOF
3: POSIX  ADVISORY  WRITE 7 00:47 0 EOF
4: POSIX  ADVISORY  WRITE 7 00:47:notaninode 0 EOF
`

	if got := decodeProcLocks([]byte(procLocks)); len(got) != 0 {
		t.Errorf("decodeProcLocks() = %v, want empty", got)
	}
}

func TestDpkgJournalIsDirty(t *testing.T) {
	cases := []struct {
		name    string
		entries []string
		// noJournalDir skips the creation of var/lib/dpkg/updates entirely.
		noJournalDir bool
		want         bool
	}{
		{name: "empty", entries: nil, want: false},
		{name: "no_journal_dir", noJournalDir: true, want: false},
		{name: "journal_entry", entries: []string{"0001"}, want: true},
		{name: "single_digit", entries: []string{"0"}, want: true},
		// Only all-digit names are journal entries.
		{name: "temporary_file", entries: []string{"tmp.i"}, want: false},
		{name: "mixed", entries: []string{"tmp.i", "0042"}, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hostRoot := t.TempDir()

			if !tc.noJournalDir {
				journal := filepath.Join(hostRoot, dpkgJournalDir)
				if err := os.MkdirAll(journal, 0o750); err != nil {
					t.Fatal(err)
				}

				for _, entry := range tc.entries {
					if err := os.WriteFile(filepath.Join(journal, entry), nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}

			checker := &autoUpgradeChecker{HostRootPath: hostRoot}

			got, err := checker.dpkgJournalIsDirty()
			if err != nil {
				t.Fatalf("dpkgJournalIsDirty() error: %v", err)
			}

			if got != tc.want {
				t.Errorf("dpkgJournalIsDirty() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDpkgHasBrokenPackage(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   bool
	}{
		{
			name: "healthy",
			status: `Package: glouton
Status: install ok installed
Version: 25.08.14.140000

Package: bash
Status: install ok installed
`,
			want: false,
		},
		{
			name: "half_configured",
			status: `Package: glouton
Status: install ok installed

Package: postfix
Status: install ok half-configured
`,
			want: true,
		},
		{
			name: "half_installed",
			status: `Package: linux-image-generic
Status: install reinstreq half-installed
`,
			want: true,
		},
		{
			name: "unpacked",
			status: `Package: some-package
Status: install ok unpacked
`,
			want: true,
		},
		{
			// Error flag set on an installed package.
			name: "reinstreq_flag",
			status: `Package: adduser
Status: install reinstreq installed
`,
			want: true,
		},
		{
			name: "config_files",
			status: `Package: removed-package
Status: deinstall ok config-files
`,
			want: false,
		},
		{
			// "hold" is the want field, not the error flag.
			name: "hold_is_not_broken",
			status: `Package: some-package
Status: hold ok installed
`,
			want: false,
		},
		{
			// APT resolves pending triggers itself.
			name: "triggers_pending",
			status: `Package: man-db
Status: install ok triggers-pending

Package: libc-bin
Status: install ok triggers-awaited
`,
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hostRoot := t.TempDir()

			if err := os.MkdirAll(filepath.Join(hostRoot, "var/lib/dpkg"), 0o750); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(filepath.Join(hostRoot, dpkgStatusPath), []byte(tc.status), 0o600); err != nil {
				t.Fatal(err)
			}

			checker := &autoUpgradeChecker{HostRootPath: hostRoot}

			got, err := checker.dpkgHasBrokenPackage()
			if err != nil {
				t.Fatalf("dpkgHasBrokenPackage() error: %v", err)
			}

			if got != tc.want {
				t.Errorf("dpkgHasBrokenPackage() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDecodeRunDuration(t *testing.T) {
	const now = 10 * time.Hour

	cases := []struct {
		name    string
		output  string
		want    time.Duration
		wantErr bool
	}{
		{
			name:   "running",
			output: "ActiveState=activating\nInactiveExitTimestampMonotonic=7200000000\n",
			want:   8 * time.Hour,
		},
		{
			name:   "just_started",
			output: "InactiveExitTimestampMonotonic=35999000000\nActiveState=activating\n",
			want:   time.Second,
		},
		// The last run's start time is kept once the unit is inactive again.
		{name: "finished", output: "ActiveState=inactive\nInactiveExitTimestampMonotonic=7200000000\n"},
		{name: "never_ran", output: "ActiveState=inactive\nInactiveExitTimestampMonotonic=0\n"},
		{name: "failed", output: "ActiveState=failed\nInactiveExitTimestampMonotonic=7200000000\n"},
		{name: "no_state", output: "", wantErr: true},
		{name: "running_without_start", output: "ActiveState=activating\nInactiveExitTimestampMonotonic=0\n", wantErr: true},
		{name: "running_bad_start", output: "ActiveState=activating\nInactiveExitTimestampMonotonic=n/a\n", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeRunDuration([]byte(tc.output), now)
			if (err != nil) != tc.wantErr {
				t.Fatalf("decodeRunDuration() error = %v, wantErr %v", err, tc.wantErr)
			}

			if got != tc.want {
				t.Errorf("decodeRunDuration() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestIsShortLivedPackageProcessOverMatch checks that a directory named like a package
// script in the command line, such as /var/log/unattended-upgrades, doesn't match.
func TestIsShortLivedPackageProcessOverMatch(t *testing.T) {
	if version.IsWindows() {
		t.Skip("there is no /bin/sh to build a command line with")
	}

	logDir := filepath.Join(t.TempDir(), "unattended-upgrades")
	if err := os.Mkdir(logDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// The shell blocks on the builtin read until startAndInspect closes its stdin.
	cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", "read -r _", "x", logDir)

	proc, name, args := startAndInspect(t, cmd, logDir)

	if isShortLivedPackageProcess(t.Context(), proc, name) {
		t.Errorf("isShortLivedPackageProcess() = true for %q, want false", args)
	}
}

// TestIsShortLivedPackageProcessScript checks that a script run through a shebang matches.
func TestIsShortLivedPackageProcessScript(t *testing.T) {
	if version.IsWindows() {
		t.Skip("there is no shebang to run a script through")
	}

	script := filepath.Join(t.TempDir(), "unattended-upgrade")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nread -r _\n"), 0o700); err != nil { //nolint:gosec
		t.Fatal(err)
	}

	proc, name, args := startAndInspect(t, exec.CommandContext(t.Context(), script), script)

	if !isShortLivedPackageProcess(t.Context(), proc, name) {
		t.Errorf("isShortLivedPackageProcess() = false for %q, want true", args)
	}
}

// startAndInspect starts cmd with its stdin held open, kills it at the end of the test,
// and returns the process with its name and command line. It fails the test if wantArg
// isn't in the command line.
func startAndInspect(t *testing.T, cmd *exec.Cmd, wantArg string) (*process.Process, string, []string) {
	t.Helper()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	proc, err := process.NewProcessWithContext(t.Context(), int32(cmd.Process.Pid)) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}

	// cmd.Start returns while the child is still in execve: the kernel resumes the parent
	// before it has set up the new program's arguments, and /proc/<pid>/cmdline reads as
	// empty until then. The name changes during execve too, so it's read afterwards.
	var args []string

	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		args, err = proc.CmdlineSliceWithContext(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		if slices.Contains(args, wantArg) || time.Now().After(deadline) {
			break
		}
	}

	name, err := proc.NameWithContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(args, wantArg) {
		status, _ := proc.StatusWithContext(t.Context())
		t.Fatalf("command line is %q (status %v), want it to contain %s", args, status, wantArg)
	}

	return proc, name, args
}

// TestDpkgIsInterrupted checks that either trace of an interrupted dpkg is reported,
// however recently the dpkg database was written.
func TestDpkgIsInterrupted(t *testing.T) {
	const (
		healthy = "Package: glouton\nStatus: install ok installed\n"
		broken  = "Package: postfix\nStatus: install ok half-configured\n"
	)

	cases := []struct {
		name   string
		status string
		// journalEntry adds an entry to the dpkg journal.
		journalEntry bool
		want         bool
	}{
		{name: "healthy", status: healthy, want: false},
		{name: "broken", status: broken, want: true},
		{name: "journal", status: healthy, journalEntry: true, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hostRoot := t.TempDir()

			if err := os.MkdirAll(filepath.Join(hostRoot, dpkgJournalDir), 0o750); err != nil {
				t.Fatal(err)
			}

			if tc.journalEntry {
				entry := filepath.Join(hostRoot, dpkgJournalDir, "0001")
				if err := os.WriteFile(entry, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			if err := os.WriteFile(filepath.Join(hostRoot, dpkgStatusPath), []byte(tc.status), 0o600); err != nil {
				t.Fatal(err)
			}

			checker := &autoUpgradeChecker{HostRootPath: hostRoot}

			got, err := checker.dpkgIsInterrupted()
			if err != nil {
				t.Fatalf("dpkgIsInterrupted() error: %v", err)
			}

			if got != tc.want {
				t.Errorf("dpkgIsInterrupted() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDpkgIsInterruptedNoDatabase checks that an unreadable dpkg database is an error,
// never a quiet "not interrupted".
func TestDpkgIsInterruptedNoDatabase(t *testing.T) {
	checker := &autoUpgradeChecker{HostRootPath: t.TempDir()}

	if _, err := checker.dpkgIsInterrupted(); err == nil {
		t.Error("dpkgIsInterrupted() error = nil, want the read error of the missing database")
	}
}

// TestCombineVerdicts checks that a check that couldn't answer keeps its previous answer,
// and is never reported as OK without one.
func TestCombineVerdicts(t *testing.T) {
	const (
		repository = autoUpgradeStatusRepositoryMissing
		dpkg       = autoUpgradeStatusDpkgInterrupted
		run        = autoUpgradeStatusRunNotFinished
	)

	cases := []struct {
		name     string
		verdicts map[string]checkVerdict
		previous string
		want     string
		wantErr  bool
	}{
		{
			name:     "all_ok",
			verdicts: map[string]checkVerdict{repository: verdictOK, dpkg: verdictOK, run: verdictOK},
			want:     autoUpgradeStatusOK,
		},
		{
			name:     "problems_sorted",
			verdicts: map[string]checkVerdict{run: verdictProblem, repository: verdictProblem, dpkg: verdictOK},
			want:     "repository_missing,run_not_finished",
		},
		{
			name:     "unknown_without_previous",
			verdicts: map[string]checkVerdict{repository: verdictOK, dpkg: verdictUnknown},
			wantErr:  true,
		},
		{
			name:     "unknown_without_previous_but_problem",
			verdicts: map[string]checkVerdict{repository: verdictProblem, dpkg: verdictUnknown},
			want:     repository,
		},
		// The dpkg check is skipped while unattended-upgrades holds a lock.
		{
			name:     "unknown_keeps_previous_ok",
			verdicts: map[string]checkVerdict{repository: verdictOK, dpkg: verdictUnknown},
			previous: autoUpgradeStatusOK,
			want:     autoUpgradeStatusOK,
		},
		{
			name:     "unknown_keeps_previous_problem",
			verdicts: map[string]checkVerdict{repository: verdictOK, dpkg: verdictUnknown},
			previous: dpkg,
			want:     dpkg,
		},
		{
			name:     "unknown_keeps_previous_problem_next_to_a_new_one",
			verdicts: map[string]checkVerdict{repository: verdictOK, dpkg: verdictUnknown, run: verdictProblem},
			previous: "dpkg_interrupted,repository_missing",
			want:     "dpkg_interrupted,run_not_finished",
		},
		// A check that answers drops its previous problem, whatever the others do.
		{
			name:     "answer_overrides_previous",
			verdicts: map[string]checkVerdict{repository: verdictUnknown, run: verdictOK},
			previous: run,
			want:     autoUpgradeStatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := combineVerdicts(tc.verdicts, tc.previous)
			if (err != nil) != tc.wantErr {
				t.Fatalf("combineVerdicts() error = %v, wantErr %v", err, tc.wantErr)
			}

			if got != tc.want {
				t.Errorf("combineVerdicts() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRefreshAutoUpgradeFacts checks that the hourly refresh recomputes the auto-upgrade
// facts, keeps the others, and only moves fact_updated_at when something changed. Without
// a Runner there is no auto-upgrade, so the facts must go.
func TestRefreshAutoUpgradeFacts(t *testing.T) {
	if version.IsWindows() || version.IsFreeBSD() {
		t.Skip("the auto-upgrade is detected without a Runner on this OS")
	}

	const oldUpdatedAt = "2026-01-01T00:00:00Z"

	t.Run("recomputed", func(t *testing.T) {
		f := NewFacter(Options{})
		f.lastFactsUpdate = time.Now()
		f.facts = map[string]string{
			"hostname":             "server",
			factAutoUpgradeEnabled: "true",
			factAutoUpgradeStatus:  autoUpgradeStatusDpkgInterrupted,
			FactUpdatedAt:          oldUpdatedAt,
		}

		f.RefreshAutoUpgradeFacts(t.Context())

		if _, ok := f.facts[factAutoUpgradeStatus]; ok {
			t.Errorf("%s = %q, want it recomputed and gone", factAutoUpgradeStatus, f.facts[factAutoUpgradeStatus])
		}

		if f.facts["hostname"] != "server" {
			t.Errorf("hostname = %q, want the other facts kept", f.facts["hostname"])
		}

		if f.facts[FactUpdatedAt] == oldUpdatedAt {
			t.Errorf("%s wasn't updated although a fact changed", FactUpdatedAt)
		}
	})

	t.Run("unchanged", func(t *testing.T) {
		f := NewFacter(Options{})
		f.lastFactsUpdate = time.Now()
		f.facts = map[string]string{"hostname": "server", FactUpdatedAt: oldUpdatedAt}

		f.RefreshAutoUpgradeFacts(t.Context())

		if f.facts[FactUpdatedAt] != oldUpdatedAt {
			t.Errorf("%s = %q, want %q since nothing changed", FactUpdatedAt, f.facts[FactUpdatedAt], oldUpdatedAt)
		}
	})

	t.Run("before_first_refresh", func(t *testing.T) {
		f := NewFacter(Options{})
		f.facts = map[string]string{factAutoUpgradeStatus: autoUpgradeStatusDpkgInterrupted}

		f.RefreshAutoUpgradeFacts(t.Context())

		if f.facts[factAutoUpgradeStatus] != autoUpgradeStatusDpkgInterrupted {
			t.Error("the facts were touched before the first full refresh")
		}
	})
}

// TestMergeAutoUpgradeFacts checks that the hourly result only replaces the auto-upgrade
// facts, and only moves fact_updated_at when one of them changed.
func TestMergeAutoUpgradeFacts(t *testing.T) {
	const oldUpdatedAt = "2026-01-01T00:00:00Z"

	newProvider := func() *FactProvider {
		f := NewFacter(Options{})
		f.lastFactsUpdate = time.Now()
		f.facts = map[string]string{
			"hostname":             "server",
			factAutoUpgradeEnabled: "true",
			factAutoUpgradeStatus:  autoUpgradeStatusOK,
			FactUpdatedAt:          oldUpdatedAt,
		}

		return f
	}
	stuck := map[string]string{
		factAutoUpgradeEnabled: "true",
		factAutoUpgradeStatus:  autoUpgradeStatusRunNotFinished,
	}

	t.Run("merged", func(t *testing.T) {
		f := newProvider()
		// Set while the checks ran, after their result was computed.
		f.SetFact("manual", "kept")

		f.mergeAutoUpgradeFacts(stuck)

		want := map[string]string{
			"hostname":             "server",
			"manual":               "kept",
			factAutoUpgradeEnabled: "true",
			factAutoUpgradeStatus:  autoUpgradeStatusRunNotFinished,
		}
		got := maps.Clone(f.facts)
		delete(got, FactUpdatedAt)

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("facts mismatch (-want +got):\n%s", diff)
		}

		if f.facts[FactUpdatedAt] == oldUpdatedAt {
			t.Errorf("%s wasn't updated although a fact changed", FactUpdatedAt)
		}
	})

	t.Run("unchanged", func(t *testing.T) {
		f := newProvider()
		before := f.facts

		f.mergeAutoUpgradeFacts(map[string]string{
			factAutoUpgradeEnabled: "true",
			factAutoUpgradeStatus:  autoUpgradeStatusOK,
		})

		if diff := cmp.Diff(before, f.facts); diff != "" {
			t.Errorf("facts changed although no auto-upgrade fact did (-want +got):\n%s", diff)
		}
	})

	t.Run("dropped_facts", func(t *testing.T) {
		f := newProvider()

		f.mergeAutoUpgradeFacts(nil)

		if _, ok := f.facts[factAutoUpgradeEnabled]; ok {
			t.Errorf("%s kept, want it removed", factAutoUpgradeEnabled)
		}

		if _, ok := f.facts[factAutoUpgradeStatus]; ok {
			t.Errorf("%s kept, want it removed", factAutoUpgradeStatus)
		}
	})
}

// TestAutoUpgradeEnabledValue checks that a failing systemctl doesn't report the
// auto-upgrade as disabled.
func TestAutoUpgradeEnabledValue(t *testing.T) {
	errSystemctl := errors.New("Failed to connect to bus") //nolint:err113

	cases := []struct {
		name     string
		enabled  bool
		err      error
		previous map[string]string
		want     string
		wantOK   bool
	}{
		{name: "enabled", enabled: true, want: "true", wantOK: true},
		{name: "disabled", enabled: false, want: "false", wantOK: true},
		{name: "error_keeps_previous", err: errSystemctl, previous: map[string]string{factAutoUpgradeEnabled: "true"}, want: "true", wantOK: true},
		{name: "error_without_previous", err: errSystemctl, previous: map[string]string{"hostname": "server"}},
		{name: "error_before_first_refresh", err: errSystemctl},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := autoUpgradeEnabledValue(tc.enabled, tc.err, tc.previous)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("autoUpgradeEnabledValue() = %q, %v, want %q, %v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestFactsRefreshesOnce checks that concurrent callers needing a refresh wait for a
// single one instead of each running the checks.
func TestFactsRefreshesOnce(t *testing.T) {
	// A host root other than "/" keeps the refresh off this machine's commands, such as
	// the auto-upgrade checks.
	hostRoot := t.TempDir()
	f := NewFacter(Options{Runner: gloutonexec.New(hostRoot), HostRootPath: hostRoot})

	var (
		l         sync.Mutex
		refreshes int
	)

	f.AddCallback(func(context.Context, map[string]string) map[string]string {
		l.Lock()
		refreshes++
		l.Unlock()

		return nil
	})

	var wg sync.WaitGroup

	for range 8 {
		wg.Go(func() {
			if _, err := f.Facts(t.Context(), time.Hour); err != nil {
				t.Error(err)
			}
		})
	}

	wg.Wait()

	if refreshes != 1 {
		t.Errorf("%d refreshes, want 1", refreshes)
	}

	// A refresh cut short isn't stored, so the next caller refreshes again.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	f.l.Lock()
	refreshedAt := f.lastFactsUpdate
	f.l.Unlock()

	if _, err := f.Facts(ctx, 0); err == nil {
		t.Error("Facts() with a cancelled context returned no error")
	}

	if !f.lastFactsUpdate.Equal(refreshedAt) {
		t.Error("a refresh with a cancelled context was stored")
	}
}
