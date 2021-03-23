package disk

import (
	"glouton/inputs/internal"

	"github.com/influxdata/telegraf"
)

type deduplicator struct {
	telegraf.Input
}

func (d deduplicator) Gather(acc telegraf.Accumulator) error {
	tmp := &internal.StoreAccumulator{}
	err := d.Input.Gather(tmp)

	deduplicate(tmp)
	tmp.Send(acc)

	return err
}

func deduplicate(acc *internal.StoreAccumulator) {
	deleted := make([]bool, len(acc.Measurement))
	pathToIndx := make(map[string]int, len(acc.Measurement))
	devToIndx := make(map[string]int, len(acc.Measurement))

	for i, m := range acc.Measurement {
		if oldI, ok := pathToIndx[m.Tags["path"]]; ok {
			deleted[oldI] = true
		}

		pathToIndx[m.Tags["path"]] = i
	}

	for i, m := range acc.Measurement {
		if deleted[i] {
			continue
		}

		if oldI, ok := devToIndx[m.Tags["device"]]; ok {
			if len(acc.Measurement[oldI].Tags["path"]) <= len(m.Tags["path"]) {
				deleted[i] = true

				continue
			}

			deleted[oldI] = true
		}

		devToIndx[m.Tags["device"]] = i
	}

	n := 0

	for i, x := range acc.Measurement {
		if !deleted[i] {
			acc.Measurement[n] = x
			n++
		}
	}

	acc.Measurement = acc.Measurement[:n]
}
