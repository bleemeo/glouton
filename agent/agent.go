// Package agent contains the glue between other components
package agent

import (
	"context"
	"errors"
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
	"agentgo/bleemeo/types"
	"agentgo/collector"
	"agentgo/config"
	"agentgo/debouncer"
	"agentgo/discovery"
	"agentgo/facts"
	"agentgo/inputs/docker"
	"agentgo/logger"
	"agentgo/nrpe"
	"agentgo/store"
	"agentgo/task"
	"agentgo/threshold"
	"agentgo/version"
	"agentgo/zabbix"

	"net/http"
)

type agent struct {
	taskRegistry *task.Registry
	config       *config.Configuration
	state        *state.State
	cancel       context.CancelFunc

	discovery        *discovery.Discovery
	dockerFact       *facts.DockerProvider
	collector        *collector.Collector
	factProvider     *facts.FactProvider
	bleemeoConnector *bleemeo.Connector
	accumulator      *threshold.Accumulator

	triggerHandler *debouncer.Debouncer
	triggerLock    sync.Mutex
	triggerDisc    bool
	triggerFact    bool

	dockerInputPresent bool
	dockerInputID      int

	l       sync.Mutex
	taskIDs map[string]int
}

func nrpeResponse(ctx context.Context, request string) (string, int16, error) {
	return "", 0, fmt.Errorf("NRPE: Command '%s' not defined", request)
}

func zabbixResponse(key string, args []string) (string, error) {
	if key == "agent.ping" {
		return "1", nil
	}
	if key == "agent.version" {
		return fmt.Sprintf("4 (Bleemeo Agent %s)", version.Version), nil
	}
	return "", errors.New("Unsupported item key") // nolint: stylecheck
}

type taskInfo struct {
	function task.Runner
	name     string
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
		taskIDs:      make(map[string]int),
	}
	if !agent.init() {
		os.Exit(1)
		return
	}
	agent.run()
}

// BleemeoAccountID returns the Account UUID of Bleemeo
// It return the empty string if the Account UUID is not available (e.g. because Bleemeo is disabled or mis-configured)
func (a *agent) BleemeoAccountID() string {
	if a.bleemeoConnector == nil {
		return ""
	}
	return a.bleemeoConnector.AccountID()
}

// BleemeoAgentID returns the Agent UUID of Bleemeo
// It return the empty string if the Agent UUID is not available (e.g. because Bleemeo is disabled or registration didn't happen yet)
func (a *agent) BleemeoAgentID() string {
	if a.bleemeoConnector == nil {
		return ""
	}
	return a.bleemeoConnector.AgentID()
}

// BleemeoRegistrationAt returns the date of Agent registration with Bleemeo API
// It return the zero time if registration didn't occurred yet
func (a *agent) BleemeoRegistrationAt() time.Time {
	if a.bleemeoConnector == nil {
		return time.Time{}
	}
	return a.bleemeoConnector.RegistrationAt()
}

// BleemeoLastReport returns the date of last report with Bleemeo API
// It return the zero time if registration didn't occurred yet or no data send to Bleemeo API
func (a *agent) BleemeoLastReport() time.Time {
	if a.bleemeoConnector == nil {
		return time.Time{}
	}
	return a.bleemeoConnector.LastReport()
}

// BleemeoConnected returns true if Bleemeo is currently connected (to MQTT)
func (a *agent) BleemeoConnected() bool {
	if a.bleemeoConnector == nil {
		return false
	}
	return a.bleemeoConnector.Connected()
}

// Tags returns tags of this Agent.
func (a *agent) Tags() []string {
	tagsSet := make(map[string]bool)
	for _, t := range a.config.StringList("tags") {
		tagsSet[t] = true
	}
	if a.bleemeoConnector != nil {
		for _, t := range a.bleemeoConnector.Tags() {
			tagsSet[t] = true
		}
	}
	tags := make([]string, 0, len(tagsSet))
	for t := range tagsSet {
		tags = append(tags, t)
	}
	return tags
}

// UpdateThresholds update the thresholds definition.
// This method will merge with threshold definition present in configuration file
func (a *agent) UpdateThresholds(thresholds map[threshold.MetricNameItem]threshold.Threshold) {
	a.updateThresholds(thresholds, false)
}

func (a *agent) updateThresholds(thresholds map[threshold.MetricNameItem]threshold.Threshold, showWarning bool) {
	rawValue, ok := a.config.Get("thresholds")
	if !ok {
		rawValue = map[string]interface{}{}
	}
	var rawThreshold map[string]interface{}
	if rawThreshold, ok = rawValue.(map[string]interface{}); !ok {
		if showWarning {
			logger.V(1).Printf("Threshold in configuration file is not map")
		}
		rawThreshold = nil
	}
	configThreshold := make(map[string]threshold.Threshold, len(rawThreshold))
	for k, v := range rawThreshold {
		v2, ok := v.(map[string]interface{})
		if !ok {
			if showWarning {
				logger.V(1).Printf("Threshold in configuration file is not well-formated: %v value is not a map", k)
			}
			continue
		}
		t, err := threshold.FromInterfaceMap(v2)
		if err != nil {
			if showWarning {
				logger.V(1).Printf("Threshold in configuration file is not well-formated: %v", err)
			}
			continue
		}
		configThreshold[k] = t
	}

	a.accumulator.SetThresholds(thresholds, configThreshold)
}

// Run will start the agent. It will terminate when sigquit/sigterm/sigint is received
func (a *agent) run() { //nolint:gocyclo
	logger.Printf("Starting agent version %v (commit %v)", version.Version, version.BuildHash)

	_ = os.Remove(a.config.String("agent.upgrade_file"))

	c := make(chan os.Signal, 1)
	a.cancel = func() {
		c <- os.Interrupt
	}
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
	a.accumulator = threshold.New(
		db.Accumulator(),
		a.state,
	)
	a.updateThresholds(nil, true)
	a.dockerFact = facts.NewDocker()
	psFact := facts.NewProcess(a.dockerFact)
	netstat := &facts.NetstatProvider{FilePath: a.config.String("agent.netstat_file")}
	a.factProvider = facts.NewFacter(
		a.config.String("agent.facts_file"),
		rootPath,
		a.config.String("agent.public_ip_indicator"),
	)
	a.factProvider.AddCallback(a.dockerFact.DockerFact)
	a.factProvider.SetFact("installation_format", a.config.String("agent.installation_format"))
	a.factProvider.SetFact("statsd_enabled", a.config.String("telegraf.statsd.enabled"))
	a.collector = collector.New(a.accumulator)
	a.discovery = discovery.New(
		discovery.NewDynamic(psFact, netstat, a.dockerFact, discovery.SudoFileReader{HostRootPath: rootPath}, a.config.String("stack")),
		a.collector,
		a.taskRegistry,
		a.state,
		a.accumulator,
		a.dockerFact,
	)
	api := api.New(db, a.dockerFact, psFact, a.factProvider, apiBindAddress, a.discovery, a)

	err := discovery.AddDefaultInputs(a.collector, rootPath, a.config)
	if err != nil {
		logger.Printf("Unable to initialize system collector: %v", err)
		return
	}

	a.triggerHandler = debouncer.New(
		a.handleTrigger,
		10*time.Second,
	)
	a.FireTrigger(true, false)

	tasks := []taskInfo{
		{db.Run, "store"},
		{a.triggerHandler.Run, "triggerHandler"},
		{a.dockerFact.Run, "docker"},
		{a.collector.Run, "collector"},
		{api.Run, "api"},
		{a.healthCheck, "healthCheck"},
		{a.hourlyDiscovery, "hourlyDiscovery"},
		{a.dailyFact, "dailyFact"},
		{a.dockerWatcher, "dockerWatcher"},
	}

	if a.config.Bool("bleemeo.enabled") {
		a.bleemeoConnector = bleemeo.New(types.GlobalOption{
			Config:                 a.config,
			State:                  a.state,
			Facts:                  a.factProvider,
			Process:                psFact,
			Docker:                 a.dockerFact,
			Store:                  db,
			Acc:                    a.accumulator,
			Discovery:              a.discovery,
			UpdateMetricResolution: a.collector.UpdateDelay,
			UpdateThresholds:       a.UpdateThresholds,
			UpdateUnits:            a.accumulator.SetUnits,
		})
		tasks = append(tasks, taskInfo{a.bleemeoConnector.Run, "bleemeo"})
	}
	if a.config.Bool("nrpe.enabled") {
		server := nrpe.New(
			fmt.Sprintf("%s:%d", a.config.String("nrpe.address"), a.config.Int("nrpe.port")),
			a.config.Bool("nrpe.ssl"),
			nrpeResponse,
		)
		tasks = append(tasks, taskInfo{server.Run, "nrpe"})
	}
	if a.config.Bool("zabbix.enabled") {
		server := zabbix.New(
			fmt.Sprintf("%s:%d", a.config.String("zabbix.address"), a.config.Int("zabbix.port")),
			zabbixResponse,
		)
		tasks = append(tasks, taskInfo{server.Run, "zabbix"})
	}

	a.startTasks(tasks)

	for s := range c {
		if s == syscall.SIGTERM || s == syscall.SIGINT || s == os.Interrupt {
			break
		}
		if s == syscall.SIGHUP {
			a.FireTrigger(true, true)
		}
	}

	a.taskRegistry.Close()
	a.discovery.Close()
	logger.V(2).Printf("Agent stopped")
}

func (a *agent) startTasks(tasks []taskInfo) {
	a.l.Lock()
	defer a.l.Unlock()

	for _, t := range tasks {
		id, err := a.taskRegistry.AddTask(t.function, t.name)
		if err != nil {
			logger.V(1).Printf("Unable to start %s: %v", t.name, err)
		}
		a.taskIDs[t.name] = id
	}
}

func (a *agent) healthCheck(ctx context.Context) error {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil
		}
		mandatoryTasks := []string{"bleemeo", "collector", "store"}
		for _, name := range mandatoryTasks {
			if a.doesTaskCrashed(ctx, name) {
				logger.Printf("Gorouting %v crashed. Stopping the agent", name)
				a.cancel()
			}
		}
		if a.bleemeoConnector != nil {
			a.bleemeoConnector.HealthCheck()
		}
	}
}

func (a *agent) doesTaskCrashed(ctx context.Context, name string) bool {
	a.l.Lock()
	defer a.l.Unlock()
	if id, ok := a.taskIDs[name]; ok {
		if !a.taskRegistry.IsRunning(id) {
			// Re-check ctx to avoid race condition
			if ctx.Err() != nil {
				return false
			}
			return true
		}
	}
	return false
}

func (a *agent) hourlyDiscovery(ctx context.Context) error {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			a.FireTrigger(true, false)
		}
	}
}

func (a *agent) dailyFact(ctx context.Context) error {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			a.FireTrigger(false, true)
		}
	}
}

func (a *agent) dockerWatcher(ctx context.Context) error {
	for {
		select {
		case ev := <-a.dockerFact.Events():
			if ev.Action == "start" || ev.Action == "die" || ev.Action == "destroy" {
				a.FireTrigger(true, false)
			}
		case <-ctx.Done():
			return nil
		}
	}
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
