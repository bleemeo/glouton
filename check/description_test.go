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

package check

import (
	"context"
	"testing"

	"github.com/bleemeo/glouton/types"
)

// TestMainCheckDescriptionIsKept covers what a check publishes when its main check is the
// whole check -- no TCP address to probe besides, which is the case for every NTP, UDP,
// process and address-less Nagios check.
//
// The description is what the panel shows next to the service, and it is the only record of
// what was actually probed: without it a healthy service is up for no stated reason.
func TestMainCheckDescriptionIsKept(t *testing.T) {
	const description = "NTP OK - 1.2ms response time"

	bc := newBase("", nil, false, func(context.Context) types.StatusDescription {
		return types.StatusDescription{CurrentStatus: types.StatusOk, StatusDescription: description}
	}, nil, types.MetricAnnotations{}, nil) //nolint:exhaustruct

	got := bc.doCheck(t.Context())

	if got.CurrentStatus != types.StatusOk {
		t.Errorf("doCheck() status = %v, want ok", got.CurrentStatus)
	}

	if got.StatusDescription != description {
		t.Errorf("doCheck() description = %q, want %q", got.StatusDescription, description)
	}
}

// TestNoMainCheckStillReportsOk covers the other side of that branch: with no main check and
// no TCP address, nothing was probed and there is nothing to describe.
//
// The status has to be built rather than passed through, because the zero value of
// types.Status is StatusUnset -- returning it would report a healthy service as unset.
func TestNoMainCheckStillReportsOk(t *testing.T) {
	bc := newBase("", nil, false, nil, nil, types.MetricAnnotations{}, nil) //nolint:exhaustruct

	got := bc.doCheck(t.Context())

	if got.CurrentStatus != types.StatusOk {
		t.Errorf("doCheck() status = %v, want ok", got.CurrentStatus)
	}

	if got.StatusDescription != "" {
		t.Errorf("doCheck() described %q though nothing was probed", got.StatusDescription)
	}
}

// TestMainCheckDescriptionKeptWhenNotOk pins that a failure reports its reason, which it
// does through an earlier return than the one above -- so it holds whether or not there are
// TCP addresses to check.
func TestMainCheckDescriptionKeptWhenNotOk(t *testing.T) {
	const reason = "No process matched"

	bc := newBase("", []string{"127.0.0.1:1"}, false, func(context.Context) types.StatusDescription {
		return types.StatusDescription{CurrentStatus: types.StatusCritical, StatusDescription: reason}
	}, nil, types.MetricAnnotations{}, nil) //nolint:exhaustruct

	if got := bc.doCheck(t.Context()); got.StatusDescription != reason {
		t.Errorf("doCheck() description = %q, want %q: a failure must say why", got.StatusDescription, reason)
	}
}
