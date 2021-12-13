package model

import (
	"glouton/logger"
	"glouton/types"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

func FamiliesToMetricPoints(now time.Time, families []*dto.MetricFamily) []types.MetricPoint {
	samples, err := expfmt.ExtractSamples(
		&expfmt.DecodeOptions{Timestamp: model.TimeFromUnixNano(now.UnixNano())},
		families...,
	)
	if err != nil {
		logger.Printf("Conversion of metrics failed, some metrics may be missing: %v", err)
	}

	result := make([]types.MetricPoint, len(samples))

	for i, sample := range samples {
		labels := make(map[string]string, len(sample.Metric))

		for k, v := range sample.Metric {
			labels[string(k)] = string(v)
		}

		result[i] = types.MetricPoint{
			Labels: labels,
			Point: types.Point{
				Time:  sample.Timestamp.Time(),
				Value: float64(sample.Value),
			},
		}
	}

	return result
}
