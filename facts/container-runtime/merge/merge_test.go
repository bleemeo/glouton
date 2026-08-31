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

package merge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bleemeo/glouton/facts"
	crTypes "github.com/bleemeo/glouton/facts/container-runtime/types"
	"github.com/bleemeo/glouton/types"
)

var errRuntimeDown = errors.New("runtime is down")

// fakeRuntime is a crTypes.RuntimeInterface stub whose Containers()/LastEnumerationComplete() answers are
// set per test. Only those two are exercised; everything else panics so a future caller can't silently
// depend on unimplemented behavior.
type fakeRuntime struct {
	containers []facts.Container
	err        error
	complete   bool
}

func (f *fakeRuntime) Containers(context.Context, time.Duration, bool) ([]facts.Container, error) {
	return f.containers, f.err
}

func (f *fakeRuntime) EnumerateContainers(context.Context, time.Duration, bool) ([]facts.Container, bool, error) {
	return f.containers, f.complete, f.err
}

func (f *fakeRuntime) CachedContainer(string) (facts.Container, bool)       { panic("not implemented") }
func (f *fakeRuntime) ContainerLastKill(string) time.Time                   { panic("not implemented") }
func (f *fakeRuntime) ContainerLastDelete(string) time.Time                 { panic("not implemented") }
func (f *fakeRuntime) ContainerByNameLastDelete(string) time.Time           { panic("not implemented") }
func (f *fakeRuntime) ContainerTerminationGracePeriod(string) time.Duration { panic("not implemented") }
func (f *fakeRuntime) ContainerExists(string) bool                          { panic("not implemented") }
func (f *fakeRuntime) Events() <-chan facts.ContainerEvent                  { panic("not implemented") }
func (f *fakeRuntime) IsRuntimeRunning(context.Context) bool                { panic("not implemented") }
func (f *fakeRuntime) LastUpdate() time.Time                                { panic("not implemented") }
func (f *fakeRuntime) Run(context.Context) error                            { panic("not implemented") }

func (f *fakeRuntime) Exec(context.Context, string, []string) ([]byte, error) {
	panic("not implemented")
}

func (f *fakeRuntime) ProcessWithCache() facts.ContainerRuntimeProcessQuerier {
	panic("not implemented")
}

func (f *fakeRuntime) RuntimeFact(context.Context, map[string]string) map[string]string {
	panic("not implemented")
}

func (f *fakeRuntime) Metrics(context.Context, time.Time) ([]types.MetricPoint, error) {
	panic("not implemented")
}

func (f *fakeRuntime) MetricsMinute(context.Context, time.Time) ([]types.MetricPoint, error) {
	panic("not implemented")
}

func (f *fakeRuntime) DiagnosticArchive(context.Context, types.ArchiveWriter) error {
	panic("not implemented")
}

// TestRuntimeEnumerateContainersCompleteness covers the cases where neither the returned error nor the length of
// the returned list reveals that the merged container list is incomplete. Both matter because callers act
// irreversibly on a container's absence (otel/logsource and otel/logprocessing forget its persisted log
// read offset, which cannot be recovered), so "one runtime couldn't answer" must never look like "those
// containers were destroyed".
func TestRuntimeEnumerateContainersCompleteness(t *testing.T) {
	t.Parallel()

	ctrA := facts.FakeContainer{FakeID: "a", FakeContainerName: "ctr-a"}
	ctrB := facts.FakeContainer{FakeID: "b", FakeContainerName: "ctr-b"}

	testCases := []struct {
		name         string
		runtimes     []crTypes.RuntimeInterface
		wantComplete bool
		wantCount    int
		wantErr      bool
	}{
		{
			name: "every runtime enumerated",
			runtimes: []crTypes.RuntimeInterface{
				&fakeRuntime{containers: []facts.Container{ctrA}, complete: true},
				&fakeRuntime{containers: []facts.Container{ctrB}, complete: true},
			},
			wantComplete: true,
			wantCount:    2,
		},
		{
			// The reported failure mode: one runtime is down while the other works, so the list is
			// non-empty and globalErr is nil, yet ctrB is missing from it.
			name: "one runtime errored, the other returned containers",
			runtimes: []crTypes.RuntimeInterface{
				&fakeRuntime{containers: []facts.Container{ctrA}, complete: true},
				&fakeRuntime{err: errRuntimeDown},
			},
			wantComplete: false,
			wantCount:    1,
		},
		{
			// Worse: a runtime that swallowed its own error (docker/containerd do this until they have
			// worked once) reports no error AND no containers, so nothing in the return values hints at it.
			name: "one runtime swallowed its error, the other returned containers",
			runtimes: []crTypes.RuntimeInterface{
				&fakeRuntime{containers: []facts.Container{ctrA}, complete: true},
				&fakeRuntime{complete: false},
			},
			wantComplete: false,
			wantCount:    1,
		},
		{
			// A genuinely container-less host: complete, so a caller may drop what it was tracking.
			name: "all runtimes enumerated nothing",
			runtimes: []crTypes.RuntimeInterface{
				&fakeRuntime{complete: true},
				&fakeRuntime{complete: true},
			},
			wantComplete: true,
			wantCount:    0,
		},
		{
			name: "every runtime errored",
			runtimes: []crTypes.RuntimeInterface{
				&fakeRuntime{err: errRuntimeDown},
				&fakeRuntime{err: errRuntimeDown},
			},
			wantComplete: false,
			wantCount:    0,
			wantErr:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runtime := &Runtime{
				Runtimes:         tc.runtimes,
				ContainerIgnored: func(facts.Container) bool { return false },
			}

			containers, complete, err := runtime.EnumerateContainers(t.Context(), time.Minute, false)
			if (err != nil) != tc.wantErr {
				t.Fatalf("EnumerateContainers() error = %v, wantErr = %v", err, tc.wantErr)
			}

			if len(containers) != tc.wantCount {
				t.Errorf("EnumerateContainers() returned %d container(s), want %d", len(containers), tc.wantCount)
			}

			if complete != tc.wantComplete {
				t.Errorf("EnumerateContainers() complete = %v, want %v", complete, tc.wantComplete)
			}
		})
	}
}
