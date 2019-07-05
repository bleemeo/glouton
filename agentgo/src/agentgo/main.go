package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"

	"agentgo/api"
	"agentgo/collector"
	"agentgo/facts"
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
	"agentgo/version"

	"github.com/influxdata/telegraf"
)

func panicOnError(i telegraf.Input, err error) telegraf.Input {
	if err != nil {
		log.Fatalf("%v", err)
	}
	return i
}

func main() {
	log.Printf("Starting agent version %v (commit %v)", version.Version, version.BuildHash)

	apiBindAddress := os.Getenv("API_ADDRESS")
	if apiBindAddress == "" {
		apiBindAddress = ":8015"
	}

	db := store.New()
	dockerFact := facts.NewDocker()
	psFact := facts.NewProcess(dockerFact)
	_ = psFact
	factProvider := facts.NewFacter(
		"",
		"/",
		"https://myip.bleemeo.com",
	)
	factProvider.AddCallback(dockerFact.DockerFact)
	factProvider.SetFact("installation_format", "golang")
	factProvider.SetFact("statsd_enabled", "false")
	api := api.New(db, dockerFact, apiBindAddress)
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
		dockerFact.Run(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		coll.Run(ctx)
	}()

	go api.Run()

	f, _ := factProvider.Facts(ctx, 0)
	keys := make([]string, 0)
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		log.Printf("%v = %v", k, f[k])
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	<-c
	cancel()
	wg.Wait()
}
