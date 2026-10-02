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

// stateWindow is how far back the collection state looks. It is the time-to-live of the
// points jmxtrans sends, so a metric counts as collected while its points are still
// there; and it is long enough, compared to jmxtrans' flush interval, that the state does
// not change between two flushes.
const stateWindow = 5 * time.Minute

const (
	errMsgNotConnected = "jmxtrans is not connected to Glouton"
	errMsgNoMetrics    = "no metrics received through JMX"
)

// collectionState records what jmxtrans sent, per service, so that the services state
// reported to Bleemeo can tell whether JMX works for each. Its zero value is ready to use.
type collectionState struct {
	l sync.Mutex
	// now is time.Now when nil. Tests set it.
	now func() time.Time

	// configuredAt is when each service was first given to jmxtrans, to leave it the time
	// of a window before reporting that nothing came.
	configuredAt map[discovery.NameInstance]time.Time
	// received is, per service, when each metric name was last received.
	received map[discovery.NameInstance]map[string]time.Time
	// connections is how many jmxtrans connections are open, and disconnectedAt when the
	// last one closed.
	connections    int
	disconnectedAt time.Time
}

// clock returns the current time. Must be called with s.l held.
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
		if len(GetJMXMetrics(service)) == 0 {
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

	names[name] = s.clock()
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

// ServiceScrapeState implements discovery.JMXStateProvider.
//
// The count is of the metric names received in the last stateWindow. An error is only
// reported once a service has been given to jmxtrans for a whole window with nothing
// received, so that a service just configured, or a jmxtrans just started, does not read
// as broken.
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
