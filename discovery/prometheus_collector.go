package discovery

import (
	"fmt"
	"glouton/prometheus/exporter/memcached"
	"glouton/prometheus/registry"
	"glouton/types"
	"runtime"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// wrapWithLabels wraps a Prometheus collector to include additional labels.
// it will also return a AnnotatedGatherer from which Glouton registry will
// extract the annotations.
func wrapWithLabels(additionalLabels map[string]string, annotations types.MetricAnnotations, collector prometheus.Collector) (result prometheus.Gatherer, err error) {
	reg := prometheus.NewRegistry()
	if len(additionalLabels) > 0 {
		wrapReg := prometheus.WrapRegistererWith(prometheus.Labels(additionalLabels), reg)
		err = wrapReg.Register(collector)
	} else {
		err = reg.Register(collector)
	}

	if err != nil {
		return nil, err
	}

	return registry.WrapWithAnnotation(reg, annotations), nil
}

func (d *Discovery) createPrometheusMemcached(service Service) error {
	ip, port := service.AddressPort()

	if ip == "" {
		return nil
	}

	address := fmt.Sprintf("%s:%d", ip, port)

	collector := memcached.NewExporter(address, 5*time.Second)
	annotations := types.MetricAnnotations{
		ServiceName: service.Name,
		ContainerID: service.ContainerID,
	}
	labels := map[string]string{}
	if service.ContainerName != "" {
		labels[types.LabelContainerName] = service.ContainerName
	}
	gatherer, err := wrapWithLabels(labels, annotations, collector)

	if err != nil {
		return err
	}

	if d.metricRegistry == nil {
		return nil
	}

	d.metricRegistry.RegisterGatherer(gatherer)
	key := NameContainer{
		Name:          service.Name,
		ContainerName: service.ContainerName,
	}
	d.activeCollector[key] = collectorDetails{
		prometheusGatherer: gatherer,
		closeFunc: func() {
			// The memcached client used by memcached exporter does not provide
			// any way to close connection :(
			// It rely on GC to close file description.
			// Trigger a GC now to avoid too much leaking of FDs
			runtime.GC()
		},
	}

	return nil
}
