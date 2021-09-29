// Copyright 2015-2021 Bleemeo
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
	"glouton/facts"
	"glouton/prometheus/registry"
	"glouton/prometheus/scrapper"
	"glouton/types"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Target represents a snmp config instance.
type Target struct {
	opt             TargetOptions
	exporterAddress *url.URL
	manager         *Manager

	l              sync.Mutex
	facts          map[string]string
	lastFactUpdate time.Time

	// MockFacts could be used for testing. It bypass real facts discovery.
	MockFacts map[string]string
}

type TargetOptions struct {
	Address     string
	InitialName string
}

func New(opt TargetOptions, exporterAddress *url.URL) *Target {
	return newTarget(opt, exporterAddress, nil)
}

func NewMock(opt TargetOptions, mockFacts map[string]string) *Target {
	r := newTarget(opt, nil, nil)
	r.MockFacts = mockFacts

	return r
}

func newTarget(opt TargetOptions, exporterAddress *url.URL, mgr *Manager) *Target {
	return &Target{
		opt:             opt,
		exporterAddress: exporterAddress,
		manager:         mgr,
	}
}

func (t *Target) Address() string {
	return t.opt.Address
}

func (t *Target) Module() string {
	return "if_mib"
}

func (t *Target) Name(ctx context.Context) (string, error) {
	if t.opt.InitialName != "" {
		return t.opt.InitialName, nil
	}

	facts, err := t.Facts(ctx, 48*time.Hour)
	if err != nil {
		return "", err
	}

	if facts["fqdn"] != "" {
		return facts["fqdn"], nil
	}

	return t.opt.Address, nil
}

func (t *Target) scrapeTarget(module string) *scrapper.Target {
	u := t.exporterAddress.ResolveReference(&url.URL{}) // clone URL
	qs := u.Query()
	qs.Set("module", module)
	qs.Set("target", t.opt.Address)
	u.RawQuery = qs.Encode()

	target := &scrapper.Target{
		ExtraLabels: map[string]string{
			types.LabelMetaScrapeJob: t.opt.InitialName,
			// HostPort could be empty, but this ExtraLabels is used by Registry which
			// correctly handle empty value value (drop the label).
			types.LabelMetaScrapeInstance: scrapper.HostPort(u),
			types.LabelMetaSNMPTarget:     t.opt.Address,
		},
		URL:       u,
		AllowList: []string{},
		DenyList:  []string{},
	}

	return target
}

func (t *Target) String() string {
	return fmt.Sprintf("initial_name=%s target=%s module=%s", t.opt.InitialName, t.opt.Address, t.Module())
}

func (t *Target) Facts(ctx context.Context, maxAge time.Duration) (facts map[string]string, err error) {
	t.l.Lock()
	defer t.l.Unlock()

	if t.exporterAddress == nil || t.MockFacts != nil {
		return t.MockFacts, nil
	}

	if time.Since(t.lastFactUpdate) < maxAge {
		return t.facts, nil
	}

	tgt := t.scrapeTarget("discovery")

	tmp, err := tgt.GatherWithState(ctx, registry.GatherState{})
	if err != nil {
		return nil, err
	}

	result := registry.FamiliesToMetricPoints(time.Now(), tmp)

	t.facts = factFromPoints(result, time.Now())
	t.lastFactUpdate = time.Now()

	return t.facts, nil
}

func factFromPoints(points []types.MetricPoint, now time.Time) map[string]string {
	result := make(map[string]string)

	convertMap := map[string]string{
		"dot1dBaseBridgeAddress": "primary_mac_address",
		"entPhysicalFirmwareRev": "boot_version",
		"entPhysicalSerialNum":   "serial_number",
		"entPhysicalSoftwareRev": "version",
		"ipAdEntAddr":            "primary_address",
		"sysDescr":               "product_name",
		"sysName":                "fqdn",
	}

	for _, p := range points {
		key := p.Labels[types.LabelName]
		value := p.Labels[key]
		target := convertMap[key]

		if target == "" || value == "" {
			continue
		}

		if result[target] == "" {
			result[target] = value
		}
	}

	result["fact_updated_at"] = now.UTC().Format(time.RFC3339)
	result["primary_mac_address"] = strings.ToLower(result["primary_mac_address"])
	result["hostname"] = result["fqdn"]

	if strings.Contains(result["fqdn"], ".") {
		l := strings.SplitN(result["fqdn"], ".", 2)
		result["hostname"] = l[0]
		result["domain"] = l[1]
	}

	facts.CleanFacts(result)

	return result
}
