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

package ssacli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	dto "github.com/prometheus/client_model/go"
)

const defaultTimeout = 10 * time.Second

var (
	ErrNoController         = errors.New("no controller found")
	ErrUnknownMethod        = errors.New("unknown method to collect ssacli metrics")
	ErrPhysicalDriveNoMatch = errors.New("rePhysicaldrive didn't matched")
)

var (
	// The "port 2I:box 2[...]" up to just before ", OK" is made optional (the `|.*`) in order to still extra status is format change
	// (e.g. a new attribute is added).
	rePhysicaldrive = regexp.MustCompile(`^\s*physicaldrive (?P<location>[^ ]+) \((port (?P<port>[^:]+):box (?P<box>[^:]+):bay (?P<bay>[^,]+), (?P<description>.+)|.*), (?P<status>[^,]+)\)$`)
	reArray         = regexp.MustCompile(`^\s*(SmartCache)? (A|a)rray (?P<arrayName>.*)$`)
)

type runFunction func(ctx context.Context) ([]byte, error)

type runFunctionArg func(ctx context.Context, arg string) ([]byte, error)

type ssaMethod int

const (
	methodNotInitialized ssaMethod = iota
	methodNoCmdAvailable ssaMethod = iota
	methodSSACli         ssaMethod = iota
)

type Gatherer struct {
	l                       sync.Mutex
	cfg                     config.SSACLI
	controllerSlots         []int
	lastOutputPerController map[int][]byte
	listControllerOutput    []byte
	usedMethod              ssaMethod
	runListController       runFunction
	runListDrive            runFunctionArg
}

type diskInfo struct {
	Controller   string
	Array        string
	Location     string
	LocationBox  string
	LocationBay  string
	LocationPort string
	Description  string
	Status       string
}

type ssacliDiskList []diskInfo

// New returns a new Smart Storage Administrator (ssa) gatherer.
// The Gatherer will probe for ssacli availability on first call and if unavailable will
// do nothing on subsequent calls. Only ssacli is supported.
func New(cfg config.SSACLI, runner *gloutonexec.Runner) *Gatherer {
	return newGatherer(
		cfg,
		func(ctx context.Context) ([]byte, error) {
			return runCmdWithRunner(ctx, runner, cfg, []string{"ssacli", "controller", "all", "show"})
		},
		func(ctx context.Context, slot string) ([]byte, error) {
			return runCmdWithRunner(ctx, runner, cfg, []string{"ssacli", "controller", "slot=" + slot, "physicaldrive", "all", "show"})
		},
	)
}

func (g *Gatherer) Gather() ([]*dto.MetricFamily, error) {
	return g.GatherWithState(context.Background(), registry.GatherState{T0: time.Now()})
}

func (g *Gatherer) GatherWithState(ctx context.Context, state registry.GatherState) ([]*dto.MetricFamily, error) {
	g.l.Lock()
	defer g.l.Unlock()

	if g.usedMethod == methodNotInitialized {
		// ensure init is not called twice
		g.usedMethod = methodNoCmdAvailable

		// we do an initialization because the first call we need to check if ssacli is installed and is able to
		// see disks.

		// For first call, increase timeout
		delay := time.Duration(g.cfg.Timeout) * time.Second
		if delay < 30*time.Second {
			delay = 30 * time.Second
		}
		// We don't use parent context, because it's likely to be short (10 seconds) and for the
		// very first run, we want to wait more (because the SDR cache could be populated).
		ctx, cancel := context.WithTimeout(context.Background(), delay)
		defer cancel()

		points, err := g.gatherWithMethod(ctx, state.T0, methodSSACli, true)

		switch {
		case err != nil:
			logger.V(2).Printf("ssacli call failed: %v", err)
		case len(points) == 0:
			logger.V(2).Printf("ssacli returned 0 points")
		default:
			logger.V(2).Printf("ssacli returned %d points. It will be used in future gather", len(points))

			g.usedMethod = methodSSACli

			return model.MetricPointsToFamilies(points), nil
		}

		logger.V(1).Printf("ssacli failed or timed-out. ssacli metrics are not available")

		return nil, nil
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(g.cfg.Timeout)*time.Second)
	defer cancel()

	points, err := g.gatherWithMethod(ctx, state.T0, g.usedMethod, false)

	return model.MetricPointsToFamilies(points), err
}

func (g *Gatherer) DiagnosticArchive(_ context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("ssacli.json")
	if err != nil {
		return err
	}

	g.l.Lock()
	defer g.l.Unlock()

	obj := struct {
		MethodUsed              string
		InitOutput              string
		LastOutputPerController map[int]string
	}{
		MethodUsed:              methodName(g.usedMethod),
		LastOutputPerController: make(map[int]string, len(g.lastOutputPerController)),
		InitOutput:              string(g.listControllerOutput),
	}

	for k, v := range g.lastOutputPerController {
		obj.LastOutputPerController[k] = string(v)
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

func (g *Gatherer) gatherWithMethod(ctx context.Context, now time.Time, method ssaMethod, isInit bool) ([]types.MetricPoint, error) {
	if method == methodNoCmdAvailable {
		return nil, nil
	}

	if method != methodSSACli {
		return nil, ErrUnknownMethod
	}

	if isInit {
		// Initialization
		listControllerOutput, err := g.runListController(ctx)

		if len(listControllerOutput) > 10240 {
			g.listControllerOutput = append(g.listControllerOutput, listControllerOutput[:10240]...)
			g.listControllerOutput = append(g.listControllerOutput, byte('\n'))
		} else {
			g.listControllerOutput = append(g.listControllerOutput, listControllerOutput...)
		}

		if err != nil {
			g.listControllerOutput = append(g.listControllerOutput, []byte(fmt.Sprintf("ERR=%v\n", err))...)

			return nil, err
		}

		controllers, err := parseListController(listControllerOutput)
		if err != nil {
			return nil, err
		}

		if len(controllers) == 0 {
			return nil, ErrNoController
		}

		g.controllerSlots = controllers
	}

	for k := range g.lastOutputPerController {
		delete(g.lastOutputPerController, k)
	}

	if g.lastOutputPerController == nil {
		g.lastOutputPerController = make(map[int][]byte)
	}

	var (
		errs        []error
		allDiskInfo ssacliDiskList
	)

	for _, slot := range g.controllerSlots {
		var err error

		g.lastOutputPerController[slot], err = g.runListDrive(ctx, strconv.FormatInt(int64(slot), 10))
		if err != nil {
			errs = append(errs, err)

			continue
		}

		tmp, err := decodeSSACLI(g.lastOutputPerController[slot], slot)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		allDiskInfo = append(allDiskInfo, tmp...)
	}

	return diskIntoToPoints(now, allDiskInfo), errors.Join(errs...)
}

func methodName(method ssaMethod) string {
	switch method {
	case methodNotInitialized:
		return "methodNotInitialized"
	case methodNoCmdAvailable:
		return "methodNoCmdAvailable"
	case methodSSACli:
		return "methodSSACli"
	default:
		return fmt.Sprintf("%d", method)
	}
}

func newGatherer(cfg config.SSACLI, runListController runFunction, runListDrive runFunctionArg) *Gatherer {
	// we never use sudo if we already are root
	if os.Getuid() == 0 {
		cfg.UseSudo = false
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = int(defaultTimeout.Seconds())
	}

	return &Gatherer{
		cfg:               cfg,
		runListController: runListController,
		runListDrive:      runListDrive,
		usedMethod:        methodNotInitialized,
	}
}

func runCmdWithRunner(ctx context.Context, runner *gloutonexec.Runner, cfg config.SSACLI, args []string) ([]byte, error) {
	searchPath := cfg.BinarySearchPath
	useSudo := cfg.UseSudo

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

func (l ssacliDiskList) IsMultiController() bool {
	currentController := ""

	for _, row := range l {
		if currentController == "" {
			currentController = row.Controller
		}

		if row.Controller != currentController {
			return true
		}
	}

	return false
}

func (l ssacliDiskList) IsMultiArray(controller string) bool {
	currentArray := ""

	for _, row := range l {
		if row.Controller != controller {
			continue
		}

		if currentArray == "" {
			currentArray = row.Array
		}

		if row.Array != currentArray {
			return true
		}
	}

	return false
}

func (l ssacliDiskList) IsMultiBox() bool {
	currentBox := ""

	for _, row := range l {
		if currentBox == "" {
			currentBox = row.LocationBox
		}

		if row.LocationBox != currentBox {
			return true
		}
	}

	return false
}

func (di diskInfo) ToStatusDescription() types.StatusDescription {
	if di.Status == "OK" {
		return types.StatusDescription{
			CurrentStatus: types.StatusOk,
		}
	}

	return types.StatusDescription{
		CurrentStatus:     types.StatusCritical,
		StatusDescription: fmt.Sprintf("ssacli return a status \"%s\"", di.Status),
	}
}

// ToDdeviceLabel return a human friendly identifier of the disk.
// Based on testdata gen8-two-controller I believe
//   - controller is likely bind to physical *cable*
//   - array is likely only a logical thing. It don't help to identify
//     the physical disk, but help to known when your logical disk will fail
//   - box & bay is probably where the physical disk is present
//
// It's possible that controller are bind to a box (like internal controller
// is the internal box and the additional controller use an external box).
// But as soon as we have two box (even if controller could allow to known
// the box), kept box in identifier.
func (di diskInfo) ToDeviceLabel(allRows ssacliDiskList) string {
	part := make([]string, 0, 4)

	if allRows.IsMultiController() {
		part = append(part, "controller "+di.Controller)
	}

	if allRows.IsMultiArray(di.Controller) {
		part = append(part, "array "+di.Array)
	}

	if di.LocationBox != "" && di.LocationBay != "" {
		if allRows.IsMultiBox() {
			part = append(part, "box "+di.LocationBox)
		}

		part = append(part, "bay "+di.LocationBay)
	} else {
		part = append(part, "location "+di.Location)
	}

	return strings.Join(part, " ")
}

func diskIntoToPoints(now time.Time, allRows ssacliDiskList) []types.MetricPoint {
	result := make([]types.MetricPoint, 0, len(allRows))

	for _, row := range allRows {
		status := row.ToStatusDescription()
		result = append(result, types.MetricPoint{
			Point: types.Point{
				Time:  now,
				Value: float64(status.CurrentStatus.NagiosCode()),
			},
			Labels: map[string]string{
				types.LabelName:   "smart_device_health_status",
				types.LabelDevice: row.ToDeviceLabel(allRows),
			},
			Annotations: types.MetricAnnotations{
				Status: status,
			},
		})
	}

	return result
}

func decodeSSACLI(output []byte, controllerSlot int) ([]diskInfo, error) {
	outputStr := string(output)
	currentArray := ""

	var (
		errs   []error
		result []diskInfo
	)

	const (
		physicaldriveStr = "physicaldrive "
	)

	for line := range strings.SplitSeq(outputStr, "\n") {
		match := reArray.FindStringSubmatch(line)
		if len(match) != 0 {
			currentArray = match[reArray.SubexpIndex("arrayName")]

			continue
		}

		if strings.HasPrefix(strings.TrimSpace(line), physicaldriveStr) {
			match := rePhysicaldrive.FindStringSubmatch(line)
			if len(match) == 0 {
				errs = append(errs, fmt.Errorf("%w: line %q", ErrPhysicalDriveNoMatch, line))

				continue
			}

			result = append(result, diskInfo{
				Controller:   strconv.FormatInt(int64(controllerSlot), 10),
				Array:        currentArray,
				Location:     match[rePhysicaldrive.SubexpIndex("location")],
				LocationBox:  match[rePhysicaldrive.SubexpIndex("box")],
				LocationBay:  match[rePhysicaldrive.SubexpIndex("bay")],
				LocationPort: match[rePhysicaldrive.SubexpIndex("port")],
				Status:       match[rePhysicaldrive.SubexpIndex("status")],
				Description:  match[rePhysicaldrive.SubexpIndex("description")],
			})

			continue
		}
	}

	return result, errors.Join(errs...)
}

func parseListController(output []byte) ([]int, error) {
	var ( //nolint:prealloc
		errs   []error
		result []int
	)

	outputStr := string(output)

	for line := range strings.SplitSeq(outputStr, "\n") {
		slotStr := "Slot "
		idxStart := strings.Index(line, slotStr)

		if idxStart == -1 {
			continue
		}

		idxStart += len(slotStr)

		length := strings.Index(line[idxStart:], " ")
		if length == -1 {
			continue
		}

		slotValueStr := line[idxStart : idxStart+length]

		slotValue, err := strconv.ParseInt(slotValueStr, 10, 0)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		result = append(result, int(slotValue))
	}

	return result, errors.Join(errs...)
}
