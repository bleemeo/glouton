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
	"slices"
	"testing"

	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/types"
)

// TestQueueSizeState checks the state discovery reads for the service types whose metric the
// agent gathers itself: their types, then each service's last gather.
func TestQueueSizeState(t *testing.T) {
	state := &queueSizeState{}
	postfix := discovery.NameInstance{Name: "postfix"}
	exim := discovery.NameInstance{Name: "exim"}

	want := []discovery.ServiceName{discovery.EximService, discovery.PostfixService}
	if got := state.ServiceTypes(); !slices.Equal(got, want) {
		t.Errorf("ServiceTypes() = %v, want %v", got, want)
	}

	if got := state.ServiceScrapeState(postfix); got.HasRun {
		t.Errorf("before any gather, got %+v, want nothing", got)
	}

	state.set(map[discovery.NameInstance]string{postfix: "", exim: "mailq: not found"})

	if got := state.ServiceScrapeState(postfix); got != (types.ScrapeState{HasRun: true}) {
		t.Errorf("after a successful gather, got %+v", got)
	}

	if got := state.ServiceScrapeState(exim); got != (types.ScrapeState{HasRun: true, Error: "mailq: not found"}) {
		t.Errorf("after a failed gather, got %+v", got)
	}

	// A service no longer gathered is forgotten.
	state.set(map[discovery.NameInstance]string{postfix: ""})

	if got := state.ServiceScrapeState(exim); got.HasRun {
		t.Errorf("a service no longer gathered still has a state: %+v", got)
	}

	// The appender may have no state, in tests.
	var none *queueSizeState

	none.set(nil)
}
