// Copyright 2015-2019 Bleemeo
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
	result []Service
}

// NewMockDiscoverer crates a new mock Discoverer for tests.
func NewMockDiscoverer() *MockDiscoverer {
	return &MockDiscoverer{
		result: []Service{},
	}
}

// Discovery implements Discoverer.
func (md MockDiscoverer) Discovery(ctx context.Context, maxAge time.Duration) (services []Service, err error) {
	return md.result, nil
}

// LastUpdate implements Discoverer.
func (md MockDiscoverer) LastUpdate() time.Time {
	return time.Now()
}

// RemoveIfNonRunning implements PersistentDiscoverer.
func (md MockDiscoverer) RemoveIfNonRunning(ctx context.Context, services []Service) {}
