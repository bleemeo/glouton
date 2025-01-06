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

package ipmi

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

//nolint:gochecknoglobals
var ipmitoolDCMI = []string{"ipmitool", "dcmi", "power", "reading"}

func ipmiToolProcessLine(line string, currentEntry powerReading, finishPreviousEntry func()) (startedNewEntry bool, entry powerReading, err error) {
	switch {
	case strings.HasPrefix(line, "instantaneous power"):
		finishPreviousEntry()

		entry := powerReading{}

		value, err := getValueWattOrMilliseconds(line)
		if err != nil {
			return false, entry, err
		}

		entry.Current = value

		return true, entry, nil
	case strings.HasPrefix(line, "minimum"):
		value, err := getValueWattOrMilliseconds(line)
		if err != nil {
			return false, currentEntry, err
		}

		currentEntry.Minimum = value

		return false, currentEntry, nil
	case strings.HasPrefix(line, "maximum"):
		value, err := getValueWattOrMilliseconds(line)
		if err != nil {
			return false, currentEntry, err
		}

		currentEntry.Maximum = value

		return false, currentEntry, nil
	case strings.HasPrefix(line, "average power"):
		value, err := getValueWattOrMilliseconds(line)
		if err != nil {
			return false, currentEntry, err
		}

		currentEntry.Average = value

		return false, currentEntry, nil
	case strings.HasPrefix(line, "ipmi timestamp"):
		// It looks like a bug, but ipmitool seems to sometime print
		// the timestamp with a missing new line at the very end, cause the next line to be on same line.
		// Find where we should split line and process the two lines.
		part := strings.SplitN(line, ":", 2)
		if len(part) != 2 {
			return false, currentEntry, fmt.Errorf("%w: %s", ErrLineFormatUnexpected, line)
		}

		part[1] = strings.TrimSpace(part[1])

		// part[1] contains the time which don't contains multiple space.
		// part[1] may contains the next line, if that the case it start with 4 whitespace.
		idx := strings.Index(part[1], "    ")
		if idx > 0 {
			// line1 is part[0] + ":" + part[1] until idx
			line1 := part[0] + ":" + part[1][:idx]
			line2 := strings.TrimSpace(part[1][idx:])

			tmp, entry, err := ipmiToolProcessLine(line1, currentEntry, finishPreviousEntry)
			if err != nil {
				return tmp, entry, err
			}

			tmp2, entry, err := ipmiToolProcessLine(line2, entry, finishPreviousEntry)

			return tmp || tmp2, entry, err
		}

		// ipmitool have multiple date format
		format := []string{
			"01/02/06 15:04:05 utc",
			time.ANSIC,
		}

		var firstErr error

		for _, fmt := range format {
			value, err := time.Parse(fmt, strings.TrimSpace(part[1]))
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}

				continue
			}

			currentEntry.Timestamp = value

			return false, currentEntry, nil
		}

		return false, currentEntry, firstErr

	case strings.HasPrefix(line, "sampling period"):
		value, err := getValueWattOrMilliseconds(line)
		if err != nil {
			return false, currentEntry, err
		}

		currentEntry.ReportPeriod = time.Duration(value) * time.Millisecond

		return false, currentEntry, nil
	case strings.HasPrefix(line, "power reading state"):
		currentEntry.Active = strings.HasSuffix(strings.ToLower(line), "activated")

		return false, currentEntry, nil
	}

	return false, currentEntry, nil
}

// decodeIPMIToolDCMI decode output of "ipmitool dcmi power reading".
func decodeIPMIToolDCMI(output []byte) ([]powerReading, error) {
	var (
		result       []powerReading
		entryStarted bool
		tmp          bool
		currentEntry powerReading
		errs         prometheus.MultiError
		err          error
	)

	scanner := bufio.NewScanner(bytes.NewReader(output))

	entryCompleted := func() {
		// To be valid, an entry must have at least non-zero value on field: current and average
		if currentEntry.Current != 0 && currentEntry.Average != 0 {
			result = append(result, currentEntry)
		}

		entryStarted = false
	}

	for scanner.Scan() {
		line := strings.ToLower(strings.TrimSpace(scanner.Text()))

		tmp, currentEntry, err = ipmiToolProcessLine(line, currentEntry, entryCompleted)
		entryStarted = entryStarted || tmp

		if err != nil {
			errs = append(errs, err)
		}
	}

	if entryStarted {
		entryCompleted()
	}

	return result, errs.MaybeUnwrap()
}
