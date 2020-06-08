package blackbox

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

type labelPair struct {
	name  string
	value string
}

// writeMFsToChan converts metrics families to new metrics, before writing them on the 'ch' channel.
// It also accepts a list of hardcoded labels, which can be useful for supplying the target label for example.
func writeMFsToChan(mfs []*dto.MetricFamily, hardcodedLabels []labelPair, ch chan<- prometheus.Metric) {
	labelsInit := []string{}
	labelsValuesInit := []string{}
	for _, label := range hardcodedLabels {
		labelsInit = append(labelsInit, label.name)
		labelsValuesInit = append(labelsValuesInit, label.value)
	}

	for _, mf := range mfs {
		labels := make([]string, len(hardcodedLabels))
		copy(labels, labelsInit)

		metrics := mf.GetMetric()
		if len(metrics) == 0 {
			continue
		}

		// update the list of labels (yes, this is yet another O(n²) algorithm)
		for _, metric := range metrics {
			metricLabelsPairs := metric.GetLabel()
			// add a label, if it isn't already registered
		OuterBreak:
			for _, labelPair := range metricLabelsPairs {
				for _, v := range labels {
					if v == *labelPair.Name {
						continue OuterBreak
					}
				}
				labels = append(labels, *labelPair.Name)
			}
		}

		desc := prometheus.NewDesc(
			prometheus.BuildFQName("", "", mf.GetName()),
			mf.GetHelp(),
			labels,
			nil,
		)

		for _, metric := range metrics {
			labelsValues := make([]string, len(hardcodedLabels))
			copy(labelsValues, labelsValuesInit)
			// let's take great care to preserve the order of the labels, or weird things are gonna happen
			// NOTE: we do not check that every metric in the family has the same labels, as we will insert the empty string otherwise
			for _, label := range labels[len(hardcodedLabels):] {
				var labelValue string
				for _, v := range metric.GetLabel() {
					if *v.Name == label {
						labelValue = *v.Value
						break
					}
				}
				labelsValues = append(labelsValues, labelValue)
			}

			// in theory, this should only be a counter or a gauge, given the fact that we only do this probing operation once (and then we start again from scratch)
			switch {
			case metric.GetCounter() != nil:
				ch <- prometheus.MustNewConstMetric(desc, prometheus.CounterValue, metric.GetCounter().GetValue(), labelsValues...)
			case metric.GetGauge() != nil:
				ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, metric.GetGauge().GetValue(), labelsValues...)
			default:
				panic(fmt.Errorf("blackbox_exporter: invalid type supplied to a probe"))
			}
		}
	}

}
