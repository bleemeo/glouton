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

package vsphere

import (
	"context"
	"errors"
	"fmt"
	"glouton/config"
	"glouton/inputs"
	"glouton/inputs/internal"
	"glouton/logger"
	"glouton/prometheus/model"
	"glouton/prometheus/registry"
	"glouton/types"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/inputs/vsphere"
	dto "github.com/prometheus/client_model/go"
	"github.com/vmware/govmomi/vim25/soap"
)

const (
	endpointCreationRetryDelay = 5 * time.Minute
	clusterMetricsPeriod       = 5 * time.Minute
)

type vSphereGatherer struct {
	cfg *config.VSphere
	// Storing the endpoint's URL, just in case the endpoint can't be
	// created at startup and will need to be instanced later.
	soapURL             *url.URL
	endpointCreateTimer *time.Timer

	endpoint *vsphere.Endpoint
	cancel   context.CancelFunc

	acc        *internal.Accumulator
	buffer     *registry.PointBuffer
	lastPoints []types.MetricPoint
	lastErr    error

	devicePropsCache *propsCaches

	lastAdditionalClusterMetricsAt time.Time

	l sync.Mutex
}

func (gatherer *vSphereGatherer) Gather() ([]*dto.MetricFamily, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commonTimeout)
	defer cancel()

	return gatherer.GatherWithState(ctx, registry.GatherState{T0: time.Now()})
}

func (gatherer *vSphereGatherer) GatherWithState(ctx context.Context, state registry.GatherState) ([]*dto.MetricFamily, error) {
	gatherer.l.Lock()
	defer gatherer.l.Unlock()

	if gatherer.endpoint == nil {
		return nil, nil
	}

	// Do the gather only in the scrape loop and reuse the last points on /metrics.
	if state.FromScrapeLoop {
		errAcc := errorAccumulator{
			Accumulator: &inputs.Accumulator{
				// Gather metric points in the buffer.
				Pusher:  gatherer.buffer,
				Context: ctx,
			},
		}
		acc := gatherer.acc
		acc.Accumulator = &errAcc

		err := gatherer.endpoint.Collect(ctx, acc)
		errAddMetrics := gatherer.collectAdditionalMetrics(ctx, acc, state.T0)
		allErrs := errors.Join(filterErrors(append(errAcc.errs, err, errAddMetrics))...)

		gatherer.lastPoints = gatherer.buffer.Points()
		gatherer.lastErr = allErrs
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

func (gatherer *vSphereGatherer) collectAdditionalMetrics(ctx context.Context, acc telegraf.Accumulator, t0 time.Time) error {
	finder, client, err := newDeviceFinder(ctx, *gatherer.cfg)
	if err != nil {
		return err
	}

	clusters, _, hosts, vms, err := findDevices(ctx, finder, false)
	if err != nil {
		return err
	}

	if len(clusters) == 0 && len(hosts) == 0 && len(vms) == 0 {
		return nil
	}

	h, err := hierarchyFrom(ctx, clusters, hosts, vms, gatherer.devicePropsCache.vmCache)
	if err != nil {
		return fmt.Errorf("can't describe hierarchy: %w", err)
	}

	if t0.IsZero() {
		t0 = time.Now()
	}

	if t0.Sub(gatherer.lastAdditionalClusterMetricsAt) >= clusterMetricsPeriod {
		// The next gathering should run in ~5min, so we schedule it in 4m50s from now to be safe.
		gatherer.lastAdditionalClusterMetricsAt = t0.Add(-10 * time.Second)

		err = additionalClusterMetrics(ctx, client, clusters, gatherer.devicePropsCache.hostCache, acc, h)
		if err != nil {
			return err
		}
	}

	// For each host, we have a list of vm states (running/stopped)
	vmStatesPerHost := make(map[string][]bool, len(hosts))

	err = additionalVMMetrics(ctx, client, vms, gatherer.devicePropsCache.vmCache, acc, h, vmStatesPerHost)
	if err != nil {
		return err
	}

	err = additionalHostMetrics(ctx, client, hosts, acc, h, vmStatesPerHost)
	if err != nil {
		return err
	}

	return nil
}

func (gatherer *vSphereGatherer) stop() {
	gatherer.l.Lock()
	defer gatherer.l.Unlock()

	gatherer.cancel()

	if gatherer.endpoint != nil {
		gatherer.endpoint.Close()
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
		ep, err := vsphere.NewEndpoint(ctx, input, epURL, input.Log)
		if err == nil {
			gatherer.endpoint = ep
		}

		return err
	}

	err := newEP(gatherer.soapURL)
	if err == nil {
		return
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Unwrap().Error() == "400 Bad Request" {
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
	}

	logger.V(1).Printf("Failed to create vSphere endpoint for %q: %v -- will retry in %s.", gatherer.soapURL.Host, err, endpointCreationRetryDelay)

	gatherer.lastErr = err
	gatherer.endpointCreateTimer = time.AfterFunc(endpointCreationRetryDelay, func() {
		gatherer.createEndpoint(ctx, input)
	})
}

// newGatherer creates a vSphere gatherer from the given endpoint.
// It will return an error if the endpoint URL is not valid.
func newGatherer(ctx context.Context, cfg *config.VSphere, input *vsphere.VSphere, acc *internal.Accumulator, devicePropsCache *propsCaches) (*vSphereGatherer, error) {
	soapURL, err := soap.ParseURL(cfg.URL)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)

	gatherer := &vSphereGatherer{
		cfg:              cfg,
		soapURL:          soapURL,
		acc:              acc,
		buffer:           new(registry.PointBuffer),
		cancel:           cancel,
		devicePropsCache: devicePropsCache,
	}

	gatherer.createEndpoint(ctx, input)

	return gatherer, nil
}

// errorAccumulator embeds a real accumulator while collecting
// all the errors given to it, instead of logging them.
type errorAccumulator struct {
	telegraf.Accumulator

	errs []error
}

func (errAcc *errorAccumulator) AddError(err error) {
	errAcc.errs = append(errAcc.errs, err)
}
