// Copyright 2015-2023 Bleemeo
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

package ipmi

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

//nolint:gochecknoglobals
var (
	freeipmiSensorsCmd   = []string{"ipmi-sensors", "-W", "discretereading", "--sdr-cache-recreate"}
	freeipmiDCMIEnhanced = []string{"ipmi-dcmi", "--get-enhanced-system-power-statistics"}
	freeipmiDCMISimple   = []string{"ipmi-dcmi", "--get-system-power-statistics"}
)

// decodeFreeIPMISensors decode output of "ipmi-sensors". Measure with "N/A" as value are skipped.
// When error during parsing occur, the line with issue is skipped, so the list of sensorData will always be
// valid (possibly empty, but valid). error could be a MultiError.
func decodeFreeIPMISensors(output []byte) ([]sensorData, error) {
	var (
		result []sensorData
		errs   prometheus.MultiError
	)

	scanner := bufio.NewScanner(bytes.NewReader(output))

	for scanner.Scan() {
		line := scanner.Text()

		fields := strings.Split(line, "|")
		if len(fields) != 6 {
			continue
		}

		for i, f := range fields {
			fields[i] = strings.TrimSpace(f)
		}

		// fields are
		// ID  | Name             | Type                     | Reading    | Units | Event

		// skip absent reading (a.k.a value)
		if fields[3] == "N/A" {
			continue
		}

		// skip header
		if fields[0] == "ID" {
			continue
		}

		value, err := strconv.ParseFloat(fields[3], 64)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		result = append(result, sensorData{
			Name:  fields[1],
			Type:  fields[2],
			Value: value,
			Units: fields[4],
		})
	}

	return result, errs.MaybeUnwrap()
}

func getValueWattOrMilliseconds(line string) (float64, error) {
	part := strings.Split(line, ":")
	if len(part) != 2 {
		return 0, fmt.Errorf("%w: %s", ErrLineFormatUnexpected, line)
	}

	part = strings.Split(strings.TrimSpace(part[1]), " ")
	if len(part) != 2 {
		return 0, fmt.Errorf("%w: %s", ErrLineFormatUnexpected, line)
	}

	valueInt, err := strconv.ParseInt(part[0], 10, 0)
	if err != nil {
		return 0, err
	}

	part[1] = strings.TrimRight(part[1], ".")

	if strings.ToLower(part[1]) == "seconds" {
		valueInt *= 1000
		part[1] = "milliseconds"
	}

	if strings.ToLower(part[1]) != "watts" && strings.ToLower(part[1]) != "milliseconds" {
		return 0, fmt.Errorf("%w: %s", ErrUnknownUnit, part[1])
	}

	return float64(valueInt), nil
}

// decodeFreeIPMIDCMI decode output of "ipmi-dcmi --get-system-power-statistics" or "ipmi-dcmi --get-enhanced-system-power-statistics".
func decodeFreeIPMIDCMI(output []byte) ([]powerReading, error) {
	var (
		result        []powerReading
		entryStarted  bool
		currentEntry  powerReading
		currentPeriod time.Duration
		errs          prometheus.MultiError
	)

	scanner := bufio.NewScanner(bytes.NewReader(output))

	entryCompleted := func() {
		// To be valid, an entry must have at least non-zero value on field: current and average
		if currentEntry.Current != 0 && currentEntry.Average != 0 {
			result = append(result, currentEntry)
		}

		currentPeriod = 0
		entryStarted = false
		currentEntry = powerReading{}
	}

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case strings.HasPrefix(line, "Power Statistics for Rolling Average Time Period "):
			if entryStarted {
				entryCompleted()
			}

			rawValue := line[len("Power Statistics for Rolling Average Time Period "):]
			part := strings.Split(rawValue, " ")

			if len(part) != 2 {
				continue
			}

			value, err := strconv.ParseInt(part[0], 10, 0)
			if err != nil {
				errs = append(errs, err)

				continue
			}

			switch strings.ToLower(part[1]) {
			case "seconds":
				currentPeriod = time.Duration(value) * time.Second
			case "minutes":
				currentPeriod = time.Duration(value) * time.Minute
			case "hours":
				currentPeriod = time.Duration(value) * time.Hour
			case "days":
				currentPeriod = time.Duration(value) * time.Hour * 24
			default:
				errs = append(errs, fmt.Errorf("%w: %s", ErrLineFormatUnexpected, line))

				continue
			}
		case strings.HasPrefix(line, "Current Power"):
			if entryStarted {
				entryCompleted()
			}

			value, err := getValueWattOrMilliseconds(line)
			if err != nil {
				errs = append(errs, err)

				continue
			}

			entryStarted = true
			currentEntry.Current = value
		case strings.HasPrefix(line, "Minimum Power"):
			value, err := getValueWattOrMilliseconds(line)
			if err != nil {
				errs = append(errs, err)

				continue
			}

			currentEntry.Minimum = value
		case strings.HasPrefix(line, "Maximum Power"):
			value, err := getValueWattOrMilliseconds(line)
			if err != nil {
				errs = append(errs, err)

				continue
			}

			currentEntry.Maximum = value
		case strings.HasPrefix(line, "Average Power"):
			value, err := getValueWattOrMilliseconds(line)
			if err != nil {
				errs = append(errs, err)

				continue
			}

			currentEntry.Average = value
		case strings.HasPrefix(line, "Time Stamp"):
			part := strings.SplitN(line, ":", 2)
			if len(part) != 2 {
				errs = append(errs, fmt.Errorf("%w: %s", ErrLineFormatUnexpected, line))

				continue
			}

			value, err := time.Parse("01/02/2006 - 15:04:05", strings.TrimSpace(part[1]))
			if err != nil {
				errs = append(errs, err)

				continue
			}

			currentEntry.Timestamp = value
		case strings.HasPrefix(line, "Statistics reporting time period"):
			value, err := getValueWattOrMilliseconds(line)
			if err != nil {
				errs = append(errs, err)

				continue
			}

			currentEntry.ReportPeriod = time.Duration(value) * time.Millisecond

			// The line "Statistics reporting time period" had as much bugs as different hardward manufacturer:
			// * On HP DL360 G7, in enhanced reading value is displayed as milliseconds when it's seconds actually.
			//   The workaround is using the header line "Power Statistics for Rolling Average Time Period" which is correct.
			// * On (some) Dell R720xd or R320, in simple reading, value is displayed as milliseconds when it's seconds actually.
			//   On this server, the period is not a rolling period, but a cummulative value (it's the age of the last reset of this counter)
			//   The workaround is assuming that all valid period will be a multiple of 1000 milliseconds (e.g. period precision is at
			//   least second). If the period isn't a multiple of 1000 milliseconds, assume the real unit is seconds.
			// * On (other) Dell R720xd the value is... strange. I guess that when period is 0 or 1 seconds it means for lifetime of the server.
			//   And most line use "1 milliseconds" instead of 1 hour or 1 day.

			switch {
			case currentPeriod == currentEntry.ReportPeriod*1000:
				// HP DL360 G7 workaround
				currentEntry.ReportPeriod = currentPeriod
			case currentPeriod > 0 && (currentEntry.ReportPeriod == 0 || currentEntry.ReportPeriod < 10*time.Millisecond):
				// Dell R720xd workaround (2nd case)
				currentEntry.ReportPeriod = currentPeriod
			case currentEntry.ReportPeriod.Truncate(time.Second) != currentEntry.ReportPeriod && currentEntry.ReportPeriod >= 10*time.Millisecond:
				// Dell R720xd workaround (1st case)
				currentEntry.ReportPeriod *= dellReportPeriodInSecondInsteadOfMillisecond
			}

		case strings.HasPrefix(line, "Power Measurement"):
			currentEntry.Active = strings.HasSuffix(strings.ToLower(line), "active")
		}
	}

	if entryStarted {
		entryCompleted()
	}

	return result, errs.MaybeUnwrap()
}
