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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gogo/protobuf/proto"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

const (
	defaultGatherTimeout   = 10 * time.Second
	ifIndexLabelName       = "ifIndex"
	ifTypeLabelName        = "ifType"
	ifOperStatusMetricName = "ifOperStatus"
	ifTypeInfoMetricName   = "ifType_info"
	snmpDiscoveryModule    = "discovery"
	mockFactMetricName     = "__snmp_metric_for_mock_fact"
	mockFactLabelKey       = "__snmp_fact_key"
	mockFactLabelValue     = "__snmp_fact_value"
)

// Target represents a snmp config instance.
type Target struct {
	opt             TargetOptions
	exporterAddress *url.URL
	scraperFacts    FactProvider

	l                sync.Mutex
	scraper          registry.GathererWithState
	facts            map[string]string
	lastFactUpdate   time.Time
	lastFactErrAt    time.Time
	lastFactErr      error
	lastSuccess      time.Time
	lastErrorMessage string
	consecutiveErr   int

	mockPerModule map[string][]byte
	now           func() time.Time
}

type TargetOptions struct {
	Address     string
	InitialName string
}

func NewMock(opt TargetOptions, mockFacts map[string]string) *Target {
	r := newTarget(opt, nil, nil)
	r.mockPerModule = mockFromFacts(mockFacts)

	return r
}

func newTarget(opt TargetOptions, scraperFact FactProvider, exporterAddress *url.URL) *Target {
	return &Target{
		opt:             opt,
		exporterAddress: exporterAddress,
		scraperFacts:    scraperFact,
		now:             time.Now,
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
			t.lastSuccess = t.now()
			t.consecutiveErr = 0
		} else {
			t.lastErrorMessage = humanError(err)
			t.consecutiveErr++
		}

		err = nil

		t.l.Unlock()
	}

	status, msg := t.getStatus()

	return processMFS(result, state, status, msg), err
}

func buildInformationMap(result []*dto.MetricFamily) (interfaceUp map[string]bool, indexToType map[string]string, totalInterfaces int, connectedInterfaces int) {
	if len(result) > 0 {
		indexToType = make(map[string]string, len(result[0].Metric))
	}

	for _, mf := range result {
		if mf.GetName() == ifTypeInfoMetricName {
			for _, m := range mf.Metric {
				var (
					idxStr  string
					typeStr string
				)

				for _, l := range m.Label {
					if l.GetName() == ifIndexLabelName {
						idxStr = l.GetValue()
					}

					if l.GetName() == ifTypeLabelName {
						typeStr = l.GetValue()
					}

					if idxStr != "" && typeStr != "" {
						indexToType[idxStr] = typeStr

						break
					}
				}
			}
		}

		if mf.GetName() == ifOperStatusMetricName {
			interfaceUp = make(map[string]bool, len(mf.Metric))

			for _, m := range mf.Metric {
				totalInterfaces++

				if m.GetGauge().GetValue() == 1 {
					connectedInterfaces++

					for _, l := range m.Label {
						if l.GetName() == ifIndexLabelName {
							interfaceUp[l.GetValue()] = true
						}
					}
				}
			}
		}
	}

	return interfaceUp, indexToType, totalInterfaces, connectedInterfaces
}

func processMFS(result []*dto.MetricFamily, state registry.GatherState, status types.Status, msg string) []*dto.MetricFamily {
	interfaceUp, indexToType, totalInterfaces, connectedInterfaces := buildInformationMap(result)

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

		if !state.NoFilter {
			result = mfsFilterInterface(result, interfaceUp)
		}
	}

	for _, mf := range result {
		if mf.GetName() == ifTypeInfoMetricName {
			continue
		}

		for _, m := range mf.Metric {
			for _, l := range m.Label {
				if l.GetName() == ifIndexLabelName && indexToType[l.GetValue()] != "" {
					l.Name = proto.String(ifTypeLabelName)
					l.Value = proto.String(indexToType[l.GetValue()])
				}
			}
		}
	}

	if status != types.StatusUnset {
		result = append(result, &dto.MetricFamily{
			Name: proto.String("snmp_device_status"),
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

	sort.Slice(result, func(i, j int) bool {
		return result[i].GetName() < result[j].GetName()
	})

	return result
}

func mfsFilterInterface(mfs []*dto.MetricFamily, interfaceUp map[string]bool) []*dto.MetricFamily {
	i := 0

	for _, mf := range mfs {
		mfFilterInterface(mf, interfaceUp)

		if len(mf.Metric) > 0 {
			mfs[i] = mf
			i++
		}
	}

	mfs = mfs[:i]

	return mfs
}

func mfFilterInterface(mf *dto.MetricFamily, interfaceUp map[string]bool) {
	// ifOperStatus is always sent
	if mf.GetName() == ifOperStatusMetricName {
		return
	}

	i := 0

	for _, m := range mf.Metric {
		var ifIndex string

		for _, l := range m.Label {
			if l.GetName() == ifIndexLabelName {
				ifIndex = l.GetValue()
			}
		}

		if ifIndex == "" || interfaceUp[ifIndex] {
			mf.Metric[i] = m
			i++
		}
	}

	mf.Metric = mf.Metric[:i]
}

func (t *Target) extraLabels() map[string]string {
	return map[string]string{
		types.LabelMetaSNMPTarget: t.opt.Address,
	}
}

func (t *Target) buildScraper(module string) registry.GathererWithState {
	if t.exporterAddress == nil {
		if _, ok := t.mockPerModule[module]; !ok {
			return errGatherer{
				alwaysErr: scrapper.TargetError{
					PartialBody: []byte(fmt.Sprintf("Unknown module '%s'", module)),
					StatusCode:  400,
				},
			}
		}

		return scrapper.NewMock(t.mockPerModule[module], t.extraLabels())
	}

	u := t.exporterAddress.ResolveReference(&url.URL{}) // clone URL
	qs := u.Query()
	qs.Set("module", module)
	qs.Set("target", t.opt.Address)
	u.RawQuery = qs.Encode()

	target := scrapper.New(u, t.extraLabels())

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

	if time.Since(t.lastFactUpdate) < maxAge {
		return t.facts, nil
	}

	if time.Since(t.lastFactErrAt) < maxAge {
		return nil, t.lastFactErr
	}

	scraperMaxAge := maxAge
	if scraperMaxAge < time.Hour {
		scraperMaxAge = time.Hour
	}

	var scraperFact map[string]string

	if t.scraperFacts != nil {
		scraperFact, err = t.scraperFacts.Facts(ctx, scraperMaxAge)
		if err != nil {
			return nil, err
		}
	}

	tgt := t.buildScraper(snmpDiscoveryModule)

	tmp, err := tgt.GatherWithState(ctx, registry.GatherState{})
	if err != nil {
		t.lastFactErrAt = t.now()
		t.lastFactErr = err

		return nil, err
	}

	t.lastFactErr = nil
	result := registry.FamiliesToMetricPoints(t.now(), tmp)

	t.facts = factFromPoints(result, t.now(), scraperFact)
	t.lastFactUpdate = t.now()

	return t.facts, nil
}

func factFromPoints(points []types.MetricPoint, now time.Time, scraperFact map[string]string) map[string]string {
	useMockFact := false
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

		if key == mockFactMetricName {
			useMockFact = true

			if p.Labels[mockFactLabelKey] != "" {
				result[p.Labels[mockFactLabelKey]] = p.Labels[mockFactLabelValue]
			}

			continue
		}

		if target == "" || value == "" {
			continue
		}

		if result[target] == "" {
			result[target] = value
		}
	}

	if useMockFact {
		return result
	}

	result["fact_updated_at"] = now.UTC().Format(time.RFC3339)
	result["primary_mac_address"] = strings.ToLower(result["primary_mac_address"])
	result["hostname"] = result["fqdn"]

	if strings.Contains(result["fqdn"], ".") {
		l := strings.SplitN(result["fqdn"], ".", 2)
		result["hostname"] = l[0]
		result["domain"] = l[1]
	}

	if strings.Contains(result["product_name"], ",") {
		part := strings.Split(result["product_name"], ",")

		result["product_name"] = part[0]

		if part[0] == "Cisco IOS Software" {
			result["product_name"] = part[0] + "," + part[1]
		}

		for _, p := range part {
			p = strings.TrimSpace(p)
			subpart := strings.SplitN(p, ":", 2)

			if len(subpart) == 2 {
				switch subpart[0] {
				case "SN":
					result["serial_number"] = subpart[1]
				case "PID":
					result["product_name"] = subpart[1]
				}
			}
		}
	}

	result["agent_version"] = scraperFact["agent_version"]
	result["glouton_version"] = scraperFact["glouton_version"]
	result["scraper_fqdn"] = scraperFact["fqdn"]

	facts.CleanFacts(result)

	return result
}

// humanError convert error from the scrapper in easier to understand format.
func humanError(err error) string {
	var targetErr scrapper.TargetError

	if errors.As(err, &targetErr) {
		switch {
		case targetErr.StatusCode >= 400 && bytes.Contains(targetErr.PartialBody, []byte("read: connection refused")):
			return "SNMP device didn't responded"
		case targetErr.StatusCode >= 400 && bytes.Contains(targetErr.PartialBody, []byte("request timeout")):
			return "SNMP device request timeout"
		case targetErr.ConnectErr != nil:
			return "snmp_exporter didn't responded"
		}
	}

	return err.Error()
}

type errGatherer struct {
	alwaysErr error
}

func (g errGatherer) GatherWithState(context.Context, registry.GatherState) ([]*dto.MetricFamily, error) {
	return nil, g.alwaysErr
}

func mockFromFacts(facts map[string]string) map[string][]byte {
	if facts == nil {
		facts = make(map[string]string)
	}

	mf := &dto.MetricFamily{
		Name: proto.String(mockFactMetricName),
		Type: dto.MetricType_GAUGE.Enum(),
	}

	// sentinel marked, used in factsFromPoints to know we use mock facts
	facts[""] = ""

	for k, v := range facts {
		mf.Metric = append(mf.Metric, &dto.Metric{
			Label: []*dto.LabelPair{
				{Name: proto.String(mockFactLabelKey), Value: proto.String(k)},
				{Name: proto.String(mockFactLabelValue), Value: proto.String(v)},
			},
			Gauge: &dto.Gauge{
				Value: proto.Float64(42),
			},
		})
	}

	writer := bytes.NewBuffer(nil)

	_, _ = expfmt.MetricFamilyToText(writer, mf)

	return map[string][]byte{
		snmpDiscoveryModule: writer.Bytes(),
	}
}
