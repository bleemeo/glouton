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

// End-to-end checks for the auto-upgrade detection, to run on a throwaway Ubuntu VM.
// They are skipped unless GLOUTON_VM_TEST=1.
//
//	GOOS=linux GOARCH=arm64 go test -c ./facts -o facts.test
//	GLOUTON_VM_TEST=1 ./facts.test -test.v -test.run TestVM

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/shirou/gopsutil/v4/process"
)

func requireVM(t *testing.T) {
	t.Helper()

	if os.Getenv("GLOUTON_VM_TEST") != "1" {
		t.Skip("set GLOUTON_VM_TEST=1 to run the on-VM checks")
	}
}

// requireScenario skips the test unless GLOUTON_VM_SCENARIO is name. The test fails if
// the machine isn't in that state.
func requireScenario(t *testing.T, name string) {
	t.Helper()

	if os.Getenv("GLOUTON_VM_SCENARIO") != name {
		t.Skipf("set GLOUTON_VM_SCENARIO=%s, once the machine is in that state", name)
	}
}

func newVMChecker() *autoUpgradeChecker {
	// GLOUTON_VM_HOSTROOT overrides the host root, e.g. to test the lock files on btrfs.
	hostRoot := os.Getenv("GLOUTON_VM_HOSTROOT")
	if hostRoot == "" {
		hostRoot = "/"
	}

	// GLOUTON_VM_AUTOUPGRADE_FILE overrides the configured marker path.
	autoUpgradeFile := os.Getenv("GLOUTON_VM_AUTOUPGRADE_FILE")
	if autoUpgradeFile == "" {
		autoUpgradeFile = packagingAutoUpgradeFile
	}

	return &autoUpgradeChecker{
		Runner:          gloutonexec.New("/"),
		AutoUpgradeFile: autoUpgradeFile,
		HostRootPath:    hostRoot,
	}
}

// TestVMStatus logs the status. If GLOUTON_VM_WANT_STATUS is set, it must match.
func TestVMStatus(t *testing.T) {
	requireVM(t)

	checker := newVMChecker()

	state, err := checker.repositoryState(t.Context())
	t.Logf("repositoryState = %d (0=ok 1=missing 2=unknown-package), err = %v", state, err)

	got, retrySoon := checker.status(t.Context())
	t.Logf("status = %q, retrySoon = %t", got, retrySoon)

	if want := os.Getenv("GLOUTON_VM_WANT_STATUS"); want != "" && got != want {
		t.Errorf("status = %q, want %q", got, want)
	}
}

// TestVMLockStuck checks that a SIGSTOPped lock holder is stuck while heldFor is below
// lockHeldMinDuration.
func TestVMLockStuck(t *testing.T) {
	requireVM(t)
	requireScenario(t, "lock-stuck")

	checker := newVMChecker()

	locks, err := checker.lockState(t.Context())
	if err != nil {
		t.Fatalf("lockState() error: %v", err)
	}

	holder := locks.holder

	if holder == nil {
		t.Fatal("no APT/dpkg lock is held, the scenario didn't set up the stuck process")
	}

	t.Logf(
		"holder: path=%s pid=%d name=%q heldFor=%v stopped=%v",
		holder.path, holder.pid, holder.name, holder.heldFor.Truncate(time.Second), holder.stopped,
	)

	if !holder.stopped {
		t.Errorf("holder.stopped = false, want true for a SIGSTOPped process")
	}

	if holder.heldFor >= lockHeldMinDuration {
		t.Errorf(
			"holder.heldFor = %v, want < %v: the test can't tell the stopped-state "+
				"shortcut apart from the plain duration rule",
			holder.heldFor, lockHeldMinDuration,
		)
	}

	if !holderIsStuck(holder) {
		t.Error("holderIsStuck() = false, want true")
	}
}

// TestVMLockFree checks that nothing is reported when no lock is held.
func TestVMLockFree(t *testing.T) {
	requireVM(t)

	checker := newVMChecker()

	locks, err := checker.lockState(t.Context())
	if err != nil {
		t.Fatalf("lockState() error: %v", err)
	}

	holder := locks.holder

	if holder != nil {
		t.Fatalf("a lock is held by pid %d (%s), the scenario didn't clean up", holder.pid, holder.name)
	}

	if holderIsStuck(holder) {
		t.Error("holderIsStuck() = true, want false")
	}
}

// TestVMShortLivedScript checks that the interpreted process GLOUTON_VM_PID matches
// shortLivedPackageScripts through its command line.
func TestVMShortLivedScript(t *testing.T) {
	requireVM(t)

	raw := os.Getenv("GLOUTON_VM_PID")
	if raw == "" {
		t.Skip("GLOUTON_VM_PID is not set")
	}

	pid, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("GLOUTON_VM_PID = %q: %v", raw, err)
	}

	proc, err := process.NewProcessWithContext(t.Context(), int32(pid)) //nolint:gosec
	if err != nil {
		t.Fatalf("process %d: %v", pid, err)
	}

	name, err := proc.NameWithContext(t.Context())
	if err != nil {
		t.Fatalf("NameWithContext() error: %v", err)
	}

	args, err := proc.CmdlineSliceWithContext(t.Context())
	if err != nil {
		t.Fatalf("CmdlineSliceWithContext() error: %v", err)
	}

	t.Logf("name = %q, cmdline = %q", name, args)

	if len(name) > 15 {
		t.Errorf("name = %q is longer than TASK_COMM_LEN-1, the premise of the split is wrong", name)
	}

	if shortLivedPackageProcesses[name] {
		t.Errorf("name = %q matches shortLivedPackageProcesses, the cmdline lookup isn't needed", name)
	}

	if !isShortLivedPackageProcess(t.Context(), proc, name) {
		t.Error("isShortLivedPackageProcess() = false, want true")
	}
}

// TestVMDpkgReadable checks that the dpkg reads and lockState work as the current user.
func TestVMDpkgReadable(t *testing.T) {
	requireVM(t)

	checker := newVMChecker()

	t.Logf("running as uid %d", os.Getuid())

	dirty, err := checker.dpkgJournalIsDirty()
	if err != nil {
		t.Errorf("dpkgJournalIsDirty() error: %v", err)
	} else {
		t.Logf("dpkgJournalIsDirty() = %v", dirty)
	}

	broken, err := checker.dpkgHasBrokenPackage()
	if err != nil {
		t.Errorf("dpkgHasBrokenPackage() error: %v", err)
	} else {
		t.Logf("dpkgHasBrokenPackage() = %v", broken)
	}

	if _, err := checker.lockState(t.Context()); err != nil {
		t.Errorf("lockState() error: %v", err)
	}
}

// TestVMRepositoryCheckFails checks that status is not OK when apt-cache fails and the
// other checks find nothing.
func TestVMRepositoryCheckFails(t *testing.T) {
	requireVM(t)
	requireScenario(t, "apt-cache-broken")

	checker := newVMChecker()

	if _, err := checker.repositoryState(t.Context()); err == nil {
		t.Fatal("repositoryState() succeeded, the scenario didn't break apt-cache")
	}

	// The other checks must find nothing.
	locks, err := checker.lockState(t.Context())
	if err != nil {
		t.Fatalf("lockState() error: %v", err)
	}

	holder := locks.holder

	locked := holderIsStuck(holder)

	interrupted, err := checker.dpkgIsInterrupted()
	if err != nil {
		t.Fatalf("dpkgIsInterrupted() error: %v", err)
	}

	stuck, err := checker.runIsStuck()
	if err != nil {
		t.Fatalf("runIsStuck() error: %v", err)
	}

	if locked || interrupted || stuck {
		t.Fatalf(
			"the scenario left a problem behind (locked=%v interrupted=%v stuck=%v)",
			locked, interrupted, stuck,
		)
	}

	got, retrySoon := checker.status(t.Context())
	if got != "" {
		t.Errorf("status() = %q, want \"\": never OK when the repository check didn't run", got)
	}

	if retrySoon {
		t.Error("status() retrySoon = true, want false for a check that may keep failing")
	}
}

// TestVMFactProvider checks that the status is in Facts but not in FastFacts.
func TestVMFactProvider(t *testing.T) {
	requireVM(t)

	facter := NewFacter(Options{
		Runner:          gloutonexec.New("/"),
		AutoUpgradeFile: "/var/lib/glouton/auto_upgrade",
		HostRootPath:    "/",
	})

	start := time.Now()

	fast, err := facter.FastFacts(t.Context())
	if err != nil {
		t.Fatalf("FastFacts() error: %v", err)
	}

	fastDuration := time.Since(start)

	if got, ok := fast[FactAutoUpgradeStatus]; ok {
		t.Errorf("FastFacts() has %s = %q, it must stay off the startup path", FactAutoUpgradeStatus, got)
	}

	if _, ok := fast[factAutoUpgradeEnabled]; !ok {
		t.Errorf("FastFacts() is missing %s, which is what gates the status check", factAutoUpgradeEnabled)
	}

	start = time.Now()

	full, err := facter.Facts(t.Context(), 0)
	if err != nil {
		t.Fatalf("Facts() error: %v", err)
	}

	t.Logf("FastFacts took %v, Facts took %v", fastDuration, time.Since(start))
	t.Logf("%s = %q", factAutoUpgradeEnabled, full[factAutoUpgradeEnabled])
	t.Logf("%s = %q", FactAutoUpgradeStatus, full[FactAutoUpgradeStatus])

	want := os.Getenv("GLOUTON_VM_WANT_STATUS")
	if want == "" {
		want = AutoUpgradeStatusOK
	}

	if got := full[FactAutoUpgradeStatus]; got != want {
		t.Errorf("Facts()[%s] = %q, want %q", FactAutoUpgradeStatus, got, want)
	}
}
