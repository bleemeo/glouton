package disk

import (
	"glouton/inputs/internal"
	"strings"

	"github.com/influxdata/telegraf"
)

type deduplicator struct {
	telegraf.Input
	hostroot string
}

func (d deduplicator) Gather(acc telegraf.Accumulator) error {
	tmp := &internal.StoreAccumulator{}
	err := d.Input.Gather(tmp)

	deduplicate(tmp, d.hostroot)
	tmp.Send(acc)

	return err
}

func deduplicate(acc *internal.StoreAccumulator, hostroot string) {
	deleted := make([]bool, len(acc.Measurement))
	pathToIndx := make(map[string]int, len(acc.Measurement))
	devToIndx := make(map[string]int, len(acc.Measurement))

	// when multiple mount on same path, last mount win
	for i, m := range acc.Measurement {
		if oldI, ok := pathToIndx[m.Tags["path"]]; ok {
			deleted[oldI] = true
		}

		pathToIndx[m.Tags["path"]] = i
	}

	// when multiple device are mounted on multiple path:
	// * sortest path win... unless it don't start by hostroot while the other does
	for i, m := range acc.Measurement {
		if deleted[i] {
			continue
		}

		if oldI, ok := devToIndx[m.Tags["device"]]; ok {
			oldIsShortest := len(acc.Measurement[oldI].Tags["path"]) <= len(m.Tags["path"])
			oldNoHostRootWhileNewHad := !strings.HasPrefix(acc.Measurement[oldI].Tags["path"], hostroot) && strings.HasPrefix(m.Tags["path"], hostroot)
			newNoHostRootWhileOldHad := !strings.HasPrefix(m.Tags["path"], hostroot) && strings.HasPrefix(acc.Measurement[oldI].Tags["path"], hostroot)

			if oldIsShortest && !oldNoHostRootWhileNewHad || newNoHostRootWhileOldHad {
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
