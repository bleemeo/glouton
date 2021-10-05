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
	"bytes"
	"context"
	"errors"
	"fmt"
	"glouton/facts"
	"glouton/prometheus/registry"
	"glouton/prometheus/scrapper"
	"glouton/types"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gogo/protobuf/proto"
	dto "github.com/prometheus/client_model/go"
)

const defaultGatherTimeout = 10 * time.Second

// Target represents a snmp config instance.
type Target struct {
	opt             TargetOptions
	exporterAddress *url.URL

	l                sync.Mutex
	scraper          *scrapper.Target
	facts            map[string]string
	lastFactUpdate   time.Time
	lastFactErrAt    time.Time
	lastFactErr      error
	lastSuccess      time.Time
	lastErrorMessage string
	consecutiveErr   int

	// MockFacts could be used for testing. It bypass real facts discovery.
	MockFacts map[string]string
}

type TargetOptions struct {
	Address     string
	InitialName string
}

func New(opt TargetOptions, exporterAddress *url.URL) *Target {
	return newTarget(opt, exporterAddress)
}

func NewMock(opt TargetOptions, mockFacts map[string]string) *Target {
	r := newTarget(opt, nil)
	r.MockFacts = mockFacts

	return r
}

func newTarget(opt TargetOptions, exporterAddress *url.URL) *Target {
	return &Target{
		opt:             opt,
		exporterAddress: exporterAddress,
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

// Gather implement prometheus.Gatherer.
func (t *Target) Gather() ([]*dto.MetricFamily, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultGatherTimeout)
	defer cancel()

	return t.GatherWithState(ctx, registry.GatherState{})
}

func (t *Target) GatherWithState(ctx context.Context, state registry.GatherState) ([]*dto.MetricFamily, error) {
	t.l.Lock()

	if t.scraper == nil {
		t.scraper = t.buildScraper(t.Module())
	}

	t.l.Unlock()

	result, err := t.scraper.GatherWithState(ctx, state)

	if state.FromScrapeLoop {
		t.l.Lock()

		if err == nil {
			t.lastSuccess = time.Now()
			t.consecutiveErr = 0
		} else {
			t.lastErrorMessage = humanError(err)
			t.consecutiveErr++
		}

		err = nil

		t.l.Unlock()
	}

	var (
		totalInterfaces     int
		connectedInterfaces int
	)

	for _, mf := range result {
		if mf.GetName() == "ifOperStatus" {
			for _, m := range mf.Metric {
				totalInterfaces++

				if m.GetGauge().GetValue() == 1 {
					connectedInterfaces++
				}
			}
		}
	}

	if totalInterfaces > 0 {
		result = append(result, &dto.MetricFamily{
			Name: proto.String("total_interfaces"),
			Type: dto.MetricType_GAUGE.Enum(),
			Metric: []*dto.Metric{
				{
					Gauge: &dto.Gauge{
						Value: proto.Float64(float64(totalInterfaces)),
					},
				},
			},
		})

		result = append(result, &dto.MetricFamily{
			Name: proto.String("connected_interfaces"),
			Type: dto.MetricType_GAUGE.Enum(),
			Metric: []*dto.Metric{
				{
					Gauge: &dto.Gauge{
						Value: proto.Float64(float64(connectedInterfaces)),
					},
				},
			},
		})
	}

	if status, msg := t.getStatus(); status != types.StatusUnset {
		result = append(result, &dto.MetricFamily{
			Name: proto.String("agent_status"),
			Type: dto.MetricType_GAUGE.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: proto.String(types.LabelMetaCurrentStatus), Value: proto.String(status.String())},
						{Name: proto.String(types.LabelMetaCurrentDescription), Value: proto.String(msg)},
					},
					Gauge: &dto.Gauge{
						Value: proto.Float64(float64(status.NagiosCode())),
					},
				},
			},
		})
	}

	return result, err
}

func (t *Target) extraLabels() map[string]string {
	return map[string]string{
		types.LabelMetaSNMPTarget: t.opt.Address,
	}
}

func (t *Target) buildScraper(module string) *scrapper.Target {
	u := t.exporterAddress.ResolveReference(&url.URL{}) // clone URL
	qs := u.Query()
	qs.Set("module", module)
	qs.Set("target", t.opt.Address)
	u.RawQuery = qs.Encode()

	target := &scrapper.Target{
		ExtraLabels: t.extraLabels(),
		URL:         u,
		AllowList:   []string{},
		DenyList:    []string{},
	}

	return target
}

func (t *Target) getStatus() (types.Status, string) {
	t.l.Lock()
	defer t.l.Unlock()

	if t.lastSuccess.IsZero() && t.consecutiveErr == 0 {
		// never scrapper, status is not yet available.
		return types.StatusUnset, ""
	}

	if t.consecutiveErr == 0 {
		return types.StatusOk, ""
	}

	if t.consecutiveErr >= 2 {
		return types.StatusCritical, t.lastErrorMessage
	}

	if t.lastSuccess.IsZero() {
		return types.StatusUnset, ""
	}

	// At this point: lastSuccess is set (we already had at least one success)
	// and consecutiveErr == 1 (e.g. the last scrape was an error, but not the one before).
	// Kept the Ok, as we allow one failure.
	return types.StatusOk, ""
}

func (t *Target) String() string {
	t.l.Lock()
	defer t.l.Unlock()

	return fmt.Sprintf("initial_name=%s target=%s module=%s lastSuccess=%s (consecutive err=%d)", t.opt.InitialName, t.opt.Address, t.Module(), t.lastSuccess, t.consecutiveErr)
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

	if time.Since(t.lastFactErrAt) < maxAge {
		return nil, t.lastFactErr
	}

	tgt := t.buildScraper("discovery")

	tmp, err := tgt.GatherWithState(ctx, registry.GatherState{})
	if err != nil {
		t.lastFactErrAt = time.Now()
		t.lastFactErr = err

		return nil, err
	}

	t.lastFactErr = nil
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

// humanError convert error from the scrapper in easier to understand format.
func humanError(err error) string {
	var targetErr scrapper.TargetError

	if errors.As(err, &targetErr) {
		switch {
		case targetErr.StatusCode >= 400 && bytes.Contains(targetErr.PartialBody, []byte("read: connection refused")):
			return "connection refused"
		case targetErr.StatusCode >= 400 && bytes.Contains(targetErr.PartialBody, []byte("request timeout")):
			return "request timeout"
		case targetErr.ConnectErr != nil:
			return "snmp_exporter is not running"
		}
	}

	return err.Error()
}
