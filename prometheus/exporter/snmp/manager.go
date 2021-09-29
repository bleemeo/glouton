package snmp

import (
	"context"
	"net/url"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Manager struct {
	exporterAddress *url.URL
	targets         []*Target
}

type GathererWithInfo struct {
	Gatherer    prometheus.Gatherer
	Address     string
	ExtraLabels map[string]string
}

// NewManager return a new SNMP manager. It will handle SNMP target status information: if
// an target failed the last time, its status will be critical. On the other hand, if
// it succeeded, its status will be ok.
// The statuses of targets is sent by PushPoint method.
func NewManager(exporterAddress *url.URL, targets ...TargetOptions) *Manager {
	mgr := &Manager{
		exporterAddress: exporterAddress,
		targets:         make([]*Target, 0, len(targets)),
	}

	for _, t := range targets {
		mgr.targets = append(mgr.targets, newTarget(t, exporterAddress))
	}

	return mgr
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

// PushPoint send point for the status of each SNMP targets.
func (m *Manager) PushPoint(ctx context.Context, t0 time.Time) {
	if m == nil {
		return
	}
}

// Targets return current SNMP target. The result list shouldn't be by caller mutated.
func (m *Manager) Targets() []*Target {
	if m == nil {
		return nil
	}

	return m.targets
}
