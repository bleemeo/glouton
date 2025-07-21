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

package vsphere

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/inputs/vsphere"
	dto "github.com/prometheus/client_model/go"
	"github.com/vmware/govmomi/vim25/soap"
)

const endpointCreationRetryDelay = 5 * time.Minute

type gatherKind string

const (
	gatherRT      gatherKind = "realtime"         // Hosts and VMs (and Clusters, aggregated from hosts metrics)
	gatherHist30m gatherKind = "historical 30min" // Datastores
)

type vSphereGatherer struct {
	kind     gatherKind
	interval time.Duration

	cfg *config.VSphere
	// Storing the endpoint's URL, just in case the endpoint can't be
	// created at startup and will need to be instanced later.
	soapURL             *url.URL
	endpointCreateTimer *time.Timer

	input  *vsphere.VSphere
	cancel context.CancelFunc

	acc        *internal.Accumulator
	buffer     *registry.PointBuffer
	lastPoints []types.MetricPoint
	lastErr    error

	hierarchy        *Hierarchy
	devicePropsCache *propsCaches

	ptsCache         pointCache
	lastInputCollect time.Time

	l sync.Mutex
}

func (gatherer *vSphereGatherer) SecretCount() int {
	return 2 // vSphere inputs each have 2 secrets
}

func (gatherer *vSphereGatherer) Gather() ([]*dto.MetricFamily, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commonTimeout)
	defer cancel()

	return gatherer.GatherWithState(ctx, registry.GatherState{T0: time.Now()})
}

func (gatherer *vSphereGatherer) GatherWithState(ctx context.Context, state registry.GatherState) ([]*dto.MetricFamily, error) {
	gatherer.l.Lock()
	defer gatherer.l.Unlock()

	if gatherer.input == nil {
		return nil, nil
	}

	// Do the gather only in the scrape loop and reuse the last points on /metrics.
	if state.FromScrapeLoop {
		acc := inputs.Accumulator{
			// Gather metric points in the buffer.
			Pusher:  gatherer.buffer,
			Context: ctx,
		}
		gatherer.acc.Accumulator = &acc

		retAcc := &retainAccumulator{
			Accumulator: gatherer.acc,
			mustRetain: map[string][]string{
				"vsphere_host_cpu":       {"usagemhz_average"},
				"vsphere_host_mem":       {"usage_average", "swapin_average"},
				"vsphere_host_datastore": {"read_average", "write_average"},
			},
			retainedPerMeasurement: make(retainedMetrics),
		}

		var err error

		if gatherer.kind != gatherHist30m || state.T0.Sub(gatherer.lastInputCollect) >= 5*time.Minute {
			// Registry calls both historical and real-time gatherer every minute, so all vSphere agents produce
			// a point every minute. But for historical metrics, we don't need to do the real call every minute:
			// every 5 minutes is enough. We prefer 5 minutes rather than 30 minutes, to get the last data point
			// a bit sooner (we don't know the delay before that point is available).
			err = gatherer.input.Gather(retAcc)
			if gatherer.kind == gatherHist30m && err == nil {
				gatherer.lastInputCollect = state.T0
			}
		}

		errAddMetrics := gatherer.collectAdditionalMetrics(ctx, state, gatherer.acc, retAcc.retainedPerMeasurement)
		allErrs := errors.Join(filterErrors(append(acc.Errors(), err, errAddMetrics))...)

		gatherer.lastPoints = gatherer.buffer.Points()

		gatherer.lastErr = allErrs
		if gatherer.kind == gatherHist30m && allErrs == nil {
			gatherer.lastPoints = gatherer.ptsCache.update(gatherer.lastPoints, state.T0)
		}

		sort.Slice(gatherer.lastPoints, func(i, j int) bool {
			return gatherer.lastPoints[i].Time.Before(gatherer.lastPoints[j].Time)
		})
	}

	mfs := model.MetricPointsToFamilies(gatherer.lastPoints)

	return mfs, gatherer.lastErr
}

func filterErrors(errs []error) []error {
	n := 0

	for _, err := range errs {
		if err != nil && !strings.Contains(err.Error(), "A specified parameter was not correct: querySpec[0]") {
			errs[n] = err
			n++
		}
	}

	return errs[:n]
}

// Additional metrics are those that we don't get (or directly get) from the telegraf input.
func (gatherer *vSphereGatherer) collectAdditionalMetrics(ctx context.Context, state registry.GatherState, acc telegraf.Accumulator, retained retainedMetrics) error {
	finder, client, err := newDeviceFinder(ctx, *gatherer.cfg)
	if err != nil {
		return err
	}

	clusters, datastores, resourcePools, hosts, vms, err := findDevices(ctx, finder, true)
	if err != nil {
		return err
	}

	if len(clusters) == 0 && len(hosts) == 0 && len(vms) == 0 {
		return nil
	}

	err = gatherer.hierarchy.Refresh(ctx, clusters, resourcePools, hosts, vms, gatherer.devicePropsCache.vmCache)
	if err != nil {
		return fmt.Errorf("can't describe hierarchy: %w", err)
	}

	switch gatherer.kind { //nolint:exhaustive,gocritic
	case gatherRT:
		// For each host, we want a list of vm states (running/stopped).
		vmStatesPerHost := make(map[string][]bool, len(hosts))

		err = additionalVMMetrics(ctx, client, vms, gatherer.devicePropsCache.vmCache, acc, gatherer.hierarchy, vmStatesPerHost, state.T0)
		if err != nil {
			return err
		}

		err = additionalHostMetrics(ctx, client, hosts, acc, gatherer.hierarchy, vmStatesPerHost, state.T0)
		if err != nil {
			return err
		}

		// Special case: we aggregate host metrics to get cluster metrics
		err = additionalClusterMetrics(ctx, client, clusters, datastores, gatherer.devicePropsCache, acc, retained, gatherer.hierarchy, state.T0)
		if err != nil {
			return err
		}
	}

	return nil
}

func (gatherer *vSphereGatherer) stop() {
	gatherer.l.Lock()
	defer gatherer.l.Unlock()

	gatherer.cancel()

	if gatherer.input != nil {
		gatherer.input.Stop()
	} else if gatherer.endpointCreateTimer != nil {
		// Case where the endpoint still has not been created.
		gatherer.endpointCreateTimer.Stop()
	}
}

func (gatherer *vSphereGatherer) createEndpoint(ctx context.Context, input *vsphere.VSphere) {
	gatherer.l.Lock()
	defer gatherer.l.Unlock()

	if ctx.Err() != nil {
		return
	}

	newEP := func(epURL *url.URL) error {
		input.Vcenters = []string{epURL.String()}

		err := input.Start(nil)
		if err == nil {
			gatherer.input = input
		}

		return err
	}

	err := newEP(gatherer.soapURL)
	if err == nil {
		return
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Unwrap().Error() == "400 Bad Request" {
		if gatherer.soapURL.Path == "/" {
			// Configuration could be tricky: https://vcenter.example.com and https://vcenter.example.com/ aren't the same.
			// With the first one (without ending slash), govmomi will automatically add "/sdk" (result in https://vcenter.example.com/sdk).
			// With the former one (with the ending slash), govmomi will NOT add /sdk (result in https://vcenter.example.com/)
			// Because that will confuse the end-user, if he entered the URL with an ending slash (https://vcenter.example.com/)
			// and it didn't work, retry with /sdk automatically.
			urlWithSlashSDK := *gatherer.soapURL
			urlWithSlashSDK.Path = "/sdk"

			err = newEP(&urlWithSlashSDK)
			if err == nil {
				logger.V(2).Printf(
					"vSphere endpoint for %q created, but on /sdk instead of %s",
					gatherer.soapURL.Host, gatherer.soapURL.Path,
				)

				gatherer.soapURL = &urlWithSlashSDK
				gatherer.cfg.URL = urlWithSlashSDK.String()

				return
			}
		}
	}

	logger.V(1).Printf("Failed to create vSphere %s endpoint for %q: %v -- will retry in %s.", gatherer.kind, gatherer.soapURL.Host, err, endpointCreationRetryDelay)

	gatherer.lastErr = err
	gatherer.endpointCreateTimer = time.AfterFunc(endpointCreationRetryDelay, func() {
		gatherer.createEndpoint(ctx, input)
	})
}

// newGatherer creates a vSphere gatherer from the given endpoint.
// It will return an error if the endpoint URL is not valid.
func newGatherer(ctx context.Context, kind gatherKind, cfg *config.VSphere, input *vsphere.VSphere, acc *internal.Accumulator, hierarchy *Hierarchy, devicePropsCache *propsCaches) (*vSphereGatherer, error) {
	soapURL, err := soap.ParseURL(cfg.URL)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)

	gatherer := &vSphereGatherer{
		kind:             kind,
		interval:         time.Duration(input.HistoricalInterval),
		cfg:              cfg,
		soapURL:          soapURL,
		acc:              acc,
		buffer:           new(registry.PointBuffer),
		cancel:           cancel,
		hierarchy:        hierarchy,
		devicePropsCache: devicePropsCache,
	}

	if kind == gatherHist30m {
		gatherer.ptsCache = make(pointCache)
	}

	gatherer.createEndpoint(ctx, input)

	return gatherer, nil
}

type addedField struct {
	field string
	value any
	tags  map[string]string
	t     []time.Time
}

type retainedMetrics map[string][]addedField

func (retMetrics retainedMetrics) sort() {
	for _, fields := range retMetrics {
		sort.Slice(fields, func(i, j int) bool {
			switch ti, tj := fields[i].t, fields[j].t; {
			case len(ti) == 0:
				return true
			case len(tj) == 0:
				return false
			default:
				return ti[0].Before(tj[0])
			}
		})
	}
}

// get returns a map timestamp -> points at this timestamp.
func (retMetrics retainedMetrics) get(measurement string, field string, filter func(tags map[string]string) bool) map[int64][]any {
	fields, ok := retMetrics[measurement]
	if !ok {
		return nil
	}

	values := make(map[int64][]any)

	for _, f := range fields {
		if f.field != field {
			continue
		}

		if len(f.t) == 0 {
			logger.V(2).Printf("Ignoring %s_%s (%v) because it has no timestamp (value=%v)", measurement, field, f.tags, f.value)

			continue
		}

		if !filter(f.tags) {
			continue
		}

		ts := f.t[0].Unix()
		values[ts] = append(values[ts], f.value)
	}

	return values
}

func (retMetrics retainedMetrics) reduce(measurement string, field string, acc any, fn func(acc, value any, tags map[string]string, t time.Time) any) any {
	fields, ok := retMetrics[measurement]
	if !ok {
		return nil
	}

	for _, f := range fields {
		if f.field != field {
			continue
		}

		if len(f.t) == 0 {
			logger.V(2).Printf("Ignoring %s_%s (%v) because it has no timestamp (value=%v)", measurement, field, f.tags, f.value)

			continue
		}

		acc = fn(acc, f.value, f.tags, f.t[0])
	}

	return acc
}

type retainAccumulator struct {
	telegraf.Accumulator

	// map measurement -> fields
	mustRetain             map[string][]string
	retainedPerMeasurement retainedMetrics
	l                      sync.Mutex
}

func (retAcc *retainAccumulator) AddFields(measurement string, fields map[string]any, tags map[string]string, t ...time.Time) {
	if retFields, ok := retAcc.mustRetain[measurement]; ok {
		retAcc.l.Lock()

		for _, f := range retFields {
			if value, ok := fields[f]; ok {
				retAcc.retainedPerMeasurement[measurement] = append(retAcc.retainedPerMeasurement[measurement], addedField{f, value, tags, t})
			}
		}

		retAcc.l.Unlock()
	}

	retAcc.Accumulator.AddFields(measurement, fields, tags, t...)
}

type cachedPoint struct {
	types.MetricPoint

	recordedAt time.Time
}

// pointCache is a map labels -> point.
type pointCache map[string]cachedPoint

func (ptsCache *pointCache) update(lastPoints []types.MetricPoint, t0 time.Time) []types.MetricPoint {
	ptsCache.purge(t0)

	for _, point := range lastPoints {
		if cachedPt, exists := (*ptsCache)[types.LabelsToText(point.Labels)]; exists {
			if point.Time.Before(cachedPt.Time) {
				continue
			}
		}

		(*ptsCache)[types.LabelsToText(point.Labels)] = cachedPoint{point, t0}
	}

	points := make([]types.MetricPoint, 0, len(*ptsCache))

	for _, point := range *ptsCache {
		metricPoint := point.MetricPoint
		metricPoint.Time = t0

		points = append(points, metricPoint)
	}

	return points
}

func (ptsCache *pointCache) purge(t0 time.Time) {
	for labels, point := range *ptsCache {
		if t0.Sub(point.recordedAt) > time.Hour {
			delete(*ptsCache, labels)
		}
	}
}
