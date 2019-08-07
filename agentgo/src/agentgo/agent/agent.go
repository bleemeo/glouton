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
	"agentgo/bleemeo"
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

	discovery    *discovery.Discovery
	dockerFact   *facts.DockerProvider
	collector    *collector.Collector
	factProvider *facts.FactProvider

	triggerHandler *debouncer.Debouncer
	triggerLock    sync.Mutex
	triggerDisc    bool
	triggerFact    bool

	dockerInputPresent bool
	dockerInputID      int
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
func (a *agent) run() { //nolint:gocyclo
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
	a.dockerFact = facts.NewDocker()
	psFact := facts.NewProcess(a.dockerFact)
	netstat := &facts.NetstatProvider{}
	a.factProvider = facts.NewFacter(
		a.config.String("agent.facts_file"),
		rootPath,
		a.config.String("agent.public_ip_indicator"),
	)
	a.factProvider.AddCallback(a.dockerFact.DockerFact)
	a.factProvider.SetFact("installation_format", a.config.String("agent.installation_format"))
	a.factProvider.SetFact("statsd_enabled", a.config.String("telegraf.statsd.enabled"))
	a.collector = collector.New(db.Accumulator())
	a.discovery = discovery.New(
		discovery.NewDynamic(psFact, netstat, a.dockerFact),
		a.collector,
		a.taskRegistry,
		nil,
		db.Accumulator(),
	)
	api := api.New(db, a.dockerFact, psFact, a.factProvider, apiBindAddress, a.discovery)

	a.collector.AddInput(panicOnError(system.New()), "system")
	a.collector.AddInput(panicOnError(process.New()), "process")
	a.collector.AddInput(panicOnError(cpu.New()), "cpu")
	a.collector.AddInput(panicOnError(mem.New()), "mem")
	a.collector.AddInput(panicOnError(swap.New()), "swap")
	a.collector.AddInput(panicOnError(net.New(a.config.StringList("network_interface_blacklist"))), "net")
	if rootPath != "" {
		a.collector.AddInput(panicOnError(disk.New(rootPath, nil)), "disk")
	}
	a.collector.AddInput(panicOnError(diskio.New(a.config.StringList("disk_monitor"))), "diskio")

	a.triggerHandler = debouncer.New(
		a.handleTrigger,
		10*time.Second,
	)
	a.FireTrigger(true, false)

	_, err := a.taskRegistry.AddTask(db, "store")
	if err != nil {
		logger.V(1).Printf("Unable to start store: %v", err)
	}
	_, err = a.taskRegistry.AddTask(a.triggerHandler, "triggerHandler")
	if err != nil {
		logger.V(1).Printf("Unable to start discovery: %v", err)
	}
	_, err = a.taskRegistry.AddTask(a.dockerFact, "docker")
	if err != nil {
		logger.V(1).Printf("Unable to start Docker watcher: %v", err)
	}
	_, err = a.taskRegistry.AddTask(a.collector, "collector")
	if err != nil {
		logger.V(1).Printf("Unable to start metric collector: %v", err)
	}
	_, err = a.taskRegistry.AddTask(api, "api")
	if err != nil {
		logger.V(1).Printf("Unable to start local API: %v", err)
	}

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
				a.FireTrigger(true, false)
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.FireTrigger(false, true)
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case ev := <-a.dockerFact.Events():
				if ev.Action == "start" || ev.Action == "die" || ev.Action == "destroy" {
					a.FireTrigger(true, false)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	if a.config.Bool("bleemeo.enabled") {
		connector := bleemeo.New(bleemeo.Option{
			Config:                 a.config,
			State:                  a.state,
			Facts:                  a.factProvider,
			UpdateMetricResolution: a.collector.UpdateDelay,
		})
		_, err := a.taskRegistry.AddTask(connector, "bleemeo")
		if err != nil {
			logger.V(1).Printf("Unable to start Bleemeo connector: %v", err)
		}
	}

	for s := range c {
		if s == syscall.SIGTERM || s == syscall.SIGINT || s == os.Interrupt {
			cancel()
			break
		}
		if s == syscall.SIGHUP {
			a.FireTrigger(true, true)
		}
	}

	cancel()
	a.taskRegistry.Close()
	a.discovery.Close()
	wg.Wait()
	logger.V(2).Printf("Agent stopped")
}

func (a *agent) FireTrigger(discovery bool, sendFacts bool) {
	a.triggerLock.Lock()
	defer a.triggerLock.Unlock()
	if discovery {
		a.triggerDisc = true
	}
	if sendFacts {
		a.triggerFact = true
	}
	a.triggerHandler.Trigger()
}

func (a *agent) cleanTrigger() (discovery bool, sendFacts bool) {
	a.triggerLock.Lock()
	defer a.triggerLock.Unlock()

	discovery = a.triggerDisc
	sendFacts = a.triggerFact
	a.triggerDisc = false
	a.triggerFact = false
	return
}

func (a *agent) handleTrigger(ctx context.Context) {
	runDiscovery, runFact := a.cleanTrigger()
	if runDiscovery {
		_, err := a.discovery.Discovery(ctx, 0)
		if err != nil {
			logger.V(1).Printf("error during discovery: %v", err)
		}
		hasConnection := a.dockerFact.HasConnection(ctx)
		if hasConnection && !a.dockerInputPresent {
			i, err := docker.New()
			if err != nil {
				logger.V(1).Printf("error when creating Docker input: %v", err)
			} else {
				logger.V(2).Printf("Enable Docker metrics")
				a.dockerInputID = a.collector.AddInput(i, "docker")
				a.dockerInputPresent = true
			}
		} else if !hasConnection && a.dockerInputPresent {
			logger.V(2).Printf("Disable Docker metrics")
			a.collector.RemoveInput(a.dockerInputID)
			a.dockerInputPresent = false
		}
	}
	if runFact {
		if _, err := a.factProvider.Facts(ctx, 0); err != nil {
			logger.V(1).Printf("error during facts gathering: %v", err)
		}
	}
}
