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

package gloutonexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bleemeo/glouton/logger"
)

// Runner allows to run command and do LookupPath.
// It's mostly a wrapper around Golang os/exec that known:
// * sudo: it could add sudo for command that needs root privilege
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
	RunAsRoot       bool
	RunOnHost       bool
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

// LookPath does the same as Golang exec.LookPath, but apply RunOnHost and SkipInContainer option:
//   - When SkipInContainer is set, always said that command isn't found if Glouton run in a container
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

func (r *Runner) makeCmd(ctx context.Context, option Option, name string, arg ...string) (*exec.Cmd, func(error) error, error) {
	if r.hostRootPath != "/" && option.SkipInContainer {
		return nil, nil, ErrExecutionSkipped
	}

	if r.hostRootPath == "" && option.RunOnHost {
		return nil, nil, ErrUnknownHostroot
	}

	if r.hostRootPath != "/" && option.RunOnHost {
		// chroot is needed to run the command on host mount namespace
		arg = append([]string{r.hostRootPath, name}, arg...)
		name = "chroot"
	}

	if option.RunAsRoot && !r.gloutonRunAsRoot {
		arg = append([]string{"-n", name}, arg...)
		name = "sudo"
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
