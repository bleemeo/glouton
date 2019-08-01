// Package agent contains the glue between other components
package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"agentgo/agent/state"
	"agentgo/api"
	"agentgo/collector"
	"agentgo/config"
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
	"agentgo/logger"
	"agentgo/store"
	"agentgo/task"
	"agentgo/version"

	"github.com/influxdata/telegraf"

	"net/http"
)

type agent struct {
	taskRegistry *task.Registry
	config       *config.Configuration
	state        *state.State
}

func panicOnError(i telegraf.Input, err error) telegraf.Input {
	if err != nil {
		logger.Printf("%v", err)
		panic(err)
	}
	return i
}

func (a *agent) init() (ok bool) {
	a.taskRegistry = task.NewRegistry(context.Background())
	cfg, warnings, err := a.loadConfiguration()
	a.config = cfg

	a.setupLogger()
	if err != nil {
		logger.Printf("Error while loading configuration: %v", err)
		return false
	}
	for _, w := range warnings {
		logger.Printf("Warning while loading configuration: %v", w)
	}

	a.state, err = state.Load(a.config.String("agent.state_file"))
	if err != nil {
		logger.Printf("Error while loading state file: %v", err)
		return false
	}
	if err := a.state.Save(); err != nil {
		logger.Printf("State file is not writable, stopping agent: %v", err)
		return false
	}
	return true
}

func (a *agent) setupLogger() {
	useSyslog := false
	if a.config.String("logging.output") == "syslog" {
		useSyslog = true
	}
	logger.UseSyslog(useSyslog)
	if level := a.config.Int("logging.level"); level != 0 {
		logger.SetLevel(level)
	} else {
		switch strings.ToLower(a.config.String("logging.level")) {
		case "0", "info", "warning", "error":
			logger.SetLevel(0)
		case "verbose":
			logger.SetLevel(1)
		case "debug":
			logger.SetLevel(2)
		default:
			logger.SetLevel(0)
			logger.Printf("Unknown logging.level = %#v. Using \"INFO\"", a.config.String("logging.level"))
		}
	}
	logger.SetPkgLevels(a.config.String("logging.package_levels"))
}

// Run runs the Bleemeo agent
func Run() {
	agent := &agent{
		taskRegistry: task.NewRegistry(context.Background()),
	}
	if !agent.init() {
		os.Exit(1)
		return
	}
	agent.run()
}

// Run will start the agent. It will terminate when sigquit/sigterm/sigint is received
func (a *agent) run() {
	logger.Printf("Starting agent version %v (commit %v)", version.Version, version.BuildHash)

	ctx, cancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	apiBindAddress := fmt.Sprintf("%s:%d", a.config.String("web.listener.address"), a.config.Int("web.listener.port"))

	if a.config.Bool("agent.http_debug.enabled") {
		go func() {
			debugAddress := a.config.String("agent.http_debug.binf_address")
			logger.Printf("Starting debug server on http://%s/debug/pprof/", debugAddress)
			log.Println(http.ListenAndServe(debugAddress, nil))
		}()
	}

	rootPath := "/"
	if a.config.String("container.type") != "" {
		rootPath = a.config.String("df.host_mount_point")
	}

	db := store.New()
	dockerFact := facts.NewDocker()
	psFact := facts.NewProcess(dockerFact)
	netstat := &facts.NetstatProvider{}
	factProvider := facts.NewFacter(
		a.config.String("agent.facts_file"),
		rootPath,
		a.config.String("agent.public_ip_indicator"),
	)
	factProvider.AddCallback(dockerFact.DockerFact)
	factProvider.SetFact("installation_format", a.config.String("agent.installation_format"))
	factProvider.SetFact("statsd_enabled", a.config.String("telegraf.statsd.enabled"))
	coll := collector.New(db.Accumulator())
	disc := discovery.New(
		discovery.NewDynamic(psFact, netstat, dockerFact),
		coll,
		a.taskRegistry,
		nil,
		db.Accumulator(),
	)
	api := api.New(db, dockerFact, psFact, factProvider, apiBindAddress, disc)

	coll.AddInput(panicOnError(system.New()), "system")
	coll.AddInput(panicOnError(process.New()), "process")
	coll.AddInput(panicOnError(cpu.New()), "cpu")
	coll.AddInput(panicOnError(mem.New()), "mem")
	coll.AddInput(panicOnError(swap.New()), "swap")
	coll.AddInput(panicOnError(net.New(a.config.StringList("network_interface_blacklist"))), "net")
	if rootPath != "" {
		coll.AddInput(panicOnError(disk.New(rootPath, nil)), "disk")
	}
	coll.AddInput(panicOnError(diskio.New(a.config.StringList("disk_monitor"))), "diskio")

	dockerInputPresent := false
	dockerInputID := 0
	discoveryTrigger := debouncer.New(
		func(ctx context.Context) {
			_, err := disc.Discovery(ctx, 0)
			if err != nil {
				logger.V(1).Printf("error during discovery: %v", err)
			}
			hasConnection := dockerFact.HasConnection(ctx)
			if hasConnection && !dockerInputPresent {
				i, err := docker.New()
				if err != nil {
					logger.V(1).Printf("error when creating Docker input: %v", err)
				} else {
					logger.V(2).Printf("Enable Docker metrics")
					dockerInputID = coll.AddInput(i, "docker")
					dockerInputPresent = true
				}
			} else if !hasConnection && dockerInputPresent {
				logger.V(2).Printf("Disable Docker metrics")
				coll.RemoveInput(dockerInputID)
				dockerInputPresent = false
			}
		},
		10*time.Second,
	)
	discoveryTrigger.Trigger()

	a.taskRegistry.AddTask(db, "store")
	a.taskRegistry.AddTask(discoveryTrigger, "discovery")
	a.taskRegistry.AddTask(dockerFact, "docker")
	a.taskRegistry.AddTask(coll, "collector")
	a.taskRegistry.AddTask(api, "api")

	var wg sync.WaitGroup
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

	for s := range c {
		if s == syscall.SIGTERM || s == syscall.SIGINT || s == os.Interrupt {
			cancel()
			break
		}
		if s == syscall.SIGHUP {
			discoveryTrigger.Trigger()
		}
	}

	cancel()
	a.taskRegistry.Close()
	disc.Close()
	wg.Wait()
	logger.V(2).Printf("Agent stopped")
}
