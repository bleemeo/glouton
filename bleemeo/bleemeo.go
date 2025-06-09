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

package bleemeo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/common"
	"github.com/bleemeo/glouton/bleemeo/internal/filter"
	"github.com/bleemeo/glouton/bleemeo/internal/mqtt"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer"
	synchronizerTypes "github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	"github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/exporter/snmp"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/prometheus/registry"
	gloutonTypes "github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/archivewriter"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"golang.org/x/oauth2"
)

var (
	errBadOption         = errors.New("bad option")
	errAgentTypeNotFound = errors.New("agent type not found")
	errAgentIDNotFound   = errors.New("agent ID not found")
)

// reloadState implements the types.BleemeoReloadState interface.
type reloadState struct {
	mqtt          types.MQTTReloadState
	nextFullSync  time.Time
	fullSyncCount int
	token         *oauth2.Token
}

func NewReloadState() types.BleemeoReloadState {
	return &reloadState{}
}

func (rs *reloadState) MQTTReloadState() types.MQTTReloadState {
	return rs.mqtt
}

func (rs *reloadState) SetMQTTReloadState(client types.MQTTReloadState) {
	rs.mqtt = client
}

func (rs *reloadState) NextFullSync() time.Time {
	return rs.nextFullSync
}

func (rs *reloadState) SetNextFullSync(t time.Time) {
	rs.nextFullSync = t
}

func (rs *reloadState) FullSyncCount() int {
	return rs.fullSyncCount
}

func (rs *reloadState) SetFullSyncCount(count int) {
	rs.fullSyncCount = count
}

func (rs *reloadState) Token() *oauth2.Token {
	return rs.token
}

func (rs *reloadState) SetToken(token *oauth2.Token) {
	rs.token = token
}

func (rs *reloadState) Close() {
	if rs.mqtt != nil {
		rs.mqtt.Close()
	}
}

// Connector manages the connection between the Agent and Bleemeo.
type Connector struct {
	option types.GlobalOption

	cache        *cache.Cache
	pushAppender *model.BufferAppender
	sync         *synchronizer.Synchronizer
	mqtt         *mqtt.Client
	mqttRestart  chan any

	l                          sync.RWMutex
	lastKnownReport            time.Time
	lastMQTTRestart            time.Time
	mqttReportConsecutiveError int
	disabledUntil              time.Time
	disableReason              types.DisableReason

	// initialized indicates whether the mqtt connector can be started
	initialized bool
}

// New creates a new Connector.
func New(option types.GlobalOption) (c *Connector, err error) {
	c = &Connector{
		option:       option,
		cache:        cache.Load(option.State),
		mqttRestart:  make(chan any, 1),
		pushAppender: model.NewBufferAppender(),
	}

	c.sync = synchronizer.New(synchronizerTypes.Option{
		GlobalOption:                c.option,
		PushAppender:                c.pushAppender,
		Cache:                       c.cache,
		UpdateConfigCallback:        c.updateConfig,
		DisableCallback:             c.disableCallback,
		SetInitialized:              c.setInitialized,
		SetBleemeoInMaintenanceMode: c.setMaintenance,
		SetBleemeoInSuspendedMode:   c.setSuspended,
		IsMqttConnected:             c.Connected,
	})

	if option.SNMPOnlineTarget == nil {
		return c, fmt.Errorf("%w: missing SNMPOnlineTarget function", errBadOption)
	}

	return c, nil
}

func (c *Connector) setInitialized() {
	c.l.Lock()
	defer c.l.Unlock()

	c.initialized = true
}

func (c *Connector) isInitialized() bool {
	c.l.RLock()
	defer c.l.RUnlock()

	return c.initialized
}

// ApplyCachedConfiguration reload metrics units & threshold & monitors from the cache.
func (c *Connector) ApplyCachedConfiguration() {
	c.l.RLock()
	disabledUntil := c.disabledUntil
	c.l.RUnlock()

	if time.Now().Before(disabledUntil) {
		return
	}

	c.sync.UpdateUnitsAndThresholds(true)

	if c.option.Config.Blackbox.Enable {
		if err := c.sync.ApplyMonitorUpdate(); err != nil {
			// we just log the error, as we will try to run the monitors later anyway
			logger.V(2).Printf("Couldn't start probes now, will retry later: %v", err)
		}
	}

	currentConfig, ok := c.cache.CurrentAccountConfig()

	if ok && c.option.UpdateMetricResolution != nil && currentConfig.AgentConfigByName[bleemeo.AgentType_Agent].MetricResolution != 0 {
		c.option.UpdateMetricResolution(currentConfig.AgentConfigByName[bleemeo.AgentType_Agent].MetricResolution, currentConfig.AgentConfigByName[bleemeo.AgentType_SNMP].MetricResolution)
	}
}

func (c *Connector) initMQTT(previousPoint []gloutonTypes.MetricPoint) {
	c.l.Lock()
	defer c.l.Unlock()

	_, password := c.option.State.BleemeoCredentials()

	c.mqtt = mqtt.New(
		mqtt.Option{
			GlobalOption:            c.option,
			Cache:                   c.cache,
			AgentID:                 types.AgentID(c.AgentID()),
			AgentPassword:           password,
			UpdateConfigCallback:    c.sync.NotifyConfigUpdate,
			UpdateMetrics:           c.sync.UpdateMetrics,
			UpdateMaintenance:       c.sync.UpdateMaintenance,
			UpdateMonitor:           c.sync.UpdateMonitor,
			HandleDiagnosticRequest: c.HandleDiagnosticRequest,
			InitialPoints:           previousPoint,
			GetToken:                c.sync.VerifyAndGetToken,
			LastMetricActivation:    c.sync.LastMetricActivation,
		},
	)

	// if the connector is disabled, disable mqtt for the same period
	if c.disabledUntil.After(time.Now()) {
		c.disableMqtt(c.mqtt, c.disableReason, c.disabledUntil)
	}

	if c.sync.IsMaintenance() {
		c.mqtt.SuspendSending(true)
	}
}

func (c *Connector) setMaintenance(ctx context.Context, maintenance bool) {
	if maintenance {
		logger.V(0).Println("Bleemeo: read only/maintenance mode enabled")
	} else if !maintenance && c.sync.IsMaintenance() {
		logger.V(0).Println("Bleemeo: read only/maintenance mode is now disabled, will resume sending metrics")
	}

	c.l.RLock()
	defer c.l.RUnlock()

	c.sync.SetMaintenance(ctx, maintenance)

	if c.mqtt != nil {
		c.mqtt.SuspendSending(maintenance)
	}
}

// setSuspended suspends sending on MQTT and point buffering.
func (c *Connector) setSuspended(suspended bool) {
	if suspended {
		logger.V(0).Println("Bleemeo: read only/suspended mode enabled")
	} else {
		logger.V(0).Println("Bleemeo: read only/suspended mode is now disabled, will resume sending metrics")
	}

	c.l.Lock()
	defer c.l.Unlock()

	if c.mqtt != nil {
		c.mqtt.SuspendSending(suspended)
		c.mqtt.SuspendBuffering(suspended)
	}
}

func (c *Connector) mqttRestarter(ctx context.Context) error {
	var (
		wg             sync.WaitGroup
		mqttErr        error
		l              sync.Mutex
		previousPoints []gloutonTypes.MetricPoint
	)

	subCtx, cancel := context.WithCancel(ctx)

	c.l.RLock()
	mqttRestart := c.mqttRestart
	c.l.RUnlock()

	if mqttRestart == nil {
		cancel() // just a matter of principle

		return nil
	}

	select {
	case mqttRestart <- nil:
	default:
	}

	for {
		select {
		case <-mqttRestart:
			cancel()

			subCtx, cancel = context.WithCancel(ctx) //nolint: fatcontext

			c.l.Lock()

			if c.mqtt != nil {
				// Try to retrieve pending points
				resultChan := make(chan []gloutonTypes.MetricPoint, 1)

				go func() {
					defer crashreport.ProcessPanic()

					resultChan <- c.mqtt.PopPoints(true)
				}()

				select {
				case previousPoints = <-resultChan:
				case <-time.After(10 * time.Second):
				}
			}

			c.mqtt = nil

			c.l.Unlock()

			c.initMQTT(previousPoints)
			previousPoints = nil

			wg.Add(1)

			go func() {
				defer crashreport.ProcessPanic()
				defer wg.Done()

				err := c.mqtt.Run(subCtx)

				l.Lock()

				if mqttErr == nil {
					mqttErr = err
				}

				l.Unlock()
			}()
		case <-ctx.Done():
			c.l.Lock()
			close(c.mqttRestart)
			c.mqttRestart = nil
			c.l.Unlock()

			cancel()
			wg.Wait()

			return mqttErr
		}
	}
}

// Run run the Connector.
func (c *Connector) Run(ctx context.Context) error {
	defer c.cache.Save()

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg               sync.WaitGroup
		syncErr, mqttErr error
	)

	wg.Add(1)

	go func() {
		defer crashreport.ProcessPanic()
		defer wg.Done()
		defer cancel()

		syncErr = c.sync.Run(subCtx)
	}()

	wg.Add(1)

	go func() {
		defer crashreport.ProcessPanic()
		defer wg.Done()
		defer cancel()

		<-subCtx.Done()

		logger.V(2).Printf("Bleemeo connector stopping")
	}()

	for ctx.Err() == nil {
		if c.AgentID() != "" && c.isInitialized() {
			wg.Add(1)

			go func() {
				defer crashreport.ProcessPanic()
				defer wg.Done()
				defer cancel()

				mqttErr = c.mqttRestarter(ctx)
			}()

			break
		}

		select {
		case <-time.After(5 * time.Second):
		case <-subCtx.Done():
		}
	}

	wg.Wait()
	logger.V(2).Printf("Bleemeo connector stopped")

	if syncErr != nil {
		return syncErr
	}

	return mqttErr
}

// UpdateContainers request to update a containers.
func (c *Connector) UpdateContainers() {
	c.l.RLock()

	disabled := time.Now().Before(c.disabledUntil)

	c.l.RUnlock()

	if disabled {
		return
	}

	c.sync.UpdateContainers()
}

// UpdateInfo request to update a info, which include the time_drift.
func (c *Connector) UpdateInfo() {
	// It's updateInfo which disable for time drift. Temporary re-enable to
	// run it.
	c.clearDisable(types.DisableTimeDrift)

	c.l.RLock()

	disabled := time.Now().Before(c.disabledUntil)

	c.l.RUnlock()

	if disabled {
		return
	}

	c.sync.UpdateInfo()
}

// UpdateMonitors trigger a reload of the monitors.
func (c *Connector) UpdateMonitors() {
	c.l.Lock()
	sync := c.sync
	c.l.Unlock()

	sync.UpdateMonitors()
}

func (c *Connector) RelabelHook(ctx context.Context, labels map[string]string) (newLabel map[string]string, retryLater bool) {
	agentID := c.AgentID()

	if agentID == "" {
		return labels, false
	}

	labels[gloutonTypes.LabelMetaBleemeoUUID] = agentID

	// For SNMP metrics, set the agent ID to the SNMP agent ID.
	if labels[gloutonTypes.LabelMetaSNMPTarget] != "" {
		var target *snmp.Target

		for _, t := range c.option.SNMP {
			if t.Address() == labels[gloutonTypes.LabelMetaSNMPTarget] {
				target = t

				break
			}
		}

		if target == nil {
			// set retryLater which will cause metrics from the gatherer to be ignored.
			// This hook will be automatically re-called every 2 minutes.
			return labels, true
		}

		snmpTypeID, err := c.agentTypeID(bleemeo.AgentType_SNMP)
		if err != nil {
			return labels, true
		}

		agent, err := c.sync.FindSNMPAgent(ctx, target, snmpTypeID, c.cache.AgentsByUUID())
		if err != nil {
			logger.V(2).Printf("FindSNMPAgent failed: %v", err)

			return labels, true
		}

		labels[gloutonTypes.LabelMetaBleemeoTargetAgentUUID] = agent.ID
	}

	// For Kubernetes cluster metrics, set the agent ID to the Kubernetes agent ID.
	if clusterName := labels[gloutonTypes.LabelMetaKubernetesCluster]; clusterName != "" {
		kubernetesAgentID, err := c.kubernetesAgentID(clusterName)
		if err != nil {
			logger.V(1).Printf("Kubernetes agent not found: %s", err)

			if errors.Is(err, errAgentIDNotFound) {
				// The cluster may have been renamed,
				// and the cache may not yet contain an agent with the new FQDN.
				c.sync.UpdateK8SAgentList()
			}

			// Kubernetes agent not found, retry later.
			return labels, true
		}

		labels[gloutonTypes.LabelMetaBleemeoTargetAgent] = clusterName
		labels[gloutonTypes.LabelMetaBleemeoTargetAgentUUID] = kubernetesAgentID
	}

	if vSphere := labels[gloutonTypes.LabelMetaVSphere]; vSphere != "" {
		moid := labels[gloutonTypes.LabelMetaVSphereMOID]

		device := c.option.FindVSphereDevice(ctx, vSphere, moid)
		if device == nil {
			return labels, true
		}

		vSphereAgentTypeID, ok := c.sync.GetVSphereAgentType(device.Kind())
		if !ok {
			return labels, true
		}

		agent, err := c.sync.FindVSphereAgent(ctx, device, vSphereAgentTypeID, c.cache.AgentsByUUID())
		if err != nil {
			logger.V(2).Printf("FindVSphereAgent failed: %v", err)

			return labels, true
		}

		labels[gloutonTypes.LabelMetaBleemeoTargetAgentUUID] = agent.ID
	}

	// Add ServiceUUID if metrics is associated with a service.
	if srvName, ok := labels[gloutonTypes.LabelMetaServiceName]; ok {
		srvKey := common.ServiceNameInstance{Name: srvName, Instance: labels[gloutonTypes.LabelMetaServiceInstance]}

		servicesByKey := c.cache.ServiceLookupFromList()

		service, ok := servicesByKey[srvKey]
		if !ok {
			return labels, true
		}

		labels[gloutonTypes.LabelMetaServiceUUID] = service.ID
	}

	// Tells UpdateDelayHook that labels are good
	labels[gloutonTypes.LabelMetaBleemeoRelabelHookOk] = "1"

	return labels, false
}

func (c *Connector) getAccountConfigAndAgentType(labels map[string]string) (string, string, error) {
	if agentID, ok := labels[gloutonTypes.LabelInstanceUUID]; ok {
		if agent, ok := c.cache.AgentsByUUID()[agentID]; ok {
			return agent.CurrentAccountConfigID, agent.AgentType, nil
		}

		// For private probe, we don't have access to the Agent, so try accessing the AccountConfig from the Service itself
		if monitor, ok := c.cache.MonitorsByAgentUUID()[types.AgentID(agentID)]; ok {
			typeID, err := c.agentTypeID(bleemeo.AgentType_Monitor)
			if err != nil {
				return "", "", err
			}

			return monitor.AccountConfig, typeID, nil
		}

		return "", "", fmt.Errorf("%w: %s is not in the cache", errAgentIDNotFound, agentID)
	}

	return "", "", fmt.Errorf("%w: missing LabelInstanceUUID in labels %v", errBadOption, labels)
}

func (c *Connector) UpdateDelayHook(labels map[string]string) (time.Duration, bool) {
	if _, ok := labels[gloutonTypes.LabelMetaBleemeoRelabelHookOk]; !ok {
		return 0, true
	}

	accountConfig, agentType, err := c.getAccountConfigAndAgentType(labels)
	if err != nil {
		logger.V(1).Printf("Can't find metric resolution for gatherer: %v", err)

		return 0, true
	}

	possibleAgentTypes := make(map[string]bool)

	if strTypes, hasAgentTypes := labels[gloutonTypes.LabelMetaAgentTypes]; hasAgentTypes {
		// When a gatherer handles agents of multiple types,
		// we need to retrieve the metric resolution of each.
		// Therefore, we're able to pick the right interval (a.k.a the smallest) between them all.
		agentTypes := strings.Split(strTypes, ",")

		for _, agentType := range c.cache.AgentTypes() {
			for _, agentTypeStr := range agentTypes {
				if agentTypeStr == string(agentType.Name) {
					possibleAgentTypes[agentType.ID] = true
				}
			}
		}
	} else {
		possibleAgentTypes[agentType] = true
	}

	var minResolution int

	for _, cfg := range c.cache.AgentConfigs() {
		if cfg.AccountConfig == accountConfig && possibleAgentTypes[cfg.AgentType] {
			if minResolution == 0 || cfg.MetricResolution < minResolution {
				minResolution = cfg.MetricResolution
			}
		}
	}

	if minResolution == 0 {
		logger.V(1).Printf("Can't find metric resolution for account config %s & agent type %s (not found in cache)", accountConfig, agentType)
	}

	return time.Duration(minResolution) * time.Second, false
}

func (c *Connector) kubernetesAgentID(clusterName string) (string, error) {
	kubernetesAgentType, err := c.agentTypeID(bleemeo.AgentType_K8s)
	if err != nil {
		return "", err
	}

	for _, agent := range c.cache.AgentsByUUID() {
		if agent.AgentType != kubernetesAgentType {
			continue
		}

		if clusterName != "" && agent.FQDN == clusterName {
			return agent.ID, nil
		}
	}

	return "", fmt.Errorf("%w: no kubernetes agent with FQDN '%s'", errAgentIDNotFound, clusterName)
}

// agentTypeID returns the ID of the given agent type.
func (c *Connector) agentTypeID(agentType bleemeo.AgentType) (string, error) {
	for _, t := range c.cache.AgentTypes() {
		if t.Name == agentType {
			return t.ID, nil
		}
	}

	return "", fmt.Errorf("%w: %s", errAgentTypeNotFound, agentType)
}

// DiagnosticPage return useful information to troubleshoot issue.
func (c *Connector) DiagnosticPage() string {
	builder := &strings.Builder{}

	registrationKey := []rune(c.option.Config.Bleemeo.RegistrationKey)
	for i := range registrationKey {
		if i >= 6 && i < len(registrationKey)-4 {
			registrationKey[i] = '*'
		}
	}

	fmt.Fprintf(
		builder,
		"Bleemeo account ID is %#v and registration key is %#v\n",
		c.AccountID(), string(registrationKey),
	)

	if c.AgentID() == "" {
		fmt.Fprintln(builder, "Glouton is not registered with Bleemeo")
	} else {
		fmt.Fprintf(builder, "Glouton is registered with Bleemeo with ID %v\n", c.AgentID())
	}

	lastReport := c.LastReport().Format(time.RFC3339)

	if c.Connected() {
		fmt.Fprintf(builder, "Glouton is currently connected. Last report to Bleemeo at %s\n", lastReport)
	} else {
		fmt.Fprintf(builder, "Glouton is currently NOT connected. Last report to Bleemeo at %s\n", lastReport)
	}

	c.l.Lock()
	if time.Now().Before(c.disabledUntil) {
		fmt.Fprintf(
			builder,
			"Glouton connection to Bleemeo is disabled until %s (%v remain) due to '%v'\n",
			c.disabledUntil.Format(time.RFC3339),
			time.Until(c.disabledUntil).Truncate(time.Second),
			c.disableReason,
		)
	}

	if now := time.Now(); c.disabledUntil.After(now) {
		fmt.Fprintf(builder, "The Bleemeo connector is currently disabled until %v due to '%v'\n", c.disabledUntil, c.disableReason)
	}

	if c.sync.IsMaintenance() {
		fmt.Fprintln(builder, "The Bleemeo connector is currently in read-only/maintenance mode, not syncing nor sending any metric")
	}

	mqtt := c.mqtt
	c.l.Unlock()

	syncPage := make(chan string)
	mqttPage := make(chan string)

	go func() {
		defer crashreport.ProcessPanic()

		syncPage <- c.sync.DiagnosticPage()
	}()

	go func() {
		defer crashreport.ProcessPanic()

		if mqtt == nil {
			mqttPage <- "MQTT connector is not (yet) initialized\n"
		} else {
			mqttPage <- c.mqtt.DiagnosticPage()
		}
	}()

	builder.WriteString(<-syncPage)
	builder.WriteString(<-mqttPage)

	return builder.String()
}

// DiagnosticArchive add to a zipfile useful diagnostic information.
func (c *Connector) DiagnosticArchive(ctx context.Context, archive gloutonTypes.ArchiveWriter) error {
	c.l.Lock()
	mqtt := c.mqtt
	c.l.Unlock()

	if mqtt != nil {
		if err := mqtt.DiagnosticArchive(ctx, archive); err != nil {
			return err
		}
	}

	failed := c.cache.MetricRegistrationsFail()
	if len(failed) > 0 {
		file, err := archive.Create("metric-registration-failed.txt")
		if err != nil {
			return err
		}

		indices := make([]int, len(failed))
		for i := range indices {
			indices[i] = i
		}

		const maxSample = 100
		if len(failed) > maxSample {
			fmt.Fprintf(file, "%d metrics fail to register. The following is 50 randomly choose metrics that fail:\n", len(failed))
			indices = rand.Perm(len(failed))[:maxSample]
		} else {
			fmt.Fprintf(file, "%d metrics fail to register. The following is the fill list\n", len(failed))
			sort.Slice(indices, func(i, j int) bool {
				return failed[indices[i]].LabelsText < failed[indices[j]].LabelsText
			})
		}

		for _, i := range indices {
			row := failed[i]
			if row.LastFailKind.IsPermanentFailure() {
				fmt.Fprintf(file, "count=%d nextRetryAt=on next full sync failureKind=%v labels=%s\n", row.FailCounter, row.LastFailKind, row.LabelsText)
			} else {
				fmt.Fprintf(file, "count=%d nextRetryAt=%s failureKind=%v labels=%s\n", row.FailCounter, row.RetryAfter().Format(time.RFC3339), row.LastFailKind, row.LabelsText)
			}
		}
	}

	file, err := archive.Create("bleemeo-cache.txt")
	if err != nil {
		return err
	}

	c.diagnosticCache(file)

	return c.sync.DiagnosticArchive(ctx, archive)
}

// DiagnosticSNMPAssociation return useful information to troubleshoot issue.
func (c *Connector) DiagnosticSNMPAssociation(ctx context.Context, file io.Writer) {
	fmt.Fprintf(file, "\n# Association with Bleemeo Agent\n")

	var snmpTypeID string

	for _, t := range c.cache.AgentTypes() {
		if t.Name == bleemeo.AgentType_SNMP {
			snmpTypeID = t.ID

			break
		}
	}

	for _, t := range c.option.SNMP {
		agent, err := c.sync.FindSNMPAgent(ctx, t, snmpTypeID, c.cache.AgentsByUUID())
		if err != nil {
			fmt.Fprintf(file, " * %s => %v\n", t.String(ctx), err)
		} else {
			fmt.Fprintf(file, " * %s => %s\n", t.String(ctx), agent.ID)
		}
	}
}

func (c *Connector) diagnosticCache(file io.Writer) {
	agents := c.cache.Agents()
	agentTypes := c.cache.AgentTypes()

	fmt.Fprintf(file, "# Cache known %d agents\n", len(agents))

	for _, a := range agents {
		agentTypeName := ""

		for _, t := range agentTypes {
			if t.ID == a.AgentType {
				agentTypeName = t.DisplayName

				break
			}
		}

		fmt.Fprintf(file, "id=%s fqdn=%s type=%s (%s) accountID=%s, config=%s\n", a.ID, a.FQDN, agentTypeName, a.AgentType, a.AccountID, a.CurrentAccountConfigID)
	}

	fmt.Fprintf(file, "\n# Cache known %d agent types\n", len(agentTypes))

	for _, t := range agentTypes {
		fmt.Fprintf(file, "id=%s name=%s display_name=%s\n", t.ID, t.Name, t.DisplayName)
	}

	metrics := c.cache.Metrics()
	activeMetrics := 0

	for _, m := range metrics {
		if m.DeactivatedAt.IsZero() {
			activeMetrics++
		}
	}

	accountConfigs := c.cache.AccountConfigs()
	agentConfigs := c.cache.AgentConfigs()

	fmt.Fprintf(file, "\n# Cache known %d account config (raw)\n", len(accountConfigs))

	for _, ac := range accountConfigs {
		fmt.Fprintf(file, "%#v\n", ac)
	}

	fmt.Fprintf(file, "\n# Cache known %d agent config (raw)\n", len(agentConfigs))

	for _, ac := range agentConfigs {
		fmt.Fprintf(file, "%#v\n", ac)
	}

	gloutonAccountConfigs := c.cache.AccountConfigsByUUID()

	fmt.Fprintf(file, "\n# Structured account config\n")

	for _, ac := range gloutonAccountConfigs {
		v, err := json.MarshalIndent(ac, "", "  ")
		if err != nil {
			fmt.Fprintf(file, "err=%v\n", err)
		} else {
			fmt.Fprintf(file, "%s\n", string(v))
		}
	}

	config, ok := c.cache.CurrentAccountConfig()
	if ok {
		fmt.Fprintf(file, "\n# And current account config is\n")

		v, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			fmt.Fprintf(file, "err=%v\n", err)
		} else {
			fmt.Fprintf(file, "%s\n", string(v))
		}
	} else {
		fmt.Fprintf(file, "\n# And current account config is not yet loaded\n")
	}

	fmt.Fprintf(file, "\n# Cache known %d metrics and %d active metrics\n", len(metrics), activeMetrics)
	fmt.Fprintf(file, "\n# Cache known %d facts\n", len(c.cache.Facts()))
	fmt.Fprintf(file, "\n# Cache known %d services\n", len(c.cache.Services()))
	fmt.Fprintf(file, "\n# Cache known %d containers\n", len(c.cache.Containers()))
	fmt.Fprintf(file, "\n# Cache known %d monitors\n", len(c.cache.Monitors()))
}

func (c *Connector) GetAllVSphereAssociations(ctx context.Context, devices []types.VSphereDevice) (map[string]string, error) {
	vSphereAgentTypes, ok := c.sync.GetVSphereAgentTypes()
	if !ok {
		return map[string]string{}, errAgentTypeNotFound
	}

	associations := make(map[string]string, len(devices))

	for _, dev := range devices {
		agent, err := c.sync.FindVSphereAgent(ctx, dev, vSphereAgentTypes[dev.Kind()], c.cache.AgentsByUUID())
		if err != nil {
			return associations, err
		}

		associations[dev.Source()+dev.MOID()] = agent.ID
	}

	return associations, nil
}

// Tags returns the Tags set on Bleemeo Cloud platform.
func (c *Connector) Tags() []string {
	agent := c.cache.Agent()
	tags := make([]string, len(agent.Tags))

	for i, t := range agent.Tags {
		tags[i] = t.Name
	}

	return tags
}

// AccountID returns the Account UUID of Bleemeo
// It return the empty string if the Account UUID is not available.
func (c *Connector) AccountID() string {
	accountID := c.cache.AccountID()
	if accountID != "" {
		return accountID
	}

	return c.option.Config.Bleemeo.AccountID
}

// AgentID returns the Agent UUID of Bleemeo
// It return the empty string if the Account UUID is not available.
func (c *Connector) AgentID() string {
	agentID, _ := c.option.State.BleemeoCredentials()

	return agentID
}

func (c *Connector) AgentIsClusterLeader() bool {
	return c.cache.Agent().IsClusterLeader
}

// RegistrationAt returns the date of registration with Bleemeo API.
func (c *Connector) RegistrationAt() time.Time {
	agent := c.cache.Agent()

	return agent.CreatedAt
}

// Connected returns whether the mqtt connector is connected.
func (c *Connector) Connected() bool {
	c.l.RLock()
	defer c.l.RUnlock()

	if c.mqtt == nil {
		return false
	}

	return c.mqtt.Connected()
}

// LastReport returns the date of last report with Bleemeo API over MQTT.
func (c *Connector) LastReport() time.Time {
	c.l.Lock()
	defer c.l.Unlock()

	if c.mqtt != nil {
		tmp := c.mqtt.LastReport()
		if tmp.After(c.lastKnownReport) {
			c.lastKnownReport = tmp
		}
	}

	return c.lastKnownReport
}

// HealthCheck perform some health check and log any issue found.
// This method could panic when health condition are bad for too long in order to cause a Glouton restart.
func (c *Connector) HealthCheck() bool {
	ok := true

	if c.AgentID() == "" {
		logger.Printf("Agent not yet registered")

		ok = false
	}

	lastReport := c.LastReport()

	c.l.Lock()
	defer c.l.Unlock()

	ok = c.sync.HealthCheck() && ok

	if time.Now().Before(c.disabledUntil) {
		delay := time.Until(c.disabledUntil)

		logger.Printf("Bleemeo connector is still disabled for %v due to '%v'", delay.Truncate(time.Second), c.disableReason)

		return false
	}

	if c.mqtt != nil {
		ok = c.mqtt.HealthCheck() && ok

		if !lastReport.IsZero() && time.Since(lastReport) > time.Hour && (c.lastMQTTRestart.IsZero() || time.Since(c.lastMQTTRestart) > 4*time.Hour) {
			c.lastMQTTRestart = time.Now()

			logger.Printf("MQTT connection fail to re-establish since %s. This may be a long network issue or a Glouton bug", lastReport.Format(time.RFC3339))

			if time.Since(lastReport) > 36*time.Hour {
				c.mqttReportConsecutiveError++

				if c.mqttReportConsecutiveError >= 3 {
					logger.Printf("Restarting MQTT is not enough. Glouton seems unhealthy, killing myself")

					// We don't know how big the buffer needs to be to collect
					// all the goroutines. Use 2MB buffer which hopefully is enough
					buffer := make([]byte, 1<<21)

					n := runtime.Stack(buffer, true)
					logger.Printf("%s", string(buffer[:n]))
					panic(fmt.Sprint("Glouton seems unhealthy (last report too old), killing myself\n", c.DiagnosticPage()))
				}
			}

			logger.Printf("Trying to restart the MQTT connection from scratch")

			if c.mqttRestart != nil {
				c.mqttRestart <- nil
			}
		} else {
			c.mqttReportConsecutiveError = 0
		}

		c.sync.SetMQTTConnected(c.mqtt.Connected())
	} else {
		c.sync.SetMQTTConnected(false)
	}

	return ok
}

func (c *Connector) EmitInternalMetric(_ context.Context, state registry.GatherState, app storage.Appender) error {
	_ = state

	c.l.RLock()
	defer c.l.RUnlock()

	if c.mqtt != nil && c.mqtt.Connected() {
		if err := c.pushAppender.CommitCopyAndReset(app); err != nil {
			return err
		}

		_, err := app.Append(
			0,
			labels.FromMap(map[string]string{
				gloutonTypes.LabelName: "agent_status",
			}),
			0, // Use time from Registry
			1.0,
		)
		if err != nil {
			return err
		}
	}

	return app.Commit()
}

func (c *Connector) IsMetricAllowed(metric gloutonTypes.LabelsAndAnnotation) (bool, types.DenyReason, error) {
	f := filter.NewFilter(c.cache)

	return f.IsAllowed(metric.Labels, metric.Annotations)
}

func (c *Connector) updateConfig(nameChanged bool) {
	currentConfig, ok := c.cache.CurrentAccountConfig()
	if !ok || currentConfig.AgentConfigByName[bleemeo.AgentType_Agent].MetricResolution == 0 {
		return
	}

	if nameChanged {
		logger.Printf("Changed to configuration %s", currentConfig.Name)
	}

	if c.option.UpdateMetricResolution != nil {
		c.option.UpdateMetricResolution(currentConfig.AgentConfigByName[bleemeo.AgentType_Agent].MetricResolution, currentConfig.AgentConfigByName[bleemeo.AgentType_SNMP].MetricResolution)
	}
}

func (c *Connector) clearDisable(reasonToClear types.DisableReason) {
	c.l.Lock()

	if time.Now().Before(c.disabledUntil) && c.disableReason == reasonToClear {
		c.disabledUntil = time.Now()
	}

	c.l.Unlock()
	c.sync.ClearDisable(reasonToClear, 0)

	if mqtt := c.mqtt; mqtt != nil {
		var mqttDisableDelay time.Duration

		switch reasonToClear { //nolint:exhaustive,nolintlint
		case types.DisableTooManyErrors:
			mqttDisableDelay = 20 * time.Second
		case types.DisableAgentTooOld, types.DisableDuplicatedAgent, types.DisableAuthenticationError, types.DisableTimeDrift:
			// give time to the synchronizer check if the error is solved
			mqttDisableDelay = 80 * time.Second
		default:
			mqttDisableDelay = 20 * time.Second
		}

		mqtt.ClearDisable(reasonToClear, mqttDisableDelay)
	}
}

func (c *Connector) disableCallback(reason types.DisableReason, until time.Time) {
	c.l.Lock()

	if c.disabledUntil.After(until) {
		return
	}

	c.disabledUntil = until
	c.disableReason = reason

	mqtt := c.mqtt

	c.l.Unlock()

	delay := time.Until(until)

	logger.Printf("Disabling Bleemeo connector for %v due to '%v'", delay.Truncate(time.Second), reason)
	c.sync.Disable(until, reason)

	c.disableMqtt(mqtt, reason, until)
}

func (c *Connector) disableMqtt(mqtt *mqtt.Client, reason types.DisableReason, until time.Time) {
	if mqtt == nil {
		// MQTT is already disabled.
		return
	}

	// Delay to apply between re-enabling the synchronizer and the mqtt client. The goal is to allow for
	// the synchronizer to disable mqtt again before mqtt have time to reconnect or send metrics.
	var mqttDisableDelay time.Duration

	switch reason { //nolint:exhaustive,nolintlint
	case types.DisableTooManyErrors:
		mqttDisableDelay = 20 * time.Second
	case types.DisableAgentTooOld, types.DisableDuplicatedAgent, types.DisableAuthenticationError, types.DisableTimeDrift:
		// Give time to the synchronizer check if the error is solved.
		mqttDisableDelay = 80 * time.Second
	default:
		mqttDisableDelay = 20 * time.Second
	}

	mqtt.Disable(until.Add(mqttDisableDelay), reason)
}

func (c *Connector) HandleDiagnosticRequest(ctx context.Context, requestToken string) {
	archiveBuf := new(bytes.Buffer)
	archive := archivewriter.NewZipWriter(archiveBuf)

	err := c.option.WriteDiagnosticArchive(ctx, archive)
	if err != nil {
		logger.V(1).Printf("Failed to write on-demand diagnostic archive: %v", err)

		return
	}

	err = archive.Close()
	if err != nil {
		logger.V(1).Printf("Failed to close on-demand diagnostic archive: %v", err)

		return
	}

	datetime := time.Now().Format("20060102-150405")
	c.sync.ScheduleDiagnosticUpload("on_demand_"+datetime+".zip", requestToken, archiveBuf.Bytes())
}

func (c *Connector) PushLogs(ctx context.Context, payload []byte) error {
	c.l.Lock()
	mqttClient := c.mqtt
	c.l.Unlock()

	if mqttClient != nil {
		return mqttClient.PushLogs(ctx, payload)
	}

	return nil
}

func (c *Connector) ShouldApplyLogBackPressure() types.LogsAvailability {
	c.l.Lock()
	mqttClient := c.mqtt
	c.l.Unlock()

	if mqttClient != nil {
		return mqttClient.LogsBackPressureStatus()
	}

	return types.LogsAvailabilityShouldBuffer
}
