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

package agent

import (
	"net/url"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/prometheus/scrapper"
	"github.com/bleemeo/glouton/store"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestParseIPOutput(t *testing.T) {
	cases := []struct {
		description string
		in          string
		want        string
	}{
		{
			description: `Output from "ip route get 8.8.8.8;ip address show"`,
			in: `8.8.8.8 via 10.0.2.2 dev eth0  src 10.0.2.15 
    cache
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN group default 
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
       valid_lft forever preferred_lft forever
    inet6 ::1/128 scope host 
       valid_lft forever preferred_lft forever
2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc pfifo_fast state UP group default qlen 1000
    link/ether 08:00:27:72:ca:f1 brd ff:ff:ff:ff:ff:ff
    inet 10.0.2.15/24 brd 10.0.2.255 scope global eth0
       valid_lft forever preferred_lft forever
    inet6 fe80::a00:27ff:fe72:caf1/64 scope link 
       valid_lft forever preferred_lft forever
3: docker0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue state UP group default 
    link/ether 02:42:8d:e2:74:26 brd ff:ff:ff:ff:ff:ff
    inet 172.17.0.1/16 brd 172.17.255.255 scope global docker0
       valid_lft forever preferred_lft forever
    inet6 fe80::42:8dff:fee2:7426/64 scope link 
       valid_lft forever preferred_lft forever
5: vethde299e2: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue master docker0 state UP group default
    link/ether fe:b8:9f:9e:36:aa brd ff:ff:ff:ff:ff:ff
    inet6 fe80::fcb8:9fff:fe9e:36aa/64 scope link
       valid_lft forever preferred_lft forever
`,
			want: "08:00:27:72:ca:f1",
		},
		{
			description: "don't crash on invalid input",
			in:          `8.8.8.8 via 10.0.2.2 dev eth0  src`,
			want:        "",
		},
	}
	for _, c := range cases {
		got := parseIPOutput([]byte(c.in))
		if got != c.want {
			t.Errorf("parseIPOutput([case %s]) == %#v, want %#v", c.description, got, c.want)
		}
	}
}

//nolint:dupl
func Test_prometheusConfigToURLs(t *testing.T) {
	mustParse := func(text string) *url.URL {
		u, err := url.Parse(text)
		if err != nil {
			t.Fatal(err)
		}

		return u
	}

	tests := []struct {
		name        string
		cfgFilename string
		want        []*scrapper.Target
	}{
		{
			name:        "old",
			cfgFilename: "testdata/old-prometheus-targets.conf",
			want: []*scrapper.Target{
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "test1",
						types.LabelMetaScrapeInstance: "localhost:9090",
					},
					URL: mustParse("http://localhost:9090/metrics"),
				},
			},
		},
		{
			name:        "new",
			cfgFilename: "testdata/new-prometheus-targets.conf",
			want: []*scrapper.Target{
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "test1",
						types.LabelMetaScrapeInstance: "localhost:9090",
					},
					URL: mustParse("http://localhost:9090/metrics"),
				},
			},
		},
		{
			name:        "both",
			cfgFilename: "testdata/both-prometheus-targets.conf",
			want: []*scrapper.Target{
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "new",
						types.LabelMetaScrapeInstance: "new:9090",
					},
					URL: mustParse("http://new:9090/metrics"),
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "old",
						types.LabelMetaScrapeInstance: "old:9090",
					},
					URL: mustParse("http://old:9090/metrics"),
				},
			},
		},
		{
			name:        "test-with-allow-deny",
			cfgFilename: "testdata/test-prometheus-targets.conf",
			want: []*scrapper.Target{
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "use-global",
						types.LabelMetaScrapeInstance: "localhost:9090",
					},
					URL: mustParse("http://localhost:9090/metrics"),
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "reset-global",
						types.LabelMetaScrapeInstance: "localhost:9090",
					},
					URL:       mustParse("http://localhost:9090/metrics"),
					AllowList: []string{},
					DenyList:  []string{},
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "set-allow",
						types.LabelMetaScrapeInstance: "localhost:9090",
					},
					AllowList: []string{"local2{item=~\"plop\"}"},
					URL:       mustParse("http://localhost:9090/metrics"),
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "set-deny",
						types.LabelMetaScrapeInstance: "localhost:9090",
					},
					DenyList: []string{"local1", "local2{item!~\"plop\"}"},
					URL:      mustParse("http://localhost:9090/metrics"),
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "set-all",
						types.LabelMetaScrapeInstance: "localhost:9090",
					},
					AllowList: []string{"hello", "world"},
					DenyList:  []string{"test"},
					URL:       mustParse("http://localhost:9090/metrics"),
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "old",
						types.LabelMetaScrapeInstance: "old:9090",
					},
					URL: mustParse("http://old:9090/metrics"),
				},
			},
		},
		{
			name:        "test-with-allow-deny-2",
			cfgFilename: "testdata/test-prometheus-targets.conf",
			want: []*scrapper.Target{
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "use-global",
						types.LabelMetaScrapeInstance: "localhost:9090",
					},
					URL: mustParse("http://localhost:9090/metrics"),
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "reset-global",
						types.LabelMetaScrapeInstance: "localhost:9090",
					},
					URL:       mustParse("http://localhost:9090/metrics"),
					AllowList: []string{},
					DenyList:  []string{},
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "set-allow",
						types.LabelMetaScrapeInstance: "localhost:9090",
					},
					AllowList: []string{"local2{item=~\"plop\"}"},
					URL:       mustParse("http://localhost:9090/metrics"),
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "set-deny",
						types.LabelMetaScrapeInstance: "localhost:9090",
					},
					DenyList: []string{"local1", "local2{item!~\"plop\"}"},
					URL:      mustParse("http://localhost:9090/metrics"),
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "set-all",
						types.LabelMetaScrapeInstance: "localhost:9090",
					},
					AllowList: []string{"hello", "world"},
					DenyList:  []string{"test"},
					URL:       mustParse("http://localhost:9090/metrics"),
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "old",
						types.LabelMetaScrapeInstance: "old:9090",
					},
					URL: mustParse("http://old:9090/metrics"),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Ignore warnings, they are already tested in the config package.
			config, _, _, err := config.Load(false, false, tt.cfgFilename)
			if err != nil {
				t.Fatalf("Failed to load config: %v", err)
			}

			got, warnings := prometheusConfigToURLs(config.Metric.Prometheus.Targets)
			if warnings != nil {
				t.Fatalf("Failed to convert config to Prometheus target: %v", warnings)
			}

			if diff := cmp.Diff(tt.want, got, cmpopts.IgnoreUnexported(scrapper.Target{})); diff != "" {
				t.Errorf("prometheusConfigToURLs() != want: %v", diff)
			}
		})
	}
}

// Test the smart_device_health_status metric description.
func TestSMARTStatus(t *testing.T) {
	t.Parallel()

	now := time.Now()

	tests := []struct {
		name           string
		points         []types.MetricPoint
		expectedMetric []types.MetricPoint
	}{
		{
			name:           "no-points",
			points:         nil,
			expectedMetric: nil,
		},
		{
			name: "multiple-critical-exit",
			points: []types.MetricPoint{
				// nvme0: health bad, exit 0
				{
					Point: types.Point{
						Time:  now,
						Value: 0,
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_ok",
						types.LabelDevice: "nvme0",
						types.LabelModel:  "PC401 NVMe SK hynix 512GB",
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: 0,
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_exit_status",
						types.LabelDevice: "nvme0",
						types.LabelModel:  "PC401 NVMe SK hynix 512GB",
					},
				},
				// sda: health ok, exit 0
				{
					Point: types.Point{
						Time:  now,
						Value: 1,
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_ok",
						types.LabelDevice: "sda",
						types.LabelModel:  "ST1000LM035",
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: 0,
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_exit_status",
						types.LabelDevice: "sda",
						types.LabelModel:  "ST1000LM035",
					},
				},
				// sdb: health bad, exit 0
				{
					Point: types.Point{
						Time:  now,
						Value: 0,
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_ok",
						types.LabelDevice: "sdb",
						types.LabelModel:  "ST1000LM035",
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: 0,
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_exit_status",
						types.LabelDevice: "sdb",
						types.LabelModel:  "ST1000LM035",
					},
				},
				// sdc: health not found, exit 1
				{
					Point: types.Point{
						Time:  now,
						Value: 1,
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_exit_status",
						types.LabelDevice: "sdc",
						types.LabelModel:  "ST1000LM035",
					},
				},
				// sdd: health ok, exit 1
				{
					Point: types.Point{
						Time:  now,
						Value: 1,
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_ok",
						types.LabelDevice: "sdd",
						types.LabelModel:  "ST1000LM035",
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: 1,
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_exit_status",
						types.LabelDevice: "sdd",
						types.LabelModel:  "ST1000LM035",
					},
				},
				// sde: health bad, exit 1
				{
					Point: types.Point{
						Time:  now,
						Value: 0,
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_ok",
						types.LabelDevice: "sde",
						types.LabelModel:  "ST1000LM035",
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: 1,
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_exit_status",
						types.LabelDevice: "sde",
						types.LabelModel:  "ST1000LM035",
					},
				},
			},
			expectedMetric: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "nvme0",
						types.LabelModel:  "PC401 NVMe SK hynix 512GB",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "SMART tests failed on nvme0 (PC401 NVMe SK hynix 512GB)",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "sda",
						types.LabelModel:  "ST1000LM035",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "SMART tests passed on sda (ST1000LM035)",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "sdb",
						types.LabelModel:  "ST1000LM035",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "SMART tests failed on sdb (ST1000LM035)",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "sdd",
						types.LabelModel:  "ST1000LM035",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "SMART tests passed on sdd (ST1000LM035)",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "sde",
						types.LabelModel:  "ST1000LM035",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "SMART tests failed on sde (ST1000LM035)",
						},
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			store := store.New("test store", time.Minute, time.Minute)

			store.PushPoints(t.Context(), test.points)

			gotMetric := statusFromLastPoint(now, store, "smart_device_health_ok", map[string]string{types.LabelName: "smart_device_health_status"}, smartHealthStatus)

			lessFunc := func(x, y types.MetricPoint) bool {
				return x.Labels[types.LabelDevice] < y.Labels[types.LabelDevice]
			}

			if diff := cmp.Diff(test.expectedMetric, gotMetric, cmpopts.SortSlices(lessFunc)); diff != "" {
				t.Fatalf("Got unexpected metric:\n %s", diff)
			}
		})
	}
}

// Test the upsd_battery_status metric description.
func TestUPSDBatteryStatus(t *testing.T) { //nolint:maintidx
	t.Parallel()

	const (
		statusCalibration    = 1 << 0
		statusSmartTrim      = 1 << 1
		statusSmartBoost     = 1 << 2
		statusOnLine         = 1 << 3
		statusOnBattery      = 1 << 4
		statusOverloaded     = 1 << 5
		statusBatteryLow     = 1 << 6
		statusReplaceBattery = 1 << 7
	)

	now := time.Now()

	tests := []struct {
		name           string
		points         []types.MetricPoint
		expectedMetric []types.MetricPoint
	}{
		{
			name:           "no-points",
			points:         nil,
			expectedMetric: nil,
		},
		{
			name: "multiple-critical",
			points: []types.MetricPoint{
				{
					Point: types.Point{
						Time: now,
						// On line
						Value: float64(1 << 3),
					},
					Labels: map[string]string{
						types.LabelName:    "upsd_status_flags",
						types.LabelUPSName: "on-line-with-service",
					},
					Annotations: types.MetricAnnotations{
						ServiceName: "upsd",
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(statusCalibration + statusSmartTrim + statusSmartBoost + statusOnLine),
					},
					Labels: map[string]string{
						types.LabelName:    "upsd_status_flags",
						types.LabelUPSName: "on-line-calibration-smart-trim-boost",
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(1 << 4),
					},
					Labels: map[string]string{
						types.LabelName:    "upsd_status_flags",
						types.LabelUPSName: "on-battery",
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(statusOnLine + statusOverloaded),
					},
					Labels: map[string]string{
						types.LabelName:    "upsd_status_flags",
						types.LabelUPSName: "overloaded-on-line",
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(statusOnBattery + statusOverloaded),
					},
					Labels: map[string]string{
						types.LabelName:    "upsd_status_flags",
						types.LabelUPSName: "overloaded-on-battery",
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(statusOnBattery + statusBatteryLow),
					},
					Labels: map[string]string{
						types.LabelName:    "upsd_status_flags",
						types.LabelUPSName: "battery-low-on-battery",
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(statusOnLine + statusOverloaded + statusBatteryLow),
					},
					Labels: map[string]string{
						types.LabelName:    "upsd_status_flags",
						types.LabelUPSName: "battery-low-overloaded-on-line",
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(statusOnLine + statusReplaceBattery),
					},
					Labels: map[string]string{
						types.LabelName:    "upsd_status_flags",
						types.LabelUPSName: "replace-battery-on-line",
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(statusOnBattery + statusOverloaded + statusBatteryLow + statusReplaceBattery),
					},
					Labels: map[string]string{
						types.LabelName:    "upsd_status_flags",
						types.LabelUPSName: "replace-battery-on-battery-overloaded-battery-low",
					},
				},
			},
			expectedMetric: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:    "upsd_battery_status",
						types.LabelUPSName: "on-line-with-service",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "On line, battery ok",
						},
						ServiceName: "upsd",
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:    "upsd_battery_status",
						types.LabelUPSName: "on-line-calibration-smart-trim-boost",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "On line, battery ok",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:    "upsd_battery_status",
						types.LabelUPSName: "on-battery",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "UPS is running on battery",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:    "upsd_battery_status",
						types.LabelUPSName: "overloaded-on-line",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "UPS is overloaded",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:    "upsd_battery_status",
						types.LabelUPSName: "overloaded-on-battery",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "UPS is overloaded",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:    "upsd_battery_status",
						types.LabelUPSName: "battery-low-on-battery",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "Battery is low",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:    "upsd_battery_status",
						types.LabelUPSName: "battery-low-overloaded-on-line",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "Battery is low",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:    "upsd_battery_status",
						types.LabelUPSName: "replace-battery-on-line",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "Battery should be replaced",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:    "upsd_battery_status",
						types.LabelUPSName: "replace-battery-on-battery-overloaded-battery-low",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "Battery should be replaced",
						},
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			store := store.New("test store", time.Minute, time.Minute)

			store.PushPoints(t.Context(), test.points)

			gotMetric := statusFromLastPoint(now, store, "upsd_status_flags", map[string]string{types.LabelName: "upsd_battery_status"}, upsdBatteryStatus)

			lessFunc := func(x, y types.MetricPoint) bool {
				return x.Labels[types.LabelUPSName] < y.Labels[types.LabelUPSName]
			}

			if diff := cmp.Diff(test.expectedMetric, gotMetric, cmpopts.SortSlices(lessFunc)); diff != "" {
				t.Fatalf("Got unexpected metric:\n %s", diff)
			}
		})
	}
}
