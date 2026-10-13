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

	// configuredAt is when each service was given to jmxtrans with its current target, which
	// gets a window before nothing received counts as an error.
	configuredAt map[discovery.NameInstance]time.Time
	// targets is what jmxtrans connects to for each service.
	targets map[discovery.NameInstance]jmxTarget
	// receivedAt is, per service, when a point was last received.
	receivedAt map[discovery.NameInstance]time.Time
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
		s.receivedAt = make(map[discovery.NameInstance]time.Time)
		s.targets = make(map[discovery.NameInstance]jmxTarget)
	}

	now := s.clock()
	current := make(map[discovery.NameInstance]bool, len(services))

	for _, service := range services {
		if !isCollected(service) {
			continue
		}

		key := discovery.NameInstance{Name: service.Name, Instance: service.Instance}
		current[key] = true

		// A new target, as when the service's address changed, starts over: what was received
		// came from the previous one.
		target := targetOf(service)
		if _, ok := s.configuredAt[key]; !ok || s.targets[key] != target {
			s.configuredAt[key] = now
			s.targets[key] = target

			delete(s.receivedAt, key)
		}
	}

	for key := range s.configuredAt {
		if !current[key] {
			delete(s.configuredAt, key)
			delete(s.receivedAt, key)
			delete(s.targets, key)
		}
	}
}

// jmxTarget is what jmxtrans connects to for a service, as written in its configuration.
type jmxTarget struct {
	host     string
	port     int
	username string
	password string
}

func targetOf(service discovery.Service) jmxTarget {
	return jmxTarget{
		host:     service.IPAddress,
		port:     service.Config.JMXPort,
		username: service.Config.JMXUsername,
		password: service.Config.JMXPassword,
	}
}

// pointReceived records a point jmxtrans sent.
func (s *collectionState) pointReceived(point types.MetricPoint) {
	key := discovery.NameInstance{Name: point.Annotations.ServiceName, Instance: point.Annotations.ServiceInstance}
	now := s.clock()

	s.l.Lock()
	defer s.l.Unlock()

	if _, ok := s.configuredAt[key]; !ok {
		return
	}

	s.receivedAt[key] = now
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

// ServiceScrapeState implements discovery.JMXStateProvider: a run without error when metrics
// were received in the last stateWindow or, once a service was given to jmxtrans a whole window
// ago with nothing received, why.
func (s *collectionState) ServiceScrapeState(service discovery.NameInstance) types.ScrapeState {
	s.l.Lock()
	defer s.l.Unlock()

	now := s.clock()

	configuredAt, ok := s.configuredAt[service]
	if !ok {
		return types.ScrapeState{}
	}

	if receivedAt, ok := s.receivedAt[service]; ok && now.Sub(receivedAt) < stateWindow {
		return types.ScrapeState{Status: types.ScrapeRan, RegisteredAt: configuredAt}
	}

	if now.Sub(configuredAt) < stateWindow {
		return types.ScrapeState{}
	}

	if s.connections == 0 && now.Sub(s.disconnectedAt) >= stateWindow {
		return types.ScrapeState{Status: types.ScrapeRan, Error: errMsgNotConnected, RegisteredAt: configuredAt}
	}

	return types.ScrapeState{Status: types.ScrapeRan, Error: errMsgNoMetrics, RegisteredAt: configuredAt}
}
