package discovery

import (
	"fmt"
	"glouton/logger"
	"glouton/prometheus/registry"
	"glouton/types"
	"runtime"
	"strconv"
	"time"

	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	memcachedExporter "github.com/prometheus/memcached_exporter/pkg/exporter"
	"github.com/prometheus/prometheus/model/labels"
)

const defaultInterval = 0

func (d *Discovery) createPrometheusMemcached(service Service) error {
	ip, port := service.AddressPort()

	if ip == "" || port == 0 {
		return nil
	}

	address := fmt.Sprintf("%s:%d", ip, port)

	extLogger := log.With(logger.GoKitLoggerWrapper(logger.V(2)), "service", "memcached", "container_name", service.ContainerName)
	collector := memcachedExporter.New(address, 5*time.Second, extLogger)
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
			Description:           "memcached exporter",
			JitterSeed:            hash,
			Interval:              defaultInterval,
			StopCallback:          stopCallback,
			ExtraLabels:           lbls,
			DisablePeriodicGather: d.metricFormat != types.MetricFormatPrometheus,
		},
		reg,
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
