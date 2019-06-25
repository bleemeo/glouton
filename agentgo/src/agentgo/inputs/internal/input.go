package internal

import "github.com/influxdata/telegraf"

// Input is a generic input that use the modifying Accumulator defined in this package
type Input struct {
	telegraf.Input
	Accumulator Accumulator
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval"
func (i *Input) Gather(acc telegraf.Accumulator) error {
	i.Accumulator.Accumulator = acc
	i.Accumulator.PrepareGather()
	err := i.Input.Gather(&i.Accumulator)
	return err
}
