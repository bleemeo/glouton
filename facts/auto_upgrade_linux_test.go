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

//go:build linux

package facts

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bleemeo/glouton/utils/gloutonexec"
)

// TestStatLockInodeMissing checks that a missing lock file gives an error matching
// fs.ErrNotExist, which lockState relies on.
func TestStatLockInodeMissing(t *testing.T) {
	_, err := statLockInode(filepath.Join(t.TempDir(), "lock-frontend"))
	if err == nil {
		t.Fatal("statLockInode() on a missing file returned no error")
	}

	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("errors.Is(err, fs.ErrNotExist) = false for %v", err)
	}
}

const (
	policyFromRepository = `glouton:
  Installed: 26.09.22.122842
  Candidate: 26.09.22.122842
  Version table:
 *** 26.09.22.122842 500
        500 https://packages.bleemeo.com/bleemeo-agent/ubuntu noble/main arm64 Packages
        100 /var/lib/dpkg/status
`
	policyWithoutRepository = `glouton:
  Installed: 26.09.22.122842
  Candidate: 26.09.22.122842
  Version table:
 *** 26.09.22.122842 100
        100 /var/lib/dpkg/status
`
	policyIndexes = `Package files:
 100 /var/lib/dpkg/status
     release a=now
 500 http://ports.ubuntu.com/ubuntu-ports noble/main arm64 Packages
`
	serviceInactive = "ActiveState=inactive\nInactiveExitTimestampMonotonic=0\n"
	// With the clock of fakeSystem, started 3 h ago.
	serviceStuck = "ActiveState=activating\nInactiveExitTimestampMonotonic=3600000000\n"
)

var errFakeCommand = errors.New("fake command failure")

// fakeSystem is what an autoUpgradeChecker sees of the system in TestAutoUpgradeCheckerStatus.
type fakeSystem struct {
	policy        string // apt-cache policy glouton
	policyErr     bool
	service       string // systemctl show glouton-auto-upgrade.service
	serviceErr    bool
	lockHeld      bool // /var/lib/dpkg/lock-frontend held by a process that isn't timed
	locksMissing  bool // /proc/locks can't be read
	lockUnchecked bool // /var/lib/apt/lists/lock can't be stat'ed
	journal       bool // the dpkg journal holds an entry
}

func (s fakeSystem) checker(t *testing.T) *autoUpgradeChecker {
	t.Helper()

	root := t.TempDir()

	write := func(path, content string) {
		t.Helper()

		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(dpkgStatusPath, "Package: glouton\nStatus: install ok installed\n")

	for _, lockPath := range packageManagerLockPaths {
		write(lockPath, "")
	}

	if s.journal {
		write(filepath.Join(dpkgJournalDir, "0001"), "")
	}

	if s.lockUnchecked {
		// A regular file where a directory is expected: stat fails with ENOTDIR.
		if err := os.RemoveAll(filepath.Join(root, "var/lib/apt/lists")); err != nil {
			t.Fatal(err)
		}

		write("var/lib/apt/lists", "")
	}

	procLocks := filepath.Join(root, "proc-locks")

	if !s.locksMissing {
		var content string

		if s.lockHeld {
			inode, err := statLockInode(filepath.Join(root, "var/lib/dpkg/lock-frontend"))
			if err != nil {
				t.Fatal(err)
			}

			// The test process holds it: its name isn't one of the timed processes.
			content = fmt.Sprintf("1: POSIX  ADVISORY  WRITE %d 00:00:%d 0 EOF\n", os.Getpid(), inode)
		}

		if err := os.WriteFile(procLocks, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	run := func(_ context.Context, _ gloutonexec.Option, name string, args ...string) ([]byte, error) {
		switch command := name + " " + strings.Join(args, " "); {
		case command == "apt-cache policy glouton":
			if s.policyErr {
				return nil, errFakeCommand
			}

			return []byte(s.policy), nil
		case command == "apt-cache policy":
			return []byte(policyIndexes), nil
		case strings.HasPrefix(command, "systemctl show"):
			if s.serviceErr {
				return nil, errFakeCommand
			}

			return []byte(s.service), nil
		default:
			t.Fatalf("unexpected command %q", command)

			return nil, nil
		}
	}

	return &autoUpgradeChecker{
		HostRootPath:  root,
		run:           run,
		lookPath:      func(file string, _ gloutonexec.Option) (string, error) { return "/usr/bin/" + file, nil },
		procLocksPath: procLocks,
		monotonicNow:  func() (time.Duration, error) { return 4 * time.Hour, nil },
	}
}

// TestAutoUpgradeCheckerStatus checks how status() turns what each check sees into a
// verdict: which check runs, which is skipped, and what a check that can't answer gives.
func TestAutoUpgradeCheckerStatus(t *testing.T) {
	healthy := fakeSystem{policy: policyFromRepository, service: serviceInactive}

	cases := []struct {
		name     string
		system   func(s *fakeSystem)
		previous string
		want     string
		wantErr  bool
	}{
		{name: "healthy", want: autoUpgradeStatusOK},
		{name: "not_from_a_package", system: func(s *fakeSystem) { s.policy = "" }, want: ""},
		{name: "repository_missing", system: func(s *fakeSystem) { s.policy = policyWithoutRepository }, want: autoUpgradeStatusRepositoryMissing},
		{name: "dpkg_interrupted", system: func(s *fakeSystem) { s.journal = true }, want: autoUpgradeStatusDpkgInterrupted},
		{name: "run_not_finished", system: func(s *fakeSystem) { s.service = serviceStuck }, want: autoUpgradeStatusRunNotFinished},
		{
			name:   "several",
			system: func(s *fakeSystem) { s.policy, s.service = policyWithoutRepository, serviceStuck },
			want:   "repository_missing,run_not_finished",
		},
		// A lock that isn't stuck isn't a problem, but the dpkg check doesn't run under it:
		// the journal entry is what a running dpkg leaves.
		{name: "lock_held_without_previous", system: func(s *fakeSystem) { s.lockHeld, s.journal = true, true }, wantErr: true},
		{name: "lock_held_previous_ok", system: func(s *fakeSystem) { s.lockHeld, s.journal = true, true }, previous: autoUpgradeStatusOK, want: autoUpgradeStatusOK},
		{
			name:     "lock_held_previous_problem",
			system:   func(s *fakeSystem) { s.lockHeld = true },
			previous: autoUpgradeStatusDpkgInterrupted,
			want:     autoUpgradeStatusDpkgInterrupted,
		},
		// The dpkg check is skipped too when the locks couldn't be checked.
		{name: "lock_file_unchecked", system: func(s *fakeSystem) { s.lockUnchecked, s.journal = true, true }, wantErr: true},
		{name: "proc_locks_unreadable", system: func(s *fakeSystem) { s.locksMissing = true }, previous: autoUpgradeStatusOK, want: autoUpgradeStatusOK},
		{
			name:     "apt_cache_fails_previous_problem",
			system:   func(s *fakeSystem) { s.policyErr = true },
			previous: autoUpgradeStatusRepositoryMissing,
			want:     autoUpgradeStatusRepositoryMissing,
		},
		{name: "apt_cache_fails_without_previous", system: func(s *fakeSystem) { s.policyErr = true }, wantErr: true},
		// The run check answers: the previous run_not_finished is dropped.
		{
			name:     "systemctl_fails_other_check_answers",
			system:   func(s *fakeSystem) { s.serviceErr = true },
			previous: "repository_missing,run_not_finished",
			want:     autoUpgradeStatusRunNotFinished,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			system := healthy
			if tc.system != nil {
				tc.system(&system)
			}

			got, err := system.checker(t).status(t.Context(), tc.previous)
			if (err != nil) != tc.wantErr {
				t.Fatalf("status() error = %v, wantErr %v", err, tc.wantErr)
			}

			if got != tc.want {
				t.Errorf("status() = %q, want %q", got, tc.want)
			}
		})
	}
}
