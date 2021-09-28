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
	"glouton/prometheus/registry"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

func Test_factFromPoints(t *testing.T) {
	now := time.Date(2021, 9, 28, 9, 43, 4, 1234, time.UTC)

	tests := []struct {
		name       string
		metricFile string
		want       map[string]string
	}{
		{
			name:       "PowerConnect 5448",
			metricFile: "powerconnect-5448.metrics",
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
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			fd, err := os.Open(filepath.Join("testdata", tt.metricFile))
			if err != nil {
				t.Fatal(err)
			}

			var parser expfmt.TextParser

			tmpMap, err := parser.TextToMetricFamilies(fd)
			if err != nil {
				t.Fatal(err)
			}

			tmp := make([]*dto.MetricFamily, 0, len(tmpMap))

			for _, v := range tmpMap {
				tmp = append(tmp, v)
			}

			result := registry.FamiliesToMetricPoints(time.Now(), tmp)
			got := factFromPoints(result, now)

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("factFromPoints() missmatch:\n%s", diff)
			}
		})
	}
}
