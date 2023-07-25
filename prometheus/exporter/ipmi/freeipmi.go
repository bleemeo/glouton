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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"glouton/config"
	"glouton/logger"
	"glouton/prometheus/model"
	"glouton/prometheus/registry"
	"glouton/types"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

var (
	ErrUnknownOutput        = errors.New("unknown ipmi-sensors output")
	ErrLineFormatUnexpected = errors.New("ignoring unexpected line")
	ErrUnknownUnit          = errors.New("unknown unit")
	ErrOutputTooLarge       = errors.New("command output is too large")
	ErrUnknownMethod        = errors.New("unknown method to collect IPMI metrics")
)

const metricSystemPowerConsumptionName = "system_power_consumption"

//nolint:gochecknoglobals
var (
	freeipmiSensorsCmd   = []string{"ipmi-sensors", "-W", "discretereading", "--sdr-cache-recreate"}
	freeipmiDCMIEnhanced = []string{"ipmi-dcmi", "--get-enhanced-system-power-statistics"}
	freeipmiDCMISimple   = []string{"ipmi-dcmi", "--get-system-power-statistics"}
)

type runFunction func(ctx context.Context, searchPath string, useSudo bool, args []string) ([]byte, error)

type ipmiMethod int

const (
	methodNotInitialized  ipmiMethod = iota
	methodNoIPMIAvailable ipmiMethod = iota
	methodDCMIEnhanced    ipmiMethod = iota
	methodDCMI            ipmiMethod = iota
	methodSensors         ipmiMethod = iota
)

type Gatherer struct {
	l              sync.Mutex
	cfg            config.IPMI
	lastOutput     []byte
	initFullOutput []byte
	runCmd         runFunction
	usedMethod     ipmiMethod
}

// New returns a new IPMI gatherer.
// The Gatherer will probe for IPMI availability on first call and if unavaialble will
// do nothing on subsequent calls.
func New(cfg config.IPMI) *Gatherer {
	return newGatherer(cfg, runCmd)
}

func newGatherer(cfg config.IPMI, runCmd runFunction) *Gatherer {
	// we never use sudo if we already are root
	if os.Getuid() == 0 {
		cfg.UseSudo = false
	}

	return &Gatherer{cfg: cfg, runCmd: runCmd, usedMethod: methodNotInitialized}
}

func (g *Gatherer) Gather() ([]*dto.MetricFamily, error) {
	return g.GatherWithState(context.Background(), registry.GatherState{})
}

func (g *Gatherer) GatherWithState(ctx context.Context, _ registry.GatherState) ([]*dto.MetricFamily, error) {
	g.l.Lock()
	defer g.l.Unlock()

	if g.usedMethod == methodNotInitialized {
		// ensure init is not called twice
		g.usedMethod = methodNoIPMIAvailable

		// we do an initialization because the first call we need to decide which method to use (ipmi-sensors vs ipmi-dcmi).
		// Also very first call of ipmi-sensor could be rather long (because a cache is created).
		// In addition on system without IPMI (like qemu VM), ipmi commands will hang during 1 minutes.
		// We don't want to create a process that took 1 minute to finish every Gather.
		methodsOrderedByPreference := []ipmiMethod{
			methodDCMIEnhanced,
			methodDCMI,
			methodSensors,
		}

		delay := time.Duration(g.cfg.Timeout) * time.Second
		if delay < 30*time.Second {
			delay = 30 * time.Second
		}
		// We don't use parent context, because it's likely to be short (10 seconds) and for the
		// very first run, we want to wait more (because the SDR cache could be populated).
		ctx, cancel := context.WithTimeout(context.Background(), delay)
		defer cancel()

		for _, method := range methodsOrderedByPreference {
			if ctx.Err() != nil {
				break
			}

			points, err := g.gatherWithMethod(ctx, method, true)

			switch {
			case err != nil:
				logger.V(2).Printf("IPMI method %s failed: %v", methodName(method), err)
			case len(points) == 0:
				logger.V(2).Printf("IPMI method %s returned 0 points", methodName(method))
			default:
				logger.V(2).Printf("IPMI method %s returned %d points. It will be used in future gather", methodName(method), len(points))
				g.usedMethod = method

				return model.MetricPointsToFamilies(points), nil
			}
		}

		logger.V(1).Printf("All IPMI method failed or timed-out. IPMI metrics are not available")

		return nil, nil //nolint:nilerr
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(g.cfg.Timeout)*time.Second)
	defer cancel()

	points, err := g.gatherWithMethod(ctx, g.usedMethod, false)

	return model.MetricPointsToFamilies(points), err
}

func (g *Gatherer) gatherWithMethod(ctx context.Context, method ipmiMethod, isInit bool) ([]types.MetricPoint, error) {
	var (
		err error
		cmd []string
	)

	switch method { //nolint:exhaustive
	case methodNoIPMIAvailable:
		return nil, nil
	case methodSensors:
		cmd = freeipmiSensorsCmd
	case methodDCMIEnhanced:
		cmd = freeipmiDCMIEnhanced
	case methodDCMI:
		cmd = freeipmiDCMISimple
	}

	if cmd == nil {
		return nil, ErrUnknownMethod
	}

	g.lastOutput, err = g.runCmd(ctx, g.cfg.BinarySearchPath, g.cfg.UseSudo, cmd)

	if isInit {
		g.initFullOutput = append(g.initFullOutput, []byte(fmt.Sprintf("+ %s\n", strings.Join(cmd, " ")))...)
		if err != nil {
			g.initFullOutput = append(g.initFullOutput, []byte(fmt.Sprintf("ERR=%v\n", err))...)
		}

		if len(g.lastOutput) > 10240 {
			g.initFullOutput = append(g.initFullOutput, g.lastOutput[:10240]...)
			g.initFullOutput = append(g.initFullOutput, byte('\n'))
		} else {
			g.initFullOutput = append(g.initFullOutput, g.lastOutput...)
		}
	}

	if err != nil {
		return nil, err
	}

	switch method { //nolint:exhaustive
	case methodNoIPMIAvailable:
		return nil, nil
	case methodSensors:
		sdr, err := decodeSensors(g.lastOutput)
		if err != nil {
			logger.V(2).Printf("freeipmi decodeSensors: %v", err)
		}

		points := sdrToPoints(sdr)

		return points, nil
	case methodDCMIEnhanced, methodDCMI:
		reading, err := decodeDCMI(g.lastOutput)
		if err != nil {
			logger.V(2).Printf("freeipmi decodeDCMI: %v", err)
		}

		points := readingToPoints(reading)

		return points, nil
	default:
		return nil, ErrUnknownMethod
	}
}

func methodName(method ipmiMethod) string {
	switch method {
	case methodNotInitialized:
		return "methodNotInitialized"
	case methodDCMI:
		return "methodDCMI"
	case methodDCMIEnhanced:
		return "methodDCMIEnhanced"
	case methodSensors:
		return "methodSensors"
	case methodNoIPMIAvailable:
		return "methodNoIPMIAvailable"
	default:
		return fmt.Sprintf("%d", method)
	}
}

func (g *Gatherer) DiagnosticArchive(_ context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("gatherer-ipmi.json")
	if err != nil {
		return err
	}

	g.l.Lock()
	defer g.l.Unlock()

	obj := struct {
		MethodUsed string
		InitOutput string
		LastOutput string
	}{
		MethodUsed: methodName(g.usedMethod),
		LastOutput: string(g.lastOutput),
		InitOutput: string(g.initFullOutput),
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

func runCmd(ctx context.Context, searchPath string, useSudo bool, args []string) ([]byte, error) {
	if searchPath != "" {
		args[0] = filepath.Join(searchPath, args[0])
	}

	if useSudo {
		args = append([]string{"sudo", "-n"}, args...)
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...) //nolint:gosec

	currentEnv := os.Environ()
	newEnv := make([]string, 0, len(currentEnv))

	for _, row := range currentEnv {
		if strings.HasPrefix(row, "LANG=") || strings.HasPrefix(row, "TZ=") {
			continue
		}

		newEnv = append(newEnv, row)
	}

	newEnv = append(newEnv, "LANG=C")
	newEnv = append(newEnv, "TZ=UTC")

	cmd.Env = newEnv

	return cmd.CombinedOutput()
}

type sensorData struct {
	Name  string
	Type  string
	Value float64
	Units string
}

type powerReading struct {
	ReportPeriod time.Duration
	Timestamp    time.Time
	Active       bool
	Current      float64
	Minimum      float64
	Maximum      float64
	Average      float64
}

// decodeSensors decode output of "ipmi-sensors". Measure with "N/A" as value are skipped.
// When error during parsing occur, the line with issue is skipped, so the list of sensorData will always be
// valid (possibly empty, but valid). error could be a MultiError.
func decodeSensors(output []byte) ([]sensorData, error) {
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

	if strings.ToLower(part[1]) == "seconds" {
		valueInt *= 1000
		part[1] = "milliseconds"
	}

	if strings.ToLower(part[1]) != "watts" && strings.ToLower(part[1]) != "milliseconds" {
		return 0, fmt.Errorf("%w: %s", ErrUnknownUnit, line)
	}

	return float64(valueInt), nil
}

// decodeDCMI decode output of "ipmi-dcmi --get-system-power-statistics" or "ipmi-dcmi --get-enhanced-system-power-statistics".
func decodeDCMI(output []byte) ([]powerReading, error) {
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
				currentEntry.ReportPeriod *= 1000
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

func sdrToPoints(sdr []sensorData) []types.MetricPoint {
	result := make([]types.MetricPoint, 0, 1)

	systemConsumptionWattCandidates := make([]sensorData, 0, 1)

	for _, row := range sdr {
		if row.Type == "Current" && row.Units == "W" {
			systemConsumptionWattCandidates = append(systemConsumptionWattCandidates, row)
		}
	}

	if len(systemConsumptionWattCandidates) == 1 {
		result = append(result, types.MetricPoint{
			Labels: map[string]string{
				types.LabelName: metricSystemPowerConsumptionName,
			},
			Point: types.Point{
				Value: systemConsumptionWattCandidates[0].Value,
			},
		})
	}

	return result
}

func readingToPoints(readings []powerReading) []types.MetricPoint {
	result := make([]types.MetricPoint, 0, 1)

	var (
		bestAverage powerReading
		powerValue  float64
	)

	for _, row := range readings {
		// We only used average reading if the reporting period is less than 60 seconds.
		// We prefer the largest period available.
		// Due to bugs, we ignore when reporting period is less than 10 seconds, because this looks too much
		// like R720xd bugs were period is reported as "1 seconds" or "1 milliseconds" instead of 1 hours.
		if row.ReportPeriod <= time.Minute && row.ReportPeriod >= 10*time.Second {
			if row.ReportPeriod > bestAverage.ReportPeriod {
				bestAverage = row
			}
		}

		if powerValue == 0 {
			powerValue = row.Current
		}
	}

	if bestAverage.ReportPeriod > 0 {
		powerValue = bestAverage.Average
	}

	if powerValue != 0 {
		result = append(result, types.MetricPoint{
			Labels: map[string]string{
				types.LabelName: metricSystemPowerConsumptionName,
			},
			Point: types.Point{
				Value: powerValue,
			},
		})
	}

	return result
}
