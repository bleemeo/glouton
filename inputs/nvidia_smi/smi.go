package nvidia

import (
	"glouton/collector"
	"glouton/inputs"
	"glouton/inputs/internal"
	"time"

	"github.com/influxdata/telegraf/config"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/nvidia_smi"
)

// AddSMIInput adds a NVIDIA SMI input to the collector, given the path to the
// nvidia-smi binary and the timeout used for GPU polling in seconds.
func AddSMIInput(coll *collector.Collector, binPath string, timeout int) error {
	input, ok := telegraf_inputs.Inputs["nvidia_smi"]
	if !ok {
		return inputs.ErrDisabledInput
	}

	nvidiaInput, _ := input().(*nvidia_smi.NvidiaSMI)

	if binPath != "" {
		nvidiaInput.BinPath = binPath
	}

	if timeout != 0 {
		nvidiaInput.Timeout = config.Duration(timeout) * config.Duration(time.Second)
	}

	internalInput := &internal.Input{
		Input:       nvidiaInput,
		Accumulator: internal.Accumulator{},
		Name:        "nvidia-smi",
	}

	if _, err := coll.AddInput(internalInput, internalInput.Name); err != nil {
		return err
	}

	return nil
}
