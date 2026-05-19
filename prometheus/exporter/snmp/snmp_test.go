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
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/prometheus/scrapper"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	prometheusModel "github.com/prometheus/common/model"
)

const (
	testLabelOther       = "other"
	testValueLabel       = "label"
	testLabelHostname    = "hostname"
	testFactUpdatedAt    = "fact_updated_at"
	testDeviceType       = "device_type"
	testTimestamp2021    = "2021-09-28T09:43:04Z"
	testIP192168         = "192.168.1.2"
	testDataDir          = "testdata"
	testNotIfIndex       = "notIfIndex"
	testDomainExample    = "example.com"
	testLabelDomain      = "domain"
	testPowerConnect5448 = "PowerConnect 5448"
	testAgentVersion     = "21.11.08.123456"
	testLinuxSNMPDInput  = "linux-snmpd.input"
)

func fileToMFS(filename string) ([]*dto.MetricFamily, error) {
	fd, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	parser := expfmt.NewTextParser(prometheusModel.LegacyValidation)

	tmpMap, err := parser.TextToMetricFamilies(fd)
	if err != nil {
		return nil, err
	}

	tmp := make([]*dto.MetricFamily, 0, len(tmpMap))

	for _, v := range tmpMap {
		tmp = append(tmp, v)
	}

	sort.Slice(tmp, func(i, j int) bool {
		return tmp[i].GetName() < tmp[j].GetName()
	})

	return tmp, nil
}

func Test_factFromPoints(t *testing.T) {
	now := time.Date(2021, 9, 28, 9, 43, 4, 1234, time.UTC)

	tests := []struct {
		name         string
		metricFile   string
		scraperFacts map[string]string
		want         map[string]string
	}{
		{
			name:       testPowerConnect5448,
			metricFile: "powerconnect-5448.metrics",
			scraperFacts: map[string]string{
				factFQDN:          "bleemeo-linux01",
				"agent_version":   testAgentVersion,
				"glouton_version": testAgentVersion,
				"something":       "else",
			},
			want: map[string]string{
				factFQDN:           "bleemeo-switch01",
				testLabelHostname:  "bleemeo-switch01",
				factBootVersion:    "1.0.0.6",
				factVersion:        "1.0.0.35",
				factSerialNumber:   "CN1234567890ABCDEFGH",
				factProductName:    testPowerConnect5448,
				factPrimaryAddress: testIP192168,
				factPrimaryMAC:     "00:1e:45:67:89:ab",
				testFactUpdatedAt:  testTimestamp2021,
				"agent_version":    testAgentVersion,
				"glouton_version":  testAgentVersion,
				"scraper_fqdn":     "bleemeo-linux01",
				testDeviceType:     deviceTypeSwitch,
			},
		},
		{
			name:       "Cisco N9000",
			metricFile: "cisco-n9000.metrics",
			want: map[string]string{
				factFQDN:          "sw-nexus.example.com",
				testLabelDomain:   testDomainExample,
				testLabelHostname: "sw-nexus",
				factBootVersion:   "6.1(2)I3(2)",
				factVersion:       "6.1(2)I3(2)",
				factSerialNumber:  "SAL1234S567",
				factProductName:   "Cisco NX-OS(tm) n9000",
				factPrimaryMAC:    "50:87:01:a0:b0:2c",
				testFactUpdatedAt: testTimestamp2021,
				testDeviceType:    deviceTypeSwitch,
			},
		},
		{
			name:       "Cisco C2960",
			metricFile: "cisco-c2960.metrics",
			want: map[string]string{
				factFQDN:           "myname-switch.example.com",
				testLabelDomain:    testDomainExample,
				testLabelHostname:  "myname-switch",
				factBootVersion:    "15.0(2)SE6",
				factVersion:        "15.0(2)SE6",
				factSerialNumber:   "FOC1234Z1Y2",
				factPrimaryAddress: testIP192168,
				factProductName:    "Cisco IOS Software, C2960 Software (C2960-LANLITEK9-M)",
				factPrimaryMAC:     "34:6f:01:02:a1:00",
				testFactUpdatedAt:  testTimestamp2021,
				testDeviceType:     deviceTypeSwitch,
			},
		},
		{
			name:       "Cisco ASA",
			metricFile: "cisco-asa.metrics",
			want: map[string]string{
				factFQDN:           "fw.example.com",
				testLabelDomain:    testDomainExample,
				testLabelHostname:  "fw",
				factBootVersion:    "2.1(9)8",
				factVersion:        "9.4(4)32",
				factSerialNumber:   "ABC1234D5EF",
				factPrimaryAddress: "81.123.210.12",
				factProductName:    "Cisco Adaptive Security Appliance Version 9.4(4)32",
				testFactUpdatedAt:  testTimestamp2021,
				testDeviceType:     deviceTypeFirewall,
			},
		},
		{
			name:       "hp-printer",
			metricFile: "hp-printer.metrics",
			want: map[string]string{
				factFQDN:           "home-printer1",
				testLabelHostname:  "home-printer1",
				factSerialNumber:   "CNB1A2B34C",
				factPrimaryAddress: testIP192168,
				factProductName:    "HP Color LaserJet MFP M476dw",
				testFactUpdatedAt:  testTimestamp2021,
				testDeviceType:     deviceTypePrinter,
			},
		},
		{
			name:       "VMware ESXi",
			metricFile: "vmware-esxi-6.5.0.metrics",
			want: map[string]string{
				factFQDN:          "localhost.bleemeo.work",
				testLabelHostname: "localhost",
				testLabelDomain:   "bleemeo.work",
				factBootVersion:   "2.8",
				factVersion:       "6.5.0",
				factProductName:   "VMware ESXi 6.5.0 build-14320405 VMware, Inc. x86_64",
				testFactUpdatedAt: testTimestamp2021,
				testDeviceType:    deviceTypeHypervisor,
			},
		},
		{
			name:       "Ubiquiti U6",
			metricFile: "ubiquiti-u6-lite.metrics",
			want: map[string]string{
				factFQDN:           "U6-Lite",
				testLabelHostname:  "U6-Lite",
				factVersion:        "5.60.19.13044",
				factPrimaryAddress: "10.1.2.3",
				factProductName:    "U6-Lite 5.60.19.13044",
				testFactUpdatedAt:  testTimestamp2021,
				testDeviceType:     deviceTypeAP,
			},
		},
		{
			name:       "Ubiquiti USW",
			metricFile: "ubiquiti-usw-24.metrics",
			want: map[string]string{
				factFQDN:           "USW-24-PoE",
				testLabelHostname:  "USW-24-PoE",
				factPrimaryAddress: "10.1.2.3",
				factPrimaryMAC:     "78:45:50:60:70:80",
				factProductName:    "USW-24-PoE Linux 3.18.24 #0 Thu Aug 30 12:10:54 2018 mips",
				testFactUpdatedAt:  testTimestamp2021,
				testDeviceType:     deviceTypeSwitch,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(testDataDir, tt.metricFile))
			if err != nil {
				t.Fatal(err)
			}

			tgt := newTarget(config.SNMPTarget{}, facts.NewMockFacter(tt.scraperFacts), nil)
			tgt.mockPerModule = map[string][]byte{
				snmpDiscoveryModule: body,
			}
			tgt.now = func() time.Time { return now }

			tmp, err := fileToMFS(filepath.Join(testDataDir, tt.metricFile))
			if err != nil {
				t.Fatal(err)
			}

			result := model.FamiliesToMetricPoints(time.Now(), tmp, true)
			got := factFromPoints(result, now, tt.scraperFacts)

			got2, err := tgt.Facts(t.Context(), 0)
			if err != nil {
				t.Error(err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("factFromPoints() mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tt.want, got2); diff != "" {
				t.Errorf("Facts() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_humanError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "random error",
			err:  os.ErrPermission, // this error shouldn't be handled by humanError
			want: os.ErrPermission.Error(),
		},
		{
			name: "SNMP connection refused",
			err: scrapper.TargetError{
				PartialBody: []byte(
					`An error has occurred while serving metrics:\n\n` +
						`error collecting metric Desc{fqName: "snmp_error", help: "Error scraping target", constLabels: {}, variableLabels: []}: ` +
						`error getting target localhost: error reading from socket: read udp 127.0.0.1:46797->127.0.0.1:161: read: connection refused`,
				),
				StatusCode: 500,
			},
			want: errMsgDeviceNotRespond,
		},
		{
			name: "exporter connection refused",
			err: scrapper.TargetError{
				ConnectErr: errors.New("something like dial tcp 127.0.0.1:9116: connect: connection refused"), //nolint: err113
			},
			want: "snmp_exporter didn't respond",
		},
		{
			name: "SNMP connection refused wrapper",
			err: fmt.Errorf("i wrap: %w", scrapper.TargetError{
				PartialBody: []byte(
					`An error has occurred while serving metrics:\n\n` +
						`error collecting metric Desc{fqName: "snmp_error", help: "Error scraping target", constLabels: {}, variableLabels: []}: ` +
						`error getting target localhost: error reading from socket: read udp 127.0.0.1:46797->127.0.0.1:161: read: connection refused`,
				),
				StatusCode: 500,
			}),
			want: errMsgDeviceNotRespond,
		},
		{
			name: "SNMP connection timeout",
			err: scrapper.TargetError{
				PartialBody: []byte(
					`An error has occurred while serving metrics:\n\n` +
						`error collecting metric Desc{fqName: "snmp_error", help: "Error scraping target", constLabels: {}, variableLabels: []}: ` +
						`error getting target 10.12.3.45: request timeout (after 3 retries)`,
				),
				StatusCode: 500,
			},
			want: "SNMP device request timeout",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanError(tt.err); got != tt.want {
				t.Errorf("humanError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_mfsFilterInterface(t *testing.T) {
	type args struct {
		mfs         []*dto.MetricFamily
		interfaceUp map[string]bool
	}

	tests := []struct {
		name string
		args args
		want []*dto.MetricFamily
	}{
		{
			name: "metric of disconnected interface are excluded",
			args: args{
				interfaceUp: map[string]bool{
					"1": true,
					"3": true,
				},
				mfs: []*dto.MetricFamily{
					{
						Name: new("ifInOctets"),
						Metric: []*dto.Metric{
							{
								Label: []*dto.LabelPair{
									{Name: new(ifIndexLabelName), Value: new("1")},
									{Name: new(testLabelOther), Value: new(testValueLabel)},
								},
							},
							{
								Label: []*dto.LabelPair{
									{Name: new(ifIndexLabelName), Value: new("2")},
									{Name: new(testLabelOther), Value: new(testValueLabel)},
								},
							},
							{
								Label: []*dto.LabelPair{
									{Name: new(ifIndexLabelName), Value: new("3")},
									{Name: new(testLabelOther), Value: new(testValueLabel)},
								},
							},
						},
					},
					{
						Name: new("all_disconnected"),
						Metric: []*dto.Metric{
							{
								Label: []*dto.LabelPair{
									{Name: new(ifIndexLabelName), Value: new("2")},
								},
							},
							{
								Label: []*dto.LabelPair{
									{Name: new(ifIndexLabelName), Value: new("4")},
								},
							},
						},
					},
					{
						Name: new("all_connected"),
						Metric: []*dto.Metric{
							{
								Label: []*dto.LabelPair{
									{Name: new(testLabelOther), Value: new(testValueLabel)},
									{Name: new(ifIndexLabelName), Value: new("1")},
								},
							},
							{
								Label: []*dto.LabelPair{
									{Name: new(ifIndexLabelName), Value: new("3")},
									{Name: new(testLabelOther), Value: new(testValueLabel)},
								},
							},
						},
					},
				},
			},
			want: []*dto.MetricFamily{
				{
					Name: new("ifInOctets"),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: new(ifIndexLabelName), Value: new("1")},
								{Name: new(testLabelOther), Value: new(testValueLabel)},
							},
						},
						{
							Label: []*dto.LabelPair{
								{Name: new(ifIndexLabelName), Value: new("3")},
								{Name: new(testLabelOther), Value: new(testValueLabel)},
							},
						},
					},
				},
				{
					Name: new("all_connected"),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: new(testLabelOther), Value: new(testValueLabel)},
								{Name: new(ifIndexLabelName), Value: new("1")},
							},
						},
						{
							Label: []*dto.LabelPair{
								{Name: new(ifIndexLabelName), Value: new("3")},
								{Name: new(testLabelOther), Value: new(testValueLabel)},
							},
						},
					},
				},
			},
		},
		{
			name: "ifOperStatus and non-interface metric are always kept",
			args: args{
				interfaceUp: map[string]bool{
					"1": true,
					"3": true,
				},
				mfs: []*dto.MetricFamily{
					{
						Name: new(ifOperStatusMetricName),
						Metric: []*dto.Metric{
							{
								Label: []*dto.LabelPair{
									{Name: new(ifIndexLabelName), Value: new("1")},
									{Name: new(testLabelOther), Value: new(testValueLabel)},
								},
							},
							{
								Label: []*dto.LabelPair{
									{Name: new(ifIndexLabelName), Value: new("2")},
									{Name: new(testLabelOther), Value: new(testValueLabel)},
								},
							},
							{
								Label: []*dto.LabelPair{
									{Name: new(ifIndexLabelName), Value: new("3")},
									{Name: new(testLabelOther), Value: new(testValueLabel)},
								},
							},
						},
					},
					{
						Name: new("another_metric"),
						Metric: []*dto.Metric{
							{
								Label: []*dto.LabelPair{
									{Name: new(testNotIfIndex), Value: new("1")},
								},
							},
							{
								Label: []*dto.LabelPair{
									{Name: new(testNotIfIndex), Value: new("2")},
								},
							},
						},
					},
					{
						Name: new("both_ifIndex_and_not"),
						Metric: []*dto.Metric{
							{
								Label: []*dto.LabelPair{
									{Name: new(testLabelOther), Value: new(testValueLabel)},
									{Name: new(ifIndexLabelName), Value: new("2")},
								},
							},
							{
								Label: []*dto.LabelPair{
									{Name: new(testLabelOther), Value: new(testValueLabel)},
								},
							},
						},
					},
				},
			},
			want: []*dto.MetricFamily{
				{
					Name: new(ifOperStatusMetricName),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: new(ifIndexLabelName), Value: new("1")},
								{Name: new(testLabelOther), Value: new(testValueLabel)},
							},
						},
						{
							Label: []*dto.LabelPair{
								{Name: new(ifIndexLabelName), Value: new("2")},
								{Name: new(testLabelOther), Value: new(testValueLabel)},
							},
						},
						{
							Label: []*dto.LabelPair{
								{Name: new(ifIndexLabelName), Value: new("3")},
								{Name: new(testLabelOther), Value: new(testValueLabel)},
							},
						},
					},
				},
				{
					Name: new("another_metric"),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: new(testNotIfIndex), Value: new("1")},
							},
						},
						{
							Label: []*dto.LabelPair{
								{Name: new(testNotIfIndex), Value: new("2")},
							},
						},
					},
				},
				{
					Name: new("both_ifIndex_and_not"),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: new(testLabelOther), Value: new(testValueLabel)},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mfsFilterInterface(tt.args.mfs, tt.args.interfaceUp)

			if diff := types.DiffMetricFamilies(tt.want, got, false, false); diff != "" {
				t.Errorf("mfsFilterInterface() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_processMFS(t *testing.T) {
	tests := []struct {
		name      string
		state     registry.GatherState
		status    types.Status
		msg       string
		inputFile string
		wantFile  string
	}{
		{
			name:      "linux snmpd",
			state:     registry.GatherState{T0: time.Now()},
			status:    types.StatusOk,
			msg:       "",
			inputFile: testLinuxSNMPDInput,
			wantFile:  "linux-snmpd.want",
		},
		{
			name:      "linux snmpd noFilter",
			state:     registry.GatherState{T0: time.Now(), NoFilter: true},
			status:    types.StatusOk,
			msg:       "",
			inputFile: testLinuxSNMPDInput,
			wantFile:  "linux-snmpd-nofilter.want",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			input, err := fileToMFS(filepath.Join(testDataDir, tt.inputFile))
			if err != nil {
				t.Fatal(err)
			}

			want, err := fileToMFS(filepath.Join(testDataDir, tt.wantFile))
			if err != nil {
				t.Fatal(err)
			}

			got := processMFS(input, tt.state, tt.status, types.StatusOk, tt.msg)

			if diff := types.DiffMetricFamilies(want, got, false, false); diff != "" {
				t.Errorf("processMFS() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNewMock(t *testing.T) {
	tests := []struct {
		name      string
		opt       config.SNMPTarget
		mockFacts map[string]string
	}{
		{
			name:      "empty facts",
			mockFacts: map[string]string{},
		},
		{
			name: "some facts",
			mockFacts: map[string]string{
				"my_facts": "test",
				factFQDN:   testDomainExample,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			wantFacts := make(map[string]string, len(tt.mockFacts))
			maps.Copy(wantFacts, tt.mockFacts)

			tgt := NewMock(tt.opt, tt.mockFacts)

			got, err := tgt.Facts(t.Context(), 0)
			if err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(wantFacts, got); diff != "" {
				t.Errorf("facts mismatch (-want +got)\n%s", diff)
			}
		})
	}
}

func TestTarget_Module(t *testing.T) {
	tests := []struct {
		name       string
		metricFile string
		want       string
	}{
		{
			name:       "VMware ESXi 6.5.0",
			metricFile: "vmware-esxi-6.5.0.metrics",
			want:       moduleIfMIB,
		},
		{
			name:       testPowerConnect5448,
			metricFile: "powerconnect-5448.metrics",
			want:       "dell",
		},
		{
			name:       "Cisco N9000",
			metricFile: "cisco-n9000.metrics",
			want:       moduleCisco,
		},
		{
			name:       "Cisco C2960",
			metricFile: "cisco-c2960.metrics",
			want:       moduleCisco,
		},
		{
			name:       "hp-printer",
			metricFile: "hp-printer.metrics",
			want:       "printer_mib",
		},
		{
			name:       "anything else",
			metricFile: testLinuxSNMPDInput,
			want:       moduleIfMIB,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(testDataDir, tt.metricFile))
			if err != nil {
				t.Fatal(err)
			}

			tr := newTarget(config.SNMPTarget{}, nil, nil)
			tr.mockPerModule = map[string][]byte{
				snmpDiscoveryModule: body,
			}

			got, err := tr.module(t.Context())
			if err != nil {
				t.Error(err)
			}

			if got != tt.want {
				t.Errorf("Target.Module() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_addressSelectPublic(t *testing.T) {
	tests := []struct {
		name  string
		addr1 string
		addr2 string
		want  string
	}{
		{
			name:  "public-better-than-private",
			addr1: "1.2.3.4",
			addr2: testIP192168,
			want:  "1.2.3.4",
		},
		{
			name:  "public-better-than-private2",
			addr1: "80.12.66.254",
			addr2: "10.11.12.24",
			want:  "80.12.66.254",
		},
		{
			name:  "public-better-than-private3",
			addr1: "172.16.12.5",
			addr2: "90.100.110.120",
			want:  "90.100.110.120",
		},
		{
			name:  "public-better-than-loopback",
			addr1: "200.250.255.0",
			addr2: "127.0.0.1",
			want:  "200.250.255.0",
		},
		{
			name:  "private-better-than-loopback",
			addr1: "127.0.0.1",
			addr2: testIP192168,
			want:  testIP192168,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got1 := addressSelectPublic(tt.addr1, tt.addr2)
			got2 := addressSelectPublic(tt.addr2, tt.addr1)

			if got1 != tt.want {
				t.Errorf("addressSelectPublic() = %v, want %v", got1, tt.want)
			}

			if got2 != tt.want {
				t.Errorf("addressSelectPublic(reverse) = %v, want %v", got2, tt.want)
			}
		})
	}
}
