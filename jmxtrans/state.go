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

package jmxtrans

import (
	"sync"
	"time"

	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/types"
)

// stateWindow is how far back the collection state looks: the TTL of the points jmxtrans
// sends, and many flush intervals, so that the state does not change between two flushes.
const stateWindow = 5 * time.Minute

const (
	errMsgNotConnected = "jmxtrans is not connected to Glouton"
	errMsgNoMetrics    = "no metrics received through JMX"
)

// collectionState records what jmxtrans sent, per service. Its zero value is ready to use.
type collectionState struct {
	l sync.Mutex
	// now is time.Now when nil. Tests set it.
	now func() time.Time

	// configuredAt is when each service was first given to jmxtrans, which gets a window
	// before nothing received counts as an error.
	configuredAt map[discovery.NameInstance]time.Time
	// received is, per service, when each metric name was last received.
	received map[discovery.NameInstance]map[string]time.Time
	// connections is how many jmxtrans connections are open, and disconnectedAt when one last
	// closed.
	connections    int
	disconnectedAt time.Time
}

// clock returns the current time. now is only set before first use, so it needs no lock.
func (s *collectionState) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}

	return s.now()
}

// setServices records the services jmxtrans collects, forgetting the others.
func (s *collectionState) setServices(services []discovery.Service) {
	s.l.Lock()
	defer s.l.Unlock()

	if s.configuredAt == nil {
		s.configuredAt = make(map[discovery.NameInstance]time.Time)
		s.received = make(map[discovery.NameInstance]map[string]time.Time)
	}

	now := s.clock()
	current := make(map[discovery.NameInstance]bool, len(services))

	for _, service := range services {
		if !isCollected(service) {
			continue
		}

		key := discovery.NameInstance{Name: service.Name, Instance: service.Instance}
		current[key] = true

		if _, ok := s.configuredAt[key]; !ok {
			s.configuredAt[key] = now
		}
	}

	for key := range s.configuredAt {
		if !current[key] {
			delete(s.configuredAt, key)
			delete(s.received, key)
		}
	}
}

// pointReceived records a point jmxtrans sent.
func (s *collectionState) pointReceived(point types.MetricPoint) {
	key := discovery.NameInstance{Name: point.Annotations.ServiceName, Instance: point.Annotations.ServiceInstance}
	name := point.Labels[types.LabelName]
	now := s.clock()

	s.l.Lock()
	defer s.l.Unlock()

	if _, ok := s.configuredAt[key]; !ok {
		return
	}

	names, ok := s.received[key]
	if !ok {
		names = make(map[string]time.Time)
		s.received[key] = names
	}

	names[name] = now
}

func (s *collectionState) connected() {
	s.l.Lock()
	defer s.l.Unlock()

	s.connections++
}

func (s *collectionState) disconnected() {
	s.l.Lock()
	defer s.l.Unlock()

	s.connections--
	s.disconnectedAt = s.clock()
}

// ServiceScrapeState implements discovery.JMXStateProvider: the number of metric names
// received in the last stateWindow or, once a service was given to jmxtrans a whole window
// ago with nothing received, why.
func (s *collectionState) ServiceScrapeState(service discovery.NameInstance) types.ScrapeState {
	s.l.Lock()
	defer s.l.Unlock()

	now := s.clock()

	configuredAt, ok := s.configuredAt[service]
	if !ok {
		return types.ScrapeState{}
	}

	count := 0

	for name, receivedAt := range s.received[service] {
		if now.Sub(receivedAt) < stateWindow {
			count++
		} else {
			delete(s.received[service], name)
		}
	}

	if count > 0 {
		return types.ScrapeState{HasRun: true, FamiliesCount: count}
	}

	if now.Sub(configuredAt) < stateWindow {
		return types.ScrapeState{}
	}

	if s.connections == 0 && now.Sub(s.disconnectedAt) >= stateWindow {
		return types.ScrapeState{HasRun: true, Error: errMsgNotConnected}
	}

	return types.ScrapeState{HasRun: true, Error: errMsgNoMetrics}
}
