// Copyright 2015-2025 Bleemeo
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
	"io"
	"math"
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

	"github.com/bleemeo/glouton/agent/state"
	"github.com/bleemeo/glouton/api"
	"github.com/bleemeo/glouton/bleemeo"
	"github.com/bleemeo/glouton/collector"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/debouncer"
	"github.com/bleemeo/glouton/delay"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/discovery/promexporter"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/facts/container-runtime/containerd"
	"github.com/bleemeo/glouton/facts/container-runtime/kubernetes"
	"github.com/bleemeo/glouton/facts/container-runtime/merge"
	"github.com/bleemeo/glouton/facts/container-runtime/veth"
	"github.com/bleemeo/glouton/fluentbit"
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/docker"
	"github.com/bleemeo/glouton/inputs/mdstat"
	nvidia "github.com/bleemeo/glouton/inputs/nvidia_smi"
	"github.com/bleemeo/glouton/inputs/smart"
	"github.com/bleemeo/glouton/inputs/statsd"
	"github.com/bleemeo/glouton/inputs/temp"
	"github.com/bleemeo/glouton/inputs/vsphere"
	"github.com/bleemeo/glouton/jmxtrans"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/mqtt"
	"github.com/bleemeo/glouton/mqtt/client"
	"github.com/bleemeo/glouton/nrpe"
	"github.com/bleemeo/glouton/otel/logprocessing"
	"github.com/bleemeo/glouton/prometheus/exporter/blackbox"
	"github.com/bleemeo/glouton/prometheus/exporter/ipmi"
	"github.com/bleemeo/glouton/prometheus/exporter/snmp"
	"github.com/bleemeo/glouton/prometheus/exporter/ssacli"
	"github.com/bleemeo/glouton/prometheus/process"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/prometheus/rules"
	"github.com/bleemeo/glouton/prometheus/scrapper"
	"github.com/bleemeo/glouton/store"
	"github.com/bleemeo/glouton/task"
	"github.com/bleemeo/glouton/telemetry"
	"github.com/bleemeo/glouton/threshold"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"
	"github.com/bleemeo/glouton/version"
	"github.com/bleemeo/glouton/zabbix"

	"github.com/getsentry/sentry-go"
	"github.com/influxdata/telegraf"
	"github.com/prometheus/prometheus/util/gate"
	"github.com/shirou/gopsutil/v4/host"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"

	dockerRuntime "github.com/bleemeo/glouton/facts/container-runtime/docker"

	crTypes "github.com/bleemeo/glouton/facts/container-runtime/types"

	processSource "github.com/bleemeo/glouton/prometheus/sources/process"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"gopkg.in/yaml.v3"
)

//nolint:gochecknoinits
func init() {
	// We want to keep Prometheus's strict name validation.
	model.NameValidationScheme = model.LegacyValidation //nolint: staticcheck,nolintlint
}

// Jitter define the aligned timestamp used for scrapping.
// System collector use 0 (baseJitter here and in registry.go).
// baseJitterPlus is a little after, useful for collector that need to re-read point of system collector.
const (
	baseJitter      = 0
	baseJitterPlus  = 500000
	defaultInterval = 0
)

var (
	// We want to reply with capitalized U to match output from a Zabbix agent.
	errUnsupportedKey     = errors.New("Unsupported item key") //nolint:staticcheck
	errFeatureUnavailable = errors.New("some features are unavailable")
)

type agent struct {
	taskRegistry *task.Registry
	config       config.Config
	configItems  []config.Item
	state        *state.State
	stateDir     string
	cancel       context.CancelFunc
	context      context.Context //nolint:containedctx

	hostRootPath           string
	commandRunner          *gloutonexec.Runner
	discovery              *discovery.Discovery
	dockerRuntime          *dockerRuntime.Docker
	containerFilter        facts.ContainerFilter
	containerdRuntime      *containerd.Containerd
	containerRuntime       crTypes.RuntimeInterface
	collector              *collector.Collector
	factProvider           *facts.FactProvider
	bleemeoConnector       *bleemeo.Connector
	threshold              *threshold.Registry
	jmx                    *jmxtrans.JMX
	snmpManager            *snmp.Manager
	store                  *store.Store
	gathererRegistry       *registry.Registry
	dynamicScrapper        *promexporter.DynamicScrapper
	lastHealthCheck        time.Time
	lastContainerEventTime time.Time
	watchdogRunAt          []time.Time
	metricFilter           *metricFilter
	promFilter             *metricFilter
	mergeMetricFilter      *metricFilter
	monitorManager         *blackbox.RegisterManager
	rulesManager           *rules.Manager
	reloadState            ReloadState
	vethProvider           *veth.Provider
	mqtt                   *mqtt.MQTT
	pahoLogWrapper         *client.LogWrapper
	fluentbitManager       *fluentbit.Manager
	vSphereManager         *vsphere.Manager
	logProcessManager      *logprocessing.Manager

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
	configWarnings   prometheus.MultiError
}

func zabbixResponse(key string, args []string) (string, error) {
	_ = args

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

	cfg, configItems, warnings, err := config.Load(true, true, configFiles...)
	if warnings != nil {
		a.addWarnings(warnings...)
	}

	a.config = cfg
	a.configItems = configItems

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
			Release:          version.Version,
		})
		if err != nil {
			logger.V(1).Printf("sentry.Init failed: %s", err)
		}
	}

	a.stateDir = a.config.Agent.StateDirectory
	crashreport.SetOptions(a.config.Agent.EnableCrashReporting, a.stateDir, a.writeDiagnosticArchive)

	if firstRun {
		crashreport.SetupStderrRedirection()

		_, _ = fmt.Fprintf(os.Stderr, "Starting Glouton at %s with PID %d\n", time.Now().Format(time.RFC3339), os.Getpid())
	}

	a.pahoLogWrapper = client.GetOrCreateWrapper()

	sentry.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetContext("agent", map[string]any{
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
			a.state.Close()

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
	case "1", "verbose":
		logger.SetLevel(1)
	case "2", "debug":
		logger.SetLevel(2)
	default:
		logger.SetLevel(0)
		a.addWarnings(fmt.Errorf(`%w: unknown logging.level "%s", using "INFO"`, config.ErrInvalidValue, a.config.Logging.Level))
	}

	logger.SetPkgLevels(a.config.Logging.PackageLevels)
}

// Run runs Glouton.
func Run(ctx context.Context, reloadState ReloadState, configFiles []string, signalChan chan os.Signal, firstRun bool) {
	agent := &agent{reloadState: reloadState}

	if !agent.init(ctx, configFiles, firstRun) {
		os.Exit(1)

		return
	}

	agent.run(ctx, signalChan)
}

// BleemeoAccountID returns the Account UUID of Bleemeo
// It return the empty string if the Account UUID is not available (e.g. because Bleemeo is disabled or miss-configured).
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
func (a *agent) UpdateThresholds(thresholds map[string]threshold.Threshold, firstUpdate bool) {
	a.updateThresholds(thresholds, firstUpdate)
}

// notifyBleemeoFirstRegistration is called when Glouton is registered with Bleemeo Cloud platform for the first time
// This means that when this function is called, BleemeoAgentID and BleemeoAccountID are set.
func (a *agent) notifyBleemeoFirstRegistration() {
	a.gathererRegistry.UpdateRegistrationHooks(a.bleemeoConnector.RelabelHook, a.bleemeoConnector.UpdateDelayHook)
	a.store.DropAllMetrics()
}

// notifyBleemeoUpdateHooks is called when Labels might change for some metrics.
// This likely happen when SNMP target are deleted/recreated.
func (a *agent) notifyBleemeoUpdateHooks() {
	a.gathererRegistry.UpdateRegistrationHooks(a.bleemeoConnector.RelabelHook, a.bleemeoConnector.UpdateDelayHook)
}

func (a *agent) registerSNMPTargets(context.Context) error {
	for _, target := range a.snmpManager.Gatherers() {
		_, err := a.gathererRegistry.RegisterGatherer(
			registry.RegistrationOption{
				Description: "snmp target " + target.Address,
				JitterSeed:  labels.FromMap(target.ExtraLabels).Hash(),
				MinInterval: time.Minute,
				Timeout:     40 * time.Second,
				ExtraLabels: target.ExtraLabels,
				Rules:       registry.DefaultSNMPRules(time.Minute),
			},
			target.Gatherer,
		)
		if err != nil {
			logger.Printf("Unable to add SNMP scrapper for target %s: %v", target.Address, err)
		}
	}

	return nil
}

func (a *agent) updateMetricResolution(defaultResolution time.Duration) {
	a.l.Lock()
	a.metricResolution = defaultResolution
	a.l.Unlock()

	// No need to check whether the connector is nil or not, since we were called from it.
	a.gathererRegistry.UpdateRegistrationHooks(a.bleemeoConnector.RelabelHook, a.bleemeoConnector.UpdateDelayHook)

	services, _ := a.discovery.GetLatestDiscovery()
	if a.jmx != nil {
		if err := a.jmx.UpdateConfig(services, defaultResolution); err != nil {
			logger.V(1).Printf("failed to update JMX configuration: %v", err)
		}
	}
}

func (a *agent) getConfigThreshold() map[string]threshold.Threshold {
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

func (a *agent) updateThresholds(thresholds map[string]threshold.Threshold, firstUpdate bool) {
	configThreshold := a.getConfigThreshold()

	oldThresholds := map[string]threshold.Threshold{}

	for _, name := range []string{"system_pending_updates", "system_pending_security_updates", "time_drift"} {
		lbls := map[string]string{
			types.LabelName:         name,
			types.LabelInstanceUUID: a.BleemeoAgentID(),
		}
		oldThresholds[name] = a.threshold.GetThreshold(types.LabelsToText(lbls))
	}

	a.threshold.SetThresholds(a.BleemeoAgentID(), thresholds, configThreshold)

	services, _ := a.discovery.GetLatestDiscovery()

	err := a.rebuildDynamicMetricAllowDenyList(services)
	if err != nil {
		logger.V(2).Printf("An error occurred while rebuilding dynamic list for updateThresholds: %v", err)
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

func (a *agent) rebuildDynamicMetricAllowDenyList(services []discovery.Service) error {
	var errs []error

	errs = append(
		errs,
		a.metricFilter.RebuildDynamicLists(a.dynamicScrapper, services, a.threshold.GetThresholdMetricNames(), a.rulesManager.MetricNames()),
	)

	errs = append(
		errs,
		a.promFilter.RebuildDynamicLists(a.dynamicScrapper, services, a.threshold.GetThresholdMetricNames(), a.rulesManager.MetricNames()),
	)

	a.mergeMetricFilter.mergeInPlace(a.metricFilter, a.promFilter)

	return errors.Join(errs...)
}

// Run will start the agent. It will terminate when sigquit/sigterm/sigint is received.
func (a *agent) run(ctx context.Context, sighupChan chan os.Signal) { //nolint:maintidx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	defer a.state.Close()

	a.cancel = cancel
	a.metricResolution = 10 * time.Second
	a.hostRootPath = "/"
	a.context = ctx

	if a.config.Container.Type != "" {
		a.hostRootPath = a.config.DF.HostMountPoint
		// Removing potential trailing slash from hostroot path
		if len(a.hostRootPath) > len(string(os.PathSeparator)) {
			// Yes, len(string(os.PathSeparator)) == 1, but it's more understandable like this.
			a.hostRootPath = strings.TrimSuffix(a.hostRootPath, string(os.PathSeparator))
		}

		setupContainer(a.hostRootPath)
	}

	a.commandRunner = gloutonexec.New(a.hostRootPath)

	a.triggerHandler = debouncer.New(
		ctx,
		a.handleTrigger,
		5*time.Second,
		10*time.Second,
	)

	a.factProvider = facts.NewFacter(
		a.commandRunner,
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
			config.ErrInvalidValue, a.config.Agent.CloudImageCreationFile, err,
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

	for key, value := range factsMap {
		logger.V(2).Printf("Fact %s = %s", key, value)
	}

	uptimeRaw, err := host.UptimeWithContext(ctx)
	if err != nil {
		logger.V(2).Printf("failed to get uptime: %v", err)
	} else {
		uptime := time.Duration(uptimeRaw) * time.Second //nolint:gosec
		boottime := time.Now().Add(-uptime)
		logger.V(2).Printf("uptime: system booted %s ago, at %s", uptime, boottime.Format(time.RFC3339))
	}

	boottimeRaw, err := host.BootTime()
	if err != nil {
		logger.V(2).Printf("failed to get bootime: %v", err)
	} else {
		boottime := time.Unix(int64(boottimeRaw), 0) //nolint:gosec
		uptime := time.Since(boottime).Truncate(time.Second)
		logger.V(2).Printf("bootime: system booted at %s, %s ago", boottime.Format(time.RFC3339), uptime)
	}

	_ = os.Remove(a.config.Agent.UpgradeFile)
	_ = os.Remove(a.config.Agent.AutoUpgradeFile)

	apiBindAddress := fmt.Sprintf("%s:%d", a.config.Web.Listener.Address, a.config.Web.Listener.Port)

	var warnings prometheus.MultiError

	a.snmpManager, warnings = snmp.NewManager(
		a.config.Metric.SNMP.ExporterAddress,
		a.factProvider,
		a.config.Metric.SNMP.Targets,
	)
	if warnings != nil {
		a.addWarnings(warnings...)
	}

	hasSwap := factsMap["swap_present"] == "true"

	bleemeoFilter, err := newMetricFilter(a.config.Metric, len(a.snmpManager.Targets()) > 0, hasSwap, true)
	if err != nil {
		logger.Printf("An error occurred while building the metric filter, allow/deny list may be partial: %v", err)
	}

	a.metricFilter = bleemeoFilter

	a.store = store.New("agent store", 3*time.Minute, 2*time.Hour)

	bleemeoFilteredStore := store.NewFilteredStore(
		a.store,
		func(m []types.MetricPoint) []types.MetricPoint {
			return bleemeoFilter.FilterPoints(m, false)
		},
		bleemeoFilter.filterMetrics,
	)
	a.threshold = threshold.New(a.state)

	promFilter, err := newMetricFilter(a.config.Metric, len(a.snmpManager.Targets()) > 0, hasSwap, false)
	if err != nil {
		logger.Printf("An error occurred while building the Prometheus metric filter, allow/deny list may be partial: %v", err)
	}

	a.promFilter = promFilter
	a.mergeMetricFilter = mergeMetricFilters(a.promFilter, bleemeoFilter)

	secretInputsGate := gate.New(inputs.MaxParallelSecrets())

	a.gathererRegistry, err = registry.New(
		registry.Option{
			PushPoint:             a.store,
			ThresholdHandler:      a.threshold,
			FQDN:                  fqdn,
			GloutonPort:           strconv.Itoa(a.config.Web.Listener.Port),
			BlackboxSendScraperID: a.config.Blackbox.ScraperSendUUID,
			Filter:                a.mergeMetricFilter,
			Queryable:             a.store,
			SecretInputsGate:      secretInputsGate,
			ShutdownDeadline:      15 * time.Second,
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
		shouldGatherClusterMetrics := func() bool {
			if a.bleemeoConnector != nil {
				return a.bleemeoConnector.AgentIsClusterLeader()
			}

			return a.config.Kubernetes.AllowClusterMetrics
		}

		var clusterNameState string

		clusterName := a.config.Kubernetes.ClusterName

		err = a.state.Get(state.KeyKubernetesCluster, &clusterNameState)
		if err != nil {
			logger.V(2).Printf("failed to get %s: %v", state.KeyKubernetesCluster, err)
		}

		if clusterName == "" && clusterNameState != "" {
			logger.V(1).Printf("kubernetes.clustername is unset, using previous value of %s", clusterNameState)
			clusterName = clusterNameState
		} else if clusterName != "" && clusterNameState != clusterName {
			err = a.state.Set(state.KeyKubernetesCluster, clusterName)
			if err != nil {
				logger.V(2).Printf("failed to set %s: %v", state.KeyKubernetesCluster, err)
			}
		}

		if clusterName != "" {
			a.factProvider.SetFact(facts.FactKubernetesCluster, clusterName)
		} else {
			a.addWarnings(fmt.Errorf(
				"%w because kubernetes.clustername is missing, see https://go.bleemeo.com/l/agent-installation-kubernetes",
				errFeatureUnavailable,
			))
		}

		kube := &kubernetes.Kubernetes{
			Runtime:                    a.containerRuntime,
			NodeName:                   a.config.Kubernetes.NodeName,
			KubeConfig:                 a.config.Kubernetes.KubeConfig,
			IsContainerIgnored:         a.containerFilter.ContainerIgnored,
			ShouldGatherClusterMetrics: shouldGatherClusterMetrics,
			ClusterName:                clusterName,
		}
		a.containerRuntime = kube

		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := kube.Test(ctx); err != nil {
			logger.Printf("Kubernetes API unreachable, service detection may misbehave: %v", err)
		}

		cancel()
	}

	if a.config.Container.Type != "" && !a.config.Container.PIDNamespaceHost {
		logger.V(1).Printf("The agent is running in a container and \"container.pid_namespace_host\", is not true. Not all processes will be seen")
	}

	psFact := a.processesLister()

	netstat := &facts.NetstatProvider{FilePath: a.config.Agent.NetstatFile}

	a.factProvider.AddCallback(a.containerRuntime.RuntimeFact)
	a.factProvider.SetFact("installation_format", a.config.Agent.InstallationFormat)

	acc := &inputs.Accumulator{
		Pusher:  a.gathererRegistry.WithTTL(5 * time.Minute),
		Context: ctx,
	}
	a.collector = collector.New(acc, secretInputsGate)

	serviceIgnored := discovery.NewIgnoredService(a.config.ServiceIgnore)
	metricsIgnored := discovery.NewIgnoredService(a.config.ServiceIgnoreMetrics)
	isCheckIgnored := discovery.NewIgnoredService(a.config.ServiceIgnoreCheck).IsServiceIgnored
	dynamicDiscovery := discovery.NewDynamic(discovery.Option{
		PS:                 psFact,
		Netstat:            netstat,
		ContainerInfo:      a.containerRuntime,
		IsContainerIgnored: a.containerFilter.ContainerIgnored,
		IsServiceIgnored:   serviceIgnored.IsServiceIgnored,
		FileReader:         discovery.SudoFileReader{HostRootPath: a.hostRootPath, Runner: a.commandRunner},
	})

	a.discovery, warnings = discovery.New(
		dynamicDiscovery,
		a.commandRunner,
		a.gathererRegistry,
		a.state,
		a.containerRuntime,
		a.config.Services,
		serviceIgnored.IsServiceIgnored,
		isCheckIgnored,
		metricsIgnored.IsServiceIgnored,
		a.containerFilter.ContainerIgnored,
		psFact,
		a.config.ServiceAbsentDeactivationDelay,
		a.config.Log.OpenTelemetry,
	)
	if warnings != nil {
		a.addWarnings(warnings...)
	}

	a.dynamicScrapper = &promexporter.DynamicScrapper{
		Registry:        a.gathererRegistry,
		DynamicJobName:  "discovered-exporters",
		FluentBitInputs: a.config.Log.Inputs,
	}

	if a.config.Blackbox.Enable {
		logger.V(1).Println("Starting blackbox_exporter...")

		a.monitorManager, err = blackbox.New(a.gathererRegistry, a.config.Blackbox)
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
		Endpoints:          a.config.Web.Endpoints,
		PsFact:             psFact,
		FactProvider:       a.factProvider,
		BindAddress:        apiBindAddress,
		Discovery:          a.discovery,
		AgentInfo:          a,
		PrometheusExporter: promExporter,
		Threshold:          a.threshold,
		StaticCDNURL:       a.config.Web.StaticCDNURL,
		DiagnosticPage:     a.DiagnosticPage,
		DiagnosticArchive:  a.writeDiagnosticArchive,
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
		{a.processUpdateMessage, "processUpdateMessage"},
		{a.discovery.Run, "discovery"},
		{psFact.Run, "processes lister"},
		{a.registerSNMPTargets, "SNMP targets registerer"},
	}

	if a.config.Agent.EnableCrashReporting {
		tasks = append(tasks, taskInfo{a.crashReportManagement, "Crash report management"})
	}

	if a.config.JMX.Enable {
		perm, err := strconv.ParseInt(a.config.JMXTrans.FilePermission, 8, 0)
		if err != nil {
			a.addWarnings(fmt.Errorf(
				"%w: failed to parse jmxtrans.file_permission '%s': %s, using the default 0640",
				config.ErrInvalidValue, a.config.JMXTrans.FilePermission, err,
			))

			perm = 0o640
		}

		a.jmx = &jmxtrans.JMX{
			OutputConfigurationFile:       a.config.JMXTrans.ConfigFile,
			OutputConfigurationPermission: os.FileMode(perm), //nolint:gosec
			ContactPort:                   a.config.JMXTrans.GraphitePort,
			Pusher:                        a.gathererRegistry.WithTTL(5 * time.Minute),
		}

		tasks = append(tasks, taskInfo{a.jmx.Run, "jmxtrans"})
	}

	baseRules := fluentbit.PromQLRulesFromInputs(a.config.Log.Inputs)
	a.rulesManager = rules.NewManager(ctx, a.store, baseRules)

	a.vSphereManager = vsphere.NewManager()

	a.metricFilter.UpdateRulesMatchers(a.rulesManager.InputMetricMatchers())

	if a.config.Bleemeo.Enable {
		scaperName := a.config.Blackbox.ScraperName
		if scaperName == "" {
			scaperName = fmt.Sprintf("%s:%d", fqdn, a.config.Web.Listener.Port)
		}

		connector, err := bleemeo.New(bleemeoTypes.GlobalOption{
			Config:                         a.config,
			ConfigItems:                    a.configItems,
			State:                          a.state,
			Facts:                          a.factProvider,
			Process:                        psFact,
			Docker:                         a.containerRuntime,
			Store:                          bleemeoFilteredStore,
			SNMP:                           a.snmpManager.Targets(),
			SNMPOnlineTarget:               a.snmpManager.OnlineCount,
			Discovery:                      a.discovery,
			MonitorManager:                 a.monitorManager,
			UpdateMetricResolution:         a.updateMetricResolution,
			UpdateThresholds:               a.UpdateThresholds,
			UpdateUnits:                    a.threshold.SetUnits,
			NotifyFirstRegistration:        a.notifyBleemeoFirstRegistration,
			NotifyHooksUpdate:              a.notifyBleemeoUpdateHooks,
			BlackboxScraperName:            scaperName,
			ReloadState:                    a.reloadState.Bleemeo(),
			WriteDiagnosticArchive:         a.writeDiagnosticArchive,
			VSphereDevices:                 a.vSphereManager.Devices,
			FindVSphereDevice:              a.vSphereManager.FindDevice,
			LastVSphereChange:              a.vSphereManager.LastChange,
			VSphereEndpointsInError:        a.vSphereManager.EndpointsInError,
			IsContainerEnabled:             a.containerFilter.ContainerEnabled,
			IsContainerNameRecentlyDeleted: a.containerRuntime.IsContainerNameRecentlyDeleted,
			IsMetricAllowed:                a.metricFilter.isAllowedAndNotDeniedMap,
			PahoLastPingCheckAt:            a.pahoLogWrapper.LastPingAt,
			LastMetricAnnotationChange:     a.store.LastAnnotationChange,
		})
		if err != nil {
			logger.Printf("unable to start Bleemeo SAAS connector: %v", err)

			return
		}

		a.l.Lock()
		a.bleemeoConnector = connector
		a.l.Unlock()

		if a.config.Log.OpenTelemetry.Enable {
			a.logProcessManager, err = logprocessing.New(
				ctx,
				a.config.Log.OpenTelemetry,
				a.hostRootPath,
				a.state,
				a.commandRunner,
				a.factProvider,
				connector.PushLogs,
				connector.ShouldApplyLogBackPressure,
				a.addWarnings,
			)
			if err != nil {
				logger.Printf("unable to setup log processing: %v", err)
			}
		}

		a.gathererRegistry.UpdateRegistrationHooks(
			a.bleemeoConnector.RelabelHook,
			a.bleemeoConnector.UpdateDelayHook,
		)

		tasks = append(tasks, taskInfo{a.bleemeoConnector.Run, "Bleemeo SAAS connector"})

		_, err = a.gathererRegistry.RegisterAppenderCallback(
			registry.RegistrationOption{
				Description:    "Bleemeo connector",
				JitterSeed:     baseJitter,
				HonorTimestamp: true, // time_drift metric emit point with the time from Bleemeo API
			},
			registry.AppenderFunc(a.bleemeoConnector.EmitInternalMetric),
		)
		if err != nil {
			logger.Printf("unable to add bleemeo connector metrics: %v", err)
		}
	}

	a.FireTrigger(true, true, false, false)

	_, err = a.gathererRegistry.RegisterPushPointsCallback(
		registry.RegistrationOption{
			Description:    "statsd & docker metrics",
			JitterSeed:     baseJitter,
			HonorTimestamp: true,
		},
		a.collector.RunGather,
	)
	if err != nil {
		logger.Printf("unable to add system metrics: %v", err)
	}

	_, err = a.gathererRegistry.RegisterAppenderCallback(
		registry.RegistrationOption{
			Description: "process status metrics",
			JitterSeed:  baseJitter,
		},
		processSource.NewStatusSource(psFact),
	)
	if err != nil {
		logger.Printf("unable to add processes metrics: %v", err)
	}

	// Register misc appender to gather some container metrics.
	_, err = a.gathererRegistry.RegisterAppenderCallback(
		registry.RegistrationOption{
			Description: "miscAppender",
			JitterSeed:  baseJitter,
			// Container metrics contain meta labels that needs to be relabeled.
			ApplyDynamicRelabel: true,
		},
		miscAppender{
			containerRuntime: a.containerRuntime,
		},
	)
	if err != nil {
		logger.Printf("unable to add miscAppender metrics: %v", err)
	}

	// Register misc appender minute to gather some various metrics
	// from containers, services and config warnings.
	_, err = a.gathererRegistry.RegisterAppenderCallback(
		registry.RegistrationOption{
			Description: "miscAppenderMinute",
			JitterSeed:  baseJitter,
			MinInterval: time.Minute,
			// Container metrics contain meta labels that needs to be relabeled.
			ApplyDynamicRelabel: true,
		},
		miscAppenderMinute{
			containerRuntime:  a.containerRuntime,
			discovery:         a.discovery,
			store:             a.store,
			runner:            a.commandRunner,
			getConfigWarnings: a.getWarnings,
		},
	)
	if err != nil {
		logger.Printf("unable to add miscAppenderMinute metrics: %v", err)
	}

	_, err = a.gathererRegistry.RegisterAppenderCallback(
		registry.RegistrationOption{
			Description:        "rulesManager",
			JitterSeed:         baseJitterPlus,
			NoLabelsAlteration: true,
		},
		a.rulesManager,
	)
	if err != nil {
		logger.Printf("unable to add recording rules metrics: %v", err)
	}

	if a.config.Agent.ProcessExporter.Enable {
		processSource.RegisterExporter(ctx, a.gathererRegistry, psFact.AllProcs, dynamicDiscovery, metricsIgnored, serviceIgnored)
	}

	prometheusTargets, warnings := prometheusConfigToURLs(a.config.Metric.Prometheus.Targets)
	if warnings != nil {
		a.addWarnings(warnings...)
	}

	for _, target := range prometheusTargets {
		_, err = a.gathererRegistry.RegisterGatherer(
			registry.RegistrationOption{
				Description:              "Prom exporter " + target.URL.String(),
				JitterSeed:               labels.FromMap(target.ExtraLabels).Hash(),
				ExtraLabels:              target.ExtraLabels,
				AcceptAllowedMetricsOnly: true,
				HonorTimestamp:           true,
			},
			target,
		)
		if err != nil {
			logger.Printf("Unable to add Prometheus scrapper for target %s: %v", target.URL, err)
		}
	}

	a.gathererRegistry.AddDefaultCollector()

	sentry.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetContext("agent", map[string]any{
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
			net.JoinHostPort(a.config.Zabbix.Address, strconv.Itoa(a.config.Zabbix.Port)),
			zabbixResponse,
		)
		tasks = append(tasks, taskInfo{server.Run, "Zabbix server"})
	}

	if a.bleemeoConnector == nil {
		a.updateThresholds(nil, true)
	} else {
		a.bleemeoConnector.ApplyCachedConfiguration()
	}

	if !reflect.DeepEqual(a.config.DiskMonitor, config.DefaultConfig().DiskMonitor) && len(a.config.DiskIgnore) > 0 {
		logger.Printf("Warning: both \"disk_monitor\" and \"disk_ignore\" are set. Only \"disk_ignore\" will be used")
	}

	if len(a.config.Log.Inputs) > 0 {
		a.fluentbitManager, warnings = fluentbit.New(a.config.Log, a.gathererRegistry, a.containerRuntime, a.commandRunner)
		if warnings != nil {
			a.addWarnings(warnings...)
		}

		if a.fluentbitManager != nil {
			tasks = append(tasks, taskInfo{
				a.fluentbitManager.Run,
				"Fluent Bit manager",
			})
		}
	}

	a.vethProvider = &veth.Provider{
		HostRootPath: a.hostRootPath,
		Runtime:      a.containerRuntime,
		Runner:       a.commandRunner,
	}

	conf, err := a.buildCollectorsConfig()
	if err != nil {
		logger.V(0).Printf("Unable to initialize system collector: %v", err)

		return
	}

	if err = discovery.AddDefaultInputs(a.commandRunner, a.gathererRegistry, conf, a.vethProvider); err != nil {
		logger.Printf("Unable to initialize system collector: %v", err)

		return
	}

	// Register inputs that are not associated to a service.
	a.registerInputs(ctx)

	// Register components only available on a given system, like node_exporter for unixes.
	a.registerOSSpecificComponents(ctx, a.vethProvider)

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
				logger.Printf("See https://go.bleemeo.com/l/agent-configuration-statsd to permanently disable StatsD integration or using an alternate port")
			} else {
				logger.Printf("Unable to create StatsD input: %v", err)
			}

			a.config.Telegraf.StatsD.Enable = false
		}
	}

	a.factProvider.SetFact("statsd_enable", strconv.FormatBool(a.config.Telegraf.StatsD.Enable))

	if a.config.MQTT.Enable {
		promFilteredStore := store.NewFilteredStore(
			a.store,
			func(m []types.MetricPoint) []types.MetricPoint {
				return promFilter.FilterPoints(m, false)
			},
			promFilter.filterMetrics,
		)

		a.mqtt = mqtt.New(mqtt.Options{
			ReloadState:         a.reloadState.MQTT(),
			Config:              a.config.MQTT,
			Store:               promFilteredStore,
			FQDN:                fqdn,
			PahoLastPingCheckAt: a.pahoLogWrapper.LastPingAt,
		})

		tasks = append(tasks, taskInfo{
			a.mqtt.Run,
			"MQTT connector",
		})
	}

	inputs.CheckLockedMemory()

	// Handle sighup signals only after the agent is completely initialized
	// to make sure an early signal won't access uninitialized fields.
	go func() {
		defer crashreport.ProcessPanic()

		a.handleSighup(ctx, sighupChan)
	}()

	a.startTasks(tasks)

	lateCtx, lateCtxCancel := context.WithCancel(context.Background())

	var lateTasks sync.WaitGroup

	if a.config.Web.Enable {
		lateTasks.Add(1)

		go func() {
			defer lateTasks.Done()

			if err := api.Run(lateCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.V(1).Printf("Error while stopping api: %v", err)
			}
		}()
	}

	<-ctx.Done()
	logger.V(2).Printf("Stopping agent...")
	a.taskRegistry.Close()
	a.discovery.Close()
	a.collector.Close()

	lateCtxCancel()
	lateTasks.Wait()

	logger.V(2).Printf("Agent stopped")
}

// Registers inputs that are not associated to a service.
func (a *agent) registerInputs(ctx context.Context) {
	makeFactCallback := func(name string) func(binaryInstalled bool) {
		return func(binaryInstalled bool) {
			a.factProvider.SetFact(name, strconv.FormatBool(binaryInstalled))
		}
	}

	if a.config.NvidiaSMI.Enable {
		input, opts, err := nvidia.New(a.config.NvidiaSMI.BinPath, a.config.NvidiaSMI.Timeout)
		a.registerInput("NVIDIA SMI", input, opts, err)
	}

	if a.config.Smart.Enable {
		input, opts, err := smart.New(a.config.Smart, a.commandRunner, a.hostRootPath, makeFactCallback("smartctl_installed"))
		a.registerInput("SMART", input, opts, err)
	}

	if a.config.Mdstat.Enable {
		input, opts, err := mdstat.New(a.config.Mdstat)
		a.registerInput("mdstat", input, opts, err)
	}

	if a.config.IPMI.Enable {
		gatherer := ipmi.New(a.config.IPMI, a.commandRunner, makeFactCallback("ipmi_installed"))

		_, err := a.gathererRegistry.RegisterGatherer(
			registry.RegistrationOption{
				Description: "IPMI metrics",
				JitterSeed:  0,
				MinInterval: time.Minute,
			},
			gatherer,
		)
		if err != nil {
			logger.V(1).Printf("unable to add IPMI input: %v", err)
		}
	}

	if a.config.SSACLI.Enable {
		gatherer := ssacli.New(a.config.SSACLI, a.commandRunner)

		_, err := a.gathererRegistry.RegisterGatherer(
			registry.RegistrationOption{
				Description: "HP ssacli metrics",
				JitterSeed:  0,
				MinInterval: time.Minute,
			},
			gatherer,
		)
		if err != nil {
			logger.V(1).Printf("unable to add ssacli input: %v", err)
		}
	}

	input, opts, err := temp.New()
	a.registerInput("Temp", input, opts, err)

	a.vSphereManager.RegisterGatherers(ctx, a.config.VSphere, a.gathererRegistry.RegisterGatherer, a.state, a.factProvider)
}

// Register a single input.
func (a *agent) registerInput(name string, input telegraf.Input, opts registry.RegistrationOption, err error) {
	if err != nil {
		if errors.Is(err, inputs.ErrMissingCommand) {
			logger.V(1).Printf("input %s: %v", name, err)
		} else {
			logger.Printf("Failed to create input %s: %v", name, err)
		}

		return
	}

	if opts.Description == "" {
		opts.Description = "Input " + name
	}

	_, err = a.gathererRegistry.RegisterInput(
		opts,
		input,
	)
	if err != nil {
		logger.Printf("Failed to register input %s: %v", name, err)
	}
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
					defer crashreport.ProcessPanic()

					a.waitAndRefreshPendingUpdates(ctx)

					l.Lock()
					systemUpdateMetricPending = false //nolint: wsl_v5
					l.Unlock()                        //nolint: wsl_v5
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
			a.commandRunner,
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
	diskFilter, err := config.NewDiskIOMatcher(a.config)
	if err != nil {
		a.addWarnings(err)

		return
	}

	return inputs.CollectorConfig{
		DFRootPath:      a.hostRootPath,
		NetIfMatcher:    config.NewNetworkInterfaceMatcher(a.config),
		IODiskMatcher:   diskFilter,
		DFPathMatcher:   config.NewDFPathMatcher(a.config),
		DFIgnoreFSTypes: a.config.DF.IgnoreFSType,
	}, nil
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

func (a *agent) crashReportManagement(ctx context.Context) error {
	crashreport.BundleCrashReportFiles(ctx, a.config.Agent.MaxCrashReportsCount)
	crashreport.PurgeCrashReports(a.config.Agent.MaxCrashReportsCount)

	return nil
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

	failingCount := 0

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
		case time.Since(lastHealthCheck) > 15*time.Minute && failingCount < 3:
			logger.V(2).Printf("Healthcheck are no longer running. Last run was at %s", lastHealthCheck.Format(time.RFC3339))

			failingCount++
		case time.Since(lastHealthCheck) > 15*time.Minute && failingCount >= 3:
			logger.Printf("Healthcheck are no longer running. Last run was at %s", lastHealthCheck.Format(time.RFC3339))
			// We don't know how big the buffer needs to be to collect
			// all the goroutines. Use 2MB buffer which hopefully is enough
			buffer := make([]byte, 1<<21)

			n := runtime.Stack(buffer, true)
			logger.Printf("%s", string(buffer[:n]))
			logger.Printf("Glouton seems unhealthy, killing myself")
			panic("Glouton seems unhealthy (health check is no longer running), killing myself")
		default:
			failingCount = 0
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

		if a.gathererRegistry != nil {
			a.gathererRegistry.HealthCheck()
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

func (a *agent) processUpdateMessage(ctx context.Context) error {
	var wg sync.WaitGroup

	chanDiscovery := a.discovery.Subscribe(ctx)

	wg.Add(1)

	go func() {
		defer crashreport.ProcessPanic()
		defer wg.Done()

		for services := range chanDiscovery {
			a.updatedDiscovery(ctx, services)
		}
	}()

	<-ctx.Done()

	wg.Wait()

	return nil
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
		defer crashreport.ProcessPanic()
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

			switch ev.Type { //nolint: exhaustive
			case facts.EventTypeKill, facts.EventTypeStop:
				checkIDs := a.discovery.GetCheckIDsForContainer(ev.ContainerID)
				for _, checkID := range checkIDs {
					// Wait a bit; other events (delete) may arrive soon.
					a.gathererRegistry.DelayRegExec(checkID, time.Now().Add(20*time.Second))
				}
			case facts.EventTypeDelete:
				checkIDs := a.discovery.GetCheckIDsForContainer(ev.ContainerID)
				for _, checkID := range checkIDs {
					a.gathererRegistry.Unregister(checkID)
				}
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
		status.StatusDescription = "Unknown health status " + message
	}

	a.gathererRegistry.WithTTL(5*time.Minute).PushPoints(ctx, []types.MetricPoint{
		{
			Labels: map[string]string{
				types.LabelName:              "container_health_status",
				types.LabelMetaContainerName: container.ContainerName(),
				types.LabelItem:              container.ContainerName(),
				types.LabelMetaContainerID:   container.ID(),
			},
			Annotations: types.MetricAnnotations{
				Status:      status,
				ContainerID: container.ID(),
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

	// Some discovery requests ask for a second discovery in 1 minutes.
	// The second discovery allows to discover services that are slow to start
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

func (a *agent) updatedDiscovery(ctx context.Context, services []discovery.Service) {
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

	err := a.rebuildDynamicMetricAllowDenyList(services)
	if err != nil {
		logger.V(2).Printf("Error during dynamic Filter rebuild: %v", err)
	}

	if a.logProcessManager != nil {
		containers, err := a.containerRuntime.Containers(ctx, time.Hour, false)
		if err != nil {
			logger.V(1).Printf("Failed to retrieve containers: %v", err)
		}

		var (
			logServices   []discovery.Service
			logContainers []facts.Container
		)

		if a.config.Log.OpenTelemetry.AutoDiscovery {
			logServices = services
			logContainers = containers
		} else {
			for _, ctr := range containers {
				logEnableStr, found := facts.LabelsAndAnnotations(ctr)["glouton.log_enable"]
				if found {
					logEnable, err := strconv.ParseBool(strings.ToLower(logEnableStr))
					if err != nil {
						logger.V(1).Printf("Failed to parse value of 'glouton.log_enable' for container %s (%s): %v", ctr.ContainerName(), ctr.ID(), err)

						continue
					}

					if logEnable {
						logContainers = append(logContainers, ctr)
					}
				}
			}
		}

		a.logProcessManager.HandleLogsFromDynamicSources(ctx, logServices, logContainers)
	}
}

func (a *agent) handleTrigger(ctx context.Context) {
	runDiscovery, runFact, runSystemUpdateMetric := a.cleanTrigger()
	if runDiscovery {
		a.discovery.TriggerUpdate()

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
		a.commandRunner,
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

func (a *agent) processesLister() *facts.ProcessProvider {
	var psLister facts.ProcessLister

	if version.IsLinux() {
		psLister = process.NewProcessLister(a.hostRootPath)
	} else {
		psLister = facts.NewPsUtilLister("")
	}

	return facts.NewProcess(
		psLister,
		a.containerRuntime,
	)
}

// DiagnosticPage return useful information to troubleshoot issue.
func (a *agent) DiagnosticPage(ctx context.Context) string {
	builder := &strings.Builder{}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	raceDetectorMsg := ""
	if version.RaceDetectorEnable() {
		raceDetectorMsg = " with race-detector enabled"
	}

	fmt.Fprintf(
		builder,
		"Run diagnostic at %s with Glouton version %s (commit %s built using Go %s%s)\n",
		time.Now().Format(time.RFC3339),
		version.Version,
		version.BuildHash,
		runtime.Version(),
		raceDetectorMsg,
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

func (a *agent) writeDiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) (err error) {
	var success bool

	startTime := time.Now()

	defer func() {
		writer, wErr := archive.Create("meta")
		if wErr != nil {
			logger.V(1).Println("Failed to add meta file to diagnostic archive:", wErr)

			return
		}

		endTime := time.Now()
		total := endTime.Sub(startTime)

		fmt.Fprintf(writer, "Start: %s\nEnd: %s\nTotal: %s (Diagnostic successfully completed: %t)\n", startTime, endTime, total, success)

		if err != nil {
			fmt.Fprintln(writer, "Error:", err)
		}
	}()

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
		a.diagnosticVSphere,
		a.metricFilter.DiagnosticArchive,
		a.gathererRegistry.DiagnosticArchive,
		a.rulesManager.DiagnosticArchive,
		a.reloadState.DiagnosticArchive,
		a.vethProvider.DiagnosticArchive,
		a.threshold.DiagnosticThresholds,
		a.threshold.DiagnosticStatusStates,
		smart.DiagnosticArchive,
		a.containerRuntime.DiagnosticArchive,
	}

	if a.bleemeoConnector != nil {
		modules = append(modules, a.bleemeoConnector.DiagnosticArchive)
	}

	if a.monitorManager != nil {
		modules = append(modules, a.monitorManager.DiagnosticArchive)
	}

	if a.mqtt != nil {
		modules = append(modules, a.mqtt.DiagnosticArchive)
	}

	if a.fluentbitManager != nil {
		modules = append(modules, a.fluentbitManager.DiagnosticArchive)
	}

	if a.logProcessManager != nil {
		modules = append(modules, a.logProcessManager.DiagnosticArchive)
	}

	for _, f := range modules {
		if err = f(ctx, archive); err != nil {
			return err
		}

		if ctx.Err() != nil {
			break
		}
	}

	success = true

	return ctx.Err()
}

func (a *agent) diagnosticGlobalInfo(ctx context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("goroutines.txt")
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

	err = crashreport.AddStderrLogToArchive(archive)
	if err != nil {
		logger.V(1).Printf("Can't add stderr log to archive: %v", err)
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

	file, err = archive.Create("memstats.txt")
	if err != nil {
		return err
	}

	if err := writeMemstat(file); err != nil {
		return err
	}

	file, err = archive.Create("diagnostic.txt")
	if err != nil {
		return err
	}

	_, err = file.Write([]byte(a.DiagnosticPage(ctx)))
	if err != nil {
		return err
	}

	return nil
}

func formatBytes(size uint64) string {
	scales := []string{"bytes", "KiB", "MiB", "GiB", "TiB", "PiB"}

	value := float64(size)

	i := 0
	for i < len(scales)-1 && math.Abs(value) >= 1024 {
		i++

		value /= 1024
	}

	return fmt.Sprintf("%.2f %s", value, scales[i])
}

func writeMemstat(writer io.Writer) error {
	var stat runtime.MemStats

	runtime.ReadMemStats(&stat)

	_, err := fmt.Fprintf(writer, "Heap in-use %s, object in-use %d\n", formatBytes(stat.HeapAlloc), stat.HeapObjects)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(writer, "Total allocated %s, object %d\n", formatBytes(stat.TotalAlloc), stat.Mallocs)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(
		writer,
		"Heap details Sys (OS allocated) %s, InUse %s, Idle %s (released %s)\n",
		formatBytes(stat.HeapSys),
		formatBytes(stat.HeapInuse),
		formatBytes(stat.HeapIdle),
		formatBytes(stat.HeapReleased),
	)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(
		writer,
		"Other memory Sys (OS allocated) / InUse: Stack %s / %s; MSpans %s / %s; MCache %s / %s\n",
		formatBytes(stat.StackSys),
		formatBytes(stat.StackInuse),
		formatBytes(stat.MSpanSys),
		formatBytes(stat.MSpanInuse),
		formatBytes(stat.MCacheSys),
		formatBytes(stat.MCacheInuse),
	)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(
		writer,
		"Other memory BuckHashSys %s, GCSys %s, OtherSys %s\n",
		formatBytes(stat.BuckHashSys),
		formatBytes(stat.GCSys),
		formatBytes(stat.OtherSys),
	)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(
		writer,
		"Total: Sys %s; Sum of all *Sys %s, RSS %s\n",
		formatBytes(stat.Sys),
		formatBytes(stat.HeapSys+stat.StackSys+stat.MSpanSys+stat.MCacheSys+stat.BuckHashSys+stat.GCSys+stat.OtherSys),
		formatBytes(getResidentMemoryOfSelf()),
	)
	if err != nil {
		return err
	}

	return nil
}

func (a *agent) diagnosticGloutonState(_ context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("glouton-state.json")
	if err != nil {
		return err
	}

	persistentSize, cacheSize := a.state.FileSizes()

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
		PahoLastPingCheckAt       time.Time
		PathToStateDir            string
		PersistentStateSize       int
		CacheStateSize            int
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
		PahoLastPingCheckAt:       a.pahoLogWrapper.LastPingAt(),
		PathToStateDir:            a.stateDir,
		PersistentStateSize:       persistentSize,
		CacheStateSize:            cacheSize,
	}

	a.triggerLock.Unlock()
	a.l.Unlock()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

func (a *agent) diagnosticJitter(_ context.Context, archive types.ArchiveWriter) error {
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

	if a.config.Kubernetes.Enable && a.bleemeoConnector != nil {
		if a.bleemeoConnector.AgentIsClusterLeader() {
			fmt.Fprintf(file, "This agent is the kubernetes cluster leader.\n\n")
		} else {
			fmt.Fprintf(file, "This agent is not the kubernetes cluster leader.\n\n")
		}
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

			imgTag, err := c.ImageTags(ctx)

			var formattedImgTag string

			if err != nil {
				formattedImgTag = fmt.Sprintf("err=%v", err)
			} else {
				formattedImgTag = fmt.Sprintf("%v", imgTag)
			}

			fmt.Fprintf(
				file,
				"Name=%s, ID=%s, ignored=%v, IP=%s, listenAddr=%v,\n"+
					"\tState=%v, CreatedAt=%v, StartedAt=%v, FinishedAt=%v, StoppedAndReplaced=%v\n"+
					"\tHealth=%v (%s) K8S=%v/%v\n"+
					"\tLogPath=%v ImageTags=%v\n",
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
				c.LogPath(),
				formattedImgTag,
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

func (a *agent) diagnosticConfig(_ context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("config.yaml")
	if err != nil {
		return err
	}

	enc := yaml.NewEncoder(file)

	fmt.Fprintln(file, "# This file contains in-memory configuration used by Glouton. Value from default, files and environment.")
	enc.SetIndent(4)

	err = enc.Encode(config.Dump(a.config))
	if err != nil {
		fmt.Fprintf(file, "# error: %v\n", err)
	}

	err = enc.Close()
	if err != nil {
		fmt.Fprintf(file, "# error: %v\n", err)
	}

	return nil
}

func (a *agent) diagnosticFilterResult(_ context.Context, archive types.ArchiveWriter) error {
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

func (a *agent) diagnosticVSphere(ctx context.Context, archive types.ArchiveWriter) error {
	associationFn := func(context.Context, []bleemeoTypes.VSphereDevice) (map[string]string, error) {
		return nil, nil //nolint:nilnil
	}

	if a.bleemeoConnector != nil {
		associationFn = a.bleemeoConnector.GetAllVSphereAssociations
	}

	return a.vSphereManager.DiagnosticVSphere(ctx, archive, associationFn)
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
func (a *agent) getWarnings() prometheus.MultiError {
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
		logger.Printf("The agent is running in a container but GLOUTON_DF_HOST_MOUNT_POINT is unset. Some information will be missing")

		return
	}

	if _, err := os.Stat(hostRootPath); os.IsNotExist(err) {
		logger.Printf("The agent is running in a container but host / partition is not mounted on %#v. Some information will be missing", hostRootPath)
		logger.Printf("Hint: to fix this issue when using Docker, add \"-v /:%v:ro\" when running the agent", hostRootPath)

		return
	}

	if hostRootPath != "" && hostRootPath != "/" {
		if os.Getenv("HOST_VAR") == "" {
			// gopsutil will use HOST_VAR as prefix to host /var
			// It's used at least for reading the number of connected user from /var/run/utmp
			_ = os.Setenv("HOST_VAR", filepath.Join(hostRootPath, "var"))

			// ... but /var/run is usually a symlink to /run.
			varRun := filepath.Join(hostRootPath, "var/run")

			target, err := os.Readlink(varRun)
			if err == nil && target == "/run" {
				_ = os.Setenv("HOST_VAR", hostRootPath)
			}
		}

		if os.Getenv("HOST_ETC") == "" {
			_ = os.Setenv("HOST_ETC", filepath.Join(hostRootPath, "etc"))
		}

		if os.Getenv("HOST_PROC") == "" {
			_ = os.Setenv("HOST_PROC", filepath.Join(hostRootPath, "proc"))
		}

		if os.Getenv("HOST_SYS") == "" {
			_ = os.Setenv("HOST_SYS", filepath.Join(hostRootPath, "sys"))
		}

		if os.Getenv("HOST_RUN") == "" {
			_ = os.Setenv("HOST_RUN", filepath.Join(hostRootPath, "run"))
		}

		if os.Getenv("HOST_DEV") == "" {
			_ = os.Setenv("HOST_DEV", filepath.Join(hostRootPath, "dev"))
		}

		if os.Getenv("HOST_MOUNT_PREFIX") == "" {
			_ = os.Setenv("HOST_MOUNT_PREFIX", hostRootPath)
		}
	}
}

// prometheusConfigToURLs convert metric.prometheus.targets config to a list of targets.
// It returns the targets and some warnings.
//
// See tests for the expected config.
func prometheusConfigToURLs(configTargets []config.PrometheusTarget) ([]*scrapper.Target, prometheus.MultiError) {
	var warnings prometheus.MultiError

	targets := make([]*scrapper.Target, 0, len(configTargets))

	for _, configTarget := range configTargets {
		targetURL, err := url.Parse(configTarget.URL)
		if err != nil {
			warnings.Append(fmt.Errorf("%w: invalid prometheus target URL: %s", config.ErrInvalidValue, err))

			continue
		}

		target := &scrapper.Target{
			ExtraLabels: map[string]string{
				types.LabelMetaScrapeJob: configTarget.Name,
				// HostPort could be empty, but this ExtraLabels is used by Registry which
				// correctly handles empty values (drop the label).
				types.LabelMetaScrapeInstance: scrapper.HostPort(targetURL),
			},
			URL:       targetURL,
			AllowList: configTarget.AllowMetrics,
			DenyList:  configTarget.DenyMetrics,
		}

		targets = append(targets, target)
	}

	return targets, warnings
}
