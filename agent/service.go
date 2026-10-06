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

package agent

import (
	"context"
	"errors"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"
)

var (
	errRunInContainer   = errors.New("can't gather the queue size of a mail server running on the host because Glouton runs in a container")
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

// queueSizeGatherer gathers the size of a mail server's queue, the one metric the agent
// collects itself for that service type, as no input does.
type queueSizeGatherer struct {
	metric string
	gather func(ctx context.Context, srv discovery.Service, runner *gloutonexec.Runner, docker dockerExecuter) (float64, error)
}

// queueSizeGatherers are the service types whose metric the agent gathers itself.
var queueSizeGatherers = map[discovery.ServiceName]queueSizeGatherer{ //nolint:gochecknoglobals
	discovery.PostfixService: {metric: "postfix_queue_size", gather: postfixQueueSize},
	discovery.EximService:    {metric: "exim_queue_size", gather: eximQueueSize},
}

// queueSizeState records how the last gather of each service's queue size went, so that
// discovery reports these services as supported, with their error. It implements
// discovery.AgentGathererStateProvider.
type queueSizeState struct {
	l sync.Mutex
	// errors is the error of each service's last gather, empty when it succeeded. A service
	// not gathered yet is absent.
	errors map[discovery.NameInstance]string
}

func (s *queueSizeState) ServiceTypes() []discovery.ServiceName {
	return slices.Sorted(maps.Keys(queueSizeGatherers))
}

func (s *queueSizeState) ServiceScrapeState(service discovery.NameInstance) types.ScrapeState {
	s.l.Lock()
	defer s.l.Unlock()

	message, ok := s.errors[service]
	if !ok {
		return types.ScrapeState{}
	}

	return types.ScrapeState{Status: types.ScrapeRan, Error: message}
}

// set records the services gathered in one run, forgetting the others.
func (s *queueSizeState) set(errors map[discovery.NameInstance]string) {
	if s == nil {
		return
	}

	s.l.Lock()
	defer s.l.Unlock()

	s.errors = errors
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
