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

//go:build linux

package mdstat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bleemeo/glouton/utils/gloutonexec"
)

const mdadmTimeout = 5 * time.Second

var errStateNotFound = errors.New("array state not found in mdadm output")

type mdadmDetailsFunc func(array, mdadmPath string, runner *gloutonexec.Runner) (mdadmInfo, error)

type mdadmInfo struct {
	state string
}

func callMdadm(array, mdadmPath string, runner *gloutonexec.Runner) (mdadmInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mdadmTimeout)
	defer cancel()

	stdout, stderr, cmdWait, err := runner.StartWithPipes(ctx, runnerOpt, mdadmPath, "--detail", "/dev/"+array)
	if err == nil {
		defer func() {
			_ = stdout.Close()
			_ = stderr.Close()
		}()

		err = cmdWait()
	}

	if err != nil {
		var errOutput string

		if stderrOutput, _ := io.ReadAll(stderr); len(stderrOutput) > 0 {
			errOutput = " (stderr: " + string(stderrOutput) + ")"
		}

		return mdadmInfo{}, fmt.Errorf("failed to run mdadm on array %s: %w%s", array, err, errOutput)
	}

	stdoutOutput, err := io.ReadAll(stdout)
	if err != nil {
		return mdadmInfo{}, err
	}

	return parseMdadmOutput(string(stdoutOutput))
}

func parseMdadmOutput(output string) (mdadmInfo, error) {
	var info mdadmInfo

	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, " : ", 2)
		if len(parts) != 2 {
			continue
		}

		switch parts[0] {
		case "State":
			info.state = parts[1]
		default:
			continue
		}
	}

	if info.state == "" {
		return mdadmInfo{}, errStateNotFound
	}

	return info, nil
}
