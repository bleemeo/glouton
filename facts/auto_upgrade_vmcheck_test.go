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

// End-to-end checks for the auto-upgrade detection. They need a real APT, a real dpkg
// database and, for some of them, a machine deliberately put in a broken state, so they
// are skipped unless GLOUTON_VM_TEST=1 and never run in CI.
//
// # The machine
//
// Any throwaway Debian or Ubuntu VM, with root on it and nobody else using it: the
// scenarios comment out APT sources, write to the dpkg journal and leave processes
// holding package locks. Glouton has to be installed from the Bleemeo repository, which
// is load-bearing rather than convenience - with no repository providing Glouton the
// checks have nothing to look at and report no status at all.
//
// These were written against Ubuntu 24.04 under Lima (https://lima-vm.io), which is one
// way to get such a VM on macOS or Linux:
//
//	limactl create --name=glouton-vm --cpus=2 --memory=4 template:ubuntu-24.04
//	limactl start glouton-vm
//	limactl shell glouton-vm sudo sh -c '\
//	  echo "deb [trusted=yes] https://packages.bleemeo.com/bleemeo-agent/ubuntu noble main" \
//	    > /etc/apt/sources.list.d/bleemeo.list; \
//	  apt-get update && apt-get install -y glouton'
//
// Lima before v2.0 spells the template "template://ubuntu-24.04". The trusted=yes skips
// signature checking, which is a shortcut worth taking only on a machine about to be
// thrown away. On a first boot "limactl start" may give up waiting with "did not receive
// an event with the running status" while the VM comes up anyway: check "limactl list"
// before assuming it failed.
//
// # Running them
//
// Build the test binary for the VM's architecture - Lima matches the host, so arm64 on
// Apple Silicon and amd64 elsewhere - and copy it over:
//
//	GOOS=linux GOARCH=arm64 go test -c ./facts -o facts.test
//	limactl copy facts.test glouton-vm:/tmp/
//	limactl shell glouton-vm sudo install -m 0755 /tmp/facts.test /usr/local/bin/facts.test
//
// Run them as the "glouton" user, not as root. Several exist to prove the checks work
// with the permissions Glouton actually has - the lock files are 0640 root:root - and
// running as root would make them pass without proving it:
//
//	limactl shell glouton-vm sudo runuser -u glouton -- \
//	  env GLOUTON_VM_TEST=1 HOME=/tmp /usr/local/bin/facts.test -test.v -test.run TestVM
//
// Any other VM works the same way: build for its architecture, scp the binary across, and
// run that last command on it. Without a scenario set, the three tests that need a broken
// machine skip and say which variable to set.
//
// # Environment
//
//	GLOUTON_VM_TEST=1            required, otherwise everything is skipped
//	GLOUTON_VM_SCENARIO=<name>   runs the test that needs that state, see below
//	GLOUTON_VM_WANT_STATUS=<v>   asserts the status instead of only reporting it
//	GLOUTON_VM_PID=<pid>         the process TestVMShortLivedScript inspects
//	GLOUTON_VM_HOSTROOT=<path>   host root to read the locks under, to check a
//	                             filesystem the machine isn't installed on (btrfs
//	                             reports a different device in /proc/locks than stat
//	                             does, which is why the locks are matched by inode)
//	GLOUTON_VM_AUTOUPGRADE_FILE  the configured marker path, to stand in for a moved
//	                             agent.state_directory
//
// # Putting the machine in each state
//
// TestVMLockStuck needs GLOUTON_VM_SCENARIO=lock-stuck and a stopped process holding one
// of the package locks, which is how the ticket suggests reproducing a blocked apt:
//
//	setsid python3 -c 'import fcntl, os, time
//	fd = os.open("/var/lib/dpkg/lock-frontend", os.O_RDWR | os.O_CREAT, 0o640)
//	fcntl.lockf(fd, fcntl.LOCK_EX)
//	time.sleep(600)' </dev/null >/dev/null 2>&1 &
//	kill -STOP $!
//
// TestVMRepositoryCheckFails needs GLOUTON_VM_SCENARIO=apt-cache-broken and an apt-cache
// that is found but fails, which is not the same as one that is absent:
//
//	mkdir -p /opt/fakebin
//	printf '#!/bin/sh\nexit 1\n' > /opt/fakebin/apt-cache
//	chmod 755 /opt/fakebin/apt-cache
//
// and then run that one test with the fake first on the path:
//
//	runuser -u glouton -- env GLOUTON_VM_TEST=1 GLOUTON_VM_SCENARIO=apt-cache-broken \
//	  HOME=/tmp PATH=/opt/fakebin:/usr/sbin:/usr/bin:/sbin:/bin \
//	  ./facts.test -test.run TestVMRepositoryCheckFails -test.v
//
// TestVMShortLivedScript needs GLOUTON_VM_PID to be a script started through a shebang
// and still running, named after one of shortLivedPackageScripts. Any executable of that
// name will do, the point being that the kernel truncates it to 15 characters so only the
// command line carries it in full:
//
//	printf '#!/bin/sh\nsleep 600\n' > /tmp/unattended-upgrade
//	chmod 755 /tmp/unattended-upgrade
//	/tmp/unattended-upgrade </dev/null >/dev/null 2>&1 &
//	echo $!   # this is GLOUTON_VM_PID
//
// TestVMLockFree and TestVMDpkgReadable want the machine left alone: they fail if a lock
// is held, which also catches a scenario that didn't clean up after itself.

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
