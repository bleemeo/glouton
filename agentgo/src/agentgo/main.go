package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"agentgo/api"
	"agentgo/collector"
	"agentgo/inputs/cpu"
	"agentgo/inputs/disk"
	"agentgo/inputs/diskio"
	"agentgo/inputs/docker"
	"agentgo/inputs/mem"
	"agentgo/inputs/net"
	"agentgo/inputs/process"
	"agentgo/inputs/swap"
	"agentgo/inputs/system"
	"agentgo/store"
	"agentgo/types"

	"github.com/influxdata/telegraf"
)

type storeInterface interface {
	Metrics(filters map[string]string) ([]types.Metric, error)
}

func stats(db storeInterface) {
	for {
		time.Sleep(60 * time.Second)
		metrics, _ := db.Metrics(nil)
		log.Printf("Count of metrics: %v", len(metrics))

		metrics, _ = db.Metrics(map[string]string{"__name__": "cpu_used"})
		for _, m := range metrics {
			log.Printf("Details for metrics %v", m)
			points, _ := m.Points(time.Now().Add(-86400*time.Second), time.Now())
			log.Printf("points count: %v", len(points))
		}
	}
}

func panicOnError(i telegraf.Input, err error) telegraf.Input {
	if err != nil {
		log.Fatalf("%v", err)
	}
	return i
}

func main() {
	log.Println("Starting agent")
	db := store.New()
	api := api.New(db)
	coll := collector.New(db.Accumulator())

	coll.AddInput(panicOnError(system.New()))
	coll.AddInput(panicOnError(process.New()))
	coll.AddInput(panicOnError(cpu.New()))
	coll.AddInput(panicOnError(mem.New()))
	coll.AddInput(panicOnError(swap.New()))
	coll.AddInput(panicOnError(net.New(
		[]string{
			"docker",
			"lo",
			"veth",
			"virbr",
			"vnet",
			"isatap",
		},
	)))
	coll.AddInput(panicOnError(disk.New("/", nil)))
	coll.AddInput(panicOnError(diskio.New(
		[]string{
			"sd?",
			"nvme.*",
		},
	)))
	coll.AddInput(panicOnError(docker.New()))

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())

	wg.Add(1)
	go func() {
		defer wg.Done()
		db.Run(ctx)
		db.Close()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		coll.Run(ctx)
	}()
	go stats(db)

	log.Println("Starting API")
	go api.Run()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	<-c
	cancel()
	wg.Wait()
}
