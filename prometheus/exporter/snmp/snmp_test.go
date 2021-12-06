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
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/google/go-cmp/cmp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

func fileToMFS(filename string) ([]*dto.MetricFamily, error) {
	fd, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	var parser expfmt.TextParser

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
			name:       "PowerConnect 5448",
			metricFile: "powerconnect-5448.metrics",
			scraperFacts: map[string]string{
				"fqdn":            "bleemeo-linux01",
				"agent_version":   "21.11.08.123456",
				"glouton_version": "21.11.08.123456",
				"somthing":        "else",
			},
			want: map[string]string{
				"fqdn":                "bleemeo-switch01",
				"hostname":            "bleemeo-switch01",
				"boot_version":        "1.0.0.6",
				"version":             "1.0.0.35",
				"serial_number":       "CN1234567890ABCDEFGH",
				"product_name":        "PowerConnect 5448",
				"primary_address":     "192.168.1.2",
				"primary_mac_address": "00:1e:45:67:89:ab",
				"fact_updated_at":     "2021-09-28T09:43:04Z",
				"agent_version":       "21.11.08.123456",
				"glouton_version":     "21.11.08.123456",
				"scraper_fqdn":        "bleemeo-linux01",
			},
		},
		{
			name:       "Cisco N9000",
			metricFile: "cisco-n9000.metrics",
			want: map[string]string{
				"fqdn":                "sw-nexus.example.com",
				"domain":              "example.com",
				"hostname":            "sw-nexus",
				"boot_version":        "6.1(2)I3(2)",
				"version":             "6.1(2)I3(2)",
				"serial_number":       "SAL1234S567",
				"product_name":        "Cisco NX-OS(tm) n9000",
				"primary_mac_address": "50:87:01:a0:b0:2c",
				"fact_updated_at":     "2021-09-28T09:43:04Z",
			},
		},
		{
			name:       "Cisco C2960",
			metricFile: "cisco-c2960.metrics",
			want: map[string]string{
				"fqdn":                "myname-switch.example.com",
				"domain":              "example.com",
				"hostname":            "myname-switch",
				"boot_version":        "15.0(2)SE6",
				"version":             "15.0(2)SE6",
				"serial_number":       "FOC1234Z1Y2",
				"primary_address":     "192.168.1.2",
				"product_name":        "Cisco IOS Software, C2960 Software (C2960-LANLITEK9-M)",
				"primary_mac_address": "34:6f:01:02:a1:00",
				"fact_updated_at":     "2021-09-28T09:43:04Z",
			},
		},
		{
			name:       "hp-printer",
			metricFile: "hp-printer.metrics",
			want: map[string]string{
				"fqdn":            "home-printer1",
				"hostname":        "home-printer1",
				"serial_number":   "CNB1A2B34C",
				"primary_address": "192.168.1.2",
				"product_name":    "HP Color LaserJet MFP M476dw",
				"fact_updated_at": "2021-09-28T09:43:04Z",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			body, err := ioutil.ReadFile(filepath.Join("testdata", tt.metricFile))
			if err != nil {
				t.Fatal(err)
			}

			tgt := newTarget(TargetOptions{}, facts.NewMockFacter(tt.scraperFacts), nil)
			tgt.mockPerModule = map[string][]byte{
				snmpDiscoveryModule: body,
			}
			tgt.now = func() time.Time { return now }

			tmp, err := fileToMFS(filepath.Join("testdata", tt.metricFile))
			if err != nil {
				t.Fatal(err)
			}

			result := registry.FamiliesToMetricPoints(time.Now(), tmp)
			got := factFromPoints(result, now, tt.scraperFacts)

			got2, err := tgt.Facts(context.Background(), 0)
			if err != nil {
				t.Error(err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("factFromPoints() missmatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tt.want, got2); diff != "" {
				t.Errorf("Facts() missmatch (-want +got):\n%s", diff)
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
			want: "SNMP device didn't responded",
		},
		{
			name: "exporter connection refused",
			err: scrapper.TargetError{
				ConnectErr: fmt.Errorf("something like dial tcp 127.0.0.1:9116: connect: connection refused"), //nolint: goerr113
			},
			want: "snmp_exporter didn't responded",
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
			want: "SNMP device didn't responded",
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
						Name: proto.String("ifInOctets"),
						Metric: []*dto.Metric{
							{
								Label: []*dto.LabelPair{
									{Name: proto.String(ifIndexLabelName), Value: proto.String("1")},
									{Name: proto.String("other"), Value: proto.String("label")},
								},
							},
							{
								Label: []*dto.LabelPair{
									{Name: proto.String(ifIndexLabelName), Value: proto.String("2")},
									{Name: proto.String("other"), Value: proto.String("label")},
								},
							},
							{
								Label: []*dto.LabelPair{
									{Name: proto.String(ifIndexLabelName), Value: proto.String("3")},
									{Name: proto.String("other"), Value: proto.String("label")},
								},
							},
						},
					},
					{
						Name: proto.String("all_disconnected"),
						Metric: []*dto.Metric{
							{
								Label: []*dto.LabelPair{
									{Name: proto.String(ifIndexLabelName), Value: proto.String("2")},
								},
							},
							{
								Label: []*dto.LabelPair{
									{Name: proto.String(ifIndexLabelName), Value: proto.String("4")},
								},
							},
						},
					},
					{
						Name: proto.String("all_connected"),
						Metric: []*dto.Metric{
							{
								Label: []*dto.LabelPair{
									{Name: proto.String("other"), Value: proto.String("label")},
									{Name: proto.String(ifIndexLabelName), Value: proto.String("1")},
								},
							},
							{
								Label: []*dto.LabelPair{
									{Name: proto.String(ifIndexLabelName), Value: proto.String("3")},
									{Name: proto.String("other"), Value: proto.String("label")},
								},
							},
						},
					},
				},
			},
			want: []*dto.MetricFamily{
				{
					Name: proto.String("ifInOctets"),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: proto.String(ifIndexLabelName), Value: proto.String("1")},
								{Name: proto.String("other"), Value: proto.String("label")},
							},
						},
						{
							Label: []*dto.LabelPair{
								{Name: proto.String(ifIndexLabelName), Value: proto.String("3")},
								{Name: proto.String("other"), Value: proto.String("label")},
							},
						},
					},
				},
				{
					Name: proto.String("all_connected"),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: proto.String("other"), Value: proto.String("label")},
								{Name: proto.String(ifIndexLabelName), Value: proto.String("1")},
							},
						},
						{
							Label: []*dto.LabelPair{
								{Name: proto.String(ifIndexLabelName), Value: proto.String("3")},
								{Name: proto.String("other"), Value: proto.String("label")},
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
						Name: proto.String(ifOperStatusMetricName),
						Metric: []*dto.Metric{
							{
								Label: []*dto.LabelPair{
									{Name: proto.String(ifIndexLabelName), Value: proto.String("1")},
									{Name: proto.String("other"), Value: proto.String("label")},
								},
							},
							{
								Label: []*dto.LabelPair{
									{Name: proto.String(ifIndexLabelName), Value: proto.String("2")},
									{Name: proto.String("other"), Value: proto.String("label")},
								},
							},
							{
								Label: []*dto.LabelPair{
									{Name: proto.String(ifIndexLabelName), Value: proto.String("3")},
									{Name: proto.String("other"), Value: proto.String("label")},
								},
							},
						},
					},
					{
						Name: proto.String("another_metric"),
						Metric: []*dto.Metric{
							{
								Label: []*dto.LabelPair{
									{Name: proto.String("notIfIndex"), Value: proto.String("1")},
								},
							},
							{
								Label: []*dto.LabelPair{
									{Name: proto.String("notIfIndex"), Value: proto.String("2")},
								},
							},
						},
					},
					{
						Name: proto.String("both_ifIndex_and_not"),
						Metric: []*dto.Metric{
							{
								Label: []*dto.LabelPair{
									{Name: proto.String("other"), Value: proto.String("label")},
									{Name: proto.String(ifIndexLabelName), Value: proto.String("2")},
								},
							},
							{
								Label: []*dto.LabelPair{
									{Name: proto.String("other"), Value: proto.String("label")},
								},
							},
						},
					},
				},
			},
			want: []*dto.MetricFamily{
				{
					Name: proto.String(ifOperStatusMetricName),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: proto.String(ifIndexLabelName), Value: proto.String("1")},
								{Name: proto.String("other"), Value: proto.String("label")},
							},
						},
						{
							Label: []*dto.LabelPair{
								{Name: proto.String(ifIndexLabelName), Value: proto.String("2")},
								{Name: proto.String("other"), Value: proto.String("label")},
							},
						},
						{
							Label: []*dto.LabelPair{
								{Name: proto.String(ifIndexLabelName), Value: proto.String("3")},
								{Name: proto.String("other"), Value: proto.String("label")},
							},
						},
					},
				},
				{
					Name: proto.String("another_metric"),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: proto.String("notIfIndex"), Value: proto.String("1")},
							},
						},
						{
							Label: []*dto.LabelPair{
								{Name: proto.String("notIfIndex"), Value: proto.String("2")},
							},
						},
					},
				},
				{
					Name: proto.String("both_ifIndex_and_not"),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: proto.String("other"), Value: proto.String("label")},
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

			if diff := cmp.Diff(tt.want, got); diff != "" {
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
			state:     registry.GatherState{},
			status:    types.StatusOk,
			msg:       "",
			inputFile: "linux-snmpd.input",
			wantFile:  "linux-snmpd.want",
		},
		{
			name:      "linux snmpd noFilter",
			state:     registry.GatherState{NoFilter: true},
			status:    types.StatusOk,
			msg:       "",
			inputFile: "linux-snmpd.input",
			wantFile:  "linux-snmpd-nofilter.want",
		},
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			input, err := fileToMFS(filepath.Join("testdata", tt.inputFile))
			if err != nil {
				t.Fatal(err)
			}

			want, err := fileToMFS(filepath.Join("testdata", tt.wantFile))
			if err != nil {
				t.Fatal(err)
			}

			got := processMFS(input, tt.state, tt.status, tt.msg)

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("processMFS() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNewMock(t *testing.T) {
	tests := []struct {
		name      string
		opt       TargetOptions
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
				"fqdn":     "example.com",
			},
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			wantFacts := make(map[string]string, len(tt.mockFacts))
			for k, v := range tt.mockFacts {
				wantFacts[k] = v
			}

			tgt := NewMock(tt.opt, tt.mockFacts)

			got, err := tgt.Facts(context.Background(), 0)
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
			name:       "PowerConnect 5448",
			metricFile: "powerconnect-5448.metrics",
			want:       "if_mib",
		},
		{
			name:       "Cisco N9000",
			metricFile: "cisco-n9000.metrics",
			want:       "cisco",
		},
		{
			name:       "Cisco C2960",
			metricFile: "cisco-c2960.metrics",
			want:       "cisco",
		},
		{
			name:       "hp-printer",
			metricFile: "hp-printer.metrics",
			want:       "printer_mib",
		},
		{
			name:       "anything else",
			metricFile: "linux-snmpd.input",
			want:       "if_mib",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := ioutil.ReadFile(filepath.Join("testdata", tt.metricFile))
			if err != nil {
				t.Fatal(err)
			}

			tr := newTarget(TargetOptions{}, nil, nil)
			tr.mockPerModule = map[string][]byte{
				snmpDiscoveryModule: body,
			}

			got, err := tr.Module(context.Background())
			if err != nil {
				t.Error(err)
			}

			if got != tt.want {
				t.Errorf("Target.Module() = %v, want %v", got, tt.want)
			}
		})
	}
}
