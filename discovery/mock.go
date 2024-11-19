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

package discovery

import (
	"context"
	"time"
)

// MockDiscoverer is useful for tests.
type MockDiscoverer struct {
	result    []Service
	err       error
	UpdatedAt time.Time
}

// SetResult fill result of Discovery.
func (md *MockDiscoverer) SetResult(services []Service, err error) {
	md.result = services
	md.err = err
}

// Discovery implements Discoverer.
func (md *MockDiscoverer) Discovery(_ context.Context, maxAge time.Duration) (services []Service, err error) {
	_ = maxAge

	return md.result, md.err
}

// LastUpdate implements Discoverer.
func (md *MockDiscoverer) LastUpdate() time.Time {
	if md.UpdatedAt.IsZero() {
		return time.Now()
	}

	return md.UpdatedAt
}

// RemoveIfNonRunning implements PersistentDiscoverer.
func (md *MockDiscoverer) RemoveIfNonRunning(context.Context, []Service) {}
