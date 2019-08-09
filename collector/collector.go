// Package collector do the metric point gathering for all configured input every fixed time interval
package collector

import (
	"agentgo/logger"
	"context"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
)

// Collector implement running Gather on inputs every fixed time interval
type Collector struct {
	acc          telegraf.Accumulator
	inputs       map[int]telegraf.Input
	inputNames   map[int]string
	currentDelay time.Duration
	updateDelayC chan interface{}
	l            sync.Mutex
}

// New returns a Collector with default option
//
// By default, no input are added (use AddInput) and collection is done every
// 10 seconds.
func New(acc telegraf.Accumulator) *Collector {
	c := &Collector{
		acc:          acc,
		inputs:       make(map[int]telegraf.Input),
		inputNames:   make(map[int]string),
		currentDelay: 10 * time.Second,
		updateDelayC: make(chan interface{}),
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
	} else {
		logger.V(2).Printf("called RemoveInput with unexisting ID %d", id)
	}

	delete(c.inputs, id)
	delete(c.inputNames, id)
}

// UpdateDelay change the delay between metric gather
func (c *Collector) UpdateDelay(delay time.Duration) {
	if c.setCurrentDelay(delay) {
		logger.V(2).Printf("Change metric collector delay to %v", delay)
		c.updateDelayC <- nil
	}
}

// Run will run the collections until context is cancelled
func (c *Collector) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		c.run(ctx)
	}
	return nil
}

func (c *Collector) getCurrentDelay() time.Duration {
	c.l.Lock()
	defer c.l.Unlock()
	return c.currentDelay
}

func (c *Collector) setCurrentDelay(delay time.Duration) (changed bool) {
	c.l.Lock()
	defer c.l.Unlock()
	if c.currentDelay == delay {
		return false
	}
	c.currentDelay = delay
	return true
}

// sleep such are time.Now() is aligned on a multiple of interval
func (c *Collector) sleepToAlign(interval time.Duration) {
	now := time.Now()
	previousMultiple := now.Truncate(interval)
	if previousMultiple == now {
		return
	}
	nextMultiple := previousMultiple.Add(interval)
	time.Sleep(nextMultiple.Sub(now))
}

func (c *Collector) inputsForCollection() ([]telegraf.Input, []string) {
	c.l.Lock()
	defer c.l.Unlock()

	inputsCopy := make([]telegraf.Input, 0)
	inputsNameCopy := make([]string, 0)
	for id, v := range c.inputs {
		inputsCopy = append(inputsCopy, v)
		inputsNameCopy = append(inputsNameCopy, c.inputNames[id])
	}
	return inputsCopy, inputsNameCopy
}

func (c *Collector) run(ctx context.Context) {
	currentDelay := c.getCurrentDelay()
	c.sleepToAlign(currentDelay)
	ticker := time.NewTicker(currentDelay)
	defer ticker.Stop()
	for {
		c.runOnce()
		select {
		case <-c.updateDelayC:
			return
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (c *Collector) runOnce() {
	inputsCopy, inputsNameCopy := c.inputsForCollection()
	var wg sync.WaitGroup

	t0 := time.Now()
	for i, input := range inputsCopy {
		i := i
		input := input
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := input.Gather(c.acc)
			if err != nil {
				logger.Printf("Input %s failed: %v", inputsNameCopy[i], err)
			}
		}()
	}
	wg.Wait()
	delta := time.Since(t0)
	logger.V(1).Printf("METRIC: Gather took %v", delta)
}