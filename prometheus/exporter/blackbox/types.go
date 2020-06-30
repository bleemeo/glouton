package blackbox

import (
	"glouton/prometheus/registry"
	"time"

	bbConf "github.com/prometheus/blackbox_exporter/config"
)

// configTarget is the information we will supply to the probe() function.
type configTarget struct {
	Name           string
	URL            string
	Module         bbConf.Module
	ModuleName     string
	BleemeoAgentID string
	RefreshRate    time.Duration
}

// We define labels to apply on a specific collector at registration, as those labels cannot be exposed
// while gathering (e.g. labels prefixed by '__').
type collectorWithLabels struct {
	collector configTarget
	labels    map[string]string
}

// RegisterManager is an abstraction that allows us to reload blackbox at runtime, enabling and disabling
// probes at will.
type RegisterManager struct {
	targets       []collectorWithLabels
	scraperName   string
	dynamicMode   bool
	registrations map[int]collectorWithLabels
	registry      *registry.Registry
}
