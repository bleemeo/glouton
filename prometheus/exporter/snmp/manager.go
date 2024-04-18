// Copyright 2015-2023 Bleemeo
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

package snmp

import (
	"context"
	"fmt"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/logger"
	"net/url"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type FactProvider interface {
	Facts(ctx context.Context, maxAge time.Duration) (facts map[string]string, err error)
}

type Manager struct {
	exporterAddress *url.URL
	targets         []*Target

	l                  sync.Mutex
	checkOnlinePending bool
}

type GathererWithInfo struct {
	Gatherer    prometheus.Gatherer
	Address     string
	ExtraLabels map[string]string
}

// NewManager return a new SNMP manager.
func NewManager(exporterAddress string, scaperFact FactProvider, targets []config.SNMPTarget) (*Manager, prometheus.MultiError) {
	var warnings prometheus.MultiError

	exporterURL, err := url.Parse(exporterAddress)
	if err != nil {
		warnings.Append(err)

		return nil, warnings
	}

	exporterURL, err = exporterURL.Parse("snmp")
	if err != nil {
		warnings.Append(err)

		return nil, warnings
	}

	mgr := &Manager{
		exporterAddress: exporterURL,
		targets:         make([]*Target, 0, len(targets)),
	}

	targetExists := make(map[string]bool)

	for i, t := range targets {
		if t.Target == "" {
			warnings.Append(fmt.Errorf("%w: metric.snmp.targets[%d] must have a target value", config.ErrInvalidValue, i))

			continue
		}

		if targetExists[t.Target] {
			warnings.Append(fmt.Errorf("%w: the SNMP target %s is duplicated", config.ErrInvalidValue, t.Target))

			continue
		}

		mgr.targets = append(mgr.targets, newTarget(t, scaperFact, exporterURL))
	}

	return mgr, warnings
}

// OnlineCount return the number of target that are available (e.g. for which Facts worked).
// To have accurate value, Facts should be used, else the value will be updated
// by OnlineCount in *background* (meaning value will be available on later call to OnlineCount).
func (m *Manager) OnlineCount() int {
	if m == nil {
		return 0
	}

	count := 0

	var needCheck []*Target

	for _, t := range m.targets {
		t.l.Lock()

		if t.lastFactErr == nil {
			count++
		} else {
			needCheck = append(needCheck, t)
		}

		t.l.Unlock()
	}

	if len(needCheck) > 0 {
		m.l.Lock()
		defer m.l.Unlock()

		if !m.checkOnlinePending {
			m.checkOnlinePending = true

			go func() {
				defer crashreport.ProcessPanic()

				m.checkTargets(needCheck)

				m.l.Lock()
				m.checkOnlinePending = false
				m.l.Unlock()
			}()
		}
	}

	return count
}

func (m *Manager) checkTargets(targets []*Target) {
	for _, t := range targets {
		logger.V(2).Printf("testing target %v", t.Address())
		_, _ = t.Facts(context.Background(), 5*time.Minute)
	}
}

// Gatherers return gatheres for SNMP metrics of each targets.
func (m *Manager) Gatherers() []GathererWithInfo {
	if m == nil {
		return nil
	}

	result := make([]GathererWithInfo, 0, len(m.targets))

	for _, t := range m.targets {
		result = append(result, GathererWithInfo{
			Gatherer:    t,
			Address:     t.Address(),
			ExtraLabels: t.extraLabels(),
		})
	}

	return result
}

// Targets return current SNMP target. The result list shouldn't be by caller mutated.
func (m *Manager) Targets() []*Target {
	if m == nil {
		return nil
	}

	return m.targets
}
