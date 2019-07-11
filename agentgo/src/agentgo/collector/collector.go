// Package collector do the metric point gathering for all configured input every fixed time interval
package collector

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
)

// Collector implement running Gather on inputs every fixed time interval
type Collector struct {
	acc        telegraf.Accumulator
	inputs     map[int]telegraf.Input
	inputNames map[int]string
	l          sync.Mutex
}

// New returns a Collector with default option
//
// By default, no input are added (use AddInput) and collection is done every
// 10 seconds.
func New(acc telegraf.Accumulator) *Collector {
	c := &Collector{
		acc:        acc,
		inputs:     make(map[int]telegraf.Input),
		inputNames: make(map[int]string),
	}
	return c
}

// AddInput add an input to this collector and return an ID
func (c *Collector) AddInput(input telegraf.Input, shortName string) int {
	c.l.Lock()
	defer c.l.Unlock()

	id := 1
	_, ok := c.inputs[id]
	for ok {
		id++
		if id == 0 {
			panic("too many inputs in the collectors. Unable to find new slot")
		}
		_, ok = c.inputs[id]
	}
	c.inputs[id] = input
	c.inputNames[id] = shortName

	if si, ok := input.(telegraf.ServiceInput); ok {
		if err := si.Start(nil); err != nil {
			panic("Failed to start input")
		}
	}

	return id
}

// RemoveInput removes an input by its ID.
func (c *Collector) RemoveInput(id int) {
	c.l.Lock()
	defer c.l.Unlock()

	if input, ok := c.inputs[id]; ok {
		if si, ok := input.(telegraf.ServiceInput); ok {
			si.Stop()
		}
	}

	delete(c.inputs, id)
	delete(c.inputNames, id)
}

// Run will run the collections until context is cancelled
func (c *Collector) Run(ctx context.Context) {
	for {
		c.run()
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

func (c *Collector) run() {
	c.l.Lock()
	inputsCopy := make([]telegraf.Input, 0)
	inputsNameCopy := make([]string, 0)
	for id, v := range c.inputs {
		inputsCopy = append(inputsCopy, v)
		inputsNameCopy = append(inputsNameCopy, c.inputNames[id])
	}
	c.l.Unlock()

	for i, input := range inputsCopy {
		err := input.Gather(c.acc)
		if err != nil {
			log.Printf("Input %s failed: %v", inputsNameCopy[i], err)
		}
	}
}
