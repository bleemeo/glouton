package zfs

import (
	"bytes"
	"errors"
	"fmt"
	"glouton/logger"
	"glouton/prometheus/model"
	"glouton/types"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
)

var ErrZFSNotAvailable = errors.New("ZFS isn't available on this server")

// Gatherer gather metrics for all ZFS pools found by "zpool" command.
// This input is special in that it does not call zpool more than minDelay, if Collect is called
// more often, the same previously gathered points will be emitted.
type Gatherer struct {
	l sync.Mutex

	zpoolPath  string
	minDelay   time.Duration
	lastGather time.Time
	lastPoints []types.MetricPoint
	lastErr    error
}

// New initializes a ZFS source.
func New(minDelay time.Duration) (*Gatherer, error) {
	path, err := exec.LookPath("zpool")
	if err != nil {
		return nil, fmt.Errorf("%w: while looking for zpool: %v", ErrZFSNotAvailable, err)
	}

	gatherer := &Gatherer{
		zpoolPath: path,
		minDelay:  minDelay,
	}

	return gatherer, nil
}

func runZpool(zpoolPath string) (string, error) {
	var outbuf bytes.Buffer

	cmd := exec.Command(zpoolPath, "list", "-Hp", "-o", "name,health,size,alloc,free")
	cmd.Stdout = &outbuf

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("zpool error: %w", err)
	}

	return outbuf.String(), nil
}

func (z *Gatherer) update() {
	z.lastGather = time.Now()
	z.lastPoints = nil

	zpool, err := runZpool(z.zpoolPath)
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

	if time.Since(z.lastGather) >= z.minDelay {
		z.update()
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
