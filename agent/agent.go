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
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"glouton/agent/state"
	"glouton/api"
	"glouton/bleemeo"
	"glouton/collector"
	"glouton/config"
	"glouton/config2"
	"glouton/debouncer"
	"glouton/delay"
	"glouton/discovery"
	"glouton/discovery/promexporter"
	"glouton/facts"
	"glouton/facts/container-runtime/containerd"
	"glouton/facts/container-runtime/kubernetes"
	"glouton/facts/container-runtime/merge"
	"glouton/facts/container-runtime/veth"
	"glouton/influxdb"
	"glouton/inputs"
	"glouton/inputs/docker"
	"glouton/inputs/statsd"
	"glouton/jmxtrans"
	"glouton/logger"
	"glouton/nrpe"
	"glouton/prometheus/exporter/blackbox"
	"glouton/prometheus/exporter/common"
	"glouton/prometheus/exporter/snmp"
	"glouton/prometheus/process"
	"glouton/prometheus/registry"
	"glouton/prometheus/rules"
	"glouton/prometheus/scrapper"
	"glouton/store"
	"glouton/task"
	"glouton/telemetry"
	"glouton/threshold"
	"glouton/types"
	"glouton/version"
	"glouton/zabbix"
	"io"
	"log"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"

	bleemeoTypes "glouton/bleemeo/types"

	dockerRuntime "glouton/facts/container-runtime/docker"

	crTypes "glouton/facts/container-runtime/types"

	processInput "glouton/inputs/process"

	"github.com/prometheus/prometheus/model/labels"
	"gopkg.in/yaml.v3"
)

// Jitter define the aligned timestamp used for scrapping.
// System collector use 0 (baseJitter here and in registry.go).
// baseJitterPlus is a little after, useful for collector that need to re-read point of system collector.
const (
	baseJitter      = 0
	baseJitterPlus  = 500000
	defaultInterval = 0
)

var errUnsupportedKey = errors.New("Unsupported item key") //nolint:stylecheck

type agent struct {
	taskRegistry *task.Registry
	oldConfig    *config.Configuration
	config       config2.Config
	state        *state.State
	cancel       context.CancelFunc
	context      context.Context //nolint:containedctx

	hostRootPath           string
	discovery              *discovery.Discovery
	dockerRuntime          *dockerRuntime.Docker
	containerFilter        facts.ContainerFilter
	containerdRuntime      *containerd.Containerd
	containerRuntime       crTypes.RuntimeInterface
	collector              *collector.Collector
	factProvider           *facts.FactProvider
	bleemeoConnector       *bleemeo.Connector
	influxdbConnector      *influxdb.Client
	threshold              *threshold.Registry
	jmx                    *jmxtrans.JMX
	snmpManager            *snmp.Manager
	snmpRegistration       []int
	store                  *store.Store
	gathererRegistry       *registry.Registry
	metricFormat           types.MetricFormat
	dynamicScrapper        *promexporter.DynamicScrapper
	lastHealthCheck        time.Time
	lastContainerEventTime time.Time
	watchdogRunAt          []time.Time
	metricFilter           *metricFilter
	monitorManager         *blackbox.RegisterManager
	rulesManager           *rules.Manager
	reloadState            ReloadState
	vethProvider           *veth.Provider

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
	configWarnings   []error
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

func (a *agent) init(ctx context.Context, configFiles []string, firstRun bool) (ok bool) {
	a.l.Lock()
	a.lastHealthCheck = time.Now()
	a.l.Unlock()

	a.taskRegistry = task.NewRegistry(ctx)
	a.taskIDs = make(map[string]int)

	cfg, oldCfg, warnings, err := loadConfiguration(configFiles, nil)

	if warnings != nil {
		a.addWarnings(warnings...)
	}

	a.oldConfig = oldCfg
	a.config = cfg

	a.setupLogger()

	if err != nil {
		logger.Printf("Error while loading configuration: %v", err)

		return false
	}

	watcherErr := a.reloadState.WatcherError()
	if watcherErr != nil && !errors.Is(watcherErr, errWatcherDisabled) {
		logger.Printf("An error occurred with the file watcher: %v.", watcherErr)
		logger.Printf("The agent might not be able to reload automatically on config change.")
	}

	// Initialize sentry only on the first run so it doesn't leak goroutines on reload.
	if firstRun && a.config.Bleemeo.Sentry.DSN != "" {
		err := sentry.Init(sentry.ClientOptions{
			Dsn:              a.config.Bleemeo.Sentry.DSN,
			AttachStacktrace: true,
		})
		if err != nil {
			logger.V(1).Printf("sentry.Init failed: %s", err)
		}
	}

	sentry.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetContext("agent", map[string]interface{}{
			"glouton_version": version.Version,
		})
	})

	a.containerFilter = facts.ContainerFilter{
		DisabledByDefault: !a.config.Container.Filter.AllowByDefault,
		AllowList:         a.config.Container.Filter.AllowList,
		DenyList:          a.config.Container.Filter.DenyList,
	}

	statePath := a.config.Agent.StateFile
	cachePath := a.config.Agent.StateCacheFile
	oldStatePath := a.config.Agent.DeprecatedStateFile

	if cachePath == "" {
		cachePath = state.DefaultCachePath(statePath)
	}

	a.state, err = state.Load(statePath, cachePath)
	if err != nil {
		logger.Printf("Error while loading state file: %v", err)

		return false
	}

	if !a.state.IsEmpty() {
		oldStatePath = ""
	}

	if oldStatePath != "" {
		oldState, err := state.Load(oldStatePath, state.DefaultCachePath(statePath))
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

	resetStateFile := a.config.Agent.StateResetFile

	if _, err := os.Stat(resetStateFile); err == nil {
		// No error means that file exists.
		if err := os.Remove(resetStateFile); err != nil {
			logger.Printf("Unable to remote state reset marked: %v", err)
			logger.Printf("Skipping state reset due to previous error")
		} else {
			a.state.KeepOnlyPersistent()
		}
	}

	a.migrateState()

	if err := a.state.SaveTo(statePath, cachePath); err != nil {
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

			err = a.state.SaveTo(oldStatePath, state.DefaultCachePath(oldStatePath))
		}

		if err != nil {
			logger.Printf("State file is not writable, stopping agent: %v", err)

			return false
		}
	} else if oldStatePath != "" {
		logger.Printf("The deprecated state file (%s) is migrated to new path (%s).", oldStatePath, statePath)
	}

	a.readXMLCredentials()

	return true
}

// readXMLCredentials reads the bleemeo account ID and registration key
// from a XML file generated by the Windows Installer.
func (a *agent) readXMLCredentials() {
	if runtime.GOOS != "windows" {
		return
	}

	// Don't override credentials if the user already set them in the config.
	if a.config.Bleemeo.AccountID != "" || a.config.Bleemeo.RegistrationKey != "" {
		return
	}

	type Credentials struct {
		XMLName         xml.Name `xml:"credentials"`
		AccountID       string   `xml:"account_id,attr"`
		RegistrationKey string   `xml:"registration_key,attr"`
	}

	xmlFile, err := os.Open(`C:\ProgramData\glouton\conf.d\credentials.xml`)
	if err != nil {
		logger.V(2).Printf("Failed to read credentials : %v", err)

		return
	}

	defer xmlFile.Close()

	xmlBytes, _ := io.ReadAll(xmlFile)

	var credentials Credentials

	if err := xml.Unmarshal(xmlBytes, &credentials); err != nil {
		logger.V(2).Printf("Failed to read credentials : %v", err)

		return
	}

	a.config.Bleemeo.AccountID = credentials.AccountID
	a.config.Bleemeo.RegistrationKey = credentials.RegistrationKey
}

func (a *agent) setupLogger() {
	logger.SetBufferCapacity(
		a.config.Logging.Buffer.HeadSizeBytes,
		a.config.Logging.Buffer.TailSizeBytes,
	)

	var err error

	switch a.config.Logging.Output {
	case "syslog":
		err = logger.UseSyslog()
	case "file":
		err = logger.UseFile(a.config.Logging.FileName)
	}

	if err != nil {
		fmt.Printf("Unable to use logging backend '%s': %v\n", a.config.Logging.Output, err) //nolint:forbidigo
	}

	switch strings.ToLower(a.config.Logging.Level) {
	case "0", "info", "warning", "error":
		logger.SetLevel(0)
	case "verbose":
		logger.SetLevel(1)
	case "debug":
		logger.SetLevel(2)
	default:
		logger.SetLevel(0)
		a.addWarnings(fmt.Errorf(`%w: unknown logging.level "%s". Using "INFO".`, config2.ErrInvalidValue, a.config.Logging.Level))
	}

	logger.SetPkgLevels(a.config.Logging.PackageLevels)
}

// Run runs Glouton.
func Run(ctx context.Context, reloadState ReloadState, configFiles []string, signalChan chan os.Signal, firstRun bool) {
	rand.Seed(time.Now().UnixNano())

	agent := &agent{reloadState: reloadState}

	if !agent.init(ctx, configFiles, firstRun) {
		os.Exit(1)

		return
	}

	agent.run(ctx, signalChan)
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

	for _, t := range a.config.Tags {
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
func (a *agent) UpdateThresholds(ctx context.Context, thresholds map[string]threshold.Threshold, firstUpdate bool) {
	a.updateThresholds(ctx, thresholds, firstUpdate)
}

// notifyBleemeoFirstRegistration is called when Glouton is registered with Bleemeo Cloud platform for the first time
// This means that when this function is called, BleemeoAgentID and BleemeoAccountID are set.
func (a *agent) notifyBleemeoFirstRegistration() {
	a.gathererRegistry.UpdateRelabelHook(a.bleemeoConnector.RelabelHook)
	a.store.DropAllMetrics()
}

// notifyBleemeoUpdateLabels is called when Labels might change for some metrics.
// This likely happen when SNMP target are deleted/recreated.
func (a *agent) notifyBleemeoUpdateLabels() {
	a.gathererRegistry.UpdateRelabelHook(a.bleemeoConnector.RelabelHook)
}

func (a *agent) updateSNMPResolution(resolution time.Duration) {
	a.l.Lock()
	defer a.l.Unlock()

	for _, id := range a.snmpRegistration {
		a.gathererRegistry.Unregister(id)
	}

	if a.snmpRegistration != nil {
		a.snmpRegistration = a.snmpRegistration[:0]
	}

	if resolution == 0 {
		return
	}

	for _, target := range a.snmpManager.Gatherers() {
		hash := labels.FromMap(target.ExtraLabels).Hash()

		id, err := a.gathererRegistry.RegisterGatherer(
			registry.RegistrationOption{
				Description: "snmp target " + target.Address,
				JitterSeed:  hash,
				Interval:    resolution,
				Timeout:     40 * time.Second,
				ExtraLabels: target.ExtraLabels,
				Rules:       registry.DefaultSNMPRules(resolution),
			},
			target.Gatherer,
		)
		if err != nil {
			logger.Printf("Unable to add SNMP scrapper for target %s: %v", target.Address, err)
		} else {
			a.snmpRegistration = append(a.snmpRegistration, id)
		}
	}
}

func (a *agent) updateMetricResolution(ctx context.Context, defaultResolution time.Duration, snmpResolution time.Duration) {
	a.l.Lock()
	a.metricResolution = defaultResolution
	a.l.Unlock()

	a.gathererRegistry.UpdateDelay(defaultResolution)

	services, err := a.discovery.Discovery(ctx, time.Hour)
	if err != nil {
		logger.V(1).Printf("error during discovery: %v", err)
	} else if a.jmx != nil {
		if err := a.jmx.UpdateConfig(services, defaultResolution); err != nil {
			logger.V(1).Printf("failed to update JMX configuration: %v", err)
		}
	}

	a.updateSNMPResolution(snmpResolution)
}

func (a *agent) getConfigThreshold(firstUpdate bool) map[string]threshold.Threshold {
	configThresholds := make(map[string]threshold.Threshold, len(a.config.Thresholds))
	defaultSoftPeriod := time.Duration(a.config.Metric.SoftStatusPeriodDefault) * time.Second

	softPeriods := make(map[string]time.Duration, len(a.config.Metric.SoftStatusPeriod))
	for metric, period := range a.config.Metric.SoftStatusPeriod {
		softPeriods[metric] = time.Duration(period) * time.Second
	}

	for metric, configThreshold := range a.config.Thresholds {
		configThresholds[metric] = threshold.FromConfig(configThreshold, metric, softPeriods, defaultSoftPeriod)
	}

	return configThresholds
}

func (a *agent) newMetricsCallback(newMetrics []types.LabelsAndAnnotation) {
	for _, m := range newMetrics {
		isAllowed := a.metricFilter.IsAllowed(m.Labels)
		isDenied := a.metricFilter.IsDenied(m.Labels)
		isBleemeoAllowed := true

		if a.bleemeoConnector != nil {
			isBleemeoAllowed, _, _ = a.bleemeoConnector.IsMetricAllowed(m)
		}

		name := types.LabelsToText(m.Labels)

		switch {
		case !isAllowed:
			logger.V(2).Printf("The metric %s is not in configured allow list", name)
		case isDenied && !isBleemeoAllowed:
			logger.V(1).Printf("The metric %s is blocked by configured deny list and is not available in current Bleemeo Plan", name)
		case isDenied:
			logger.V(1).Printf("The metric %s is blocked by configured deny list", name)
		case !isBleemeoAllowed:
			logger.V(1).Printf("The metric %s is not available in current Bleemeo Plan", name)
		}
	}
}

func (a *agent) updateThresholds(ctx context.Context, thresholds map[string]threshold.Threshold, firstUpdate bool) {
	configThreshold := a.getConfigThreshold(firstUpdate)

	oldThresholds := map[string]threshold.Threshold{}

	for _, name := range []string{"system_pending_updates", "system_pending_security_updates", "time_drift"} {
		lbls := map[string]string{
			types.LabelName:         name,
			types.LabelInstanceUUID: a.BleemeoAgentID(),
		}
		oldThresholds[name] = a.threshold.GetThreshold(types.LabelsToText(lbls))
	}

	a.threshold.SetThresholds(thresholds, configThreshold)

	services, err := a.discovery.Discovery(ctx, 1*time.Hour)

	if err != nil {
		logger.V(2).Printf("An error occurred while running discoveries for updateThresholds: %v", err)
	} else {
		err = a.metricFilter.RebuildDynamicLists(a.dynamicScrapper, services, a.threshold.GetThresholdMetricNames(), a.rulesManager.MetricNames())
		if err != nil {
			logger.V(2).Printf("An error occurred while rebuilding dynamic list for updateThresholds: %v", err)
		}
	}

	for _, name := range []string{"system_pending_updates", "system_pending_security_updates", "time_drift"} {
		lbls := map[string]string{
			types.LabelName:         name,
			types.LabelInstanceUUID: a.BleemeoAgentID(),
		}
		newThreshold := a.threshold.GetThreshold(types.LabelsToText(lbls))

		if !firstUpdate && !oldThresholds[name].Equal(newThreshold) {
			if name == "time_drift" && a.bleemeoConnector != nil {
				a.bleemeoConnector.UpdateInfo()
			} else {
				a.FireTrigger(false, false, true, false)
			}
		}
	}
}

// Run will start the agent. It will terminate when sigquit/sigterm/sigint is received.
func (a *agent) run(ctx context.Context, sighupChan chan os.Signal) { //nolint:maintidx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	a.cancel = cancel
	a.metricResolution = 10 * time.Second
	a.hostRootPath = "/"
	a.context = ctx

	if a.config.Container.Type != "" {
		a.hostRootPath = a.config.DF.HostMountPoint
		setupContainer(a.hostRootPath)
	}

	a.triggerHandler = debouncer.New(
		ctx,
		a.handleTrigger,
		5*time.Second,
		10*time.Second,
	)

	go func() {
		defer types.ProcessPanic()

		a.handleSighup(ctx, sighupChan)
	}()

	a.factProvider = facts.NewFacter(
		a.config.Agent.FactsFile,
		a.hostRootPath,
		a.config.Agent.PublicIPIndicator,
	)

	factsMap, err := a.factProvider.FastFacts(ctx)
	if err != nil {
		logger.Printf("Warning: get facts failed, some information (e.g. name of this server) may be wrong. %v", err)
	}

	fqdn := factsMap["fqdn"]
	if fqdn == "" {
		fqdn = "localhost"
	}

	content, err := os.ReadFile(a.config.Agent.CloudImageCreationFile)
	if err != nil && !os.IsNotExist(err) {
		warning := fmt.Errorf(
			"%w: unable to read agent.cloudimage_creation_file %s: %v",
			config2.ErrInvalidValue, a.config.Agent.CloudImageCreationFile, err,
		)
		a.addWarnings(warning)
	}

	if err == nil || !os.IsNotExist(err) {
		initialMac := parseIPOutput(content)
		currentMac := factsMap["primary_mac_address"]

		if currentMac == initialMac || currentMac == "" || initialMac == "" {
			logger.Printf("Not starting Glouton since installation for creation of a cloud image was requested and agent is still running on the same machine")
			logger.Printf("If this is wrong and agent should run on this machine, remove %s file", a.config.Agent.CloudImageCreationFile)

			return
		}
	}

	_ = os.Remove(a.config.Agent.CloudImageCreationFile)

	logger.Printf("Starting agent version %v (commit %v)", version.Version, version.BuildHash)

	_ = os.Remove(a.config.Agent.UpgradeFile)
	_ = os.Remove(a.config.Agent.AutoUpgradeFile)

	a.metricFormat = types.StringToMetricFormat(a.config.Agent.MetricsFormat)
	if a.metricFormat == types.MetricFormatUnknown {
		logger.Printf("Invalid metric format %#v. Supported option are \"Bleemeo\" and \"Prometheus\". Falling back to Bleemeo", a.config.Agent.MetricsFormat)
		a.metricFormat = types.MetricFormatBleemeo
	}

	apiBindAddress := fmt.Sprintf("%s:%d", a.config.Web.Listener.Address, a.config.Web.Listener.Port)

	if a.config.Agent.HTTPDebug.Enable {
		go func() {
			defer types.ProcessPanic()

			debugAddress := a.config.Agent.HTTPDebug.BindAddress

			logger.Printf("Starting debug server on http://%s/debug/pprof/", debugAddress)
			log.Println(http.ListenAndServe(debugAddress, nil)) //nolint:gosec
		}()
	}

	var warnings config2.Warnings

	a.snmpManager, warnings = snmp.NewManager(
		a.config.Metric.SNMP.ExporterAddress,
		a.factProvider,
		a.config.Metric.SNMP.Targets,
	)

	if warnings != nil {
		a.addWarnings(warnings...)
	}

	hasSwap := factsMap["swap_present"] == "true"

	mFilter, err := newMetricFilter(a.config, len(a.snmpManager.Targets()) > 0, hasSwap, a.metricFormat)
	if err != nil {
		logger.Printf("An error occurred while building the metric filter, allow/deny list may be partial: %v", err)
	}

	a.metricFilter = mFilter

	if a.config.Web.LocalUI.Enable {
		a.store = store.New(time.Hour, 2*time.Hour)
	} else {
		a.store = store.New(2*time.Minute, 2*time.Hour)
	}

	filteredStore := store.NewFilteredStore(a.store, mFilter.FilterPoints, mFilter.filterMetrics)
	a.threshold = threshold.New(a.state)

	a.gathererRegistry, err = registry.New(
		registry.Option{
			PushPoint:             a.store,
			ThresholdHandler:      a.threshold,
			FQDN:                  fqdn,
			GloutonPort:           fmt.Sprint(a.config.Web.Listener.Port),
			MetricFormat:          a.metricFormat,
			BlackboxSentScraperID: a.config.Blackbox.ScraperSendUUID,
			Filter:                mFilter,
			Queryable:             a.store,
		})
	if err != nil {
		logger.Printf("Unable to create the metrics registry: %v", err)
		logger.Printf("The metrics registry is required for Glouton. Exiting.")

		return
	}

	a.store.SetNewMetricCallback(a.newMetricsCallback)

	a.dockerRuntime = dockerRuntime.New(
		a.config.Container.Runtime.Docker,
		a.hostRootPath,
		a.deletedContainersCallback,
		a.containerFilter.ContainerIgnored,
	)
	a.containerdRuntime = containerd.New(
		a.config.Container.Runtime.ContainerD,
		a.hostRootPath,
		a.deletedContainersCallback,
		a.containerFilter.ContainerIgnored,
	)
	a.containerRuntime = &merge.Runtime{
		Runtimes: []crTypes.RuntimeInterface{
			a.dockerRuntime,
			a.containerdRuntime,
		},
		ContainerIgnored: a.containerFilter.ContainerIgnored,
	}

	if a.config.Kubernetes.Enable {
		kube := &kubernetes.Kubernetes{
			Runtime:            a.containerRuntime,
			NodeName:           a.config.Kubernetes.NodeName,
			KubeConfig:         a.config.Kubernetes.KubeConfig,
			IsContainerIgnored: a.containerFilter.ContainerIgnored,
		}
		a.containerRuntime = kube

		var clusterNameState string

		clusterName := a.config.Kubernetes.ClusterName

		err = a.state.Get("kubernetes_cluster_name", &clusterNameState)
		if err != nil {
			logger.V(2).Printf("failed to get kubernetes_cluster_name: %v", err)
		}

		if clusterName == "" && clusterNameState != "" {
			logger.V(1).Printf("kubernetes.clustername is unset, using previous value of %s", clusterNameState)
			clusterName = clusterNameState
		}

		if clusterName != "" && clusterNameState != clusterName {
			err = a.state.Set("kubernetes_cluster_name", clusterNameState)
			if err != nil {
				logger.V(2).Printf("failed to set kubernetes_cluster_name: %v", err)
			}
		}

		if clusterName != "" {
			a.factProvider.SetFact("kubernetes_cluster_name", clusterName)
		} else {
			a.addWarnings(fmt.Errorf("kubernetes.clustername is missing, some feature are unavailable. See https://docs.bleemeo.com/agent/installation#installation-on-kubernetes"))
		}

		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := kube.Test(ctx); err != nil {
			logger.Printf("Kubernetes API unreachable, service detection may misbehave: %v", err)
		}

		cancel()
	}

	var psLister facts.ProcessLister

	useProc := a.config.Container.Type == "" || a.config.Container.PIDNamespaceHost
	if !useProc {
		logger.V(1).Printf("The agent is running in a container and \"container.pid_namespace_host\", is not true. Not all processes will be seen")
	} else {
		if !version.IsLinux() {
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
	netstat := &facts.NetstatProvider{FilePath: a.config.Agent.NetstatFile}

	a.factProvider.AddCallback(a.containerRuntime.RuntimeFact)
	a.factProvider.SetFact("installation_format", a.config.Agent.InstallationFormat)

	acc := &inputs.Accumulator{
		Pusher:  a.gathererRegistry.WithTTL(5 * time.Minute),
		Context: ctx,
	}
	a.collector = collector.New(acc)

	isCheckIgnored := discovery.NewIgnoredService(a.config.ServiceIgnoreCheck).IsServiceIgnored
	isInputIgnored := discovery.NewIgnoredService(a.config.ServiceIgnoreMetrics).IsServiceIgnored
	dynamicDiscovery := discovery.NewDynamic(psFact, netstat, a.containerRuntime, a.containerFilter.ContainerIgnored, discovery.SudoFileReader{HostRootPath: a.hostRootPath}, a.config.Stack)

	a.discovery, warnings = discovery.New(
		dynamicDiscovery,
		a.collector,
		a.gathererRegistry,
		a.taskRegistry,
		a.state,
		acc,
		a.containerRuntime,
		a.config.Services,
		isCheckIgnored,
		isInputIgnored,
		a.containerFilter.ContainerIgnored,
		a.metricFormat,
		psFact,
	)
	if warnings != nil {
		a.addWarnings(warnings...)
	}

	a.dynamicScrapper = &promexporter.DynamicScrapper{
		Registry:       a.gathererRegistry,
		DynamicJobName: "discovered-exporters",
	}

	if a.config.Blackbox.Enable {
		logger.V(1).Println("Starting blackbox_exporter...")

		a.monitorManager, err = blackbox.New(a.gathererRegistry, a.config.Blackbox, a.metricFormat)
		if err != nil {
			logger.V(0).Printf("Couldn't start blackbox_exporter: %v\nMonitors will not be able to run on this agent.", err)
		}
	} else {
		logger.V(1).Println("blackbox_exporter not enabled, will not start...")
	}

	promExporter := a.gathererRegistry.Exporter()

	api := &api.API{
		DB:                 api.NewQueryable(a.store, a.BleemeoAgentID),
		ContainerRuntime:   a.containerRuntime,
		PsFact:             psFact,
		FactProvider:       a.factProvider,
		BindAddress:        apiBindAddress,
		Discovery:          a.discovery,
		AgentInfo:          a,
		PrometheurExporter: promExporter,
		Threshold:          a.threshold,
		StaticCDNURL:       a.config.Web.StaticCDNURL,
		DiagnosticPage:     a.DiagnosticPage,
		DiagnosticArchive:  a.writeDiagnosticArchive,
		MetricFormat:       a.metricFormat,
		LocalUIDisabled:    !a.config.Web.LocalUI.Enable,
	}

	tasks := []taskInfo{
		{a.watchdog, "Agent Watchdog"},
		{a.store.Run, "Metric store"},
		{a.containerRuntime.Run, "Docker connector"},
		{a.healthCheck, "Agent healthcheck"},
		{a.hourlyDiscovery, "Service Discovery"},
		{a.dailyFact, "Facts gatherer"},
		{a.dockerWatcher, "Docker event watcher"},
		{a.netstatWatcher, "Netstat file watcher"},
		{a.miscTasks, "Miscelanous tasks"},
		{a.sendToTelemetry, "Send Facts information to our telemetry tool"},
		{a.threshold.Run, "Threshold state"},
	}

	if a.config.Web.Enable {
		tasks = append(tasks, taskInfo{api.Run, "Local Web UI"})
	}

	if a.config.JMX.Enable {
		perm, err := strconv.ParseInt(a.config.JMXTrans.FilePermission, 8, 0)
		if err != nil {
			a.addWarnings(fmt.Errorf(
				"%w: failed to parse jmxtrans.file_permission '%s': %s, using the default 0640",
				config2.ErrInvalidValue, a.config.JMXTrans.FilePermission, err,
			))

			perm = 0o640
		}

		a.jmx = &jmxtrans.JMX{
			OutputConfigurationFile:       a.config.JMXTrans.ConfigFile,
			OutputConfigurationPermission: os.FileMode(perm),
			ContactPort:                   a.config.JMXTrans.GraphitePort,
			Pusher:                        a.gathererRegistry.WithTTL(5 * time.Minute),
		}

		tasks = append(tasks, taskInfo{a.jmx.Run, "jmxtrans"})
	}

	a.rulesManager = rules.NewManager(ctx, a.store)

	if a.config.Bleemeo.Enable {
		scaperName := a.config.Blackbox.ScraperName
		if scaperName == "" {
			scaperName = fmt.Sprintf("%s:%d", fqdn, a.config.Web.Listener.Port)
		}

		connector, err := bleemeo.New(bleemeoTypes.GlobalOption{
			Config:                  a.oldConfig,
			State:                   a.state,
			Facts:                   a.factProvider,
			Process:                 psFact,
			Docker:                  a.containerRuntime,
			Store:                   filteredStore,
			SNMP:                    a.snmpManager.Targets(),
			SNMPOnlineTarget:        a.snmpManager.OnlineCount,
			PushPoints:              a.gathererRegistry.WithTTL(5 * time.Minute),
			Discovery:               a.discovery,
			MonitorManager:          a.monitorManager,
			UpdateMetricResolution:  a.updateMetricResolution,
			UpdateThresholds:        a.UpdateThresholds,
			UpdateUnits:             a.threshold.SetUnits,
			MetricFormat:            a.metricFormat,
			NotifyFirstRegistration: a.notifyBleemeoFirstRegistration,
			NotifyLabelsUpdate:      a.notifyBleemeoUpdateLabels,
			BlackboxScraperName:     scaperName,
			RebuildPromQLRules:      a.rulesManager.RebuildPromQLRules,
			ReloadState:             a.reloadState.Bleemeo(),
			IsContainerEnabled:      a.containerFilter.ContainerEnabled,
			IsMetricAllowed:         a.metricFilter.isAllowedAndNotDenied,
		})
		if err != nil {
			logger.Printf("unable to start Bleemeo SAAS connector: %v", err)

			return
		}

		a.l.Lock()
		a.bleemeoConnector = connector
		a.l.Unlock()

		a.gathererRegistry.UpdateRelabelHook(a.bleemeoConnector.RelabelHook)
		tasks = append(tasks, taskInfo{a.bleemeoConnector.Run, "Bleemeo SAAS connector"})

		_, err = a.gathererRegistry.RegisterPushPointsCallback(
			registry.RegistrationOption{
				Description: "Bleemeo connector",
				JitterSeed:  baseJitter,
				Interval:    defaultInterval,
			},
			a.bleemeoConnector.EmitInternalMetric,
		)
		if err != nil {
			logger.Printf("unable to add bleemeo connector metrics: %v", err)
		}
	}

	a.FireTrigger(true, true, false, false)

	// Only start gatherers after the relabel hook is set to avoid sending metrics without
	// instance uuid to the bleemeo connector.
	a.updateSNMPResolution(time.Minute)

	_, err = a.gathererRegistry.RegisterPushPointsCallback(
		registry.RegistrationOption{
			Description: "system & services metrics",
			JitterSeed:  baseJitter,
		},
		a.collector.RunGather,
	)
	if err != nil {
		logger.Printf("unable to add system metrics: %v", err)
	}

	if a.metricFormat == types.MetricFormatBleemeo {
		processInput := processInput.New(psFact, a.gathererRegistry.WithTTL(5*time.Minute))

		_, err = a.gathererRegistry.RegisterPushPointsCallback(
			registry.RegistrationOption{
				Description: "process status metrics",
				JitterSeed:  baseJitter,
			},
			processInput.Gather,
		)
		if err != nil {
			logger.Printf("unable to add processes metrics: %v", err)
		}
	}

	_, err = a.gathererRegistry.RegisterPushPointsCallback(
		registry.RegistrationOption{
			Description: "miscGather",
			JitterSeed:  baseJitter,
		},
		a.miscGather(a.gathererRegistry.WithTTL(5*time.Minute)),
	)
	if err != nil {
		logger.Printf("unable to add miscGathere metrics: %v", err)
	}

	_, err = a.gathererRegistry.RegisterPushPointsCallback(
		registry.RegistrationOption{
			Description: "miscGatherMinute",
			JitterSeed:  baseJitter,
			MinInterval: time.Minute,
		},
		a.miscGatherMinute(a.gathererRegistry.WithTTL(5*time.Minute)),
	)
	if err != nil {
		logger.Printf("unable to add miscGathere metrics: %v", err)
	}

	_, err = a.gathererRegistry.RegisterAppenderCallback(
		registry.RegistrationOption{
			Description:        "rulesManager",
			JitterSeed:         baseJitterPlus,
			NoLabelsAlteration: true,
		},
		registry.AppenderRegistrationOption{},
		a.rulesManager,
	)
	if err != nil {
		logger.Printf("unable to add recording rules metrics: %v", err)
	}

	if a.config.Agent.ProcessExporter.Enable {
		process.RegisterExporter(ctx, a.gathererRegistry, psLister, dynamicDiscovery, a.metricFormat == types.MetricFormatBleemeo)
	}

	prometheusTargets, warnings := prometheusConfigToURLs(a.config.Metric.Prometheus.Targets)
	if warnings != nil {
		a.addWarnings(warnings...)
	}

	for _, target := range prometheusTargets {
		_, err = a.gathererRegistry.RegisterGatherer(
			registry.RegistrationOption{
				Description: "Prom exporter " + target.URL.String(),
				JitterSeed:  labels.FromMap(target.ExtraLabels).Hash(),
				Interval:    defaultInterval,
				ExtraLabels: target.ExtraLabels,
			},
			target,
		)
		if err != nil {
			logger.Printf("Unable to add Prometheus scrapper for target %s: %v", target.URL, err)
		}
	}

	a.gathererRegistry.AddDefaultCollector()

	sentry.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetContext("agent", map[string]interface{}{
			"agent_id":        a.BleemeoAgentID(),
			"glouton_version": version.Version,
		})
	})

	if a.config.NRPE.Enable {
		nrpeConfFile := a.config.NRPE.ConfPaths
		nrperesponse := nrpe.NewResponse(a.config.Services, a.discovery, nrpeConfFile)
		server := nrpe.New(
			fmt.Sprintf("%s:%d", a.config.NRPE.Address, a.config.NRPE.Port),
			a.config.NRPE.SSL,
			nrperesponse.Response,
		)
		tasks = append(tasks, taskInfo{server.Run, "NRPE server"})
	}

	if a.config.Zabbix.Enable {
		server := zabbix.New(
			net.JoinHostPort(a.config.Zabbix.Address, fmt.Sprint(a.config.Zabbix.Port)),
			zabbixResponse,
		)
		tasks = append(tasks, taskInfo{server.Run, "Zabbix server"})
	}

	if a.config.InfluxDB.Enable {
		server := influxdb.New(
			fmt.Sprintf("http://%s", net.JoinHostPort(a.config.InfluxDB.Host, fmt.Sprint(a.config.InfluxDB.Port))),
			a.config.InfluxDB.DBName,
			a.store,
			a.config.InfluxDB.Tags,
		)
		a.influxdbConnector = server
		tasks = append(tasks, taskInfo{server.Run, "influxdb"})

		logger.V(2).Printf("Influxdb is activated !")
	}

	if a.bleemeoConnector == nil {
		a.updateThresholds(ctx, nil, true)
	} else {
		a.bleemeoConnector.ApplyCachedConfiguration(ctx)
	}

	if !reflect.DeepEqual(a.config.DiskMonitor, config2.DefaultConfig().DiskMonitor) {
		if a.metricFormat == types.MetricFormatBleemeo && len(a.config.DiskIgnore) > 0 {
			logger.Printf("Warning: both \"disk_monitor\" and \"disk_ignore\" are set. Only \"disk_ignore\" will be used")
		} else if a.metricFormat != types.MetricFormatBleemeo {
			logger.Printf("Warning: configuration \"disk_monitor\" is not used in Prometheus mode. Use \"disk_ignore\"")
		}
	}

	a.vethProvider = &veth.Provider{
		HostRootPath: a.hostRootPath,
		Runtime:      a.containerRuntime,
	}

	if a.metricFormat == types.MetricFormatBleemeo {
		conf, err := a.buildCollectorsConfig()
		if err != nil {
			logger.V(0).Printf("Unable to initialize system collector: %v", err)

			return
		}

		if err = discovery.AddDefaultInputs(a.collector, conf, a.vethProvider); err != nil {
			logger.Printf("Unable to initialize system collector: %v", err)

			return
		}
	}

	// register components only available on a given system, like node_exporter for unixes
	a.registerOSSpecificComponents(a.vethProvider)

	tasks = append(tasks, taskInfo{
		a.gathererRegistry.Run,
		"Metric collector",
	})

	if a.config.Telegraf.StatsD.Enable {
		input, err := statsd.New(fmt.Sprintf("%s:%d", a.config.Telegraf.StatsD.Address, a.config.Telegraf.StatsD.Port))
		if err != nil {
			logger.Printf("Unable to create StatsD input: %v", err)

			a.config.Telegraf.StatsD.Enable = false
		} else if _, err = a.collector.AddInput(input, "statsd"); err != nil {
			if strings.Contains(err.Error(), "address already in use") {
				logger.Printf("Unable to listen on StatsD port because another program already use it")
				logger.Printf("The StatsD integration is now disabled. Restart the agent to try re-enabling it.")
				logger.Printf("See https://docs.bleemeo.com/agent/configuration#telegrafstatsdenable to permanently disable StatsD integration or using an alternate port")
			} else {
				logger.Printf("Unable to create StatsD input: %v", err)
			}

			a.config.Telegraf.StatsD.Enable = false
		}
	}

	a.factProvider.SetFact("statsd_enable", fmt.Sprint(a.config.Telegraf.StatsD.Enable))
	a.factProvider.SetFact("metrics_format", a.metricFormat.String())

	a.startTasks(tasks)

	<-ctx.Done()
	logger.V(2).Printf("Stopping agent...")
	a.taskRegistry.Close()
	a.discovery.Close()
	a.collector.Close()
	logger.V(2).Printf("Agent stopped")
}

func (a *agent) handleSighup(ctx context.Context, sighupChan chan os.Signal) {
	var (
		l                         sync.Mutex
		systemUpdateMetricPending bool
	)

	for ctx.Err() == nil {
		select {
		case <-sighupChan:
			a.l.Lock()
			connector := a.bleemeoConnector
			a.l.Unlock()

			if connector != nil {
				connector.UpdateMonitors()
			}

			l.Lock()
			if !systemUpdateMetricPending {
				systemUpdateMetricPending = true

				go func() {
					defer types.ProcessPanic()

					a.waitAndRefreshPendingUpdates(ctx)

					l.Lock()
					systemUpdateMetricPending = false
					l.Unlock()
				}()
			}
			l.Unlock()

			a.FireTrigger(true, true, false, true)
		case <-ctx.Done():
			return
		}
	}
}

// Wait for the pending system updates to be refreshed and update the system metrics.
func (a *agent) waitAndRefreshPendingUpdates(ctx context.Context) {
	const maxWaitPendingUpdates = 90 * time.Second

	t0 := time.Now()

	// Wait for the pending updates file to be updated.
	for ctx.Err() == nil && time.Since(t0) < maxWaitPendingUpdates {
		time.Sleep(time.Second)

		updatedAt := facts.PendingSystemUpdateFreshness(
			ctx,
			a.config.Container.Type != "",
			a.hostRootPath,
		)
		if updatedAt.IsZero() || updatedAt.After(t0) {
			break
		}
	}

	a.FireTrigger(false, false, true, false)
}

func (a *agent) buildCollectorsConfig() (conf inputs.CollectorConfig, err error) {
	whitelistRE, err := common.CompileREs(a.config.DiskMonitor)
	if err != nil {
		a.addWarnings(fmt.Errorf("%w: failed to compile regexp in disk_monitor: %s", config2.ErrInvalidValue, err))

		return
	}

	blacklistRE, err := common.CompileREs(a.config.DiskIgnore)
	if err != nil {
		a.addWarnings(fmt.Errorf("%w: failed to compile regexp in disk_ignore: %s", config2.ErrInvalidValue, err))

		return
	}

	pathIgnoreTrimed := make([]string, len(a.config.DF.PathIgnore))

	for i, v := range a.config.DF.PathIgnore {
		pathIgnoreTrimed[i] = strings.TrimRight(v, "/")
	}

	return inputs.CollectorConfig{
		DFRootPath:      a.hostRootPath,
		NetIfBlacklist:  a.config.NetworkInterfaceBlacklist,
		IODiskWhitelist: whitelistRE,
		IODiskBlacklist: blacklistRE,
		DFPathBlacklist: pathIgnoreTrimed,
	}, nil
}

func (a *agent) miscGather(pusher types.PointPusher) func(context.Context, time.Time) {
	return func(ctx context.Context, t0 time.Time) {
		points, err := a.containerRuntime.Metrics(ctx, t0)
		if err != nil {
			logger.V(2).Printf("container Runtime metrics gather failed: %v", err)
		}

		// We don't really care about having up-to-date information because
		// when containers are started/stopped, the information is updated anyway.
		containers, err := a.containerRuntime.Containers(ctx, 2*time.Hour, false)
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

		pusher.PushPoints(ctx, points)
	}
}

func (a *agent) sendToTelemetry(ctx context.Context) error {
	if a.config.Agent.Telemetry.Enable {
		select {
		case <-time.After(delay.JitterDelay(5*time.Minute, 0.2)):
		case <-ctx.Done():
			return nil
		}

		telemetryID := a.state.TelemetryID()

		for {
			facts, err := a.factProvider.Facts(ctx, time.Hour)
			if err != nil {
				logger.V(2).Printf("error facts load %v", err)

				continue
			}

			telemetry.PostInformation(ctx, telemetryID, a.config.Agent.Telemetry.Address, a.BleemeoAgentID(), facts)

			select {
			case <-time.After(delay.JitterDelay(24*time.Hour, 0.05)):
			case <-ctx.Done():
				return nil
			}
		}
	}

	return nil
}

func (a *agent) miscGatherMinute(pusher types.PointPusher) func(context.Context, time.Time) {
	return func(ctx context.Context, t0 time.Time) {
		points, err := a.containerRuntime.MetricsMinute(ctx, t0)
		if err != nil {
			logger.V(2).Printf("container Runtime metrics gather failed: %v", err)
		}

		service, err := a.discovery.Discovery(ctx, 2*time.Hour)
		if err != nil {
			logger.V(1).Printf("get service failed to every-minute metrics: %v", err)

			service = nil
		}

		for _, srv := range service {
			if !srv.Active {
				continue
			}

			switch srv.ServiceType { //nolint:exhaustive,nolintlint
			case discovery.PostfixService:
				n, err := postfixQueueSize(ctx, srv, a.hostRootPath, a.containerRuntime)
				if err != nil {
					logger.V(1).Printf("Unabled to gather postfix queue size on %s: %v", srv, err)

					continue
				}

				labels := map[string]string{
					types.LabelName: "postfix_queue_size",
					types.LabelItem: srv.Instance,
				}

				annotations := types.MetricAnnotations{
					BleemeoItem:     srv.Instance,
					ContainerID:     srv.ContainerID,
					ServiceName:     srv.Name,
					ServiceInstance: srv.Instance,
				}

				points = append(points, types.MetricPoint{
					Labels:      labels,
					Annotations: annotations,
					Point: types.Point{
						Time:  time.Now(),
						Value: n,
					},
				})
			case discovery.EximService:
				n, err := eximQueueSize(ctx, srv, a.hostRootPath, a.containerRuntime)
				if err != nil {
					logger.V(1).Printf("Unabled to gather exim queue size on %s: %v", srv, err)

					continue
				}

				labels := map[string]string{
					types.LabelName: "exim_queue_size",
					types.LabelItem: srv.Instance,
				}

				annotations := types.MetricAnnotations{
					BleemeoItem:     srv.Instance,
					ContainerID:     srv.ContainerID,
					ServiceName:     srv.Name,
					ServiceInstance: srv.Instance,
				}

				points = append(points, types.MetricPoint{
					Labels:      labels,
					Annotations: annotations,
					Point: types.Point{
						Time:  time.Now(),
						Value: n,
					},
				})
			}
		}

		desc := a.getWarnings().Error()
		status := types.StatusWarning

		if len(desc) == 0 {
			status = types.StatusOk
			desc = "configuration returned no warnings."
		}

		points = append(points, types.MetricPoint{
			Point: types.Point{
				Value: float64(status.NagiosCode()),
				Time:  t0,
			},
			Labels: map[string]string{
				types.LabelName: "agent_config_warning",
			},
			Annotations: types.MetricAnnotations{
				Status: types.StatusDescription{
					StatusDescription: desc,
					CurrentStatus:     status,
				},
			},
		})

		pusher.PushPoints(ctx, points)
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

		now := time.Now()

		a.l.Lock()

		lastHealthCheck := a.lastHealthCheck
		a.watchdogRunAt = append(a.watchdogRunAt, now)

		if len(a.watchdogRunAt) > 90 {
			copy(a.watchdogRunAt[0:60], a.watchdogRunAt[len(a.watchdogRunAt)-60:len(a.watchdogRunAt)])
			a.watchdogRunAt = a.watchdogRunAt[:60]
		}

		a.l.Unlock()

		switch {
		case time.Since(lastHealthCheck) > 15*time.Minute && !failing:
			logger.V(2).Printf("Healthcheck are no longer running. Last run was at %s", lastHealthCheck.Format(time.RFC3339))

			failing = true
		case time.Since(lastHealthCheck) > 15*time.Minute && failing:
			logger.Printf("Healthcheck are no longer running. Last run was at %s", lastHealthCheck.Format(time.RFC3339))
			// We don't know how big the buffer needs to be to collect
			// all the goroutines. Use 2MB buffer which hopefully is enough
			buffer := make([]byte, 1<<21)

			runtime.Stack(buffer, true)
			logger.Printf("%s", string(buffer))
			logger.Printf("Glouton seems unhealthy, killing myself")
			panic("Glouton seems unhealthy (health check is no longer running), killing myself")
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
		a.lastHealthCheck = time.Now()
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
	a.waitAndRefreshPendingUpdates(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay.JitterDelay(time.Hour, 0.1)):
			a.FireTrigger(true, false, true, false)
		}
	}
}

func (a *agent) dailyFact(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay.JitterDelay(24*time.Hour, 0.1)):
			a.FireTrigger(false, true, false, false)
		}
	}
}

func (a *agent) dockerWatcher(ctx context.Context) error {
	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer types.ProcessPanic()
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

				a.sendDockerContainerHealth(ctx, ev.Container)
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
				a.sendDockerContainerHealth(ctx, c)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (a *agent) sendDockerContainerHealth(ctx context.Context, container facts.Container) {
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

	a.gathererRegistry.WithTTL(5*time.Minute).PushPoints(ctx, []types.MetricPoint{
		{
			Labels: map[string]string{
				types.LabelName:              "container_health_status",
				types.LabelMetaContainerName: container.ContainerName(),
				types.LabelMetaContainerID:   container.ID(),
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
	stat, _ := os.Stat(a.config.Agent.NetstatFile)

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		newStat, _ := os.Stat(a.config.Agent.NetstatFile)
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

func (a *agent) handleTrigger(ctx context.Context) {
	runDiscovery, runFact, runSystemUpdateMetric := a.cleanTrigger()
	if runDiscovery {
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

			err := a.metricFilter.RebuildDynamicLists(a.dynamicScrapper, services, a.threshold.GetThresholdMetricNames(), a.rulesManager.MetricNames())
			if err != nil {
				logger.V(2).Printf("Error during dynamic Filter rebuild: %v", err)
			}
		}

		hasConnection := a.dockerRuntime.IsRuntimeRunning(ctx)
		if hasConnection && !a.dockerInputPresent && a.config.Telegraf.DockerMetricsEnable {
			i, err := docker.New(a.dockerRuntime.ServerAddress(), a.dockerRuntime, a.containerFilter.ContainerIgnored)
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
		a.config.Container.Type != "",
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

	a.gathererRegistry.WithTTL(time.Hour).PushPoints(ctx, points)
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
func (a *agent) DiagnosticPage(ctx context.Context) string {
	builder := &strings.Builder{}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	fmt.Fprintf(
		builder,
		"Run diagnostic at %s with Glouton version %s (commit %s built using Go %s)\n",
		time.Now().Format(time.RFC3339),
		version.Version,
		version.BuildHash,
		runtime.Version(),
	)

	if a.config.Bleemeo.Enable {
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
		fmt.Fprintf(builder, "Glouton measures %d metrics\n", len(allMetrics))
	}

	fmt.Fprintf(builder, "Glouton was built for %s %s\n", runtime.GOOS, runtime.GOARCH)

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

func (a *agent) writeDiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error {
	modules := []func(ctx context.Context, archive types.ArchiveWriter) error{
		a.diagnosticGlobalInfo,
		a.diagnosticGloutonState,
		a.diagnosticJitter,
		a.taskRegistry.DiagnosticArchive,
		a.store.DiagnosticArchive,
		a.diagnosticConfig,
		a.discovery.DiagnosticArchive,
		a.diagnosticContainers,
		a.diagnosticSNMP,
		a.diagnosticFilterResult,
		a.metricFilter.DiagnosticArchive,
		a.gathererRegistry.DiagnosticArchive,
		a.rulesManager.DiagnosticArchive,
		a.reloadState.DiagnosticArchive,
		a.vethProvider.DiagnosticArchive,
	}

	if a.bleemeoConnector != nil {
		modules = append(modules, a.bleemeoConnector.DiagnosticArchive)
	}

	if a.monitorManager != nil {
		modules = append(modules, a.monitorManager.DiagnosticArchive)
	}

	for _, f := range modules {
		if err := f(ctx, archive); err != nil {
			return err
		}

		if ctx.Err() != nil {
			break
		}
	}

	return ctx.Err()
}

func (a *agent) diagnosticGlobalInfo(ctx context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("diagnostic.txt")
	if err != nil {
		return err
	}

	_, err = file.Write([]byte(a.DiagnosticPage(ctx)))
	if err != nil {
		return err
	}

	file, err = archive.Create("log.txt")
	if err != nil {
		return err
	}

	tmp := logger.Buffer()

	_, err = file.Write(tmp)
	if err != nil {
		return err
	}

	compressedSize := logger.CompressedSize()

	fmt.Fprintf(file, "-- Log size = %d, compressed = %d (ratio: %.2f)\n", len(tmp), compressedSize, float64(compressedSize)/float64(len(tmp)))

	file, err = archive.Create("goroutines.txt")
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

	return nil
}

func (a *agent) diagnosticGloutonState(ctx context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("glouton-state.json")
	if err != nil {
		return err
	}

	a.l.Lock()
	a.triggerLock.Lock()

	obj := struct {
		HostRootPath              string
		LastHealthCheck           time.Time
		LastContainerEventTime    time.Time
		TriggerDiscAt             time.Time
		TriggerDiscImmediate      bool
		TriggerFact               bool
		TriggerSystemUpdateMetric bool
		DockerInputPresent        bool
		DockerInputID             int
		MetricResolutionSeconds   float64
	}{
		HostRootPath:              a.hostRootPath,
		LastHealthCheck:           a.lastHealthCheck,
		LastContainerEventTime:    a.lastContainerEventTime,
		TriggerDiscAt:             a.triggerDiscAt,
		TriggerDiscImmediate:      a.triggerDiscImmediate,
		TriggerFact:               a.triggerFact,
		TriggerSystemUpdateMetric: a.triggerSystemUpdateMetric,
		DockerInputPresent:        a.dockerInputPresent,
		DockerInputID:             a.dockerInputID,
		MetricResolutionSeconds:   a.metricResolution.Seconds(),
	}

	a.triggerLock.Unlock()
	a.l.Unlock()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

func (a *agent) diagnosticJitter(ctx context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("jitter.txt")
	if err != nil {
		return err
	}

	a.l.Lock()
	defer a.l.Unlock()

	fmt.Fprintln(file, "# This file contains time & jitter delay")
	fmt.Fprintln(file, "# A variable jitter may indidate overloaded system")

	var (
		previousTime time.Time
		maxJitter    time.Duration
		avgJitter    time.Duration
	)

	for i, t := range a.watchdogRunAt {
		if i == 0 {
			fmt.Fprintf(file, "run_at=%v jitter=n/a\n", t)
		} else {
			delay := t.Sub(previousTime)
			jitter := delay - time.Minute

			fmt.Fprintf(file, "run_at=%v jitter=%v\n", t, jitter)

			if jitter < 0 {
				jitter = -jitter
			}

			if jitter > maxJitter {
				maxJitter = jitter
			}

			avgJitter += maxJitter
		}

		previousTime = t
	}

	if len(a.watchdogRunAt) > 1 {
		avgJitter /= time.Duration(len(a.watchdogRunAt) - 1)
	}

	fmt.Fprintf(file, "max jitter=%v, avg jitter=%v\n", maxJitter, avgJitter)

	return nil
}

func (a *agent) diagnosticContainers(ctx context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("containers.txt")
	if err != nil {
		return err
	}

	containers, err := a.containerRuntime.Containers(ctx, time.Hour, true)
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
			health, healthMsg := c.Health()
			fmt.Fprintf(
				file,
				"Name=%s, ID=%s, ignored=%v, IP=%s, listenAddr=%v,\n\tState=%v, CreatedAt=%v, StartedAt=%v, FinishedAt=%v, StoppedAndReplaced=%v\n\tHealth=%v (%s) K8S=%v/%v\n",
				c.ContainerName(),
				c.ID(),
				a.containerFilter.ContainerIgnored(c),
				c.PrimaryAddress(),
				c.ListenAddresses(),
				c.State(),
				c.CreatedAt(),
				c.StartedAt(),
				c.FinishedAt(),
				c.StoppedAndReplaced(),
				health,
				strings.ReplaceAll(healthMsg, "\n", "\\n"),
				c.PodNamespace(),
				c.PodName(),
			)
		}
	}

	return nil
}

func (a *agent) diagnosticSNMP(ctx context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("snmp-targets.txt")
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	fmt.Fprintf(file, "# %d SNMP target configured\n", len(a.snmpManager.Targets()))

	for _, t := range a.snmpManager.Targets() {
		fmt.Fprintf(file, "\n%s\n", t.String(ctx))
		facts, err := t.Facts(ctx, 48*time.Hour)

		if err != nil {
			fmt.Fprintf(file, " facts failed: %v\n", err)
		} else {
			for k, v := range facts {
				fmt.Fprintf(file, " * %s = %s\n", k, v)
			}
		}
	}

	if a.bleemeoConnector != nil {
		a.bleemeoConnector.DiagnosticSNMPAssociation(ctx, file)
	}

	return nil
}

func (a *agent) diagnosticConfig(ctx context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("config.yaml")
	if err != nil {
		return err
	}

	enc := yaml.NewEncoder(file)

	fmt.Fprintln(file, "# This file contains in-memory configuration used by Glouton. Value from from default, files and environment.")
	enc.SetIndent(4)

	err = enc.Encode(config2.Dump(a.config))
	if err != nil {
		fmt.Fprintf(file, "# error: %v\n", err)
	}

	err = enc.Close()
	if err != nil {
		fmt.Fprintf(file, "# error: %v\n", err)
	}

	return nil
}

func (a *agent) diagnosticFilterResult(ctx context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("metric-filter-result.txt")
	if err != nil {
		return err
	}

	localMetrics, err := a.store.Metrics(nil)
	if err != nil {
		return err
	}

	sort.Slice(localMetrics, func(i, j int) bool {
		lblsA := labels.FromMap(localMetrics[i].Labels())
		lblsB := labels.FromMap(localMetrics[j].Labels())

		return labels.Compare(lblsA, lblsB) < 0
	})

	for _, m := range localMetrics {
		isAllowed := a.metricFilter.IsAllowed(m.Labels())
		isDenied := a.metricFilter.IsDenied(m.Labels())
		isBleemeoAllowed := true

		var denyReason bleemeoTypes.DenyReason

		if a.bleemeoConnector != nil {
			isBleemeoAllowed, denyReason, _ = a.bleemeoConnector.IsMetricAllowed(types.LabelsAndAnnotation{
				Labels:      m.Labels(),
				Annotations: m.Annotations(),
			})
		}

		name := types.LabelsToText(m.Labels())

		switch {
		case !isAllowed:
			fmt.Fprintf(file, "The metric %s is not in configured allow list\n", name)
		case isDenied && !isBleemeoAllowed:
			fmt.Fprintf(file, "The metric %s is blocked by configured deny list and is not available in current Bleemeo Plan\n", name)
		case isDenied:
			fmt.Fprintf(file, "The metric %s is blocked by configured deny list\n", name)
		case !isBleemeoAllowed:
			fmt.Fprintf(file, "The metric %s is not allowed: %s\n", name, denyReason)
		default:
			fmt.Fprintf(file, "The metric %s is allowed\n", name)
		}
	}

	return nil
}

// Add a warning for the configuration.
func (a *agent) addWarnings(warnings ...error) {
	var warningsStr strings.Builder
	for _, w := range warnings {
		warningsStr.WriteByte('\n')
		warningsStr.WriteString(w.Error())
	}

	logger.Printf("Warning while loading configuration:%s", warningsStr.String())

	a.l.Lock()
	defer a.l.Unlock()

	a.configWarnings = append(a.configWarnings, warnings...)
}

// Get configuration warnings.
func (a *agent) getWarnings() types.MultiErrors {
	a.l.Lock()
	defer a.l.Unlock()

	return a.configWarnings
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

	if hostRootPath != "" && hostRootPath != "/" {
		if os.Getenv("HOST_VAR") == "" {
			// gopsutil will use HOST_VAR as prefix to host /var
			// It's used at least for reading the number of connected user from /var/run/utmp
			os.Setenv("HOST_VAR", filepath.Join(hostRootPath, "var"))

			// ... but /var/run is usually a symlink to /run.
			varRun := filepath.Join(hostRootPath, "var/run")
			target, err := os.Readlink(varRun)

			if err == nil && target == "/run" {
				os.Setenv("HOST_VAR", hostRootPath)
			}
		}

		if os.Getenv("HOST_ETC") == "" {
			os.Setenv("HOST_ETC", filepath.Join(hostRootPath, "etc"))
		}

		if os.Getenv("HOST_PROC") == "" {
			os.Setenv("HOST_PROC", filepath.Join(hostRootPath, "proc"))
		}

		if os.Getenv("HOST_SYS") == "" {
			os.Setenv("HOST_SYS", filepath.Join(hostRootPath, "sys"))
		}

		if os.Getenv("HOST_RUN") == "" {
			os.Setenv("HOST_RUN", filepath.Join(hostRootPath, "run"))
		}

		if os.Getenv("HOST_DEV") == "" {
			os.Setenv("HOST_DEV", filepath.Join(hostRootPath, "dev"))
		}

		if os.Getenv("HOST_MOUNT_PREFIX") == "" {
			os.Setenv("HOST_MOUNT_PREFIX", hostRootPath)
		}
	}
}

// prometheusConfigToURLs convert metric.prometheus.targets config to a map of target name to URL
//
// See tests for the expected config.
func prometheusConfigToURLs(configTargets []config2.PrometheusTarget) ([]*scrapper.Target, config2.Warnings) {
	var warnings config2.Warnings

	targets := make([]*scrapper.Target, 0, len(configTargets))

	for _, config := range configTargets {
		targetURL, err := url.Parse(config.URL)
		if err != nil {
			warnings = append(warnings, fmt.Errorf("%w: invalid prometheus target URL: %s", config2.ErrInvalidValue, err))

			continue
		}

		target := &scrapper.Target{
			ExtraLabels: map[string]string{
				types.LabelMetaScrapeJob: config.Name,
				// HostPort could be empty, but this ExtraLabels is used by Registry which
				// correctly handle empty value value (drop the label).
				types.LabelMetaScrapeInstance: scrapper.HostPort(targetURL),
			},
			URL:       targetURL,
			AllowList: config.AllowMetrics,
			DenyList:  config.DenyMetrics,
		}

		targets = append(targets, target)
	}

	return targets, warnings
}
