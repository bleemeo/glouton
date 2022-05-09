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

package synchronizer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"glouton/bleemeo/client"
	"glouton/bleemeo/internal/common"
	"glouton/bleemeo/internal/filter"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/logger"
	"glouton/threshold"
	"glouton/types"
	"math/rand"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// agentStatusName is the name of the special metrics used to store the agent connection status.
const agentStatusName = "agent_status"

// Those constant are here to make linter happy. We should likely drop them and use boolean type,
// they are only used in API call and I'm pretty sure the API accept boolean.
const (
	stringFalse = "False"
	stringTrue  = "True"
)

// gracePeriod is the minimum time for which the agent should not deactivate
// an allowed metric between firstSeen and now. 6 Minutes is the minimum time,
// as the prometheus alert time is 5 minutes.
const gracePeriod = 6 * time.Minute

// Registrations that haven't been retried for failedRegistrationExpiration will removed.
const failedRegistrationExpiration = 24 * time.Hour

// metricFields is the fields used on the API for a metric, we always use all fields to
// make sure no Metric object is returned with some empty fields which could create bugs.
const metricFields = "id,label,item,labels_text,unit,unit_text,service,container,deactivated_at," +
	"threshold_low_warning,threshold_low_critical,threshold_high_warning,threshold_high_critical," +
	"status_of,agent,promql_query,is_user_promql_alert,alerting_rule"

var (
	errRetryLater     = errors.New("metric registration should be retried later")
	errIgnore         = errors.New("metric registration fail but can be ignored (because it registration will be retried automatically)")
	errAttemptToSpoof = errors.New("attempt to spoof another agent (or missing label)")
	errNotImplemented = errors.New("not implemented")
)

type needRegisterError struct {
	remoteMetric bleemeoTypes.Metric
}

func (e needRegisterError) Error() string {
	return fmt.Sprintf("metric %v was deleted from API, it need to be re-registered", e.remoteMetric.LabelsText)
}

// The metric registration function is done in 4 pass
// * first will only ensure agent_status is registered (this metric is required before connecting to MQTT and is produced once connected to MQTT...).
// * main pass will do the bulk of the jobs. But some metric may fail and will be done in Recreate and Retry pass.
// * The Recreate pass is for metric that failed (to update) because they were deleted from API.
// * The Retry pass is for metric that failed to create due to dependency with other metric (e.g. status_of).
type metricRegisterPass int

const (
	metricPassAgentStatus metricRegisterPass = iota
	metricPassMain
	metricPassRecreate
	metricPassRetry
)

func (p metricRegisterPass) String() string {
	switch p {
	case metricPassAgentStatus:
		return "agent-status"
	case metricPassMain:
		return "main-pass"
	case metricPassRecreate:
		return "recreate-deleted"
	case metricPassRetry:
		return "retry"
	}

	return "unknown"
}

type fakeMetric struct {
	label string
}

func (m fakeMetric) Points(time.Time, time.Time) ([]types.Point, error) {
	return nil, errNotImplemented
}

func (m fakeMetric) LastPointReceivedAt() time.Time {
	return time.Now()
}

func (m fakeMetric) Annotations() types.MetricAnnotations {
	return types.MetricAnnotations{}
}

func (m fakeMetric) Labels() map[string]string {
	return map[string]string{
		types.LabelName: m.label,
	}
}

type metricPayload struct {
	bleemeoTypes.Metric
	Name string `json:"label,omitempty"`
	Item string `json:"item,omitempty"`
}

// metricFromAPI convert a metricPayload received from API to a bleemeoTypes.Metric.
func (mp metricPayload) metricFromAPI(lastTime time.Time) bleemeoTypes.Metric {
	if mp.LabelsText == "" {
		mp.Labels = map[string]string{
			types.LabelName:         mp.Name,
			types.LabelInstanceUUID: mp.AgentID,
		}

		if mp.Item != "" {
			mp.Labels[types.LabelItem] = mp.Item
		}

		annotations := types.MetricAnnotations{
			BleemeoItem: mp.Item,
		}
		mp.LabelsText = common.LabelsToText(mp.Labels, annotations, true)
	} else {
		mp.Labels = types.TextToLabels(mp.LabelsText)
	}

	if !lastTime.IsZero() {
		mp.Metric.FirstSeenAt = lastTime
	} else {
		mp.Metric.FirstSeenAt = time.Now().Truncate(time.Second)
	}

	return mp.Metric
}

type metricComparator struct {
	isEssentials      map[string]bool
	isHighCardinality map[string]bool
	isImportant       map[string]bool
	isGoodItem        *regexp.Regexp
}

func newComparator(format types.MetricFormat) *metricComparator {
	essentials := []string{
		"node_cpu_seconds_global",
		"node_cpu_seconds_total",
		"node_memory_MemTotal_bytes",
		"node_memory_MemAvailable_bytes",
		"node_memory_MemFree_bytes",
		"node_memory_Buffers_bytes",
		"node_memory_Cached_bytes",
		"node_memory_SwapFree_bytes",
		"node_memory_SwapTotal_bytes",
		"node_disk_io_time_seconds_total",
		"node_disk_read_bytes_total",
		"node_disk_written_bytes_total",
		"node_disk_reads_completed_total",
		"node_disk_writes_completed_total",
		"node_filesystem_avail_bytes",
		"node_filesystem_size_bytes",
		"node_network_receive_bytes_total",
		"node_network_transmit_bytes_total",
		"node_network_receive_packets_total",
		"node_network_transmit_packets_total",
		"node_network_receive_errs_total",
		"node_network_transmit_errs_total",
		"agent_config_warning",
		agentStatusName,
	}

	if runtime.GOOS == "windows" {
		essentials = []string{
			"windows_cpu_time_global",
			"windows_cs_physical_memory_bytes",
			"windows_memory_available_bytes",
			"windows_memory_standby_cache_bytes",
			"windows_os_paging_free_bytes",
			"windows_os_paging_limit_bytes",
			"windows_logical_disk_idle_seconds_total",
			"windows_logical_disk_read_bytes_total",
			"windows_logical_disk_write_bytes_total",
			"windows_logical_disk_reads_total",
			"windows_logical_disk_writes_total",
			"windows_logical_disk_free_bytes",
			"windows_logical_disk_size_bytes",
			"windows_net_bytes_received_total",
			"windows_net_bytes_sent_total",
			"windows_net_packets_received_total",
			"windows_net_packets_sent_total",
			"windows_net_packets_received_errors",
			"windows_net_packets_outbound_errors",
			agentStatusName,
		}
	}

	if format == types.MetricFormatBleemeo {
		essentials = []string{
			"cpu_idle", "cpu_wait", "cpu_nice", "cpu_user", "cpu_system", "cpu_interrupt", "cpu_softirq", "cpu_steal", "cpu_guest_nice", "cpu_guest",
			"mem_free", "mem_cached", "mem_buffered", "mem_used",
			"io_utilization", "io_read_bytes", "io_write_bytes", "io_reads",
			"io_writes", "net_bits_recv", "net_bits_sent", "net_packets_recv",
			"net_packets_sent", "net_err_in", "net_err_out", "disk_used_perc",
			"swap_used_perc", "cpu_used", "mem_used_perc", "agent_config_warning", agentStatusName,
		}
	}

	highCard := []string{
		"node_filesystem_avail_bytes",
		"node_filesystem_size_bytes",
		"node_network_receive_bytes_total",
		"node_network_transmit_bytes_total",
		"node_network_receive_packets_total",
		"node_network_transmit_packets_total",
		"node_network_receive_errs_total",
		"node_network_transmit_errs_total",
		"windows_logical_disk_free_bytes",
		"windows_logical_disk_size_bytes",
		"windows_net_bytes_received_total",
		"windows_net_bytes_sent_total",
		"windows_net_packets_received_total",
		"windows_net_packets_sent_total",
		"windows_net_packets_received_errors",
		"windows_net_packets_outbound_errors",
		"net_bits_recv",
		"net_bits_sent",
		"net_packets_recv",
		"net_packets_sent",
		"net_err_in",
		"net_err_out",
		"disk_used_perc",
	}

	important := []string{
		"cpu_used_status", "disk_used_perc_status", "mem_used_perc_status", "swap_used_perc_status",
		"system_pending_security_updates", "system_pending_security_updates_status",
	}

	isEssentials := make(map[string]bool, len(essentials))
	isHighCard := make(map[string]bool, len(highCard))
	isImportant := make(map[string]bool, len(important))

	for _, v := range essentials {
		isEssentials[v] = true
	}

	for _, v := range highCard {
		isHighCard[v] = true
	}

	for _, v := range important {
		isImportant[v] = true
	}

	return &metricComparator{
		isEssentials:      isEssentials,
		isHighCardinality: isHighCard,
		isImportant:       isImportant,
		isGoodItem:        regexp.MustCompile(`^(/|/home|/var|/srv|eth.|eno.|en(s|p)(\d|1\d)(.\d)?)$`),
	}
}

func (m *metricComparator) KeepInOnlyEssential(metric map[string]string) bool {
	name := metric[types.LabelName]
	item := getItem(metric)
	essential := m.isEssentials[name]
	highCard := m.isHighCardinality[name]
	goodItem := m.IsSignificantItem(item)

	return essential && (!highCard || goodItem)
}

// IsSignificantItem return true of "important" item. Like "/", "/home" or "eth0".
// It's aim is having standard filesystem/network interface before exotic one.
func (m *metricComparator) IsSignificantItem(item string) bool {
	return m.isGoodItem.MatchString(item)
}

// importanceFactor return a weight to indicate how important the metric is.
// The lowest the more important the metric is.
func (m *metricComparator) importanceWeight(metric map[string]string) int {
	name := metric[types.LabelName]
	item := getItem(metric)
	essential := m.isEssentials[name]
	highCard := m.isHighCardinality[name]
	goodItem := m.IsSignificantItem(item)
	important := m.isImportant[name]
	seemsStatus := strings.HasSuffix(name, "_status")

	switch {
	case essential && item == "":
		return 0
	case essential && (!highCard || goodItem):
		return 10
	case important && (!highCard || goodItem):
		return 20
	case seemsStatus && (!highCard || goodItem):
		return 30
	case essential:
		return 40
	case important:
		return 50
	case seemsStatus:
		return 60
	default:
		return 70
	}
}

func getItem(metric map[string]string) string {
	source := []string{
		"item",
		"device",
		"mountpoint",
	}

	for _, n := range source {
		if metric[n] != "" {
			return metric[n]
		}
	}

	return ""
}

func (m *metricComparator) isLess(metricA map[string]string, metricB map[string]string) bool {
	weightA := m.importanceWeight(metricA)
	weightB := m.importanceWeight(metricB)

	switch {
	case weightA < weightB:
		return true
	case weightB < weightA:
		return false
	case len(metricA) < len(metricB):
		return true
	default:
		return len(metricA) == len(metricB) && getItem(metricA) < getItem(metricB)
	}
}

func prioritizeAndFilterMetrics(format types.MetricFormat, metrics []types.Metric, onlyEssential bool) []types.Metric {
	cmp := newComparator(format)

	if onlyEssential {
		i := 0

		for _, m := range metrics {
			if !cmp.KeepInOnlyEssential(m.Labels()) {
				continue
			}

			metrics[i] = m
			i++
		}

		metrics = metrics[:i]
	}

	sort.Slice(metrics, func(i, j int) bool {
		return cmp.isLess(metrics[i].Labels(), metrics[j].Labels())
	})

	return metrics
}

func httpResponseToMetricFailureKind(content string) bleemeoTypes.FailureKind {
	switch {
	case strings.Contains(content, "metric is not whitelisted"):
		return bleemeoTypes.FailureAllowList
	case strings.Contains(content, "metric is not in allow-list"):
		return bleemeoTypes.FailureAllowList
	case strings.Contains(content, "Too many non standard metrics"):
		return bleemeoTypes.FailureTooManyCustomMetrics
	case strings.Contains(content, "Too many metrics"):
		return bleemeoTypes.FailureTooManyStandardMetrics
	default:
		return bleemeoTypes.FailureUnknown
	}
}

func (s *Synchronizer) metricKey(lbls map[string]string, annotations types.MetricAnnotations) string {
	if lbls[types.LabelInstanceUUID] == "" {
		// In name+item mode, we treat empty instance_uuid and instance_uuid=agentID as the same.
		// This reflect in:
		// * metricFromAPI which fill the instance_uuid when labels_text is empty
		// * MetricOnlyHasItem that cause instance_uuid to not be sent on registration in name+item mode
		agentID := s.agentID

		if annotations.BleemeoAgentID != "" {
			agentID = annotations.BleemeoAgentID
		}

		if common.MetricOnlyHasItem(lbls, agentID) {
			tmp := make(map[string]string, len(lbls)+1)

			for k, v := range lbls {
				tmp[k] = v
			}

			tmp[types.LabelInstanceUUID] = s.agentID
			lbls = tmp
		}
	}

	return common.LabelsToText(lbls, annotations, s.option.MetricFormat == types.MetricFormatBleemeo)
}

// nearly a duplicate of mqtt.filterPoint, but not quite. Alas we cannot easily generalize this as go doesn't have generics (yet).
func (s *Synchronizer) filterMetrics(input []types.Metric) []types.Metric {
	result := make([]types.Metric, 0)

	f := filter.NewFilter(s.option.Cache)

	for _, m := range input {
		allow, err := f.IsAllowed(m.Labels(), m.Annotations())
		if err != nil {
			logger.V(2).Printf("sync: %s", err)

			continue
		}

		if allow {
			result = append(result, m)
		}
	}

	return result
}

// excludeUnregistrableMetrics remove metrics that cannot be registered, due to missing
// dependency (like a container that must be registered before).
func (s *Synchronizer) excludeUnregistrableMetrics(metrics []types.Metric) []types.Metric {
	result := make([]types.Metric, 0, len(metrics))
	containersByContainerID := s.option.Cache.ContainersByContainerID()
	services := s.option.Cache.Services()
	servicesByKey := make(map[serviceNameInstance]bleemeoTypes.Service, len(services))

	for _, v := range services {
		k := serviceNameInstance{name: v.Label, instance: v.Instance}
		servicesByKey[k] = v
	}

	for _, metric := range metrics {
		annotations := metric.Annotations()
		containerName := ""

		if annotations.ContainerID != "" {
			container, ok := containersByContainerID[annotations.ContainerID]
			if !ok {
				continue
			}

			containerName = container.Name
		}

		if annotations.ServiceName != "" {
			srvKey := serviceNameInstance{name: annotations.ServiceName, instance: containerName}
			srvKey.truncateInstance()

			if _, ok := servicesByKey[srvKey]; !ok {
				continue
			}
		}

		result = append(result, metric)
	}

	return result
}

func (s *Synchronizer) findUnregisteredMetrics(metrics []types.Metric) []types.Metric {
	registeredMetricsByKey := s.option.Cache.MetricLookupFromList()

	result := make([]types.Metric, 0)

	for _, v := range metrics {
		key := s.metricKey(v.Labels(), v.Annotations())

		if _, ok := registeredMetricsByKey[key]; ok {
			continue
		}

		result = append(result, v)
	}

	return result
}

func (s *Synchronizer) syncMetrics(ctx context.Context, fullSync bool, onlyEssential bool) (updateThresholds bool, err error) {
	localMetrics, err := s.option.Store.Metrics(nil)
	if err != nil {
		return false, err
	}

	filteredMetrics := s.filterMetrics(localMetrics)
	unregisteredMetrics := s.findUnregisteredMetrics(filteredMetrics)
	unregisteredMetrics = s.excludeUnregistrableMetrics(unregisteredMetrics)

	if s.successiveErrors == 3 {
		// After 3 error, try to force a full synchronization to see if it solve the issue.
		fullSync = true
	}

	previousMetrics := s.option.Cache.MetricsByUUID()
	activeMetricsCount := 0

	for _, m := range previousMetrics {
		if m.DeactivatedAt.IsZero() {
			activeMetricsCount++
		}
	}

	if fullSync {
		s.retryableMetricFailure[bleemeoTypes.FailureUnknown] = true
		s.retryableMetricFailure[bleemeoTypes.FailureAllowList] = true
		s.retryableMetricFailure[bleemeoTypes.FailureTooManyCustomMetrics] = true
		s.retryableMetricFailure[bleemeoTypes.FailureTooManyStandardMetrics] = true
	}

	pendingMetricsUpdate := s.popPendingMetricsUpdate()
	if len(pendingMetricsUpdate) > activeMetricsCount*3/100 {
		// If more than 3% of known active metrics needs update, do a full
		// update. 3% is arbitrary choose, based on assumption request for
		// one page of (100) metrics is cheaper than 3 request for
		// one metric.
		fullSync = true
	}

	if len(unregisteredMetrics) > 3*len(previousMetrics)/100 {
		fullSync = true
	}

	err = s.metricUpdatePendingOrSync(fullSync, &pendingMetricsUpdate)

	if err != nil {
		return false, err
	}

	unregisteredMetrics = s.findUnregisteredMetrics(filteredMetrics)
	unregisteredMetrics = s.excludeUnregistrableMetrics(unregisteredMetrics)

	logger.V(2).Printf("Searching %d metrics that may be inactive", len(unregisteredMetrics))

	err = s.metricUpdateInactiveList(unregisteredMetrics, fullSync)
	if err != nil {
		return false, err
	}

	updateThresholds = fullSync || len(unregisteredMetrics) > 0 || len(pendingMetricsUpdate) > 0

	s.metricDeleteFromRemote(filteredMetrics, previousMetrics)

	localMetrics, err = s.option.Store.Metrics(nil)
	if err != nil {
		return updateThresholds, err
	}

	filteredMetrics = s.filterMetrics(localMetrics)

	// If one metric fail to register, it may block other metric that would register correctly.
	// To reduce this risk, randomize the list, so on next run, the metric that failed to register
	// may no longer block other.
	rand.Shuffle(len(filteredMetrics), func(i, j int) {
		filteredMetrics[i], filteredMetrics[j] = filteredMetrics[j], filteredMetrics[i]
	})

	filteredMetrics = prioritizeAndFilterMetrics(s.option.MetricFormat, filteredMetrics, onlyEssential)

	if err := newMetricRegisterer(s).registerMetrics(filteredMetrics); err != nil {
		return updateThresholds, err
	}

	if onlyEssential {
		return updateThresholds, nil
	}

	if err := s.metricDeleteIgnoredServices(); err != nil {
		return updateThresholds, err
	}

	if err := s.metricDeactivate(filteredMetrics); err != nil {
		return updateThresholds, err
	}

	s.l.Lock()
	defer s.l.Unlock()

	s.lastMetricCount = len(localMetrics)

	return updateThresholds, nil
}

func (s *Synchronizer) metricUpdatePendingOrSync(fullSync bool, pendingMetricsUpdate *[]string) error {
	if fullSync {
		err := s.metricUpdateAll(false)
		if err != nil {
			// Re-add the metric on the pending update
			s.UpdateMetrics(*pendingMetricsUpdate...)

			return err
		}
	} else if len(*pendingMetricsUpdate) > 0 {
		logger.V(2).Printf("Update %d metrics by UUID", len(*pendingMetricsUpdate))

		if err := s.metricUpdateListUUID(*pendingMetricsUpdate); err != nil {
			// Re-add the metric on the pending update
			s.UpdateMetrics(*pendingMetricsUpdate...)

			return err
		}
	}

	return nil
}

// UpdateUnitsAndThresholds update metrics units & threshold (from cache).
func (s *Synchronizer) UpdateUnitsAndThresholds(ctx context.Context, firstUpdate bool) {
	thresholds := make(map[string]threshold.Threshold)
	units := make(map[string]threshold.Unit)
	defaultSoftPeriod := time.Duration(s.option.Config.Int("metric.softstatus_period_default")) * time.Second
	softPeriods := s.option.Config.DurationMap("metric.softstatus_period")

	s.l.Lock()
	thresholdOverrides := s.thresholdOverrides
	s.l.Unlock()

	for _, m := range s.option.Cache.Metrics() {
		units[m.LabelsText] = m.Unit

		metricName := m.Labels[types.LabelName]

		// Apply the threshold overrides.
		overrideKey := thresholdOverrideKey{
			MetricName: metricName,
			AgentID:    m.AgentID,
		}

		if override, ok := thresholdOverrides[overrideKey]; ok {
			logger.V(1).Printf("Overriding threshold for metric %s on agent %s", metricName, m.AgentID)

			thresholds[m.LabelsText] = override

			continue
		}

		thresh := m.Threshold.ToInternalThreshold()
		// Apply delays from config or default delay.
		thresh.WarningDelay = defaultSoftPeriod
		thresh.CriticalDelay = defaultSoftPeriod

		if softPeriod, ok := softPeriods[metricName]; ok {
			thresh.WarningDelay = softPeriod
			thresh.CriticalDelay = softPeriod
		}

		thresholds[m.LabelsText] = thresh
	}

	if s.option.UpdateThresholds != nil {
		s.option.UpdateThresholds(ctx, thresholds, firstUpdate)
	}

	if s.option.UpdateUnits != nil {
		s.option.UpdateUnits(units)
	}
}

func (s *Synchronizer) isOwnedMetric(metric metricPayload) bool {
	if metric.AgentID == s.agentID {
		return true
	}

	agentUUID, present := types.TextToLabels(metric.LabelsText)[types.LabelScraperUUID]

	if present && agentUUID == s.agentID {
		return true
	}

	scraperName, present := types.TextToLabels(metric.LabelsText)[types.LabelScraper]
	if present && scraperName == s.option.BlackboxScraperName {
		return true
	}

	// Only monitors have metric that are owned by multiple agents
	agentTypeID, found := s.getAgentType(bleemeoTypes.AgentTypeMonitor)
	if found {
		agents := s.option.Cache.AgentsByUUID()
		if agentTypeID == agents[metric.AgentID].AgentType {
			return false
		}
	}

	return true
}

// metricsListWithAgentID fetches the list of all metrics for a given agent, and returns a UUID:metric mapping.
func (s *Synchronizer) metricsListWithAgentID(fetchInactive bool) (map[string]bleemeoTypes.Metric, error) {
	params := map[string]string{
		"fields": metricFields,
	}

	if fetchInactive {
		params["active"] = stringFalse
	} else {
		params["active"] = stringTrue
	}

	result, err := s.client.Iter(s.ctx, "metric", params)
	if err != nil {
		return nil, err
	}

	metricsByUUID := make(map[string]bleemeoTypes.Metric, len(result))

	for _, m := range s.option.Cache.Metrics() {
		if m.DeactivatedAt.IsZero() == fetchInactive {
			metricsByUUID[m.ID] = m
		}
	}

	for _, jsonMessage := range result {
		var metric metricPayload

		if err := json.Unmarshal(jsonMessage, &metric); err != nil {
			continue
		}

		// If the metric is associated with another agent, make sure we "own" the metric.
		// This may not be the case for monitor because multiple agent will process such metrics.
		if !s.isOwnedMetric(metric) {
			continue
		}

		metricsByUUID[metric.ID] = metric.metricFromAPI(metricsByUUID[metric.ID].FirstSeenAt)
	}

	return metricsByUUID, nil
}

func (s *Synchronizer) metricUpdateAll(fetchInactive bool) error {
	metricsMap, err := s.metricsListWithAgentID(fetchInactive)
	if err != nil {
		return err
	}

	metrics := make([]bleemeoTypes.Metric, 0, len(metricsMap))

	for _, metric := range metricsMap {
		metrics = append(metrics, metric)
	}

	s.option.Cache.SetMetrics(metrics)

	return nil
}

// metricUpdateInactiveList fetches a list of inactive (or non-existing) metrics.
//
// if allowList is true, this function may fetches all inactive metrics. This should only
// be true if the full list of active metrics was fetched just before.
func (s *Synchronizer) metricUpdateInactiveList(metrics []types.Metric, allowList bool) error {
	if len(metrics) < 10 || !allowList {
		return s.metricUpdateList(metrics)
	}

	result := struct {
		Count int
	}{}

	_, err := s.client.Do(
		s.ctx,
		"GET",
		"v1/metric/",
		map[string]string{
			"fields":    "id",
			"active":    "False",
			"page_size": "0",
		},
		nil,
		&result,
	)
	if err != nil {
		return err
	}

	if len(metrics) <= result.Count/100 {
		// should be faster with one HTTP request per metrics
		return s.metricUpdateList(metrics)
	}

	// fewer query with fetching the whole list
	return s.metricUpdateAll(true)
}

// metricUpdateList fetches a list of metrics, and updates the cache.
func (s *Synchronizer) metricUpdateList(metrics []types.Metric) error {
	if len(metrics) == 0 {
		return nil
	}

	metricsByUUID := s.option.Cache.MetricsByUUID()

	for _, metric := range metrics {
		agentID := s.agentID
		if metric.Annotations().BleemeoAgentID != "" {
			agentID = metric.Annotations().BleemeoAgentID
		}

		params := map[string]string{
			"labels_text": common.LabelsToText(metric.Labels(), metric.Annotations(), s.option.MetricFormat == types.MetricFormatBleemeo),
			"agent":       agentID,
			"fields":      "id,agent,label,item,labels_text,unit,unit_text,service,container,deactivated_at,threshold_low_warning,threshold_low_critical,threshold_high_warning,threshold_high_critical,status_of,promql_query,is_user_promql_alert",
		}

		if s.option.MetricFormat == types.MetricFormatBleemeo && common.MetricOnlyHasItem(metric.Labels(), agentID) {
			annotations := metric.Annotations()
			params["label"] = metric.Labels()[types.LabelName]
			params["item"] = common.TruncateItem(annotations.BleemeoItem, annotations.ServiceName != "")
			delete(params, "labels_text")
		}

		result, err := s.client.Iter(s.ctx, "metric", params)
		if err != nil {
			return err
		}

		for _, jsonMessage := range result {
			var metric metricPayload

			if err := json.Unmarshal(jsonMessage, &metric); err != nil {
				continue
			}

			// Do not modify metrics declared by other agents when the target agent is a monitor
			agentUUID, present := types.TextToLabels(metric.LabelsText)[types.LabelScraperUUID]

			if present && agentUUID != s.agentID {
				continue
			}

			if !present {
				scraperName, present := types.TextToLabels(metric.LabelsText)[types.LabelScraper]
				if present && scraperName != s.option.BlackboxScraperName {
					continue
				}
			}

			metricsByUUID[metric.ID] = metric.metricFromAPI(metricsByUUID[metric.ID].FirstSeenAt)
		}
	}

	rebuiltMetrics := make([]bleemeoTypes.Metric, 0, len(metricsByUUID))

	for _, m := range metricsByUUID {
		rebuiltMetrics = append(rebuiltMetrics, m)
	}

	s.option.Cache.SetMetrics(rebuiltMetrics)

	return nil
}

func (s *Synchronizer) metricUpdateListUUID(requests []string) error {
	metricsByUUID := s.option.Cache.MetricsByUUID()

	for _, key := range requests {
		var metric metricPayload

		params := map[string]string{
			"fields": metricFields,
		}

		_, err := s.client.Do(
			s.ctx,
			"GET",
			fmt.Sprintf("v1/metric/%s/", key),
			params,
			nil,
			&metric,
		)
		if err != nil && client.IsNotFound(err) {
			delete(metricsByUUID, key)

			continue
		} else if err != nil {
			return err
		}

		metricsByUUID[metric.ID] = metric.metricFromAPI(metricsByUUID[metric.ID].FirstSeenAt)
	}

	metrics := make([]bleemeoTypes.Metric, 0, len(metricsByUUID))

	for _, m := range metricsByUUID {
		metrics = append(metrics, m)
	}

	s.option.Cache.SetMetrics(metrics)

	return nil
}

func (s *Synchronizer) metricDeleteFromRemote(localMetrics []types.Metric, previousMetrics map[string]bleemeoTypes.Metric) {
	newMetrics := s.option.Cache.MetricsByUUID()

	deletedMetricLabelItem := make(map[string]bool)

	for _, m := range previousMetrics {
		if _, ok := newMetrics[m.ID]; !ok {
			key := m.LabelsText
			deletedMetricLabelItem[key] = true
		}
	}

	localMetricToDelete := make([]map[string]string, 0)

	for _, m := range localMetrics {
		labels := m.Labels()
		key := s.metricKey(labels, m.Annotations())

		if _, ok := deletedMetricLabelItem[key]; ok {
			localMetricToDelete = append(localMetricToDelete, labels)
		}
	}

	s.option.Store.DropMetrics(localMetricToDelete)
}

type metricRegisterer struct {
	s                       *Synchronizer
	registeredMetricsByUUID map[string]bleemeoTypes.Metric
	registeredMetricsByKey  map[string]bleemeoTypes.Metric
	containersByContainerID map[string]bleemeoTypes.Container
	servicesByKey           map[serviceNameInstance]bleemeoTypes.Service
	failedRegistrationByKey map[string]bleemeoTypes.MetricRegistration
	monitors                []bleemeoTypes.Monitor

	// Metric that need to be re-created
	needReregisterMetrics []types.Metric
	// Metric that should be re-tried (likely because they depend on another metric)
	retryMetrics []types.Metric

	regCountBeforeUpdate int
	errorCount           int
	pendingErr           error
}

func newMetricRegisterer(s *Synchronizer) *metricRegisterer {
	fails := s.option.Cache.MetricRegistrationsFail()
	services := s.option.Cache.Services()
	servicesByKey := make(map[serviceNameInstance]bleemeoTypes.Service, len(services))
	failedRegistrationByKey := make(map[string]bleemeoTypes.MetricRegistration, len(fails))

	for _, v := range s.option.Cache.MetricRegistrationsFail() {
		failedRegistrationByKey[v.LabelsText] = v
	}

	for _, v := range services {
		k := serviceNameInstance{name: v.Label, instance: v.Instance}
		servicesByKey[k] = v
	}

	return &metricRegisterer{
		s:                       s,
		registeredMetricsByUUID: s.option.Cache.MetricsByUUID(),
		registeredMetricsByKey:  common.MetricLookupFromList(s.option.Cache.Metrics()),
		containersByContainerID: s.option.Cache.ContainersByContainerID(),
		servicesByKey:           servicesByKey,
		failedRegistrationByKey: failedRegistrationByKey,
		monitors:                s.option.Cache.Monitors(),
		regCountBeforeUpdate:    30,
		needReregisterMetrics:   make([]types.Metric, 0),
		retryMetrics:            make([]types.Metric, 0),
	}
}

func (mr *metricRegisterer) registerMetrics(localMetrics []types.Metric) error {
	err := mr.do(localMetrics)

	metrics := make([]bleemeoTypes.Metric, 0, len(mr.registeredMetricsByUUID))

	for _, v := range mr.registeredMetricsByUUID {
		metrics = append(metrics, v)
	}

	mr.s.option.Cache.SetMetrics(metrics)

	now := mr.s.now()
	minRetryAt := now.Add(time.Hour)

	failedRegistrations := make([]bleemeoTypes.MetricRegistration, 0, len(mr.failedRegistrationByKey))
	nbTooManyMetrics := make(map[bleemeoTypes.FailureKind]int, 2)

	for key, failedRegistration := range mr.failedRegistrationByKey {
		if !failedRegistration.LastFailKind.IsPermanentFailure() && minRetryAt.After(failedRegistration.RetryAfter()) {
			minRetryAt = failedRegistration.RetryAfter()
		}

		if failedRegistration.LastFailKind == bleemeoTypes.FailureTooManyCustomMetrics ||
			failedRegistration.LastFailKind == bleemeoTypes.FailureTooManyStandardMetrics {
			// Registration that haven't been retried for a long time correspond to metrics
			// that are no longer in the store, we can remove them.
			if now.Sub(failedRegistration.LastFailAt) > failedRegistrationExpiration {
				delete(mr.failedRegistrationByKey, key)

				continue
			}

			nbTooManyMetrics[failedRegistration.LastFailKind]++
		}

		failedRegistrations = append(failedRegistrations, failedRegistration)
	}

	mr.s.l.Lock()
	mr.s.metricRetryAt = minRetryAt
	mr.s.l.Unlock()

	mr.s.option.Cache.SetMetricRegistrationsFail(failedRegistrations)

	mr.logTooManyMetrics(
		nbTooManyMetrics[bleemeoTypes.FailureTooManyStandardMetrics],
		nbTooManyMetrics[bleemeoTypes.FailureTooManyCustomMetrics],
	)

	return err
}

func (mr *metricRegisterer) logTooManyMetrics(nbFailedStandardMetrics, nbFailedCustomMetrics int) {
	if nbFailedCustomMetrics > 0 {
		mr.s.logOnce.Do(func() {
			config, ok := mr.s.option.Cache.CurrentAccountConfig()

			maxCustomMetricsStr := ""
			if ok {
				maxCustomMetricsStr = fmt.Sprintf(" (%d)", config.MaxCustomMetrics)
			}

			const msg = "Failed to register %d metrics because you reached the maximum number of custom metrics%s. " +
				"Consider removing some metrics from your allowlist, see the documentation for more details " +
				"(https://docs.bleemeo.com/metrics-sources/filtering)."

			logger.V(0).Printf(msg, nbFailedCustomMetrics, maxCustomMetricsStr)
		})
	}

	if nbFailedStandardMetrics > 0 {
		mr.s.logOnce.Do(func() {
			const msg = "Failed to register %d metrics because you reached the maximum number of metrics. " +
				"Consider removing some metrics from your allowlist, see the documentation for more details " +
				"(https://docs.bleemeo.com/metrics-sources/filtering)."

			logger.V(0).Printf(msg, nbFailedStandardMetrics)
		})
	}
}

func (mr *metricRegisterer) do(localMetrics []types.Metric) error {
	for state := metricPassAgentStatus; state <= metricPassRetry; state++ {
		var currentList []types.Metric

		switch state {
		case metricPassAgentStatus:
			currentList = []types.Metric{
				fakeMetric{label: agentStatusName},
			}
		case metricPassMain:
			currentList = localMetrics
		case metricPassRecreate:
			currentList = mr.needReregisterMetrics

			if len(mr.needReregisterMetrics) > 0 {
				metrics := make([]bleemeoTypes.Metric, 0, len(mr.registeredMetricsByUUID))

				for _, v := range mr.registeredMetricsByUUID {
					metrics = append(metrics, v)
				}

				mr.s.option.Cache.SetMetrics(metrics)

				if err := mr.s.metricUpdateList(mr.needReregisterMetrics); err != nil {
					return err
				}

				mr.registeredMetricsByUUID = mr.s.option.Cache.MetricsByUUID()
				mr.registeredMetricsByKey = common.MetricLookupFromList(mr.s.option.Cache.Metrics())
			}
		case metricPassRetry:
			currentList = mr.retryMetrics
		}

		if err := mr.doOnePass(currentList, state); err != nil {
			return err
		}
	}

	return mr.pendingErr
}

func (mr *metricRegisterer) doOnePass(currentList []types.Metric, state metricRegisterPass) error {
	logger.V(3).Printf("Metric registration phase %v start with %d metrics to process", state, len(currentList))

	for _, metric := range currentList {
		if mr.s.ctx.Err() != nil {
			break
		}

		labels := metric.Labels()
		annotations := metric.Annotations()
		key := mr.s.metricKey(labels, annotations)

		registration, hadPreviousFailure := mr.failedRegistrationByKey[key]
		if hadPreviousFailure {
			if registration.LastFailKind.IsPermanentFailure() && !mr.s.retryableMetricFailure[registration.LastFailKind] {
				continue
			}

			if mr.s.now().Before(registration.RetryAfter()) {
				continue
			}
		}

		err := mr.metricRegisterAndUpdateOne(metric)
		if err != nil && errors.Is(err, errRetryLater) && state < metricPassRetry {
			mr.retryMetrics = append(mr.retryMetrics, metric)

			continue
		}

		if errReReg, ok := err.(needRegisterError); ok && err != nil && state < metricPassRecreate {
			mr.needReregisterMetrics = append(mr.needReregisterMetrics, metric)

			delete(mr.registeredMetricsByUUID, errReReg.remoteMetric.ID)
			delete(mr.registeredMetricsByKey, errReReg.remoteMetric.LabelsText)

			continue
		}

		if err != nil {
			if client.IsServerError(err) {
				return err
			}

			if client.IsBadRequest(err) {
				content := client.APIErrorContent(err)
				registration.LastFailKind = httpResponseToMetricFailureKind(content)
				registration.LastFailAt = mr.s.now()
				registration.FailCounter++
				registration.LabelsText = key

				mr.failedRegistrationByKey[key] = registration
				mr.s.retryableMetricFailure[registration.LastFailKind] = false
			} else if mr.pendingErr == nil {
				mr.pendingErr = err
			}

			mr.errorCount++
			if mr.errorCount > 10 {
				return err
			}

			time.Sleep(time.Duration(mr.errorCount/2) * time.Second)

			continue
		}

		delete(mr.failedRegistrationByKey, key)

		mr.regCountBeforeUpdate--
		if mr.regCountBeforeUpdate == 0 {
			mr.regCountBeforeUpdate = 60

			metrics := make([]bleemeoTypes.Metric, 0, len(mr.registeredMetricsByUUID))

			for _, v := range mr.registeredMetricsByUUID {
				metrics = append(metrics, v)
			}

			mr.s.option.Cache.SetMetrics(metrics)

			registrations := make([]bleemeoTypes.MetricRegistration, 0, len(mr.failedRegistrationByKey))
			for _, v := range mr.failedRegistrationByKey {
				registrations = append(registrations, v)
			}

			mr.s.option.Cache.SetMetricRegistrationsFail(registrations)
		}
	}

	return mr.s.ctx.Err()
}

func (mr *metricRegisterer) metricRegisterAndUpdateOne(metric types.Metric) error {
	params := map[string]string{
		"fields": metricFields,
	}
	labels := metric.Labels()
	annotations := metric.Annotations()
	key := mr.s.metricKey(labels, annotations)
	remoteMetric, remoteFound := mr.registeredMetricsByKey[key]

	if remoteFound {
		result, err := mr.s.metricUpdateOne(metric, remoteMetric)
		if err != nil {
			return err
		}

		mr.registeredMetricsByKey[key] = result
		mr.registeredMetricsByUUID[result.ID] = result

		return nil
	}

	payload, err := mr.s.prepareMetricPayload(metric, mr.registeredMetricsByKey, mr.containersByContainerID, mr.servicesByKey, mr.monitors)
	if errors.Is(err, errIgnore) {
		return nil
	}

	if err != nil {
		return err
	}

	var result metricPayload

	_, err = mr.s.client.Do(mr.s.ctx, "POST", "v1/metric/", params, payload, &result)
	if err != nil {
		return err
	}

	logger.V(2).Printf("Metric %v registered with UUID %s", key, result.ID)
	mr.registeredMetricsByKey[key] = result.metricFromAPI(mr.registeredMetricsByKey[result.ID].FirstSeenAt)
	mr.registeredMetricsByUUID[result.ID] = result.metricFromAPI(mr.registeredMetricsByKey[result.ID].FirstSeenAt)

	return nil
}

func (s *Synchronizer) prepareMetricPayload(metric types.Metric, registeredMetricsByKey map[string]bleemeoTypes.Metric, containersByContainerID map[string]bleemeoTypes.Container, servicesByKey map[serviceNameInstance]bleemeoTypes.Service, monitors []bleemeoTypes.Monitor) (metricPayload, error) {
	labels := metric.Labels()
	annotations := metric.Annotations()
	key := s.metricKey(labels, annotations)

	payload := metricPayload{
		Metric: bleemeoTypes.Metric{
			LabelsText: key,
			AgentID:    s.agentID,
		},
		Name: labels[types.LabelName],
	}

	if s.option.MetricFormat == types.MetricFormatBleemeo {
		agentID := s.agentID
		if metric.Annotations().BleemeoAgentID != "" {
			agentID = metric.Annotations().BleemeoAgentID
		}

		payload.Item = common.TruncateItem(annotations.BleemeoItem, annotations.ServiceName != "")
		if common.MetricOnlyHasItem(labels, agentID) {
			payload.LabelsText = ""
		}
	}

	var containerName string

	if annotations.StatusOf != "" {
		subLabels := make(map[string]string, len(labels))

		for k, v := range labels {
			subLabels[k] = v
		}

		subLabels[types.LabelName] = annotations.StatusOf

		subKey := s.metricKey(subLabels, annotations)
		metricStatusOf, ok := registeredMetricsByKey[subKey]

		if !ok {
			return payload, errRetryLater
		}

		payload.StatusOf = metricStatusOf.ID
	}

	if annotations.ContainerID != "" {
		container, ok := containersByContainerID[annotations.ContainerID]
		if !ok {
			// No error. When container get registered we trigger a metric synchronization
			return payload, errIgnore
		}

		containerName = container.Name
		payload.ContainerID = container.ID
	}

	if annotations.ServiceName != "" {
		srvKey := serviceNameInstance{name: annotations.ServiceName, instance: containerName}
		srvKey.truncateInstance()

		service, ok := servicesByKey[srvKey]
		if !ok {
			// No error. When service get registered we trigger a metric synchronization
			return payload, errIgnore
		}

		payload.ServiceID = service.ID
	}

	if annotations.AlertingRuleID != "" {
		payload.AlertingRuleID = annotations.AlertingRuleID
	}

	// override the agent and service UUIDs when the metric is a probe's
	if metric.Annotations().BleemeoAgentID != "" {
		if err := s.prepareMetricPayloadOtherAgent(&payload, metric.Annotations().BleemeoAgentID, labels, monitors); err != nil {
			return payload, err
		}
	}

	return payload, nil
}

func (s *Synchronizer) prepareMetricPayloadOtherAgent(payload *metricPayload, agentID string, labels map[string]string, monitors []bleemeoTypes.Monitor) error {
	if scaperID := labels[types.LabelScraperUUID]; scaperID != "" && scaperID != s.agentID {
		return fmt.Errorf("%w: %s, want %s", errAttemptToSpoof, scaperID, s.agentID)
	}

	for _, monitor := range monitors {
		if monitor.AgentID == agentID {
			payload.AgentID = monitor.AgentID
			payload.ServiceID = monitor.ID

			return nil
		}
	}

	agent, found := s.option.Cache.AgentsByUUID()[agentID]
	if found {
		payload.AgentID = agent.ID

		return nil
	}

	// This is not an error: either this is a metric from a local monitor, and it should
	// never be registred, or this is from a monitor that is not yet loaded, and it is
	// not an issue per se as it will get registered when monitors are loaded, as it will
	// trigger a metric synchronization.
	return errIgnore
}

func (s *Synchronizer) metricUpdateOne(metric types.Metric, remoteMetric bleemeoTypes.Metric) (bleemeoTypes.Metric, error) {
	updates := make(map[string]string)

	if !remoteMetric.DeactivatedAt.IsZero() {
		points, err := metric.Points(s.now().Add(-10*time.Minute), s.now())
		if err != nil {
			return remoteMetric, nil //nolint:nilerr // assume not seen, we don't re-activate this metric
		}

		if len(points) == 0 {
			return remoteMetric, nil
		}

		maxTime := points[0].Time

		for _, p := range points {
			if p.Time.After(maxTime) {
				maxTime = p.Time
			}
		}

		if maxTime.After(remoteMetric.DeactivatedAt.Add(10 * time.Second)) {
			logger.V(2).Printf("Mark active the metric %v (uuid %s)", remoteMetric.LabelsText, remoteMetric.ID)

			updates["active"] = stringTrue
			remoteMetric.DeactivatedAt = time.Time{}
		}
	}

	if len(updates) > 0 {
		fields := make([]string, 0, len(updates))
		for k := range updates {
			fields = append(fields, k)
		}

		_, err := s.client.Do(
			s.ctx,
			"PATCH",
			fmt.Sprintf("v1/metric/%s/", remoteMetric.ID),
			map[string]string{"fields": strings.Join(fields, ",")},
			updates,
			nil,
		)
		if err != nil && client.IsNotFound(err) {
			return remoteMetric, needRegisterError{remoteMetric: remoteMetric}
		} else if err != nil {
			return remoteMetric, err
		}
	}

	return remoteMetric, nil
}

// metricDeleteIgnoredServices deletes the metrics $SERVICE_NAME_status from services in service_ignore_check.
func (s *Synchronizer) metricDeleteIgnoredServices() error {
	localServices, err := s.option.Discovery.Discovery(s.ctx, 24*time.Hour)
	if err != nil {
		return err
	}

	longToShortKeyLookup := longToShortKey(localServices)

	registeredMetrics := s.option.Cache.MetricsByUUID()
	registeredMetricsByKey := s.option.Cache.MetricLookupFromList()

	for _, srv := range localServices {
		if !srv.CheckIgnored {
			continue
		}

		key := serviceNameInstance{name: srv.Name, instance: srv.ContainerName}

		_, ok := longToShortKeyLookup[key]
		if !ok {
			continue
		}

		labels := srv.LabelsOfStatus()
		metricKey := s.metricKey(labels, srv.AnnotationsOfStatus())

		if metric, ok := registeredMetricsByKey[metricKey]; ok {
			_, err := s.client.Do(s.ctx, "DELETE", fmt.Sprintf("v1/metric/%s/", metric.ID), nil, nil, nil)
			if err != nil {
				return err
			}

			delete(registeredMetrics, metric.ID)
			logger.V(2).Printf("Metric %v deleted", metricKey)
		}
	}

	metrics := make([]bleemeoTypes.Metric, 0, len(registeredMetrics))

	for _, v := range registeredMetrics {
		metrics = append(metrics, v)
	}

	s.option.Cache.SetMetrics(metrics)

	return nil
}

func (s *Synchronizer) undeactivableMetric(v bleemeoTypes.Metric, agents map[string]bleemeoTypes.Agent) bool {
	if v.Labels[types.LabelName] == "agent_sent_message" {
		return true
	}

	if v.Labels[types.LabelName] == agentStatusName {
		agent := agents[v.AgentID]
		snmpTypeID, found := s.getAgentType(bleemeoTypes.AgentTypeSNMP)

		// We only skip deactivation of agent_status when it not an SNMP agent.
		// On SNMP agent, "agent_status" isn't special.
		if !found || agent.AgentType != snmpTypeID {
			return true
		}
	}

	return false
}

func (s *Synchronizer) localMetricToMap(localMetrics []types.Metric) map[string]types.Metric {
	localByMetricKey := make(map[string]types.Metric, len(localMetrics))

	for _, v := range localMetrics {
		labels := v.Labels()
		key := s.metricKey(labels, v.Annotations())
		localByMetricKey[key] = v
	}

	return localByMetricKey
}

// metricDeactivate deactivates the registered metrics that didn't receive any points for some time.
func (s *Synchronizer) metricDeactivate(localMetrics []types.Metric) error {
	duplicatedKey := make(map[string]bool)
	localByMetricKey := s.localMetricToMap(localMetrics)
	agents := s.option.Cache.AgentsByUUID()

	registeredMetrics := s.option.Cache.MetricsByUUID()
	for k, v := range registeredMetrics {
		// Wait some time to receive points from the allowed metrics before deactivating them.
		//
		// Deactivate the registered custom metrics that are no longer allowed in the config.
		// When a user has reached the maximum number of custom metrics and removes a metric
		// from the allowlist (or add one to the denylist), we want to deactivate it quickly
		// so the number of custom metrics goes under the limit.
		now := s.now()
		if s.option.IsMetricAllowed(v.Labels) &&
			(now.Sub(s.startedAt) < 5*time.Minute || now.Sub(v.FirstSeenAt) < gracePeriod) {
			continue
		}

		if !v.DeactivatedAt.IsZero() {
			if s.now().Sub(v.DeactivatedAt) > 200*24*time.Hour {
				delete(registeredMetrics, k)
			}

			continue
		}

		if s.undeactivableMetric(v, agents) {
			continue
		}

		key := v.LabelsText
		metric, ok := localByMetricKey[key]

		if ok && !duplicatedKey[key] {
			duplicatedKey[key] = true

			if s.now().Sub(metric.LastPointReceivedAt()) < 70*time.Minute {
				continue
			}
		}

		logger.V(2).Printf("Mark inactive the metric %v (uuid %s)", key, v.ID)

		_, err := s.client.Do(
			s.ctx,
			"PATCH",
			fmt.Sprintf("v1/metric/%s/", v.ID),
			map[string]string{"fields": "active"},
			map[string]string{"active": stringFalse},
			nil,
		)
		if err != nil && client.IsNotFound(err) {
			delete(registeredMetrics, k)

			continue
		}

		if err != nil {
			return err
		}

		v.DeactivatedAt = s.now()
		registeredMetrics[k] = v
		s.retryableMetricFailure[bleemeoTypes.FailureTooManyCustomMetrics] = true
		s.retryableMetricFailure[bleemeoTypes.FailureTooManyStandardMetrics] = true

		if len(s.option.Cache.MetricRegistrationsFail()) > 0 {
			s.l.Lock()
			s.metricRetryAt = s.now()
			s.l.Unlock()
		}
	}

	metrics := make([]bleemeoTypes.Metric, 0, len(registeredMetrics))

	for _, v := range registeredMetrics {
		metrics = append(metrics, v)
	}

	s.option.Cache.SetMetrics(metrics)

	return nil
}
