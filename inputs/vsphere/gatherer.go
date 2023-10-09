package vsphere

import (
	"context"
	"errors"
	"glouton/inputs"
	"glouton/inputs/internal"
	"glouton/logger"
	"glouton/prometheus/model"
	"glouton/prometheus/registry"
	"glouton/types"
	"net/url"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/inputs/vsphere"
	dto "github.com/prometheus/client_model/go"
	"github.com/vmware/govmomi/vim25/soap"
)

const endpointCreationRetryDelay = 5 * time.Minute

type vSphereGatherer struct {
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

	l sync.Mutex
}

func (gatherer *vSphereGatherer) Gather() ([]*dto.MetricFamily, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
		allErrs := errors.Join(append(errAcc.errs, err)...)

		if allErrs != nil {
			logger.Printf("Collected some error(s) for vSphere %q:\n%s", gatherer.soapURL.Host, allErrs)
		}

		gatherer.lastPoints = gatherer.buffer.Points()
		gatherer.lastErr = allErrs
	}

	mfs := model.MetricPointsToFamilies(gatherer.lastPoints)

	return mfs, gatherer.lastErr
}

func (gatherer *vSphereGatherer) stop() {
	gatherer.cancel()

	if gatherer.endpoint != nil {
		gatherer.l.Lock()
		defer gatherer.l.Unlock()

		gatherer.endpoint.Close()
	} else if gatherer.endpointCreateTimer != nil {
		// Case where the endpoint still has not been created.
		gatherer.endpointCreateTimer.Stop()
	}
}

func (gatherer *vSphereGatherer) createEndpoint(ctx context.Context, input *vsphere.VSphere) {
	endpoint, err := vsphere.NewEndpoint(ctx, input, gatherer.soapURL, input.Log)
	if err == nil {
		gatherer.l.Lock()
		gatherer.endpoint = endpoint
		gatherer.l.Unlock()

		logger.Printf("vSphere endpoint for %q successfully created", gatherer.soapURL.Host) // TODO: remove

		return
	}

	// As this input is not running, no concurrent access should occur with this field.
	gatherer.lastErr = err

	logger.V(1).Printf("Failed to create vSphere endpoint for %q: %v -- will retry in %s.", gatherer.soapURL.Host, err, endpointCreationRetryDelay)

	gatherer.endpointCreateTimer = time.AfterFunc(endpointCreationRetryDelay, func() {
		if ctx.Err() == nil {
			gatherer.createEndpoint(ctx, input)
		}
	})
}

// newGatherer creates a vSphere gatherer from the given endpoint.
// It will return an error if the endpoint URL is not valid.
func newGatherer(endpointURL string, input *vsphere.VSphere, acc *internal.Accumulator) (*vSphereGatherer, error) {
	soapURL, err := soap.ParseURL(endpointURL)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	gatherer := &vSphereGatherer{
		soapURL: soapURL,
		acc:     acc,
		buffer:  new(registry.PointBuffer),
		cancel:  cancel,
	}

	gatherer.createEndpoint(ctx, input)

	return gatherer, nil
}

type errorAccumulator struct {
	telegraf.Accumulator
	errs []error
}

func (errAcc *errorAccumulator) AddError(err error) {
	errAcc.errs = append(errAcc.errs, err)
}
