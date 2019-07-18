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

// Start the ServiceInput.  The Accumulator may be retained and used until
// Stop returns.
func (i *Input) Start(acc telegraf.Accumulator) error {
	i.Accumulator.Accumulator = acc
	if si, ok := i.Input.(telegraf.ServiceInput); ok {
		return si.Start(&i.Accumulator)
	}
	return nil
}

// Stop stops the services and closes any necessary channels and connections
func (i *Input) Stop() {
	if si, ok := i.Input.(telegraf.ServiceInput); ok {
		si.Stop()
	}
}
