package discovery

import (
	"fmt"
	"glouton/prometheus/exporter/memcached"
	"glouton/prometheus/registry"
	"glouton/types"
	"runtime"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/pkg/labels"
)

const defaultInterval = 0

func (d *Discovery) createPrometheusMemcached(service Service) error {
	ip, port := service.AddressPort()

	if ip == "" || port == 0 {
		return nil
	}

	address := fmt.Sprintf("%s:%d", ip, port)

	collector := memcached.NewExporter(address, 5*time.Second)
	lbls := map[string]string{
		types.LabelMetaServiceName:   service.Name,
		types.LabelMetaContainerID:   service.ContainerID,
		types.LabelMetaContainerName: service.ContainerName,
		types.LabelMetaServicePort:   strconv.FormatInt(int64(port), 10),
	}

	if d.metricRegistry == nil {
		return nil
	}

	reg := prometheus.NewRegistry()
	if err := reg.Register(collector); err != nil {
		return err
	}

	stopCallback := func() {
		// The memcached client used by memcached exporter does not provide
		// any way to close connection :(
		// It rely on GC to close file description.
		// Trigger a GC now to avoid too much leaking of FDs
		runtime.GC()
	}

	hash := labels.FromMap(lbls).Hash()

	id, err := d.metricRegistry.RegisterGatherer(
		registry.RegistrationOption{
			Description:  "memcached exporter",
			JitterSeed:   hash,
			Interval:     defaultInterval,
			StopCallback: stopCallback,
			ExtraLabels:  lbls,
		},
		reg,
		d.metricFormat == types.MetricFormatPrometheus,
	)
	if err != nil {
		return err
	}

	key := NameContainer{
		Name:          service.Name,
		ContainerName: service.ContainerName,
	}
	d.activeCollector[key] = collectorDetails{
		gathererID: id,
	}

	return nil
}
