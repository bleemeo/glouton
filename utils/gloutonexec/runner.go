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

package gloutonexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bleemeo/glouton/logger"
)

// Runner allows to run command and do LookupPath.
// It's mostly a wrapper around Golang os/exec that known:
// * sudo: it could add sudo for command that needs root privilege
// * sudo.ws: if available, prefer sudo.wg rather than sudo-rs due to missing features.
// * hostroot path: when running in a container and you want to run a command on the host.
type Runner struct {
	hostRootPath     string
	gloutonRunAsRoot bool
}

var ErrTimeout = errors.New("command timed out")

func New(hostRootPath string) *Runner {
	return &Runner{
		hostRootPath:     hostRootPath,
		gloutonRunAsRoot: os.Getuid() == 0,
	}
}

type Option struct {
	RunAsRoot bool
	RunOnHost bool
	// InContainerPID runs the command inside the filesystem of the container whose init
	// process has that PID, using that container's own binaries and libraries rather than
	// the ones next to Glouton or on the host. Set it when what has to be read only exists
	// in a container, like the varnishstat matching a containerised Varnish.
	//
	// It takes precedence over RunOnHost, which asks for the opposite namespace, and
	// unlike RunOnHost it applies to a Glouton installed on the machine too: the container
	// is never the namespace Glouton already runs in.
	//
	// Reaching the container needs /proc of the machine it runs on, which for a Glouton in
	// a container means being started with the host's PID namespace.
	InContainerPID  int
	SkipInContainer bool
	CombinedOutput  bool
	// If GraceDelay is > 0, send TERM signal when Run() context expire and wait for GraceDelay before send KILL signal.
	// When GraceDelay is == 0, KILL signal is sent as soon as context expire.
	GraceDelay time.Duration
	Environ    []string
}

var (
	ErrUnknownHostroot  = errors.New("glouton is running in a container but hostroot is unset")
	ErrExecutionSkipped = errors.New("execution skipped when glouton run in a container")
)

// LookPath does the same as Golang exec.LookPath, but apply RunOnHost, InContainerPID and
// SkipInContainer option:
//   - When SkipInContainer is set, always said that command isn't found if Glouton run in a container
//   - When InContainerPID is set, the command is looked up in that container's mount namespace,
//     the same one Runner.Run() will chroot into.
//   - When RunOnHost is set, the command isn't looked up in the container mount namespace but in the host
//     mount namespace (using /hostroot mount point).
//     BUT the result will NOT include the /hostroot mount point part. It will be something like "/sbin/zpool"
//     for a executable found at /hostroot/sbin/zpool.
//     This allow to work with Runner.Run() which take care to prefix by mount point
//
// When Glouton is running outside a container, this function is actually just a call to Golang version.
func (r *Runner) LookPath(file string, option Option) (string, error) {
	if r.hostRootPath != "/" && option.SkipInContainer {
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}

	if r.hostRootPath == "" && option.InContainerPID > 0 {
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}

	// Looked up under the chroot Run() would use, and returned without that prefix, for the
	// same reason as the hostroot case below: Run() puts it back.
	if chrootPath := r.chrootPath(option); chrootPath != "" && option.InContainerPID > 0 {
		return lookPathUnder(chrootPath, file)
	}

	if r.hostRootPath == "" && option.RunOnHost {
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}

	if r.hostRootPath == "/" || !option.RunOnHost {
		return exec.LookPath(file)
	}

	if strings.Contains(file, "/") {
		hostRootFile := filepath.Join(r.hostRootPath, file)

		return exec.LookPath(hostRootFile)
	}

	hostRootPathWithLastSlash := strings.TrimSuffix(r.hostRootPath, string(os.PathSeparator))

	path := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(path) {
		dir = filepath.Join(r.hostRootPath, dir)
		path := filepath.Join(dir, file)

		// Use exec.LookPath even if we don't lookup $PATH, as this allow
		// to re-use Golang findExecutable implementation.
		fullPath, err := exec.LookPath(path)
		if err == nil {
			return strings.TrimPrefix(fullPath, hostRootPathWithLastSlash), nil
		}
	}

	return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
}

// lookPathUnder looks a command up inside root, and returns its path relative to root so
// that a caller chrooting into root can use it as-is.
func lookPathUnder(root string, file string) (string, error) {
	rootWithoutLastSlash := strings.TrimSuffix(root, string(os.PathSeparator))

	if strings.Contains(file, "/") {
		if _, err := exec.LookPath(filepath.Join(root, file)); err != nil {
			return "", err
		}

		return file, nil
	}

	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		fullPath, err := exec.LookPath(filepath.Join(root, dir, file))
		if err == nil {
			return strings.TrimPrefix(fullPath, rootWithoutLastSlash), nil
		}
	}

	return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
}

func (r *Runner) ResolvePath(file string, option Option) (string, error) {
	if r.hostRootPath != "/" && option.SkipInContainer {
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}

	if r.hostRootPath == "" && option.InContainerPID > 0 {
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}

	if chrootPath := r.chrootPath(option); chrootPath != "" && option.InContainerPID > 0 {
		return filepath.Join(chrootPath, file), nil
	}

	if r.hostRootPath == "" && option.RunOnHost {
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}

	if r.hostRootPath == "/" || !option.RunOnHost {
		return file, nil
	}

	return filepath.Join(r.hostRootPath, file), nil
}

// UseSudoRS tells whether command that need root privilege will be
// executed using sudo-rs. This runner try to prefer sudo-ws when available.
// This function also return false when no sudo is needed at all, because
// Glouton is already root for example.
func (r *Runner) UseSudoRS(ctx context.Context) bool {
	if r.gloutonRunAsRoot {
		return false
	}

	name := r.getSudoCommand()

	if name != "sudo" {
		// A alternative sudo was found, it's not sudo-rs
		return false
	}

	cmd := exec.CommandContext(ctx, name, "--version")

	out, err := cmd.CombinedOutput()
	if err != nil {
		// Unsure... maybe sudo isn't installed ? Fallback on saying sudo-rs isn't used.
		return false
	}

	return bytes.Contains(out, []byte("sudo-rs"))
}

// getSudoCommand returns the command to do a sudo. Default to "sudo" but
// use "sudo.ws" if present.
func (r *Runner) getSudoCommand() string {
	if _, err := os.Stat("/usr/bin/sudo.ws"); err == nil {
		return "sudo.ws"
	}

	return "sudo"
}

// chrootPath returns the directory the command has to be chrooted into, or "" to run it
// in Glouton's own mount namespace.
func (r *Runner) chrootPath(option Option) string {
	if option.InContainerPID > 0 {
		// The container's filesystem, named from wherever Glouton can read /proc: a
		// Glouton in a container reaches it through its hostroot mount, one installed on
		// the machine reads /proc directly.
		//
		// An unset hostroot is neither of those -- it means Glouton cannot tell where the
		// machine's filesystem is -- so no directory is named for it and the callers turn
		// that into ErrUnknownHostroot rather than reading Glouton's own /proc.
		if r.hostRootPath == "" {
			return ""
		}

		procRoot := filepath.Join("/proc", strconv.Itoa(option.InContainerPID), "root")

		if r.hostRootPath != "/" {
			return filepath.Join(r.hostRootPath, procRoot)
		}

		return procRoot
	}

	if r.hostRootPath != "/" && option.RunOnHost {
		return r.hostRootPath
	}

	return ""
}

func (r *Runner) makeCmd(ctx context.Context, option Option, name string, arg ...string) (*exec.Cmd, func(error) error, error) {
	if r.hostRootPath != "/" && option.SkipInContainer {
		return nil, nil, ErrExecutionSkipped
	}

	if r.hostRootPath == "" && (option.RunOnHost || option.InContainerPID > 0) {
		return nil, nil, ErrUnknownHostroot
	}

	if chrootPath := r.chrootPath(option); chrootPath != "" {
		// chroot is needed to run the command in another mount namespace than Glouton's
		arg = append([]string{chrootPath, name}, arg...)
		name = "chroot"
	}

	if option.RunAsRoot && !r.gloutonRunAsRoot {
		arg = append([]string{"-n", name}, arg...)
		name = r.getSudoCommand()
	}

	fullCommand := name + " " + strings.Join(arg, " ")

	logger.V(2).Printf("running command %s", fullCommand)

	cmd := exec.CommandContext(ctx, name, arg...)

	if option.Environ != nil {
		cmd.Env = option.Environ
	}

	var (
		l        sync.Mutex
		termSent bool
	)

	if option.GraceDelay > 0 {
		cmd.Cancel = func() error {
			logger.V(2).Printf("command %s timeout, killing with SIGTERM", fullCommand)

			l.Lock()

			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				logger.V(2).Printf("command %s: unable to send term signal: %v", fullCommand, err)
			}

			termSent = true

			l.Unlock()

			return os.ErrProcessDone
		}
		cmd.WaitDelay = option.GraceDelay
	}

	handleErrorFn := func(err error) error {
		if option.GraceDelay > 0 {
			l.Lock()
			defer l.Unlock()

			// If the program was killed and didn't finish successfully, use ErrTimeout.
			// If kept err == nil if program successfully completed after receiving a sig term.
			if err != nil && termSent {
				err = ErrTimeout
			}
		}

		return err
	}

	return cmd, handleErrorFn, nil
}

func (r *Runner) Run(ctx context.Context, option Option, name string, arg ...string) ([]byte, error) {
	cmd, handleError, err := r.makeCmd(ctx, option, name, arg...)
	if err != nil {
		return nil, err
	}

	var out []byte

	if option.CombinedOutput {
		out, err = cmd.CombinedOutput()
	} else {
		out, err = cmd.Output()
	}

	err = handleError(err)

	return out, err
}

func (r *Runner) StartWithPipes(ctx context.Context, option Option, name string, arg ...string) (
	stdoutPipe, stderrPipe io.ReadCloser,
	wait func() error,
	err error,
) {
	cmd, _, err := r.makeCmd(ctx, option, name, arg...)
	if err != nil {
		return nil, nil, nil, err
	}

	stdoutPipe, err = cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("can't get stdout pipe: %w", err)
	}

	stderrPipe, err = cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("can't get stderr pipe: %w", err)
	}

	return stdoutPipe, stderrPipe, cmd.Wait, cmd.Start()
}
