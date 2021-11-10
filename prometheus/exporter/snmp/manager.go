package snmp

import (
	"context"
	"glouton/logger"
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
func NewManager(exporterAddress *url.URL, scaperFact FactProvider, targets ...TargetOptions) *Manager {
	mgr := &Manager{
		exporterAddress: exporterAddress,
		targets:         make([]*Target, 0, len(targets)),
	}

	for _, t := range targets {
		mgr.targets = append(mgr.targets, newTarget(t, scaperFact, exporterAddress))
	}

	return mgr
}

// OnlineCount return the number of target that are available (e.g. for which Facts worked).
// To have accurate value, Facts should be used, else the value will be updated
// by OnlineCount in *background* (meaning value will be available on later call to OnlineCount).
func (m *Manager) OnlineCount() int {
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