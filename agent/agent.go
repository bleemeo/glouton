// Copyright 2015-2019 Bleemeo
//
// bleemeo.com an infrastructure monitoring solution in the Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package agent contains the glue between other components
package agent

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"glouton/agent/state"
	"glouton/api"
	"glouton/bleemeo"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/collector"
	"glouton/config"
	"glouton/debouncer"
	"glouton/discovery"
	"glouton/discovery/promexporter"
	"glouton/facts"
	"glouton/facts/container-runtime/containerd"
	dockerRuntime "glouton/facts/container-runtime/docker"
	"glouton/facts/container-runtime/kubernetes"
	"glouton/facts/container-runtime/merge"
	crTypes "glouton/facts/container-runtime/types"
	"glouton/influxdb"
	"glouton/inputs"
	"glouton/inputs/docker"
	processInput "glouton/inputs/process"
	"glouton/inputs/statsd"
	"glouton/jmxtrans"
	"glouton/logger"
	"glouton/nrpe"
	"glouton/prometheus/exporter/blackbox"
	"glouton/prometheus/exporter/common"
	"glouton/prometheus/process"
	"glouton/prometheus/registry"
	"glouton/prometheus/scrapper"
	"glouton/store"
	"glouton/task"
	"glouton/threshold"
	"glouton/types"
	"glouton/version"
	"glouton/zabbix"

	"net/http"
	"net/url"

	"gopkg.in/yaml.v3"
)

var errUnsupportedKey = errors.New("Unsupported item key") //nolint: stylecheck

type agent struct {
	taskRegistry *task.Registry
	config       *config.Configuration
	state        *state.State
	cancel       context.CancelFunc
	context      context.Context

	hostRootPath           string
	discovery              *discovery.Discovery
	dockerRuntime          *dockerRuntime.Docker
	containerdRuntime      *containerd.Containerd
	containerRuntime       crTypes.RuntimeInterface
	collector              *collector.Collector
	factProvider           *facts.FactProvider
	bleemeoConnector       *bleemeo.Connector
	influxdbConnector      *influxdb.Client
	threshold              *threshold.Registry
	jmx                    *jmxtrans.JMX
	store                  *store.Store
	gathererRegistry       *registry.Registry
	metricFormat           types.MetricFormat
	dynamicScrapper        *promexporter.DynamicScrapper
	lastHealCheck          time.Time
	lastContainerEventTime time.Time

	triggerHandler            *debouncer.Debouncer
	triggerLock               sync.Mutex
	triggerDiscAt             time.Time
	triggerDiscImmediate      bool
	triggerFact               bool
	triggerSystemUpdateMetric bool

	dockerInputPresent bool
	dockerInputID      int

	l                sync.Mutex
	taskIDs          map[string]int
	metricResolution time.Duration
}

func zabbixResponse(key string, args []string) (string, error) {
	if key == "agent.ping" {
		return "1", nil
	}

	if key == "agent.version" {
		return fmt.Sprintf("4 (Glouton %s)", version.Version), nil
	}

	return "", errUnsupportedKey
}

type taskInfo struct {
	function task.Runner
	name     string
}

func (a *agent) init(configFiles []string) (ok bool) {
	a.l.Lock()
	a.lastHealCheck = time.Now()
	a.l.Unlock()

	a.taskRegistry = task.NewRegistry(context.Background())
	cfg, warnings, err := a.loadConfiguration(configFiles)
	a.config = cfg

	a.setupLogger()

	if err != nil {
		logger.Printf("Error while loading configuration: %v", err)
		return false
	}

	for _, w := range warnings {
		logger.Printf("Warning while loading configuration: %v", w)
	}

	statePath := a.config.String("agent.state_file")
	oldStatePath := a.config.String("agent.deprecated_state_file")

	a.state, err = state.Load(statePath)
	if err != nil {
		logger.Printf("Error while loading state file: %v", err)
		return false
	}

	if !a.state.IsEmpty() {
		oldStatePath = ""
	}

	if oldStatePath != "" {
		oldState, err := state.Load(oldStatePath)
		if err != nil {
			logger.Printf("Error while loading state file: %v", err)
			return false
		}

		if oldState.IsEmpty() {
			oldStatePath = ""
		} else {
			a.state = oldState
		}
	}

	a.migrateState()

	if err := a.state.SaveTo(statePath); err != nil {
		if oldStatePath != "" {
			stateDir := filepath.Dir(statePath)
			logger.Printf("State file can't we wrote at new path (%s): %v", statePath, err)
			logger.Printf("Keeping the deprecated path (%s).", oldStatePath)
			logger.Printf(
				"To migrate to new path, simply create a persistent folder %s or move %s to %s while Glouton is stopped",
				stateDir,
				oldStatePath,
				statePath,
			)

			err = a.state.SaveTo(oldStatePath)
		}

		if err != nil {
			logger.Printf("State file is not writable, stopping agent: %v", err)
			return false
		}
	} else if oldStatePath != "" {
		logger.Printf("The deprecated state file (%s) is migrated to new path (%s).", oldStatePath, statePath)
	}

	return true
}

func (a *agent) setupLogger() {
	logger.SetBufferCapacity(
		a.config.Int("logging.buffer.head_size"),
		a.config.Int("logging.buffer.tail_size"),
	)

	var err error

	switch a.config.String("logging.output") {
	case "syslog":
		err = logger.UseSyslog()
	case "file":
		err = logger.UseFile(a.config.String("logging.filename"))
	}

	if err != nil {
		fmt.Printf("Unable to use logging backend '%s': %v\n", a.config.String("logging.output"), err)
	}

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

// Run runs Glouton.
func Run(configFiles []string) {
	rand.Seed(time.Now().UnixNano())

	agent := &agent{
		taskRegistry: task.NewRegistry(context.Background()),
		taskIDs:      make(map[string]int),
	}

	agent.initOSSpecificParts()

	if !agent.init(configFiles) {
		os.Exit(1)
		return
	}

	agent.run()
}

// BleemeoAccountID returns the Account UUID of Bleemeo
// It return the empty string if the Account UUID is not available (e.g. because Bleemeo is disabled or mis-configured).
func (a *agent) BleemeoAccountID() string {
	if a.bleemeoConnector == nil {
		return ""
	}

	return a.bleemeoConnector.AccountID()
}

// BleemeoAgentID returns the Agent UUID of Bleemeo
// It return the empty string if the Agent UUID is not available (e.g. because Bleemeo is disabled or registration didn't happen yet).
func (a *agent) BleemeoAgentID() string {
	if a.bleemeoConnector == nil {
		return ""
	}

	return a.bleemeoConnector.AgentID()
}

// BleemeoRegistrationAt returns the date of Agent registration with Bleemeo API
// It return the zero time if registration didn't occurred yet.
func (a *agent) BleemeoRegistrationAt() time.Time {
	if a.bleemeoConnector == nil {
		return time.Time{}
	}

	return a.bleemeoConnector.RegistrationAt()
}

// BleemeoLastReport returns the date of last report with Bleemeo API
// It return the zero time if registration didn't occurred yet or no data send to Bleemeo API.
func (a *agent) BleemeoLastReport() time.Time {
	if a.bleemeoConnector == nil {
		return time.Time{}
	}

	return a.bleemeoConnector.LastReport()
}

// BleemeoConnected returns true if Bleemeo is currently connected (to MQTT).
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
// This method will merge with threshold definition present in configuration file.
func (a *agent) UpdateThresholds(thresholds map[threshold.MetricNameItem]threshold.Threshold, firstUpdate bool) {
	a.updateThresholds(thresholds, firstUpdate)
}

// notifyBleemeoFirstRegistration is called when Glouton is registered with Bleemeo Cloud platform for the first time
// This means that when this function is called, BleemeoAgentID and BleemeoAccountID are set.
func (a *agent) notifyBleemeoFirstRegistration(ctx context.Context) {
	a.gathererRegistry.UpdateBleemeoAgentID(ctx, a.BleemeoAgentID())
	a.store.DropAllMetrics()
}

func (a *agent) updateMetricResolution(resolution time.Duration) {
	a.l.Lock()
	a.metricResolution = resolution
	a.l.Unlock()

	a.gathererRegistry.UpdateDelay(resolution)

	services, err := a.discovery.Discovery(a.context, time.Hour)
	if err != nil {
		logger.V(1).Printf("error during discovery: %v", err)
	} else if a.jmx != nil {
		if err := a.jmx.UpdateConfig(services, resolution); err != nil {
			logger.V(1).Printf("failed to update JMX configuration: %v", err)
		}
	}
}

func (a *agent) updateThresholds(thresholds map[threshold.MetricNameItem]threshold.Threshold, firstUpdate bool) {
	rawValue, ok := a.config.Get("thresholds")
	if !ok {
		rawValue = map[string]interface{}{}
	}

	var rawThreshold map[string]interface{}

	checkThresholdIsMap(&rawThreshold, &rawValue, firstUpdate)

	configThreshold := make(map[string]threshold.Threshold, len(rawThreshold))

	for k, v := range rawThreshold {
		v2, ok := v.(map[string]interface{})
		if !ok {
			if firstUpdate {
				logger.V(1).Printf("Threshold in configuration file is not well-formated: %v value is not a map", k)
			}

			continue
		}

		t, err := threshold.FromInterfaceMap(v2)
		if err != nil {
			if firstUpdate {
				logger.V(1).Printf("Threshold in configuration file is not well-formated: %v", err)
			}

			continue
		}

		configThreshold[k] = t
	}

	oldThresholds := map[string]threshold.Threshold{}

	for _, name := range []string{"system_pending_updates", "system_pending_security_updates", "time_drift"} {
		key := threshold.MetricNameItem{
			Name: name,
			Item: "",
		}
		oldThresholds[name] = a.threshold.GetThreshold(key)
	}

	a.threshold.SetThresholds(thresholds, configThreshold)

	for _, name := range []string{"system_pending_updates", "system_pending_security_updates"} {
		key := threshold.MetricNameItem{
			Name: name,
			Item: "",
		}
		newThreshold := a.threshold.GetThreshold(key)

		if !firstUpdate && !oldThresholds[key.Name].Equal(newThreshold) {
			a.FireTrigger(false, false, true, false)
		}
	}

	key := threshold.MetricNameItem{
		Name: "time_drift",
		Item: "",
	}
	newThreshold := a.threshold.GetThreshold(key)

	if !firstUpdate && !oldThresholds[key.Name].Equal(newThreshold) && a.bleemeoConnector != nil {
		a.bleemeoConnector.UpdateInfo()
	}
}

func checkThresholdIsMap(rawThreshold *map[string]interface{}, rawValue interface{}, firstUpdate bool) {
	ok := false

	if *rawThreshold, ok = rawValue.(map[string]interface{}); !ok {
		if firstUpdate {
			logger.V(1).Printf("Threshold in configuration file is not map")
		}

		*rawThreshold = nil
	}
}

// Run will start the agent. It will terminate when sigquit/sigterm/sigint is received.
func (a *agent) run() { //nolint:gocyclo
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a.cancel = cancel
	a.metricResolution = 10 * time.Second
	a.hostRootPath = "/"
	a.context = ctx

	if a.config.String("container.type") != "" {
		a.hostRootPath = a.config.String("df.host_mount_point")
		setupContainer(a.hostRootPath)
	}

	a.triggerHandler = debouncer.New(
		a.handleTrigger,
		10*time.Second,
	)
	a.factProvider = facts.NewFacter(
		a.config.String("agent.facts_file"),
		a.hostRootPath,
		a.config.String("agent.public_ip_indicator"),
	)

	factsMap, err := a.factProvider.FastFacts(ctx)
	if err != nil {
		logger.Printf("Warning: get facts failed, some information (e.g. name of this server) may be wrong. %v", err)
	}

	fqdn := factsMap["fqdn"]
	if fqdn == "" {
		fqdn = "localhost"
	}

	cloudImageFile := a.config.String("agent.cloudimage_creation_file")

	content, err := ioutil.ReadFile(cloudImageFile)
	if err != nil && !os.IsNotExist(err) {
		logger.Printf("Unable to read content of %#v file: %v", cloudImageFile, err)
	}

	if err == nil || !os.IsNotExist(err) {
		initialMac := parseIPOutput(content)
		currentMac := factsMap["primary_mac_address"]

		if currentMac == initialMac || currentMac == "" || initialMac == "" {
			logger.Printf("Not starting Glouton since installation for creation of a cloud image was requested and agent is still running on the same machine")
			logger.Printf("If this is wrong and agent should run on this machine, remove %#v file", cloudImageFile)

			return
		}
	}

	_ = os.Remove(cloudImageFile)

	logger.Printf("Starting agent version %v (commit %v)", version.Version, version.BuildHash)

	_ = os.Remove(a.config.String("agent.upgrade_file"))

	a.metricFormat = types.StringToMetricFormat(a.config.String("agent.metrics_format"))
	if a.metricFormat == types.MetricFormatUnknown {
		logger.Printf("Invalid metric format %#v. Supported option are \"Bleemeo\" and \"Prometheus\". Falling back to Bleemeo", a.config.String("agent.metrics_format"))
		a.metricFormat = types.MetricFormatBleemeo
	}

	apiBindAddress := fmt.Sprintf("%s:%d", a.config.String("web.listener.address"), a.config.Int("web.listener.port"))

	if a.config.Bool("agent.http_debug.enabled") {
		go func() {
			debugAddress := a.config.String("agent.http_debug.bind_address")

			logger.Printf("Starting debug server on http://%s/debug/pprof/", debugAddress)
			log.Println(http.ListenAndServe(debugAddress, nil))
		}()
	}

	a.store = store.New()
	a.gathererRegistry = &registry.Registry{
		PushPoint:      a.store,
		FQDN:           fqdn,
		BleemeoAgentID: a.BleemeoAgentID(),
		GloutonPort:    strconv.FormatInt(int64(a.config.Int("web.listener.port")), 10),
		MetricFormat:   a.metricFormat,
	}
	a.threshold = threshold.New(a.state)
	acc := &inputs.Accumulator{Pusher: a.threshold.WithPusher(a.gathererRegistry.WithTTL(5 * time.Minute))}

	a.dockerRuntime = &dockerRuntime.Docker{
		DockerSockets:             dockerRuntime.DefaultAddresses(a.hostRootPath),
		DeletedContainersCallback: a.deletedContainersCallback,
	}
	a.containerdRuntime = &containerd.Containerd{
		Addresses:                 containerd.DefaultAddresses(a.hostRootPath),
		DeletedContainersCallback: a.deletedContainersCallback,
	}
	a.containerRuntime = &merge.Runtime{
		Runtimes: []crTypes.RuntimeInterface{
			a.dockerRuntime,
			a.containerdRuntime,
		},
	}

	if a.config.Bool("kubernetes.enabled") {
		kube := &kubernetes.Kubernetes{
			Runtime:    a.containerRuntime,
			NodeName:   a.config.String("kubernetes.nodename"),
			KubeConfig: a.config.String("kubernetes.kubeconfig"),
		}
		a.containerRuntime = kube

		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := kube.Test(ctx); err != nil {
			logger.Printf("Kubernetes API unreachable, service detection may misbehave: %v", err)
		}

		cancel()
	}

	var (
		psLister facts.ProcessLister
	)

	useProc := a.config.String("container.type") == "" || a.config.Bool("container.pid_namespace_host")
	if !useProc {
		logger.V(1).Printf("The agent is running in a container and \"container.pid_namespace_host\", is not true. Not all processes will be seen")
	} else {
		if version.IsWindows() {
			psLister = facts.NewPsUtilLister("")
		} else {
			psLister = process.NewProcessLister(a.hostRootPath, 9*time.Second)
		}
	}

	psFact := facts.NewProcess(
		psLister,
		a.hostRootPath,
		a.containerRuntime,
	)
	netstat := &facts.NetstatProvider{FilePath: a.config.String("agent.netstat_file")}

	a.factProvider.AddCallback(a.containerRuntime.RuntimeFact)
	a.factProvider.SetFact("installation_format", a.config.String("agent.installation_format"))

	processInput := processInput.New(psFact, a.threshold.WithPusher(a.gathererRegistry.WithTTL(5*time.Minute)))

	a.collector = collector.New(acc)
	a.gathererRegistry.AddPushPointsCallback(a.collector.RunGather)

	if a.metricFormat == types.MetricFormatBleemeo {
		a.gathererRegistry.AddPushPointsCallback(processInput.Gather)
	}

	a.gathererRegistry.AddPushPointsCallback(
		a.miscGather(a.threshold.WithPusher(a.gathererRegistry.WithTTL(5 * time.Minute))),
	)

	services, _ := a.config.Get("service")
	servicesIgnoreCheck, _ := a.config.Get("service_ignore_check")
	servicesIgnoreMetrics, _ := a.config.Get("service_ignore_metrics")
	overrideServices := confFieldToSliceMap(services, "service override")
	serviceIgnoreCheck := confFieldToSliceMap(servicesIgnoreCheck, "service ignore check")
	serviceIgnoreMetrics := confFieldToSliceMap(servicesIgnoreMetrics, "service ignore metrics")
	isCheckIgnored := discovery.NewIgnoredService(serviceIgnoreCheck).IsServiceIgnored
	isInputIgnored := discovery.NewIgnoredService(serviceIgnoreMetrics).IsServiceIgnored
	dynamicDiscovery := discovery.NewDynamic(psFact, netstat, a.containerRuntime, discovery.SudoFileReader{HostRootPath: a.hostRootPath}, a.config.String("stack"))
	a.discovery = discovery.New(
		dynamicDiscovery,
		a.collector,
		a.gathererRegistry,
		a.taskRegistry,
		a.state,
		acc,
		a.containerRuntime,
		overrideServices,
		isCheckIgnored,
		isInputIgnored,
		a.metricFormat,
	)

	var targets []*scrapper.Target

	if promCfg, found := a.config.Get("metric.prometheus.targets"); found {
		targets = prometheusConfigToURLs(
			promCfg,
			a.config.StringList("metric.prometheus.allow_metrics"),
			a.config.StringList("metric.prometheus.deny_metrics"),
			a.config.Bool("metric.prometheus.include_default_metrics"),
		)
	}

	for _, target := range targets {
		if _, err := a.gathererRegistry.RegisterGatherer(target, nil, target.ExtraLabels, true); err != nil {
			logger.Printf("Unable to add Prometheus scrapper for target %s: %v", target.URL.String(), err)
		}
	}

	a.gathererRegistry.AddDefaultCollector()

	if _, found := a.config.Get("metric.pull"); found {
		logger.Printf("metric.pull is deprecated and not supported by Glouton.")
		logger.Printf("For your custom metrics, please use Prometheus exporter & metric.prometheus")
	}

	a.dynamicScrapper = &promexporter.DynamicScrapper{
		Registry:       a.gathererRegistry,
		DynamicJobName: "discovered-exporters",
	}

	var monitorManager *blackbox.RegisterManager

	if a.config.Bool("blackbox.enabled") {
		logger.V(1).Println("Starting blackbox_exporter...")
		// the config is present, otherwise we would not be in this block
		blackboxConf, _ := a.config.Get("blackbox")

		monitorManager, err = blackbox.New(a.gathererRegistry, blackboxConf, a.metricFormat)
		if err != nil {
			logger.V(0).Printf("Couldn't start blackbox_exporter: %v\nMonitors will not be able to run on this agent.", err)
		}
	} else {
		logger.V(1).Println("blackbox_exporter not enabled, will not start...")
	}

	promExporter := a.gathererRegistry.Exporter()

	if a.config.Bool("agent.process_exporter.enabled") {
		process.RegisterExporter(a.gathererRegistry, psLister, dynamicDiscovery, a.metricFormat == types.MetricFormatBleemeo)
	}

	api := &api.API{
		DB:                 a.store,
		ContainerRuntime:   a.containerRuntime,
		PsFact:             psFact,
		FactProvider:       a.factProvider,
		BindAddress:        apiBindAddress,
		Disccovery:         a.discovery,
		AgentInfo:          a,
		PrometheurExporter: promExporter,
		Threshold:          a.threshold,
		StaticCDNURL:       a.config.String("web.static_cdn_url"),
		DiagnosticPage:     a.DiagnosticPage,
		DiagnosticZip:      a.DiagnosticZip,
	}

	a.FireTrigger(true, true, false, false)

	tasks := []taskInfo{
		{a.watchdog, "Agent Watchdog"},
		{a.store.Run, "Metric store"},
		{a.triggerHandler.Run, "Internal trigger handler"},
		{a.containerRuntime.Run, "Docker connector"},
		{a.healthCheck, "Agent healthcheck"},
		{a.hourlyDiscovery, "Service Discovery"},
		{a.dailyFact, "Facts gatherer"},
		{a.dockerWatcher, "Docker event watcher"},
		{a.netstatWatcher, "Netstat file watcher"},
		{a.miscTasks, "Miscelanous tasks"},
		{a.minuteMetric, "Metrics every minute"},
	}

	if a.config.Bool("web.enabled") {
		tasks = append(tasks, taskInfo{api.Run, "Local Web UI"})
	}

	if a.config.Bool("jmx.enabled") {
		perm, err := strconv.ParseInt(a.config.String("jmxtrans.file_permission"), 8, 0)
		if err != nil {
			logger.Printf("invalid permission %#v: %v", a.config.String("jmxtrans.file_permission"), err)
			logger.Printf("using the default 0640")

			perm = 0640
		}

		a.jmx = &jmxtrans.JMX{
			OutputConfigurationFile:       a.config.String("jmxtrans.config_file"),
			OutputConfigurationPermission: os.FileMode(perm),
			ContactPort:                   a.config.Int("jmxtrans.graphite_port"),
			Pusher:                        a.threshold.WithPusher(a.gathererRegistry.WithTTL(5 * time.Minute)),
		}

		tasks = append(tasks, taskInfo{a.jmx.Run, "jmxtrans"})
	}

	if a.config.Bool("bleemeo.enabled") {
		a.bleemeoConnector = bleemeo.New(bleemeoTypes.GlobalOption{
			Config:                  a.config,
			State:                   a.state,
			Facts:                   a.factProvider,
			Process:                 psFact,
			Docker:                  a.containerRuntime,
			Store:                   a.store,
			Acc:                     acc,
			Discovery:               a.discovery,
			MonitorManager:          monitorManager,
			UpdateMetricResolution:  a.updateMetricResolution,
			UpdateThresholds:        a.UpdateThresholds,
			UpdateUnits:             a.threshold.SetUnits,
			MetricFormat:            a.metricFormat,
			NotifyFirstRegistration: a.notifyBleemeoFirstRegistration,
		})
		a.gathererRegistry.UpdateBleemeoAgentID(ctx, a.BleemeoAgentID())
		tasks = append(tasks, taskInfo{a.bleemeoConnector.Run, "Bleemeo SAAS connector"})

		if a.metricFormat == types.MetricFormatPrometheus {
			logger.Printf("Prometheus format is not yet supported with Bleemeo")
			return
		}
	}

	if a.config.Bool("nrpe.enabled") {
		nrpeConfFile := a.config.StringList("nrpe.conf_paths")
		nrperesponse := nrpe.NewResponse(overrideServices, a.discovery, nrpeConfFile)
		server := nrpe.New(
			fmt.Sprintf("%s:%d", a.config.String("nrpe.address"), a.config.Int("nrpe.port")),
			a.config.Bool("nrpe.ssl"),
			nrperesponse.Response,
		)
		tasks = append(tasks, taskInfo{server.Run, "NRPE server"})
	}

	if a.config.Bool("zabbix.enabled") {
		server := zabbix.New(
			fmt.Sprintf("%s:%d", a.config.String("zabbix.address"), a.config.Int("zabbix.port")),
			zabbixResponse,
		)
		tasks = append(tasks, taskInfo{server.Run, "Zabbix server"})
	}

	if a.config.Bool("influxdb.enabled") {
		server := influxdb.New(
			fmt.Sprintf("http://%s:%s", a.config.String("influxdb.host"), a.config.String("influxdb.port")),
			a.config.String("influxdb.db_name"),
			a.store,
			a.config.StringMap("influxdb.tags"),
		)
		a.influxdbConnector = server
		tasks = append(tasks, taskInfo{server.Run, "influxdb"})

		logger.V(2).Printf("Influxdb is activated !")
	}

	if a.bleemeoConnector == nil {
		a.updateThresholds(nil, true)
	} else {
		a.bleemeoConnector.ApplyCachedConfiguration()
	}

	tmp, _ := a.config.Get("metric.softstatus_period")

	a.threshold.SetSoftPeriod(
		time.Duration(a.config.Int("metric.softstatus_period_default"))*time.Second,
		softPeriodsFromInterface(tmp),
	)

	if !reflect.DeepEqual(a.config.StringList("disk_monitor"), defaultConfig["disk_monitor"]) {
		if a.metricFormat == types.MetricFormatBleemeo && len(a.config.StringList("disk_ignore")) > 0 {
			logger.Printf("Warning: both \"disk_monitor\" and \"disk_ignore\" are set. Only \"disk_ignore\" will be used")
		} else if a.metricFormat != types.MetricFormatBleemeo {
			logger.Printf("Warning: configuration \"disk_monitor\" is not used in Prometheus mode. Use \"disk_ignore\"")
		}
	}

	if a.metricFormat == types.MetricFormatBleemeo {
		conf, err := a.buildCollectorsConfig()
		if err != nil {
			logger.V(0).Printf("Unable to initialize system collector: %v", err)
			return
		}

		if err = discovery.AddDefaultInputs(a.collector, conf); err != nil {
			logger.Printf("Unable to initialize system collector: %v", err)
			return
		}
	}

	// register components only available on a given system, like node_exporter for unixes
	a.registerOSSpecificComponents()

	tasks = append(tasks, taskInfo{
		a.gathererRegistry.RunCollection,
		"Metric collector",
	})

	if a.config.Bool("telegraf.statsd.enabled") {
		input, err := statsd.New(fmt.Sprintf("%s:%d", a.config.String("telegraf.statsd.address"), a.config.Int("telegraf.statsd.port")))
		if err != nil {
			logger.Printf("Unable to create StatsD input: %v", err)
			a.config.Set("telegraf.statsd.enabled", false)
		} else if _, err = a.collector.AddInput(input, "statsd"); err != nil {
			if strings.Contains(err.Error(), "address already in use") {
				logger.Printf("Unable to listen on StatsD port because another program already use it")
				logger.Printf("The StatsD integration is now disabled. Restart the agent to try re-enabling it.")
				logger.Printf("See https://docs.bleemeo.com/agent/configuration#telegrafstatsdenabled to permanently disable StatsD integration or using an alternate port")
			} else {
				logger.Printf("Unable to create StatsD input: %v", err)
			}

			a.config.Set("telegraf.statsd.enabled", false)
		}
	}

	a.factProvider.SetFact("statsd_enabled", a.config.String("telegraf.statsd.enabled"))
	a.factProvider.SetFact("metrics_format", a.metricFormat.String())

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	go func() {
		for s := range c {
			if s == syscall.SIGTERM || s == syscall.SIGINT || s == os.Interrupt {
				cancel()
				break
			}

			if s == syscall.SIGHUP {
				if a.bleemeoConnector != nil {
					a.bleemeoConnector.UpdateMonitors()
				}

				a.FireTrigger(true, true, false, true)
			}
		}
	}()

	a.startTasks(tasks)

	<-ctx.Done()
	logger.V(2).Printf("Stopping agent...")
	signal.Stop(c)
	close(c)
	a.taskRegistry.Close()
	a.discovery.Close()
	logger.V(2).Printf("Agent stopped")
}

func (a *agent) buildCollectorsConfig() (conf inputs.CollectorConfig, err error) {
	whitelistRE, err := common.CompileREs(a.config.StringList("disk_monitor"))
	if err != nil {
		logger.V(1).Printf("the whitelist for diskio regexp couldn't compile: %s", err)
		return
	}

	blacklistRE, err := common.CompileREs(a.config.StringList("disk_ignore"))
	if err != nil {
		logger.V(1).Printf("the blacklist for diskio regexp couldn't compile: %s", err)
		return
	}

	pathBlacklist := a.config.StringList("df.path_ignore")
	pathBlacklistTrimed := make([]string, len(pathBlacklist))

	for i, v := range pathBlacklist {
		pathBlacklistTrimed[i] = strings.TrimRight(v, "/")
	}

	return inputs.CollectorConfig{
		DFRootPath:      a.hostRootPath,
		NetIfBlacklist:  a.config.StringList("network_interface_blacklist"),
		IODiskWhitelist: whitelistRE,
		IODiskBlacklist: blacklistRE,
		DFPathBlacklist: pathBlacklistTrimed,
	}, nil
}

func (a *agent) miscGather(pusher types.PointPusher) func(time.Time) {
	return func(t0 time.Time) {
		points, err := a.containerRuntime.Metrics(context.Background())
		if err != nil {
			logger.V(2).Printf("container Runtime metrics gather failed: %v", err)
		}

		// We don't really care about having up-to-date information because
		// when containers are started/stopped, the information is updated anyway.
		containers, err := a.containerRuntime.Containers(context.Background(), 2*time.Hour, false)
		if err != nil {
			logger.V(2).Printf("gather on DockerProvider failed: %v", err)
			return
		}

		countRunning := 0

		for _, c := range containers {
			if c.State().IsRunning() {
				countRunning++
			}
		}

		points = append(points, types.MetricPoint{
			Point: types.Point{Time: t0, Value: float64(countRunning)},
			Labels: map[string]string{
				"__name__": "containers_count",
			},
		})

		pusher.PushPoints(points)
	}
}

func (a *agent) minuteMetric(ctx context.Context) error {
	for {
		select {
		case <-time.After(time.Minute):
		case <-ctx.Done():
			return nil
		}

		service, err := a.discovery.Discovery(ctx, 2*time.Hour)
		if err != nil {
			logger.V(1).Printf("get service failed to every-minute metrics: %v", err)
			continue
		}

		for _, srv := range service {
			if !srv.Active {
				continue
			}

			switch srv.ServiceType {
			case discovery.PostfixService:
				n, err := postfixQueueSize(ctx, srv, a.hostRootPath, a.containerRuntime)
				if err != nil {
					logger.V(1).Printf("Unabled to gather postfix queue size on %s: %v", srv, err)
					continue
				}

				labels := map[string]string{
					types.LabelName:              "postfix_queue_size",
					types.LabelMetaContainerName: srv.ContainerName,
					types.LabelMetaContainerID:   srv.ContainerID,
					types.LabelMetaServiceName:   srv.ContainerName,
				}

				annotations := types.MetricAnnotations{
					BleemeoItem: srv.ContainerName,
					ContainerID: srv.ContainerID,
					ServiceName: srv.Name,
				}

				a.threshold.WithPusher(a.gathererRegistry.WithTTL(5 * time.Minute)).PushPoints([]types.MetricPoint{
					{
						Labels:      labels,
						Annotations: annotations,
						Point: types.Point{
							Time:  time.Now(),
							Value: n,
						},
					},
				})
			case discovery.EximService:
				n, err := eximQueueSize(ctx, srv, a.hostRootPath, a.containerRuntime)
				if err != nil {
					logger.V(1).Printf("Unabled to gather exim queue size on %s: %v", srv, err)
					continue
				}

				labels := map[string]string{
					types.LabelName:              "exim_queue_size",
					types.LabelMetaContainerName: srv.ContainerName,
					types.LabelMetaContainerID:   srv.ContainerID,
					types.LabelMetaServiceName:   srv.ContainerName,
				}

				annotations := types.MetricAnnotations{
					BleemeoItem: srv.ContainerName,
					ContainerID: srv.ContainerID,
					ServiceName: srv.Name,
				}

				a.threshold.WithPusher(a.gathererRegistry.WithTTL(5 * time.Minute)).PushPoints([]types.MetricPoint{
					{
						Labels:      labels,
						Annotations: annotations,
						Point: types.Point{
							Time:  time.Now(),
							Value: n,
						},
					},
				})
			}
		}
	}
}

func (a *agent) miscTasks(ctx context.Context) error {
	lastTime := time.Now()

	for {
		select {
		case <-time.After(30 * time.Second):
		case <-ctx.Done():
			return nil
		}

		now := time.Now()

		jump := math.Abs(30 - float64(now.Unix()-lastTime.Unix()))
		if jump > 60 {
			// It looks like time jumped. This could be either:
			// * suspending
			// * or time changed (ntp fixed the time ?)
			// Trigger a UpdateInfo to check time_drift
			if a.bleemeoConnector != nil {
				a.bleemeoConnector.UpdateInfo()
			}
		}

		lastTime = now

		a.triggerLock.Lock()
		if !a.triggerDiscAt.IsZero() && time.Now().After(a.triggerDiscAt) {
			a.triggerDiscAt = time.Time{}
			a.triggerDiscImmediate = true
			a.triggerHandler.Trigger()
		}
		a.triggerLock.Unlock()
	}
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

func (a *agent) watchdog(ctx context.Context) error {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	failing := false

	for {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil
		}

		a.l.Lock()

		lastHealCheck := a.lastHealCheck

		a.l.Unlock()

		switch {
		case time.Since(lastHealCheck) > 15*time.Minute && !failing:
			logger.V(2).Printf("Healcheck are no longer running. Last run was at %s", lastHealCheck.Format(time.RFC3339))

			failing = true
		case time.Since(lastHealCheck) > 15*time.Minute && failing:
			logger.Printf("Healcheck are no longer running. Last run was at %s", lastHealCheck.Format(time.RFC3339))
			// We don't know how big the buffer needs to be to collect
			// all the goroutines. Use 2MB buffer which hopefully is enough
			buffer := make([]byte, 1<<21)

			runtime.Stack(buffer, true)
			logger.Printf("%s", string(buffer))
			logger.Printf("Glouton seems unhealthy, killing myself")
			panic("Glouton seems unhealthy, killing myself")
		default:
			failing = false
		}
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

		mandatoryTasks := []string{"Bleemeo SAAS connector", "Metric collector", "Metric store"}
		for _, name := range mandatoryTasks {
			if crashed, err := a.doesTaskCrashed(ctx, name); crashed && err != nil {
				logger.Printf("Task %#v crashed: %v", name, err)
				logger.Printf("Stopping the agent as task %#v is critical", name)
				a.cancel()
			}
		}

		if a.bleemeoConnector != nil {
			a.bleemeoConnector.HealthCheck()
		}

		if a.influxdbConnector != nil {
			a.influxdbConnector.HealthCheck()
		}

		a.l.Lock()
		a.lastHealCheck = time.Now()
		a.l.Unlock()
	}
}

// Return true if the given task exited before ctx was terminated
// Also return the error the tasks returned.
func (a *agent) doesTaskCrashed(ctx context.Context, name string) (bool, error) {
	a.l.Lock()
	defer a.l.Unlock()

	if id, ok := a.taskIDs[name]; ok {
		running, err := a.taskRegistry.IsRunning(id)
		if !running {
			// Re-check ctx to avoid race condition, it crashed only if we are still running
			return ctx.Err() == nil, err
		}
	}

	return false, nil
}

func (a *agent) hourlyDiscovery(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return nil
	case <-time.After(15 * time.Second):
	}

	a.FireTrigger(false, false, true, false)

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			a.FireTrigger(true, false, true, false)
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
			a.FireTrigger(false, true, false, false)
		}
	}
}

func (a *agent) dockerWatcher(ctx context.Context) error {
	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()
		a.dockerWatcherContainerHealth(ctx)
	}()

	defer wg.Wait()

	pendingTimer := time.NewTimer(0 * time.Second)
	// drain (expire) the timer, so the invariant "pendingTimer is expired when pendingDiscovery == false" hold.
	<-pendingTimer.C

	pendingDiscovery := false
	pendingSecondDiscovery := false

	for {
		select {
		case ev := <-a.containerRuntime.Events():
			a.l.Lock()
			a.lastContainerEventTime = time.Now()
			a.l.Unlock()

			if ev.Type == facts.EventTypeStart {
				pendingSecondDiscovery = true
			}

			if !pendingDiscovery && (ev.Type == facts.EventTypeStart || ev.Type == facts.EventTypeStop || ev.Type == facts.EventTypeDelete) {
				pendingDiscovery = true

				pendingTimer.Reset(5 * time.Second)
			}

			if ev.Type == facts.EventTypeHealth && ev.Container != nil {
				if a.bleemeoConnector != nil {
					a.bleemeoConnector.UpdateContainers()
				}

				a.sendDockerContainerHealth(ev.Container)
			}
		case <-pendingTimer.C:
			if pendingDiscovery {
				a.FireTrigger(pendingDiscovery, false, false, pendingSecondDiscovery)
				pendingDiscovery = false
				pendingSecondDiscovery = false
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func (a *agent) dockerWatcherContainerHealth(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// It not needed to have fresh container information. When health event occur,
			// DockerFact already update the container information
			containers, err := a.containerRuntime.Containers(ctx, 3600*time.Second, false)
			if err != nil {
				continue
			}

			for _, c := range containers {
				a.sendDockerContainerHealth(c)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (a *agent) sendDockerContainerHealth(container facts.Container) {
	health, message := container.Health()
	if health == facts.ContainerNoHealthCheck {
		return
	}

	status := types.StatusDescription{}

	switch {
	case !container.State().IsRunning():
		status.CurrentStatus = types.StatusCritical
		status.StatusDescription = "Container stopped"
	case health == facts.ContainerHealthy:
		status.CurrentStatus = types.StatusOk
		status.StatusDescription = message
	case health == facts.ContainerStarting:
		startedAt := container.StartedAt()
		if time.Since(startedAt) < time.Minute || startedAt.IsZero() {
			status.CurrentStatus = types.StatusOk
		} else {
			status.CurrentStatus = types.StatusWarning
			status.StatusDescription = "Container is still starting"
		}
	case health == facts.ContainerUnhealthy:
		status.CurrentStatus = types.StatusCritical
		status.StatusDescription = message
	default:
		status.CurrentStatus = types.StatusUnknown
		status.StatusDescription = fmt.Sprintf("Unknown health status %s", message)
	}

	a.gathererRegistry.WithTTL(5 * time.Minute).PushPoints([]types.MetricPoint{
		{
			Labels: map[string]string{
				types.LabelName:              "container_health_status",
				types.LabelMetaContainerName: container.ContainerName(),
			},
			Annotations: types.MetricAnnotations{
				Status:      status,
				ContainerID: container.ID(),
				BleemeoItem: container.ContainerName(),
			},
			Point: types.Point{
				Time:  time.Now(),
				Value: float64(status.CurrentStatus.NagiosCode()),
			},
		},
	})
}

func (a *agent) netstatWatcher(ctx context.Context) error {
	filePath := a.config.String("agent.netstat_file")
	stat, _ := os.Stat(filePath)

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		newStat, _ := os.Stat(filePath)
		if newStat != nil && (stat == nil || !newStat.ModTime().Equal(stat.ModTime())) {
			a.FireTrigger(true, false, false, false)
		}

		stat = newStat
	}
}

func (a *agent) FireTrigger(discovery bool, sendFacts bool, systemUpdateMetric bool, secondDiscovery bool) {
	a.triggerLock.Lock()
	defer a.triggerLock.Unlock()

	if discovery {
		a.triggerDiscImmediate = true
	}

	if sendFacts {
		a.triggerFact = true
	}

	if systemUpdateMetric {
		a.triggerSystemUpdateMetric = true
	}

	// Some discovery request ask for a second discovery in 1 minutes.
	// The second discovery allow to discovery service that are slow to start
	if secondDiscovery {
		deadline := time.Now().Add(time.Minute)
		a.triggerDiscAt = deadline
	}

	a.triggerHandler.Trigger()
}

func (a *agent) cleanTrigger() (discovery bool, sendFacts bool, systemUpdateMetric bool) {
	a.triggerLock.Lock()
	defer a.triggerLock.Unlock()

	discovery = a.triggerDiscImmediate
	sendFacts = a.triggerFact
	systemUpdateMetric = a.triggerSystemUpdateMetric
	a.triggerSystemUpdateMetric = false
	a.triggerDiscImmediate = false
	a.triggerFact = false

	return
}

//nolint: gocyclo
func (a *agent) handleTrigger(ctx context.Context) {
	runDiscovery, runFact, runSystemUpdateMetric := a.cleanTrigger()
	if runDiscovery {
		// force update of containers. This is important so that discovery correctly
		// associate service with container or remove service when container is stopped.
		_, err := a.containerRuntime.Containers(ctx, 0, false)
		if err != nil {
			logger.V(1).Printf("error while updating containers: %v", err)
		}

		services, err := a.discovery.Discovery(ctx, 0)
		if err != nil {
			logger.V(1).Printf("error during discovery: %v", err)
		} else {
			if a.jmx != nil {
				a.l.Lock()
				resolution := a.metricResolution
				a.l.Unlock()

				if err := a.jmx.UpdateConfig(services, resolution); err != nil {
					logger.V(1).Printf("failed to update JMX configuration: %v", err)
				}
			}
			if a.dynamicScrapper != nil {
				if containers, err := a.containerRuntime.Containers(ctx, time.Hour, false); err == nil {
					a.dynamicScrapper.Update(containers)
				}
			}
		}

		hasConnection := a.dockerRuntime.IsRuntimeRunning(ctx)
		if hasConnection && !a.dockerInputPresent && a.config.Bool("telegraf.docker_metrics_enabled") {
			i, err := docker.New(a.dockerRuntime.ServerAddress(), a.dockerRuntime)
			if err != nil {
				logger.V(1).Printf("error when creating Docker input: %v", err)
			} else {
				logger.V(2).Printf("Enable Docker metrics")
				a.dockerInputID, _ = a.collector.AddInput(i, "docker")
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

	if runSystemUpdateMetric {
		systemUpdateMetric(ctx, a)
	}
}

func systemUpdateMetric(ctx context.Context, a *agent) {
	pendingUpdate, pendingSecurityUpdate := facts.PendingSystemUpdate(
		ctx,
		a.config.String("container.type") != "",
		a.hostRootPath,
	)

	points := make([]types.MetricPoint, 0)

	if pendingUpdate >= 0 {
		points = append(points, types.MetricPoint{
			Labels: map[string]string{
				types.LabelName: "system_pending_updates",
			},
			Point: types.Point{
				Time:  time.Now(),
				Value: float64(pendingUpdate),
			},
		})
	}

	if pendingSecurityUpdate >= 0 {
		points = append(points, types.MetricPoint{
			Labels: map[string]string{
				types.LabelName: "system_pending_security_updates",
			},
			Point: types.Point{
				Time:  time.Now(),
				Value: float64(pendingSecurityUpdate),
			},
		})
	}

	a.threshold.WithPusher(a.gathererRegistry.WithTTL(time.Hour)).PushPoints(points)
}

func (a *agent) deletedContainersCallback(containersID []string) {
	metrics, err := a.store.Metrics(nil)
	if err != nil {
		logger.V(1).Printf("Unable to list metrics to cleanup after container deletion: %v", err)
		return
	}

	var metricToDelete []map[string]string

	for _, m := range metrics {
		annotations := m.Annotations()
		for _, c := range containersID {
			if annotations.ContainerID == c {
				metricToDelete = append(metricToDelete, m.Labels())
			}
		}
	}

	if len(metricToDelete) > 0 {
		a.store.DropMetrics(metricToDelete)
	}
}

// migrateState update older state to latest version.
func (a *agent) migrateState() {
	// This "secret" was only present in Bleemeo agent and not really used.
	_ = a.state.Delete("web_secret_key")
}

// DiagnosticPage return useful information to troubleshoot issue.
func (a *agent) DiagnosticPage() string {
	builder := &strings.Builder{}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fmt.Fprintf(
		builder,
		"Run diagnostic at %s with Glouton version %s (commit %s built using Go %s)\n",
		time.Now().Format(time.RFC3339),
		version.Version,
		version.BuildHash,
		runtime.Version(),
	)

	if a.config.Bool("bleemeo.enabled") {
		fmt.Fprintln(builder, "Glouton has Bleemeo connection enabled")

		if a.bleemeoConnector == nil {
			fmt.Fprintln(builder, "Unexpected error: Bleemeo is enabled by Bleemeo connector is not created")
		} else {
			builder.WriteString(a.bleemeoConnector.DiagnosticPage())
		}
	} else {
		fmt.Fprintln(builder, "Glouton has Bleemeo connection DISABLED")
	}

	allMetrics, err := a.store.Metrics(nil)
	if err != nil {
		fmt.Fprintf(builder, "Unable to query internal metrics store: %v\n", err)
	} else {
		fmt.Fprintf(builder, "Glouton measure %d metrics\n", len(allMetrics))
	}

	fmt.Fprintf(builder, "Glouton was build for %s %s\n", runtime.GOOS, runtime.GOARCH)

	facts, err := a.factProvider.Facts(ctx, time.Hour)
	if err != nil {
		fmt.Fprintf(builder, "Unable to gather facts: %v\n", err)
	} else {
		lines := make([]string, 0, len(facts))

		for k, v := range facts {
			lines = append(lines, " * "+k+" = "+v)
		}

		sort.Strings(lines)

		fmt.Fprintln(builder, "Facts:")
		for _, l := range lines {
			fmt.Fprintln(builder, l)
		}
	}

	return builder.String()
}

func (a *agent) DiagnosticZip(w io.Writer) error {
	zipFile := zip.NewWriter(w)
	defer zipFile.Close()

	file, err := zipFile.Create("diagnostic.txt")
	if err != nil {
		return err
	}

	_, err = file.Write([]byte(a.DiagnosticPage()))
	if err != nil {
		return err
	}

	file, err = zipFile.Create("goroutines.txt")
	if err != nil {
		return err
	}

	// We don't know how big the buffer needs to be to collect
	// all the goroutines. Use 2MB buffer which hopefully is enough
	buffer := make([]byte, 1<<21)

	n := runtime.Stack(buffer, true)
	buffer = buffer[:n]

	_, err = file.Write(buffer)
	if err != nil {
		return err
	}

	file, err = zipFile.Create("log.txt")
	if err != nil {
		return err
	}

	_, err = file.Write(logger.Buffer())
	if err != nil {
		return err
	}

	if a.bleemeoConnector != nil {
		err = a.bleemeoConnector.DiagnosticZip(zipFile)
		if err != nil {
			return err
		}
	}

	err = a.discovery.DiagnosticZip(zipFile)
	if err != nil {
		return err
	}

	file, err = zipFile.Create("containers.txt")
	if err != nil {
		return err
	}

	containers, err := a.containerRuntime.Containers(context.Background(), time.Hour, true)
	if err != nil {
		fmt.Fprintf(file, "can't list containers: %v", err)
	} else {
		sort.Slice(containers, func(i, j int) bool {
			return containers[i].ContainerName() < containers[j].ContainerName()
		})

		a.l.Lock()
		lastEvent := a.lastContainerEventTime
		a.l.Unlock()

		fmt.Fprintf(file, "# Containers (count=%d, last update=%s, last event=%s)\n", len(containers), a.containerRuntime.LastUpdate().Format(time.RFC3339), lastEvent.Format(time.RFC3339))

		for _, c := range containers {
			addr, _ := c.ListenAddresses()
			fmt.Fprintf(file, "Name=%s, ID=%s, ignored=%v, IP=%s, listenAddr=%v\n", c.ContainerName(), c.ID(), facts.ContainerIgnored(c), c.PrimaryAddress(), addr)
		}
	}

	err = yamlZip(zipFile, a)

	return err
}

func yamlZip(zipFile *zip.Writer, a *agent) error {
	file, err := zipFile.Create("config.yaml")

	if err != nil {
		return err
	}

	enc := yaml.NewEncoder(file)

	fmt.Fprintln(file, "# This file contains in-memory configuration used by Glouton. Value from from default, files and environement.")
	enc.SetIndent(4)

	err = enc.Encode(a.config.Dump())
	if err != nil {
		fmt.Fprintf(file, "# error: %v\n", err)
	}

	err = enc.Close()
	if err != nil {
		fmt.Fprintf(file, "# error: %v\n", err)
	}

	return nil
}

func parseIPOutput(content []byte) string {
	lines := strings.Split(string(content), "\n")
	if len(lines) == 0 {
		return ""
	}

	ipRoute := lines[0]
	lines = lines[1:]

	ipAddress := ""
	macAddress := ""

	splitOutput := strings.Split(ipRoute, " ")
	for i, s := range splitOutput {
		if s == "src" && len(splitOutput) > i+1 {
			ipAddress = splitOutput[i+1]
		}
	}

	reNewInterface := regexp.MustCompile(`^\d+: .*$`)
	reEtherAddress := regexp.MustCompile(`^\s+link/ether ([0-9a-fA-F]{2}(:[0-9a-fA-F]{2}){5}) .*`)
	reInetAddress := regexp.MustCompile(`\s+inet (\d+(\.\d+){3})/\d+ .*`)
	currentMacAddress := ""

	for _, line := range lines {
		if reNewInterface.MatchString(line) {
			currentMacAddress = ""
		}

		match := reInetAddress.FindStringSubmatch(line)
		if len(match) > 0 && match[1] == ipAddress {
			macAddress = currentMacAddress

			break
		}

		match = reEtherAddress.FindStringSubmatch(line)
		if len(match) > 0 {
			currentMacAddress = match[1]
		}
	}

	return macAddress
}

// setupContainer will tune container to improve information gathered.
// Mostly it make that access to file pass though hostroot.
func setupContainer(hostRootPath string) {
	if hostRootPath == "" {
		logger.Printf("The agent is running in a container but GLOUTON_DF_HOST_MOUNT_POINT is unset. Some informations will be missing")
		return
	}

	if _, err := os.Stat(hostRootPath); os.IsNotExist(err) {
		logger.Printf("The agent is running in a container but host / partition is not mounted on %#v. Some informations will be missing", hostRootPath)
		logger.Printf("Hint: to fix this issue when using Docker, add \"-v /:%v:ro\" when running the agent", hostRootPath)

		return
	}

	if hostRootPath != "" && hostRootPath != "/" && os.Getenv("HOST_VAR") == "" {
		// gopsutil will use HOST_VAR as prefix to host /var
		// It's used at least for reading the number of connected user from /var/run/utmp
		os.Setenv("HOST_VAR", hostRootPath+"/var")

		// ... but /var/run is usually a symlink to /run.
		varRun := filepath.Join(hostRootPath, "var/run")
		target, err := os.Readlink(varRun)

		if err == nil && target == "/run" {
			os.Setenv("HOST_VAR", hostRootPath)
		}
	}
}

// prometheusConfigToURLs convert metric.prometheus.targets config to a map of target name to URL
//
// See tests for the expected config.
func prometheusConfigToURLs(cfg interface{}, globalAllow []string, globalDeny []string, globalIncludeDefault bool) (result []*scrapper.Target) {
	configList, ok := cfg.([]interface{})
	if !ok {
		return nil
	}

	for _, v := range configList {
		vMap, ok := v.(map[string]interface{})
		if !ok {
			continue
		}

		uText, ok := vMap["url"].(string)
		if !ok {
			continue
		}

		u, err := url.Parse(uText)
		if err != nil {
			logger.Printf("ignoring invalid exporter config: %v", err)
			continue
		}

		name, _ := vMap["name"].(string)

		target := &scrapper.Target{
			ExtraLabels: map[string]string{
				types.LabelMetaScrapeJob:      name,
				types.LabelMetaScrapeInstance: scrapper.HostPort(u),
			},
			URL:            u,
			AllowList:      globalAllow,
			DenyList:       globalDeny,
			IncludeDefault: globalIncludeDefault,
		}

		if allow, ok := vMap["allow_metrics"].([]interface{}); ok {
			target.AllowList = make([]string, 0, len(allow))

			for _, x := range allow {
				s, _ := x.(string)
				if s != "" {
					target.AllowList = append(target.AllowList, x.(string))
				}
			}
		}

		denyMetricsConfig(vMap, target)

		switch value := vMap["include_default_metrics"].(type) {
		case bool:
			target.IncludeDefault = value
		case int:
			target.IncludeDefault = (value != 0)
		case string:
			v, err := config.ConvertBoolean(value)
			if err != nil {
				logger.Printf("ignoring invalid boolean \"%s\" for include_default on target %s: %v", value, uText, err)
			} else {
				target.IncludeDefault = v
			}
		default:
			if value != nil {
				logger.Printf("ignoring invalid boolean \"%v\" for include_default on target %s: unknown type", value, uText)
			}
		}

		result = append(result, target)
	}

	return result
}

func denyMetricsConfig(vMap map[string]interface{}, target *scrapper.Target) {
	if deny, ok := vMap["deny_metrics"].([]interface{}); ok {
		target.DenyList = make([]string, 0, len(deny))

		for _, x := range deny {
			s, _ := x.(string)
			if s != "" {
				target.DenyList = append(target.DenyList, x.(string))
			}
		}
	}
}
