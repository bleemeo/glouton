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

package facts

import (
	"context"
	"maps"
	"time"
)

// FactProviderMock provides only hardcoded facts but is useful for testing.
type FactProviderMock struct {
	facts map[string]string
}

// NewMockFacter creates a new lying Fact provider.
func NewMockFacter(facts map[string]string) *FactProviderMock {
	if facts == nil {
		facts = make(map[string]string)
	}

	return &FactProviderMock{
		facts: facts,
	}
}

// Facts returns the list of facts for this system.
func (f *FactProviderMock) Facts(_ context.Context, maxAge time.Duration) (facts map[string]string, err error) {
	_ = maxAge

	cpy := make(map[string]string, len(f.facts))
	maps.Copy(cpy, f.facts)

	return cpy, nil
}

// SetFact override/add a manual facts
//
// Any fact set using this method is valid until next call to SetFact.
func (f *FactProviderMock) SetFact(key string, value string) {
	f.facts[key] = value
}
