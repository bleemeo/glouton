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

package zfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	dto "github.com/prometheus/client_model/go"
)

var ErrZFSNotAvailable = errors.New("ZFS isn't available on this server")

// Gatherer gather metrics for all ZFS pools found by "zpool" command.
// This input is special in that it does not call zpool more than minDelay, if Collect is called
// more often, the same previously gathered points will be emitted.
type Gatherer struct {
	l sync.Mutex

	zpoolPath    string
	runner       *gloutonexec.Runner
	runnerOption gloutonexec.Option
	minDelay     time.Duration
	lastGather   time.Time
	lastPoints   []types.MetricPoint
	lastErr      error
}

// New initializes a ZFS source.
func New(runner *gloutonexec.Runner, minDelay time.Duration) (*Gatherer, error) {
	runnerOption := gloutonexec.Option{
		RunAsRoot: true,
		RunOnHost: true,
	}

	path, err := runner.LookPath("zpool", runnerOption)
	if err != nil {
		return nil, fmt.Errorf("%w: while looking for zpool: %v", ErrZFSNotAvailable, err)
	}

	gatherer := &Gatherer{
		zpoolPath:    path,
		runnerOption: runnerOption,
		runner:       runner,
		minDelay:     minDelay,
	}

	return gatherer, nil
}

func (z *Gatherer) DiagnosticArchive(_ context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("zfs.json")
	if err != nil {
		return err
	}

	z.l.Lock()
	defer z.l.Unlock()

	obj := struct {
		ZPoolPath  string
		MinDelay   string
		LastGather string
		LastErr    string
		LastPoints []string
	}{
		ZPoolPath:  z.zpoolPath,
		MinDelay:   z.minDelay.String(),
		LastGather: z.lastGather.String(),
	}

	if z.lastErr != nil {
		obj.LastErr = z.lastErr.Error()
	}

	for _, pts := range z.lastPoints {
		obj.LastPoints = append(obj.LastPoints, fmt.Sprintf("%s = %f", types.LabelsToText(pts.Labels), pts.Value))
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

func (z *Gatherer) runZpool(ctx context.Context) (string, error) {
	output, err := z.runner.Run(ctx, z.runnerOption, z.zpoolPath, "list", "-Hp", "-o", "name,health,size,alloc,free")
	if err != nil {
		return "", fmt.Errorf("zpool error: %w", err)
	}

	return string(output), nil
}

func (z *Gatherer) update(ctx context.Context) {
	z.lastGather = time.Now()
	z.lastPoints = nil

	zpool, err := z.runZpool(ctx)
	if err != nil {
		z.lastErr = err

		return
	}

	z.lastErr = nil
	z.lastPoints = decodeZpool(zpool)
}

func (z *Gatherer) Gather() ([]*dto.MetricFamily, error) {
	z.l.Lock()
	defer z.l.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if time.Since(z.lastGather) >= z.minDelay {
		z.update(ctx)
	}

	return model.MetricPointsToFamilies(z.lastPoints), z.lastErr
}

func decodeZpool(output string) []types.MetricPoint {
	lines := strings.Split(output, "\n")
	result := make([]types.MetricPoint, 0, len(lines)*5)

	for _, line := range lines {
		part := strings.Split(line, "\t")
		if len(part) != 5 {
			continue
		}

		poolName := part[0]
		health := part[1]

		var status types.Status

		switch health {
		case "ONLINE":
			status = types.StatusOk
		case "DEGRADED":
			status = types.StatusCritical
		case "OFFLINE":
			status = types.StatusWarning
		default:
			status = types.StatusCritical
		}

		annotation := types.MetricAnnotations{
			Status: types.StatusDescription{
				CurrentStatus:     status,
				StatusDescription: "ZFS pool is " + health,
			},
		}

		result = append(result, types.MetricPoint{
			Point: types.Point{
				// It's not a mistake to use zero value for time.
				// Those points are only used with model.MetricPointsToFamilies and a zero
				// time is converted to no timestamp specified.
				Time:  time.Time{},
				Value: float64(annotation.Status.CurrentStatus.NagiosCode()),
			},
			Labels: map[string]string{
				types.LabelName: "zfs_pool_health_status",
				types.LabelItem: poolName,
			},
			Annotations: annotation,
		})

		size, err := strconv.ParseInt(part[2], 10, 0)
		if err != nil {
			logger.V(2).Printf("unable to parse zpool size: %v", err)

			continue
		}

		alloc, err := strconv.ParseInt(part[3], 10, 0)
		if err != nil {
			logger.V(2).Printf("unable to parse zpool alloc: %v", err)

			continue
		}

		free, err := strconv.ParseInt(part[4], 10, 0)
		if err != nil {
			logger.V(2).Printf("unable to parse zpool free: %v", err)

			continue
		}

		result = append(result, types.MetricPoint{
			Point: types.Point{
				Time:  time.Time{},
				Value: float64(alloc),
			},
			Labels: map[string]string{
				types.LabelName: "disk_used",
				types.LabelItem: poolName,
			},
		})
		result = append(result, types.MetricPoint{
			Point: types.Point{
				Time:  time.Time{},
				Value: float64(size),
			},
			Labels: map[string]string{
				types.LabelName: "disk_total",
				types.LabelItem: poolName,
			},
		})
		result = append(result, types.MetricPoint{
			Point: types.Point{
				Time:  time.Time{},
				Value: float64(free),
			},
			Labels: map[string]string{
				types.LabelName: "disk_free",
				types.LabelItem: poolName,
			},
		})
		result = append(result, types.MetricPoint{
			Point: types.Point{
				Time:  time.Time{},
				Value: float64(alloc) / float64(size) * 100,
			},
			Labels: map[string]string{
				types.LabelName: "disk_used_perc",
				types.LabelItem: poolName,
			},
		})
	}

	return result
}
