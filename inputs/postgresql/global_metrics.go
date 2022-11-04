package postgresql

import (
	"glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/inputs/postgresql"
)

// globalMetrics adds metrics with the sum on all databases.
type globalMetrics struct {
	input *postgresql.Postgresql
}

func (g globalMetrics) Gather(acc telegraf.Accumulator) error {
	tmp := &internal.StoreAccumulator{}
	err := g.input.Gather(tmp)

	sum(tmp)
	tmp.Send(acc)

	return err
}

// Add sum metrics to the accumulator.
func sum(acc *internal.StoreAccumulator) {
	sumMetrics := make(map[string]float64)

	for _, m := range acc.Measurement {
		for name, value := range m.Fields {
			vFloat, err := internal.ConvertToFloat(value)
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
			"db": "global",
		},
	}

	acc.Measurement = append(acc.Measurement, sumMeasurement)
}

// SampleConfig returns the default configuration of the Processor.
func (g globalMetrics) SampleConfig() string {
	return g.input.SampleConfig()
}

func (g globalMetrics) Init() error {
	return g.input.Init()
}

func (g globalMetrics) Start(acc telegraf.Accumulator) (err error) {
	return g.input.Start(acc)
}

func (g globalMetrics) Stop() {
	g.input.Stop()
}
