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

//nolint:dupl
package ipmi

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

const (
	sensorTypeTemperature    = "Temperature"
	sensorTypeFan            = "Fan"
	sensorTypeCurrent        = "Current"
	sensorTypeVoltage        = "Voltage"
	sensorNameInletTemp      = "Inlet Temp"
	sensorNamePwrConsumption = "Pwr Consumption"
	sensorNameTemp           = "Temp"
	unitRPM                  = "RPM"
	testCurrent1             = "Current 1"
	testVoltage1             = "Voltage 1"
)

func Test_freeIPMIdecodeSensors(t *testing.T) {
	tests := []struct {
		name     string
		testfile string
		want     []sensorData
	}{
		{
			name:     "R310",
			testfile: "dell-r310-ipmi-sensors.txt",
			want: []sensorData{
				{Name: "Ambient Temp", Type: sensorTypeTemperature, Value: 29, Units: "C"},
				{Name: "FAN MOD 1A RPM", Type: sensorTypeFan, Value: 6720, Units: unitRPM},
				{Name: "FAN MOD 1B RPM", Type: sensorTypeFan, Value: 5640, Units: unitRPM},
				{Name: "FAN MOD 2A RPM", Type: sensorTypeFan, Value: 6720, Units: unitRPM},
				{Name: "FAN MOD 2B RPM", Type: sensorTypeFan, Value: 5520, Units: unitRPM},
				{Name: "FAN MOD 3A RPM", Type: sensorTypeFan, Value: 4920, Units: unitRPM},
				{Name: "FAN MOD 3B RPM", Type: sensorTypeFan, Value: 3840, Units: unitRPM},
				{Name: "FAN MOD 4A RPM", Type: sensorTypeFan, Value: 5400, Units: unitRPM},
				{Name: "FAN MOD 4B RPM", Type: sensorTypeFan, Value: 3720, Units: unitRPM},
				{Name: "FAN MOD 5A RPM", Type: sensorTypeFan, Value: 5400, Units: unitRPM},
				{Name: "FAN MOD 5B RPM", Type: sensorTypeFan, Value: 3600, Units: unitRPM},
				{Name: testCurrent1, Type: sensorTypeCurrent, Value: 0.28, Units: "A"},
				{Name: testVoltage1, Type: sensorTypeVoltage, Value: 236, Units: "V"},
				{Name: "System Level", Type: sensorTypeCurrent, Value: 84, Units: "W"},
			},
		},
		{
			name:     "R320",
			testfile: "dell-r320-ipmi-sensors.txt",
			want: []sensorData{
				{Name: "Fan1A RPM", Type: sensorTypeFan, Value: 2160, Units: unitRPM},
				{Name: "Fan1B RPM", Type: sensorTypeFan, Value: 1920, Units: unitRPM},
				{Name: "Fan2A RPM", Type: sensorTypeFan, Value: 3360, Units: unitRPM},
				{Name: "Fan2B RPM", Type: sensorTypeFan, Value: 2400, Units: unitRPM},
				{Name: "Fan3A RPM", Type: sensorTypeFan, Value: 3360, Units: unitRPM},
				{Name: "Fan3B RPM", Type: sensorTypeFan, Value: 2400, Units: unitRPM},
				{Name: "Fan4A RPM", Type: sensorTypeFan, Value: 3120, Units: unitRPM},
				{Name: "Fan4B RPM", Type: sensorTypeFan, Value: 2280, Units: unitRPM},
				{Name: "Fan5A RPM", Type: sensorTypeFan, Value: 3120, Units: unitRPM},
				{Name: "Fan5B RPM", Type: sensorTypeFan, Value: 2280, Units: unitRPM},
				{Name: sensorNameInletTemp, Type: sensorTypeTemperature, Value: 30, Units: "C"},
				{Name: testCurrent1, Type: sensorTypeCurrent, Value: 0.2, Units: "A"},
				{Name: testVoltage1, Type: sensorTypeVoltage, Value: 238, Units: "V"},
				{Name: sensorNamePwrConsumption, Type: sensorTypeCurrent, Value: 56, Units: "W"},
				{Name: sensorNameTemp, Type: sensorTypeTemperature, Value: 66, Units: "C"},
			},
		},
		{
			name:     "R320-2",
			testfile: "dell-r320-2-ipmi-sensors.txt",
			want: []sensorData{
				{Name: "Fan1A RPM", Type: sensorTypeFan, Value: 2520, Units: unitRPM},
				{Name: "Fan1B RPM", Type: sensorTypeFan, Value: 2520, Units: unitRPM},
				{Name: "Fan2A RPM", Type: sensorTypeFan, Value: 3720, Units: unitRPM},
				{Name: "Fan2B RPM", Type: sensorTypeFan, Value: 2640, Units: unitRPM},
				{Name: "Fan3A RPM", Type: sensorTypeFan, Value: 3720, Units: unitRPM},
				{Name: "Fan3B RPM", Type: sensorTypeFan, Value: 2760, Units: unitRPM},
				{Name: "Fan4A RPM", Type: sensorTypeFan, Value: 3360, Units: unitRPM},
				{Name: "Fan4B RPM", Type: sensorTypeFan, Value: 2400, Units: unitRPM},
				{Name: "Fan5A RPM", Type: sensorTypeFan, Value: 3480, Units: unitRPM},
				{Name: "Fan5B RPM", Type: sensorTypeFan, Value: 2520, Units: unitRPM},
				{Name: sensorNameInletTemp, Type: sensorTypeTemperature, Value: 31, Units: "C"},
				{Name: testCurrent1, Type: sensorTypeCurrent, Value: 0.4, Units: "A"},
				{Name: testVoltage1, Type: sensorTypeVoltage, Value: 238, Units: "V"},
				{Name: sensorNamePwrConsumption, Type: sensorTypeCurrent, Value: 70, Units: "W"},
				{Name: sensorNameTemp, Type: sensorTypeTemperature, Value: 67, Units: "C"},
			},
		},
		{
			name:     testNameR720xd,
			testfile: "dell-r720xd-ipmi-sensors.txt",
			want: []sensorData{
				{Name: "Fan1 RPM", Type: sensorTypeFan, Value: 10440, Units: unitRPM},
				{Name: "Fan2 RPM", Type: sensorTypeFan, Value: 10560, Units: unitRPM},
				{Name: "Fan3 RPM", Type: sensorTypeFan, Value: 10200, Units: unitRPM},
				{Name: "Fan4 RPM", Type: sensorTypeFan, Value: 10200, Units: unitRPM},
				{Name: "Fan5 RPM", Type: sensorTypeFan, Value: 10200, Units: unitRPM},
				{Name: "Fan6 RPM", Type: sensorTypeFan, Value: 10080, Units: unitRPM},
				{Name: sensorNameInletTemp, Type: sensorTypeTemperature, Value: 28, Units: "C"},
				{Name: "Exhaust Temp", Type: sensorTypeTemperature, Value: 43, Units: "C"},
				{Name: "Current 2", Type: sensorTypeCurrent, Value: 1.6, Units: "A"},
				{Name: "Voltage 2", Type: sensorTypeVoltage, Value: 236, Units: "V"},
				{Name: sensorNamePwrConsumption, Type: sensorTypeCurrent, Value: 378, Units: "W"},
				{Name: sensorNameTemp, Type: sensorTypeTemperature, Value: 55, Units: "C"},
				{Name: sensorNameTemp, Type: sensorTypeTemperature, Value: 55, Units: "C"},
			},
		},
		{
			name:     testNameR720xd2,
			testfile: "dell-r720xd-2-ipmi-sensors.txt",
			want: []sensorData{
				{Name: "Fan1", Type: sensorTypeFan, Value: 4920, Units: unitRPM},
				{Name: "Fan2", Type: sensorTypeFan, Value: 5160, Units: unitRPM},
				{Name: "Fan3", Type: sensorTypeFan, Value: 5040, Units: unitRPM},
				{Name: "Fan4", Type: sensorTypeFan, Value: 5280, Units: unitRPM},
				{Name: "Fan5", Type: sensorTypeFan, Value: 5520, Units: unitRPM},
				{Name: "Fan6", Type: sensorTypeFan, Value: 5640, Units: unitRPM},
				{Name: sensorNameInletTemp, Type: sensorTypeTemperature, Value: 23, Units: "C"},
				{Name: "Exhaust Temp", Type: sensorTypeTemperature, Value: 36, Units: "C"},
				{Name: testCurrent1, Type: sensorTypeCurrent, Value: 0.4, Units: "A"},
				{Name: "Current 2", Type: sensorTypeCurrent, Value: 0.4, Units: "A"},
				{Name: testVoltage1, Type: sensorTypeVoltage, Value: 228, Units: "V"},
				{Name: "Voltage 2", Type: sensorTypeVoltage, Value: 228, Units: "V"},
				{Name: sensorNamePwrConsumption, Type: sensorTypeCurrent, Value: 168, Units: "W"},
				{Name: sensorNameTemp, Type: sensorTypeTemperature, Value: 44, Units: "C"},
			},
		},
		{
			name:     testNameHPProliantDL360G7,
			testfile: "hp-dl360-g7-ipmi-sensors.txt",
			want: []sensorData{
				{Name: "Power Supply 1", Type: "Power Supply", Value: 35, Units: "W"},
				{Name: "Power Supply 2", Type: "Power Supply", Value: 40, Units: "W"},
				{Name: "Fan Block 1", Type: sensorTypeFan, Value: 19.60, Units: "%"},
				{Name: "Fan Block 3", Type: sensorTypeFan, Value: 19.60, Units: "%"},
				{Name: "Fan Block 4", Type: sensorTypeFan, Value: 19.60, Units: "%"},
				{Name: "Fans", Type: sensorTypeFan, Units: "%"},
				{Name: "Temp 1", Type: sensorTypeTemperature, Value: 23, Units: "C"},
				{Name: "Temp 2", Type: sensorTypeTemperature, Value: 40, Units: "C"},
				{Name: "Temp 4", Type: sensorTypeTemperature, Value: 34, Units: "C"},
				{Name: "Temp 5", Type: sensorTypeTemperature, Value: 37, Units: "C"},
				{Name: "Temp 6", Type: sensorTypeTemperature, Value: 33, Units: "C"},
				{Name: "Temp 7", Type: sensorTypeTemperature, Value: 35, Units: "C"},
				{Name: "Temp 9", Type: sensorTypeTemperature, Value: 35, Units: "C"},
				{Name: "Temp 11", Type: sensorTypeTemperature, Value: 35, Units: "C"},
				{Name: "Temp 12", Type: sensorTypeTemperature, Value: 36, Units: "C"},
				{Name: "Temp 13", Type: sensorTypeTemperature, Value: 49, Units: "C"},
				{Name: "Temp 14", Type: sensorTypeTemperature, Value: 31, Units: "C"},
				{Name: "Temp 15", Type: sensorTypeTemperature, Value: 37, Units: "C"},
				{Name: "Temp 16", Type: sensorTypeTemperature, Value: 34, Units: "C"},
				{Name: "Temp 17", Type: sensorTypeTemperature, Value: 31, Units: "C"},
				{Name: "Temp 18", Type: sensorTypeTemperature, Value: 42, Units: "C"},
				{Name: "Temp 19", Type: sensorTypeTemperature, Value: 40, Units: "C"},
				{Name: "Temp 20", Type: sensorTypeTemperature, Value: 41, Units: "C"},
				{Name: "Temp 21", Type: sensorTypeTemperature, Value: 48, Units: "C"},
				{Name: "Temp 22", Type: sensorTypeTemperature, Value: 50, Units: "C"},
				{Name: "Temp 23", Type: sensorTypeTemperature, Value: 43, Units: "C"},
				{Name: "Temp 24", Type: sensorTypeTemperature, Value: 51, Units: "C"},
				{Name: "Temp 25", Type: sensorTypeTemperature, Value: 38, Units: "C"},
				{Name: "Temp 26", Type: sensorTypeTemperature, Value: 50, Units: "C"},
				{Name: "Temp 27", Type: sensorTypeTemperature, Value: 35, Units: "C"},
				{Name: "Temp 28", Type: sensorTypeTemperature, Value: 71, Units: "C"},
				{Name: "Power Meter", Type: sensorTypeCurrent, Value: 98, Units: "W"},
			},
		},
		{
			name:     "HP server",
			testfile: "hp-ipmi-sensors.txt",
			want: []sensorData{
				{Name: "01-Inlet Ambient", Type: sensorTypeTemperature, Value: 23, Units: "C"},
				{Name: "02-CPU 1", Type: sensorTypeTemperature, Value: 40, Units: "C"},
				{Name: "06-P1 DIMM 7-12", Type: sensorTypeTemperature, Value: 26, Units: "C"},
				{Name: "12-HD Max", Type: sensorTypeTemperature, Value: 35, Units: "C"},
				{Name: "14-Stor Batt 1", Type: sensorTypeTemperature, Value: 24, Units: "C"},
				{Name: "15-Front Ambient", Type: sensorTypeTemperature, Value: 24, Units: "C"},
				{Name: "16-VR P1", Type: sensorTypeTemperature, Value: 30, Units: "C"},
				{Name: "18-VR P1 Mem 1", Type: sensorTypeTemperature, Value: 28, Units: "C"},
				{Name: "19-VR P1 Mem 2", Type: sensorTypeTemperature, Value: 27, Units: "C"},
				{Name: "22-Chipset", Type: sensorTypeTemperature, Value: 37, Units: "C"},
				{Name: "23-BMC", Type: sensorTypeTemperature, Value: 66, Units: "C"},
				{Name: "24-BMC Zone", Type: sensorTypeTemperature, Value: 38, Units: "C"},
				{Name: "25-HD Controller", Type: sensorTypeTemperature, Value: 52, Units: "C"},
				{Name: "26-HD Cntlr Zone", Type: sensorTypeTemperature, Value: 31, Units: "C"},
				{Name: "29-I/O Zone", Type: sensorTypeTemperature, Value: 29, Units: "C"},
				{Name: "31-PCI 1 Zone", Type: sensorTypeTemperature, Value: 30, Units: "C"},
				{Name: "33-PCI 2 Zone", Type: sensorTypeTemperature, Value: 30, Units: "C"},
				{Name: "38-Battery Zone", Type: sensorTypeTemperature, Value: 31, Units: "C"},
				{Name: "43-E-Fuse", Type: sensorTypeTemperature, Value: 20, Units: "C"},
				{Name: "44-P/S 2 Zone", Type: sensorTypeTemperature, Value: 26, Units: "C"},
				{Name: "Power Meter", Type: "Other Units Based Sensor", Value: 60, Units: "W"},
				{Name: "CPU Utilization", Type: "Processor", Value: 15, Units: "unspecified"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			content, err := os.ReadFile(filepath.Join("testdata", tt.testfile))
			if err != nil {
				t.Fatal(err)
			}

			got, err := decodeFreeIPMISensors(content)
			if err != nil {
				t.Error(err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("decodeSensors() mismatch: (-want +got)\n%s", diff)
			}
		})
	}
}

func Test_freeIPMIdecodeDCMI(t *testing.T) {
	tests := []struct {
		name     string
		testfile string
		want     []powerReading
	}{
		{
			name:     "wrong",
			testfile: "wrong-output-ipmi-sensors.txt",
			want:     []powerReading{},
		},
		{
			name:     "r310",
			testfile: "dell-r310-ipmi-dcmi.txt",
			want:     []powerReading{},
		},
		{
			name:     "r310-enhanced",
			testfile: "dell-r310-ipmi-dcmi-enhanced.txt",
			want:     []powerReading{},
		},
		{
			name:     "r320",
			testfile: "dell-r320-ipmi-dcmi.txt",
			want: []powerReading{
				{
					ReportPeriod: 26547088 * time.Second,
					Timestamp:    time.Date(2023, 7, 25, 15, 34, 31, 0, time.UTC),
					Active:       true,
					Current:      61,
					Minimum:      0,
					Maximum:      133,
					Average:      63,
				},
			},
		},
		{
			name:     "r320-2",
			testfile: "dell-r320-2-ipmi-dcmi.txt",
			want: []powerReading{
				{
					ReportPeriod: 26547205 * time.Second,
					Timestamp:    time.Date(2023, 7, 25, 15, 38, 52, 0, time.UTC),
					Active:       true,
					Current:      75,
					Minimum:      0,
					Maximum:      159,
					Average:      73,
				},
			},
		},
		{
			name:     testNameR720xd,
			testfile: "dell-r720xd-ipmi-dcmi.txt",
			want: []powerReading{
				{
					ReportPeriod: 26351204 * time.Second,
					Timestamp:    time.Date(2023, 7, 25, 15, 41, 53, 0, time.UTC),
					Active:       true,
					Current:      391,
					Minimum:      123,
					Maximum:      637,
					Average:      374,
				},
			},
		},
		{
			name:     testNameR720xd2,
			testfile: "dell-r720xd-2-ipmi-dcmi.txt",
			want: []powerReading{
				{
					ReportPeriod: 1 * time.Second,
					Timestamp:    time.Date(2023, 7, 25, 15, 44, 18, 0, time.UTC),
					Active:       true,
					Current:      170,
					Minimum:      69,
					Maximum:      312,
					Average:      173,
				},
			},
		},
		{
			name:     "R720xd-2-enhanced",
			testfile: "dell-r720xd-2-ipmi-dcmi-enhanced.txt",
			want: []powerReading{
				{
					ReportPeriod: 0 * time.Second,
					Timestamp:    time.Date(2023, 7, 25, 15, 44, 18, 0, time.UTC),
					Active:       true,
					Current:      170,
					Minimum:      69,
					Maximum:      312,
					Average:      173,
				},
				{
					ReportPeriod: 1 * time.Hour,
					Timestamp:    time.Date(2023, 7, 25, 15, 0, 56, 0, time.UTC),
					Active:       true,
					Current:      170,
					Minimum:      166,
					Maximum:      205,
					Average:      170,
				},
				{
					ReportPeriod: 24 * time.Hour,
					Timestamp:    time.Date(2023, 7, 25, 15, 0, 56, 0, time.UTC),
					Active:       true,
					Current:      170,
					Minimum:      166,
					Maximum:      205,
					Average:      171,
				},
				{
					ReportPeriod: 7 * 24 * time.Hour,
					Timestamp:    time.Date(2023, 7, 22, 7, 59, 58, 0, time.UTC),
					Active:       true,
					Current:      170,
					Minimum:      166,
					Maximum:      215,
					Average:      172,
				},
			},
		},
		{
			name:     testNameHPProliantDL360G7,
			testfile: "hp-dl360-g7-ipmi-dcmi.txt",
			want: []powerReading{
				{
					ReportPeriod: 300 * time.Second,
					Timestamp:    time.Date(2023, 7, 25, 11, 51, 54, 0, time.UTC),
					Active:       true,
					Current:      98,
					Minimum:      94,
					Maximum:      99,
					Average:      95,
				},
			},
		},
		{
			name:     "HP Proliant DL360 G7 enhanced",
			testfile: "hp-dl360-g7-ipmi-dcmi-enhanced.txt",
			want: []powerReading{
				{
					ReportPeriod: 10 * time.Second,
					Timestamp:    time.Date(2023, 7, 25, 11, 51, 54, 0, time.UTC),
					Active:       true,
					Current:      98,
					Minimum:      98,
					Maximum:      99,
					Average:      98,
				},
				{
					ReportPeriod: 300 * time.Second,
					Timestamp:    time.Date(2023, 7, 25, 11, 51, 54, 0, time.UTC),
					Active:       true,
					Current:      98,
					Minimum:      94,
					Maximum:      99,
					Average:      95,
				},
				{
					ReportPeriod: 24 * time.Hour,
					Timestamp:    time.Date(2023, 7, 25, 11, 51, 54, 0, time.UTC),
					Active:       true,
					Current:      98,
					Minimum:      35,
					Maximum:      118,
					Average:      95,
				},
			},
		},
		{
			name:     "HP",
			testfile: "hp-ipmi-dcmi.txt",
			want: []powerReading{
				{
					ReportPeriod: 300 * time.Second,
					Timestamp:    time.Date(2023, 7, 25, 15, 23, 5, 0, time.UTC),
					Active:       true,
					Current:      62,
					Minimum:      61,
					Maximum:      86,
					Average:      62,
				},
			},
		},
		{
			name:     "HP enhanced",
			testfile: "hp-ipmi-dcmi-enhanced.txt",
			want: []powerReading{
				{
					ReportPeriod: 10 * time.Second,
					Timestamp:    time.Date(2023, 7, 25, 15, 23, 5, 0, time.UTC),
					Active:       true,
					Current:      62,
					Minimum:      62,
					Maximum:      75,
					Average:      62,
				},
				{
					ReportPeriod: 300 * time.Second,
					Timestamp:    time.Date(2023, 7, 25, 15, 23, 5, 0, time.UTC),
					Active:       true,
					Current:      62,
					Minimum:      61,
					Maximum:      86,
					Average:      62,
				},
				{
					ReportPeriod: 24 * time.Hour,
					Timestamp:    time.Date(2023, 7, 25, 15, 23, 5, 0, time.UTC),
					Active:       true,
					Current:      62,
					Minimum:      60,
					Maximum:      103,
					Average:      61,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			content, err := os.ReadFile(filepath.Join("testdata", tt.testfile))
			if err != nil {
				t.Fatal(err)
			}

			got, err := decodeFreeIPMIDCMI(content)
			if err != nil {
				t.Error(err)
			}

			timeComparer := cmp.Comparer(func(x, y time.Time) bool {
				return x.Equal(y)
			})

			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty(), timeComparer); diff != "" {
				t.Errorf("decodeSensors() mismatch: (-want +got)\n%s", diff)
			}
		})
	}
}
