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
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestDecodeAPTCachePolicy(t *testing.T) {
	cases := []struct {
		name          string
		in            string
		installed     bool
		hasRepository bool
	}{
		{
			// Nominal case: Glouton installed and the Bleemeo repository still configured.
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
			// What a do-release-upgrade leaves behind: the source is commented out, so
			// the only version APT knows about is the installed one. "apt-get install
			// glouton" still exits 0, saying the package is already the newest version.
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
			// A newer version is available but not installed yet: still healthy.
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
			// apt-cache prints nothing at all (and still exits 0) for a package it
			// doesn't know: Glouton wasn't installed from a .deb.
			name:          "unknown_package",
			in:            "",
			installed:     false,
			hasRepository: false,
		},
		{
			// Only the labels are translated, the version table isn't, which is why we
			// parse the table instead of the "Installed:"/"Candidate:" lines.
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
			// A local mirror is a perfectly valid repository.
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
	// Captured on Ubuntu 24.04 while two processes held /var/lib/dpkg/lock-frontend
	// (inode 1425030) and /var/lib/dpkg/lock (inode 1425029), plus a flock() on an
	// unrelated file. The "->" line is a process blocked on the lock, and must not be
	// taken for its holder. The last two lines are the kinds of lock APT and dpkg never
	// take: a read lock, and a byte-range lock of the sort SQLite uses.
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

// TestDecodeProcLocksSharedInode pins down what keying on the inode alone gives up: two
// locked files with the same inode on different filesystems become one entry. That is
// accepted because the device /proc/locks prints is the superblock's, which stat(2) does
// not return on btrfs, and because only the four APT/dpkg lock inodes are ever looked up.
func TestDecodeProcLocksSharedInode(t *testing.T) {
	const procLocks = `1: POSIX  ADVISORY  WRITE 42 00:47:900 0 EOF
2: POSIX  ADVISORY  WRITE 43 08:01:900 0 EOF
`

	want := map[uint64]int{900: 42}

	if diff := cmp.Diff(want, decodeProcLocks([]byte(procLocks))); diff != "" {
		t.Errorf("decodeProcLocks mismatch (-want +got):\n%s", diff)
	}
}

// Only the shape of the MAJOR:MINOR:INODE field and the inode itself are validated: the
// device is not parsed, so a malformed one alone doesn't reject a line.
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
		// dpkg removes its journal once the transaction is complete.
		{name: "empty", entries: nil, want: false},
		// No journal directory means no journal, hence no interrupted transaction. An
		// error here would stop status() ever answering AutoUpgradeStatusOK.
		{name: "no_journal_dir", noJournalDir: true, want: false},
		// What an interrupted dpkg leaves behind. APT then refuses to do anything.
		{name: "journal_entry", entries: []string{"0001"}, want: true},
		// An empty journal entry blocks APT just the same as a filled one.
		{name: "single_digit", entries: []string{"0"}, want: true},
		// dpkg's own temporary files aren't journal entries, APT ignores them.
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
			// dpkg unpacked the package but was interrupted before configuring it.
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
			// The error flag, not the state: dpkg wants this one reinstalled before
			// anything else touches it, and apt-get refuses to install until it is.
			name: "reinstreq_flag",
			status: `Package: adduser
Status: install reinstreq installed
`,
			want: true,
		},
		{
			// A removed package keeping its configuration files is perfectly normal.
			name: "config_files",
			status: `Package: removed-package
Status: deinstall ok config-files
`,
			want: false,
		},
		{
			// A held package is flagged through "want", not the error flag, and is
			// perfectly installable.
			name: "hold_is_not_broken",
			status: `Package: some-package
Status: hold ok installed
`,
			want: false,
		},
		{
			// Pending triggers are normal too, APT resolves them on its own.
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
		// age of the marker, or -1 when there is no marker at all.
		age time.Duration
		// directory creates the marker as a directory instead of a file.
		directory bool
		want      bool
	}{
		// The usual case: no auto-upgrade running.
		{name: "no_marker", age: -1, want: false},
		// A run in progress, still within the time an upgrade legitimately takes.
		{name: "run_in_progress", age: 5 * time.Minute, want: false},
		// The EXIT trap of glouton-auto-upgrade never fired and Glouton wasn't restarted:
		// the run is stuck, most likely waiting on dpkg or on a mirror.
		{name: "stuck_run", age: 6 * time.Hour, want: true},
		// An empty agent.auto_upgrade_file resolves to the state directory itself, whose
		// mtime is old enough to look like a run that never ended, and which nothing the
		// user deletes would clear. A directory is never a marker.
		{name: "marker_is_a_directory", age: 6 * time.Hour, directory: true, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "auto_upgrade")

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
			if err != nil {
				t.Fatalf("runIsStuck() error: %v", err)
			}

			if got != tc.want {
				t.Errorf("runIsStuck() = %v, want %v", got, tc.want)
			}
		})
	}
}
