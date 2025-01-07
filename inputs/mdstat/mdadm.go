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
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const mdadmTimeout = 5 * time.Second

var errStateNotFound = errors.New("array state not found in mdadm output")

type mdadmDetailsFunc func(array, mdadmPath string, useSudo bool) (mdadmInfo, error)

type mdadmInfo struct {
	state string
}

func callMdadm(array, mdadmPath string, useSudo bool) (mdadmInfo, error) {
	fullPath, err := exec.LookPath(mdadmPath)
	if err != nil {
		return mdadmInfo{}, err
	}

	fullCmd := []string{fullPath, "--detail", "/dev/" + array}

	if useSudo {
		fullCmd = append([]string{"sudo", "-n"}, fullCmd...)
	}

	ctx, cancel := context.WithTimeout(context.Background(), mdadmTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, fullCmd[0], fullCmd[1:]...) //nolint:gosec
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		var errOutput string

		if stderr.Len() > 0 {
			errOutput = " (stderr: " + stderr.String() + ")"
		}

		return mdadmInfo{}, fmt.Errorf("failed to run mdadm on array %s: %w%s", array, err, errOutput)
	}

	return parseMdadmOutput(stdout.String())
}

func parseMdadmOutput(output string) (mdadmInfo, error) {
	var info mdadmInfo

	for _, line := range strings.Split(output, "\n") {
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
