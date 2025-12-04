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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	dto "github.com/prometheus/client_model/go"
)

const (
	metricSystemPowerConsumptionName = "system_power_consumption"
	defaultTimeout                   = 10 * time.Second

	// Some Dell server seems to report the period with a value in seconds when ipmi CLI expect a
	// value in milliseconds.
	// With freeipmi we workaround this issue when we see a period which isn't a rounded second.
	// With ipmitool we can't workaround this issue.
	dellReportPeriodInSecondInsteadOfMillisecond = 1000
)

var (
	ErrLineFormatUnexpected = errors.New("ignoring unexpected line")
	ErrUnknownUnit          = errors.New("unknown unit")
	ErrUnknownMethod        = errors.New("unknown method to collect IPMI metrics")
)

type runFunction func(ctx context.Context, searchPath string, useSudo bool, args []string) ([]byte, error)

type ipmiMethod int

const (
	methodNotInitialized       ipmiMethod = iota
	methodNoIPMIAvailable      ipmiMethod = iota
	methodFreeIPMIDCMIEnhanced ipmiMethod = iota
	methodFreeIPMIDCMI         ipmiMethod = iota
	methodIPMITool             ipmiMethod = iota
	methodFreeIPMISensors      ipmiMethod = iota
)

type Gatherer struct {
	l                  sync.Mutex
	cfg                config.IPMI
	lastOutput         []byte
	initFullOutput     []byte
	runCmd             runFunction
	usedMethod         ipmiMethod
	factStatusCallback func(binaryInstalled bool)
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

// New returns a new IPMI gatherer.
// The Gatherer will probe for IPMI availability on first call and if unavailable will
// do nothing on subsequent calls. It will use either freeipmi (preferred) or ipmitool.
func New(cfg config.IPMI, runner *gloutonexec.Runner, factStatusCallback func(binaryInstalled bool)) *Gatherer {
	return newGatherer(cfg, runCmdWithRunner(runner), factStatusCallback)
}

func (g *Gatherer) Gather() ([]*dto.MetricFamily, error) {
	return g.GatherWithState(context.Background(), registry.GatherState{T0: time.Now()})
}

func (g *Gatherer) GatherWithState(ctx context.Context, _ registry.GatherState) ([]*dto.MetricFamily, error) {
	g.l.Lock()
	defer g.l.Unlock()

	if g.usedMethod == methodNotInitialized {
		// ensure init is not called twice
		g.usedMethod = methodNoIPMIAvailable

		isIPMIInstalled := false

		defer func() {
			g.factStatusCallback(isIPMIInstalled)
		}()

		// we do an initialization because the first call we need to decide which method to use (ipmi-sensors vs ipmi-dcmi).
		// Also very first call of ipmi-sensor could be rather long (because a cache is created).
		// In addition on system without IPMI (like qemu VM), ipmi commands will hang during 1 minutes.
		// We don't want to create a process that took 1 minute to finish every Gather.
		methodsOrderedByPreference := []ipmiMethod{
			methodFreeIPMIDCMIEnhanced,
			methodFreeIPMIDCMI,
			methodIPMITool,
			methodFreeIPMISensors,
		}

		delay := max(time.Duration(g.cfg.Timeout)*time.Second, 30*time.Second)

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

				isIPMIInstalled = isIPMIInstalled || !errors.Is(err, exec.ErrNotFound) && !strings.HasSuffix(err.Error(), syscall.ENOENT.Error())
			case len(points) == 0:
				logger.V(2).Printf("IPMI method %s returned 0 points", methodName(method))

				isIPMIInstalled = true
			default:
				logger.V(2).Printf("IPMI method %s returned %d points. It will be used in future gather", methodName(method), len(points))
				g.usedMethod = method
				isIPMIInstalled = true

				return model.MetricPointsToFamilies(points), nil
			}
		}

		logger.V(1).Printf("All IPMI method failed or timed-out. IPMI metrics are not available")

		return nil, nil
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(g.cfg.Timeout)*time.Second)
	defer cancel()

	points, err := g.gatherWithMethod(ctx, g.usedMethod, false)

	return model.MetricPointsToFamilies(points), err
}

func (g *Gatherer) DiagnosticArchive(_ context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("ipmi.json")
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

func (g *Gatherer) gatherWithMethod(ctx context.Context, method ipmiMethod, isInit bool) ([]types.MetricPoint, error) {
	var (
		err error
		cmd []string
	)

	switch method { //nolint:exhaustive
	case methodNoIPMIAvailable:
		return nil, nil
	case methodFreeIPMISensors:
		cmd = freeipmiSensorsCmd
	case methodFreeIPMIDCMIEnhanced:
		cmd = freeipmiDCMIEnhanced
	case methodFreeIPMIDCMI:
		cmd = freeipmiDCMISimple
	case methodIPMITool:
		cmd = ipmitoolDCMI
	default:
		return nil, ErrUnknownMethod
	}

	g.lastOutput, err = g.runCmd(ctx, g.cfg.BinarySearchPath, g.cfg.UseSudo, cmd)

	if isInit {
		g.initFullOutput = append(g.initFullOutput, fmt.Appendf(nil, "+ %s\n", strings.Join(cmd, " "))...)
		if err != nil {
			g.initFullOutput = append(g.initFullOutput, fmt.Appendf(nil, "ERR=%v\n", err)...)
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
	case methodFreeIPMISensors:
		sdr, err := decodeFreeIPMISensors(g.lastOutput)
		if err != nil {
			logger.V(2).Printf("freeipmi decodeSensors: %v", err)
		}

		points := sdrToPoints(sdr)

		return points, nil
	case methodFreeIPMIDCMIEnhanced, methodFreeIPMIDCMI:
		reading, err := decodeFreeIPMIDCMI(g.lastOutput)
		if err != nil {
			logger.V(2).Printf("freeipmi decodeDCMI: %v", err)
		}

		points := readingToPoints(reading)

		return points, nil
	case methodIPMITool:
		reading, err := decodeIPMIToolDCMI(g.lastOutput)
		if err != nil {
			logger.V(2).Printf("ipmitool decodeDCMI: %v", err)
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
	case methodFreeIPMIDCMI:
		return "methodFreeIPMIDCMI"
	case methodFreeIPMIDCMIEnhanced:
		return "methodFreeIPMIDCMIEnhanced"
	case methodFreeIPMISensors:
		return "methodFreeIPMISensors"
	case methodNoIPMIAvailable:
		return "methodNoIPMIAvailable"
	case methodIPMITool:
		return "methodIPMITool"
	default:
		return fmt.Sprintf("%d", method)
	}
}

func newGatherer(cfg config.IPMI, runCmd runFunction, factStatusCallback func(binaryInstalled bool)) *Gatherer {
	// we never use sudo if we already are root
	if os.Getuid() == 0 {
		cfg.UseSudo = false
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = int(defaultTimeout.Seconds())
	}

	return &Gatherer{
		cfg:                cfg,
		runCmd:             runCmd,
		usedMethod:         methodNotInitialized,
		factStatusCallback: factStatusCallback,
	}
}

func runCmdWithRunner(runner *gloutonexec.Runner) runFunction {
	return func(ctx context.Context, searchPath string, useSudo bool, args []string) ([]byte, error) {
		if searchPath != "" {
			args = append([]string{filepath.Join(searchPath, args[0])}, args[1:]...)
		}

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

		runOption := gloutonexec.Option{RunAsRoot: useSudo, Environ: newEnv, CombinedOutput: true, RunOnHost: true}

		fullPath, err := runner.LookPath(args[0], runOption)
		if err != nil {
			return nil, err
		}

		return runner.Run(ctx, runOption, fullPath, args[1:]...)
	}
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
