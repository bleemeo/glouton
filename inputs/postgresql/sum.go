package postgresql

import (
	"glouton/inputs"
	"glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/inputs/postgresql"
)

// sumMetrics adds metrics with the sum on all databases.
type sumMetrics struct {
	input *postgresql.Postgresql
}

func (s sumMetrics) Gather(acc telegraf.Accumulator) error {
	tmp := &internal.StoreAccumulator{}
	err := s.input.Gather(tmp)

	sum(tmp)
	tmp.Send(acc)

	return err
}

// Add sum metrics to the accumulator.
func sum(acc *internal.StoreAccumulator) {
	sumMetrics := make(map[string]float64)

	for _, m := range acc.Measurement {
		for name, value := range m.Fields {
			vFloat, err := inputs.ConvertToFloat(value)
			if err != nil {
				continue
			}

			sumMetrics[name] += vFloat
		}
	}

	// Convert the sum metrics to interfaces.
	newFields := make(map[string]interface{}, len(sumMetrics))

	for name, value := range sumMetrics {
		newFields[name] = value
	}

	sumMeasurement := internal.Measurement{
		Name:   "postgresql",
		Fields: newFields,
		Tags: map[string]string{
			"sum": "true",
		},
	}

	acc.Measurement = append(acc.Measurement, sumMeasurement)
}

// SampleConfig returns the default configuration of the Processor.
func (s sumMetrics) SampleConfig() string {
	return s.input.SampleConfig()
}

func (s sumMetrics) Init() error {
	return s.input.Init()
}

func (s sumMetrics) Start(acc telegraf.Accumulator) (err error) {
	return s.input.Start(acc)
}

func (s sumMetrics) Stop() {
	s.input.Stop()
}
