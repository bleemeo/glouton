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
	"agentgo/debouncer"
	"agentgo/discovery"
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
	netstat := &facts.NetstatProvider{}
	factProvider := facts.NewFacter(
		"",
		"/",
		"https://myip.bleemeo.com",
	)
	factProvider.AddCallback(dockerFact.DockerFact)
	factProvider.SetFact("installation_format", "golang")
	factProvider.SetFact("statsd_enabled", "false")
	api := api.New(db, dockerFact, psFact, factProvider, apiBindAddress)
	coll := collector.New(db.Accumulator())

	coll.AddInput(panicOnError(system.New()), "system")
	coll.AddInput(panicOnError(process.New()), "process")
	coll.AddInput(panicOnError(cpu.New()), "cpu")
	coll.AddInput(panicOnError(mem.New()), "mem")
	coll.AddInput(panicOnError(swap.New()), "swap")
	coll.AddInput(panicOnError(net.New(
		[]string{
			"docker",
			"lo",
			"veth",
			"virbr",
			"vnet",
			"isatap",
		},
	)),
		"net",
	)
	coll.AddInput(panicOnError(disk.New("/", nil)), "disk")
	coll.AddInput(panicOnError(diskio.New(
		[]string{
			"sd?",
			"nvme.*",
		},
	)), "diskio")

	disc := discovery.New(
		discovery.NewDynamic(psFact, netstat, dockerFact),
		coll,
		nil,
	)

	dockerInputPresent := false
	dockerInputID := 0
	discoveryTrigger := debouncer.New(
		func(ctx context.Context) {
			_, err := disc.Discovery(ctx, 0)
			if err != nil {
				log.Printf("DBG: error during discovery: %v", err)
			}
			hasConnection := dockerFact.HasConnection(ctx)
			if hasConnection && !dockerInputPresent {
				i, err := docker.New()
				if err != nil {
					log.Printf("DBG: error when creating Docker input: %v", err)
				} else {
					log.Printf("DBG2: Enable Docker metrics")
					dockerInputID = coll.AddInput(i, "docker")
					dockerInputPresent = true
				}
			} else if !hasConnection && dockerInputPresent {
				log.Printf("DBG2: Disable Docker metrics")
				coll.RemoveInput(dockerInputID)
				dockerInputPresent = false
			}
		},
		10*time.Second,
	)
	discoveryTrigger.Trigger()

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
		discoveryTrigger.Run(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				discoveryTrigger.Trigger()
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case ev := <-dockerFact.Events():
				if ev.Action == "start" || ev.Action == "die" || ev.Action == "destroy" {
					discoveryTrigger.Trigger()
				}
			case <-ctx.Done():
				return
			}
		}
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

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	for s := range c {
		if s == syscall.SIGTERM || s == syscall.SIGINT || s == os.Interrupt {
			cancel()
			break
		}
		if s == syscall.SIGHUP {
			discoveryTrigger.Trigger()
		}
	}
	wg.Wait()
}
