// Copyright 2015-2025 Bleemeo
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
	"maps"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/prometheus/scrapper"
	"github.com/bleemeo/glouton/types"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"google.golang.org/protobuf/proto"
)

const (
	defaultGatherTimeout   = 10 * time.Second
	ifIndexLabelName       = "ifIndex"
	ifTypeLabelName        = "ifType"
	ifOperStatusMetricName = "ifOperStatus"
	ifTypeInfoMetricName   = "ifType_info"
	snmpDiscoveryModule    = "discovery"
	mockFactMetricName     = "_mock_snmp_metric_for_mock_fact"
	mockFactLabelKey       = "_mock_snmp_fact_key"
	mockFactLabelValue     = "_mock_snmp_fact_value"
)

const (
	deviceTypeSwitch     = "switch"
	deviceTypeFirewall   = "firewall"
	deviceTypeAP         = "access-point"
	deviceTypePrinter    = "printer"
	deviceTypeHypervisor = "hypervisor"
)

// Target represents a snmp config instance.
type Target struct {
	opt             config.SNMPTarget
	exporterAddress *url.URL
	scraperFacts    FactProvider

	l                sync.Mutex
	scraper          registry.GathererWithState
	lastFacts        map[string]string
	lastFactUpdate   time.Time
	lastFactErrAt    time.Time
	lastFactErr      error
	lastSuccess      time.Time
	lastErrorMessage string
	lastStatus       types.Status
	consecutiveErr   int

	mockPerModule map[string][]byte
	now           func() time.Time
}

type TargetOptions struct {
	Address     string
	InitialName string
}

func NewMock(opt config.SNMPTarget, mockFacts map[string]string) *Target {
	r := newTarget(opt, nil, nil)
	r.mockPerModule = mockFromFacts(mockFacts)

	return r
}

func newTarget(opt config.SNMPTarget, scraperFact FactProvider, exporterAddress *url.URL) *Target {
	return &Target{
		opt:             opt,
		exporterAddress: exporterAddress,
		scraperFacts:    scraperFact,
		now:             time.Now,
	}
}

func (t *Target) Address() string {
	return t.opt.Target
}

func (t *Target) module(ctx context.Context) (string, error) {
	facts, err := t.facts(ctx, 24*time.Hour)
	if err != nil {
		return "", err
	}

	if strings.Contains(facts["product_name"], "Cisco") {
		return "cisco", nil
	}

	if strings.Contains(facts["product_name"], "LaserJet") {
		return "printer_mib", nil
	}

	if strings.Contains(facts["product_name"], "PowerConnect") {
		return "dell", nil
	}

	return "if_mib", nil
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

	return t.opt.Target, nil
}

// Gather implement prometheus.Gatherer.
func (t *Target) Gather() ([]*dto.MetricFamily, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultGatherTimeout)
	defer cancel()

	return t.GatherWithState(ctx, registry.GatherState{T0: time.Now()})
}

func (t *Target) GatherWithState(ctx context.Context, state registry.GatherState) ([]*dto.MetricFamily, error) {
	t.l.Lock()

	if t.scraper == nil {
		mod, err := t.module(ctx)
		if err != nil {
			t.l.Unlock()

			return nil, err
		}

		t.scraper = t.buildScraper(mod)
	}

	t.l.Unlock()

	result, err := t.scraper.GatherWithState(ctx, state)

	t.l.Lock()
	defer t.l.Unlock()

	if state.FromScrapeLoop {
		if err == nil {
			t.lastSuccess = t.now()
			t.consecutiveErr = 0
		} else {
			t.lastErrorMessage = humanError(err)
			t.consecutiveErr++
		}

		err = nil
	}

	status, msg := t.getStatus()
	mfs := processMFS(result, state, status, t.lastStatus, msg)

	if state.FromScrapeLoop {
		t.lastStatus = status
	}

	return mfs, err
}

func buildInformationMap(
	result []*dto.MetricFamily,
) (interfaceUp map[string]bool, indexToType map[string]string, totalInterfaces int, connectedInterfaces int) {
	if len(result) > 0 {
		indexToType = make(map[string]string, len(result[0].GetMetric()))
	}

	for _, mf := range result {
		if mf.GetName() == ifTypeInfoMetricName {
			for _, m := range mf.GetMetric() {
				var (
					idxStr  string
					typeStr string
				)

				for _, l := range m.GetLabel() {
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
			interfaceUp = make(map[string]bool, len(mf.GetMetric()))

			for _, m := range mf.GetMetric() {
				totalInterfaces++

				if m.GetGauge().GetValue() == 1 {
					connectedInterfaces++

					for _, l := range m.GetLabel() {
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

func processMFS(
	result []*dto.MetricFamily,
	state registry.GatherState,
	status types.Status,
	lastStatus types.Status,
	msg string,
) []*dto.MetricFamily {
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

		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == ifIndexLabelName && indexToType[l.GetValue()] != "" {
					l.Name = proto.String(ifTypeLabelName)
					l.Value = proto.String(indexToType[l.GetValue()])
				}
			}

			if mf.GetName() == "sysUpTime" && m.GetGauge() != nil {
				g := m.GetGauge()
				g.Value = proto.Float64(g.GetValue() / 100) // convert from 1/100th of seconds to seconds.
				m.Gauge = g
			}
		}
	}

	// Send the snmp_device_status metric when the agent status is ok, when
	// the agent has just become unreachable or when called from /metrics.
	if status == types.StatusOk || status == types.StatusCritical && lastStatus != types.StatusCritical || !state.FromScrapeLoop {
		snmpDeviceStatus := &dto.MetricFamily{
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
		}

		result = append(result, snmpDeviceStatus)
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

		if len(mf.GetMetric()) > 0 {
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

	for _, m := range mf.GetMetric() {
		var ifIndex string

		for _, l := range m.GetLabel() {
			if l.GetName() == ifIndexLabelName {
				ifIndex = l.GetValue()
			}
		}

		if ifIndex == "" || interfaceUp[ifIndex] {
			mf.Metric[i] = m
			i++
		}
	}

	mf.Metric = mf.GetMetric()[:i]
}

func (t *Target) extraLabels() map[string]string {
	return map[string]string{
		types.LabelMetaSNMPTarget: t.opt.Target,
	}
}

func (t *Target) buildScraper(module string) registry.GathererWithState {
	if t.exporterAddress == nil {
		if _, ok := t.mockPerModule[module]; !ok {
			return errGatherer{
				alwaysErr: scrapper.TargetError{
					PartialBody: fmt.Appendf(nil, "Unknown module '%s'", module),
					StatusCode:  400,
				},
			}
		}

		return scrapper.NewMock(t.mockPerModule[module], t.extraLabels())
	}

	u := t.exporterAddress.ResolveReference(&url.URL{}) // clone URL
	qs := u.Query()
	qs.Set("module", module)
	qs.Set("target", t.opt.Target)
	u.RawQuery = qs.Encode()

	target := scrapper.New(u, t.extraLabels())

	return target
}

// getStatus returns the current status of the SNMP device.
// t.l must be held before calling this method.
func (t *Target) getStatus() (types.Status, string) {
	if t.lastSuccess.IsZero() && t.consecutiveErr == 0 {
		// Never scraped, status is not yet available.
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

func (t *Target) String(ctx context.Context) string {
	t.l.Lock()
	defer t.l.Unlock()

	mod, err := t.module(ctx)
	if err != nil {
		mod = "!ERR: " + err.Error()
	}

	return fmt.Sprintf("initial_name=%s target=%s module=%s lastSuccess=%s (consecutive err=%d)", t.opt.InitialName, t.opt.Target, mod, t.lastSuccess, t.consecutiveErr)
}

func (t *Target) Facts(ctx context.Context, maxAge time.Duration) (facts map[string]string, err error) {
	t.l.Lock()
	defer t.l.Unlock()

	return t.facts(ctx, maxAge)
}

func (t *Target) facts(ctx context.Context, maxAge time.Duration) (facts map[string]string, err error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	if time.Since(t.lastFactUpdate) < maxAge {
		return t.lastFacts, nil
	}

	if time.Since(t.lastFactErrAt) < maxAge {
		return nil, t.lastFactErr
	}

	scraperMaxAge := max(maxAge, time.Hour)

	var scraperFact map[string]string

	if t.scraperFacts != nil {
		scraperFact, err = t.scraperFacts.Facts(ctx, scraperMaxAge)
		if err != nil {
			return nil, err
		}
	}

	tgt := t.buildScraper(snmpDiscoveryModule)

	tmp, err := tgt.GatherWithState(ctx, registry.GatherState{T0: time.Now()})
	if err != nil {
		t.lastFactErrAt = t.now()
		t.lastFactErr = err

		return nil, err
	}

	t.lastFactErr = nil
	result := model.FamiliesToMetricPoints(t.now(), tmp, true)

	t.lastFacts = factFromPoints(result, t.now(), scraperFact)
	t.lastFactUpdate = t.now()

	return t.lastFacts, nil
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

	mergeMap := map[string]func(string, string) string{
		"primary_address": addressSelectPublic,
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
		} else if mergeMap[target] != nil {
			result[target] = mergeMap[target](result[target], value)
		}
	}

	if useMockFact {
		return result
	}

	result[facts.FactUpdatedAt] = now.UTC().Format(time.RFC3339)
	result["primary_mac_address"] = strings.ToLower(result["primary_mac_address"])
	result["hostname"] = result["fqdn"]

	if strings.Contains(result["fqdn"], ".") {
		l := strings.SplitN(result["fqdn"], ".", 2)
		result["hostname"] = l[0]
		result["domain"] = l[1]
	}

	result = parseProductname(result)

	if value := deviceType(result); value != "" {
		result["device_type"] = value
	}

	result["agent_version"] = scraperFact["agent_version"]
	result["glouton_version"] = scraperFact["glouton_version"]
	result["scraper_fqdn"] = scraperFact["fqdn"]

	facts.CleanFacts(result)

	return result
}

// Parse the format KEY1:VALUE1,KEY2:VALUE2.
func parseProductnameComaKV(productName string) map[string]string {
	if !strings.Contains(productName, ",") {
		return nil
	}

	part := strings.Split(productName, ",")

	if len(part) < 2 || !strings.Contains(part[1], ":") {
		return nil
	}

	// Don't parse this format if it looks like an English sentence in which a
	// colon should be followed by space.
	value := strings.SplitN(part[1], ":", 2)[1]
	if len(value) == 0 || value[0] == ' ' {
		return nil
	}

	facts := make(map[string]string)

	for _, p := range part {
		p = strings.TrimSpace(p)
		subpart := strings.SplitN(p, ":", 2)

		if len(subpart) == 2 {
			switch subpart[0] {
			case "SN":
				facts["serial_number"] = subpart[1]
			case "PID":
				facts["product_name"] = subpart[1]
			}
		}
	}

	return facts
}

func parseProductname(facts map[string]string) map[string]string {
	maps.Copy(facts, parseProductnameComaKV(facts["product_name"]))

	// Some product name contains multiple information separated by coma. Only kept the first one
	// ... useless the first part isn't specific enough.
	part := strings.Split(facts["product_name"], ",")
	if len(part) > 2 {
		facts["product_name"] = part[0]
		if part[0] == "Cisco IOS Software" {
			facts["product_name"] = part[0] + "," + part[1]
		}
	}

	if strings.HasPrefix(facts["product_name"], "VMware ESXi ") {
		part := strings.Split(facts["product_name"], " ")
		if len(part) >= 3 && len(part[2]) > 0 && isDigit(part[2][0]) {
			facts["version"] = part[2]
		}
	}

	if strings.HasPrefix(facts["product_name"], "U6-Lite ") {
		part := strings.Split(facts["product_name"], " ")
		if len(part) >= 2 && len(part[1]) > 0 && isDigit(part[1][0]) {
			facts["version"] = part[1]
		}
	}

	if strings.HasPrefix(facts["product_name"], "Linux USW-") {
		// Swap Linux and the 2nd word
		part := strings.SplitN(facts["product_name"], " ", 3)
		part[0], part[1] = part[1], part[0]
		facts["product_name"] = strings.Join(part, " ")
	}

	return facts
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func addressSelectPublic(addr1 string, addr2 string) string {
	ip1 := net.ParseIP(addr1)
	ip2 := net.ParseIP(addr2)

	switch {
	case ip1.IsPrivate() && !ip2.IsPrivate() && !ip2.IsLoopback():
		return addr2
	case ip1.IsLoopback() && !ip2.IsLoopback():
		return addr2
	default:
		return addr1
	}
}

// humanError convert error from the scrapper in easier to understand format.
func humanError(err error) string {
	var targetErr scrapper.TargetError
	if errors.As(err, &targetErr) {
		switch {
		case targetErr.StatusCode >= 400 && bytes.Contains(targetErr.PartialBody, []byte("read: connection refused")):
			return "SNMP device didn't respond"
		case targetErr.StatusCode >= 400 && bytes.Contains(targetErr.PartialBody, []byte("request timeout")):
			return "SNMP device request timeout"
		case targetErr.ConnectErr != nil:
			return "snmp_exporter didn't respond"
		}
	}

	return err.Error()
}

func deviceType(facts map[string]string) string {
	switch {
	case strings.HasPrefix(facts["product_name"], "PowerConnect"):
		return deviceTypeSwitch
	case strings.Contains(facts["product_name"], "Adaptive Security Appliance"):
		return deviceTypeFirewall
	case strings.HasPrefix(facts["product_name"], "Cisco"):
		return deviceTypeSwitch
	case strings.HasPrefix(facts["product_name"], "U6-Lite"):
		return deviceTypeAP
	case strings.HasPrefix(facts["product_name"], "USW-"):
		return deviceTypeSwitch
	case strings.Contains(facts["product_name"], "LaserJet"):
		return deviceTypePrinter
	case strings.HasPrefix(facts["product_name"], "VMware ESX"):
		return deviceTypeHypervisor
	}

	return ""
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
		mf.Metric = append(mf.GetMetric(), &dto.Metric{
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
