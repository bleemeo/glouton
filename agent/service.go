// Copyright 2015-2024 Bleemeo
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

package agent

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/utils/gloutonexec"
)

var (
	errRunInContainer   = errors.New("can't gather the postfix running on host because Glouton run in a container")
	errUnexpectedOutput = errors.New("postqueue output don't contains expected output")
)

var (
	postfixRECount = regexp.MustCompile(
		`-- \d+ Kbytes in (\d+) Request.`,
	)
	postfixREEmpty = regexp.MustCompile(
		`Mail queue is empty`,
	)
)

type dockerExecuter interface {
	Exec(ctx context.Context, containerID string, cmd []string) ([]byte, error)
}

func postfixQueueSize(ctx context.Context, srv discovery.Service, runner *gloutonexec.Runner, docker dockerExecuter) (float64, error) {
	if srv.ContainerID != "" {
		out, err := docker.Exec(ctx, srv.ContainerID, []string{"postqueue", "-p"})
		if err != nil {
			return 0, err
		}

		return parsePostfix(out)
	}

	out, err := runner.Run(ctx, gloutonexec.Option{RunOnHost: true}, "postqueue", "-p")
	if errors.Is(err, gloutonexec.ErrExecutionSkipped) {
		return 0, errRunInContainer
	} else if err != nil {
		return 0, err
	}

	return parsePostfix(out)
}

func parsePostfix(output []byte) (n float64, err error) {
	if postfixREEmpty.Match(output) {
		return 0, nil
	}

	result := postfixRECount.FindSubmatch(output)
	if len(result) == 0 {
		return 0, errUnexpectedOutput
	}

	return strconv.ParseFloat(string(result[1]), 64)
}

func eximQueueSize(ctx context.Context, srv discovery.Service, runner *gloutonexec.Runner, docker dockerExecuter) (float64, error) {
	if srv.ContainerID != "" {
		out, err := docker.Exec(ctx, srv.ContainerID, []string{"exim4", "-bpc"})
		if err != nil {
			return 0, err
		}

		return strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	}

	out, err := runner.Run(ctx, gloutonexec.Option{RunOnHost: true}, "exim4", "-bpc")
	if errors.Is(err, gloutonexec.ErrExecutionSkipped) {
		return 0, errRunInContainer
	} else if err != nil {
		return 0, err
	}

	return strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
}
