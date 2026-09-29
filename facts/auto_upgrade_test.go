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
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

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

func TestRunIsStuck(t *testing.T) {
	cases := []struct {
		name string
		// age of the marker, or -1 for no marker.
		age time.Duration
		// directory creates the marker as a directory.
		directory bool
		// underAFile puts the marker under a regular file, which can't be stat'ed.
		underAFile bool
		want       bool
		wantErr    bool
	}{
		{name: "no_marker", age: -1, want: false},
		{name: "run_in_progress", age: 5 * time.Minute, want: false},
		{name: "stuck_run", age: 6 * time.Hour, want: true},
		// An empty agent.auto_upgrade_file resolves to the state directory.
		{name: "marker_is_a_directory", age: 6 * time.Hour, directory: true, want: false},
		// A file used as a directory gives a non-ENOENT error as any user.
		{name: "unreadable", age: -1, underAFile: true, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "auto_upgrade")

			if tc.underAFile {
				blocker := filepath.Join(dir, "blocker")
				if err := os.WriteFile(blocker, nil, 0o600); err != nil {
					t.Fatal(err)
				}

				marker = filepath.Join(blocker, "auto_upgrade")
			}

			if tc.age >= 0 {
				if tc.directory {
					if err := os.Mkdir(marker, 0o750); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(marker, nil, 0o600); err != nil {
					t.Fatal(err)
				}

				modTime := time.Now().Add(-tc.age)
				if err := os.Chtimes(marker, modTime, modTime); err != nil {
					t.Fatal(err)
				}
			}

			checker := &autoUpgradeChecker{AutoUpgradeFile: marker}

			got, err := checker.runIsStuck()
			if (err != nil) != tc.wantErr {
				t.Fatalf("runIsStuck() error = %v, wantErr %v", err, tc.wantErr)
			}

			if got != tc.want {
				t.Errorf("runIsStuck() = %v, want %v", got, tc.want)
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

	name, err := proc.NameWithContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	args, err := proc.CmdlineSliceWithContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(args, wantArg) {
		status, _ := proc.StatusWithContext(t.Context())
		t.Fatalf("command line is %q (status %v), want it to contain %s", args, status, wantArg)
	}

	return proc, name, args
}

// TestDpkgIsInterrupted checks the age gate that tells an interrupted dpkg apart from an
// operation in progress: both leave the same traces, only a stale dpkg database counts.
func TestDpkgIsInterrupted(t *testing.T) {
	const (
		healthy = "Package: glouton\nStatus: install ok installed\n"
		broken  = "Package: postfix\nStatus: install ok half-configured\n"
	)

	cases := []struct {
		name   string
		status string
		// statusAge is how long ago the dpkg database was last written.
		statusAge time.Duration
		// journalEntry adds an entry to the dpkg journal.
		journalEntry bool
		want         bool
	}{
		{name: "healthy", status: healthy, statusAge: 6 * time.Hour, want: false},
		{name: "broken_and_stale", status: broken, statusAge: 6 * time.Hour, want: true},
		// What a running apt looks like: the same broken state, freshly written.
		{name: "broken_but_running", status: broken, statusAge: time.Minute, want: false},
		{name: "journal_and_stale", status: healthy, statusAge: 6 * time.Hour, journalEntry: true, want: true},
		{name: "journal_but_running", status: healthy, statusAge: time.Minute, journalEntry: true, want: false},
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

			status := filepath.Join(hostRoot, dpkgStatusPath)
			if err := os.WriteFile(status, []byte(tc.status), 0o600); err != nil {
				t.Fatal(err)
			}

			modTime := time.Now().Add(-tc.statusAge)
			if err := os.Chtimes(status, modTime, modTime); err != nil {
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
		t.Error("dpkgIsInterrupted() error = nil, want the stat error of the missing database")
	}
}
