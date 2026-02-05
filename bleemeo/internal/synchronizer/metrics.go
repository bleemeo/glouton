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

package synchronizer

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/common"
	"github.com/bleemeo/glouton/bleemeo/internal/filter"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/threshold"
	gloutonTypes "github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/metricutils"
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
	"threshold_low_warning,threshold_low_critical,threshold_high_warning,threshold_high_critical,status_of,agent"

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

// The metric registration function is done in four passes
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

func (m fakeMetric) Points(time.Time, time.Time) ([]gloutonTypes.Point, error) {
	return nil, errNotImplemented
}

func (m fakeMetric) LastPointReceivedAt() time.Time {
	return time.Now()
}

func (m fakeMetric) Annotations() gloutonTypes.MetricAnnotations {
	return gloutonTypes.MetricAnnotations{}
}

func (m fakeMetric) Labels() map[string]string {
	return map[string]string{
		gloutonTypes.LabelName: m.label,
	}
}

// metricFromAPI convert a metricPayload received from API to a bleemeoTypes.Metric.
func metricFromAPI(mp bleemeoapi.MetricPayload, lastTime time.Time) bleemeoTypes.Metric {
	if mp.LabelsText == "" {
		mp.Labels = map[string]string{
			gloutonTypes.LabelName:         mp.Name,
			gloutonTypes.LabelInstanceUUID: mp.AgentID,
		}

		if mp.Item != "" {
			mp.Labels[gloutonTypes.LabelItem] = mp.Item
		}

		mp.LabelsText = gloutonTypes.LabelsToText(mp.Labels)
	} else {
		mp.Labels = gloutonTypes.TextToLabels(mp.LabelsText)
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

func newComparator() *metricComparator {
	essentials := []string{
		"cpu_idle", "cpu_wait", "cpu_nice", "cpu_user", "cpu_system", "cpu_interrupt", "cpu_softirq", "cpu_steal", "cpu_guest_nice", "cpu_guest",
		"mem_free", "mem_cached", "mem_buffered", "mem_used",
		"io_utilization", "io_read_bytes", "io_write_bytes", "io_reads",
		"io_writes", "net_bits_recv", "net_bits_sent", "net_packets_recv",
		"net_packets_sent", "net_err_in", "net_err_out", "disk_used_perc",
		"swap_used_perc", "cpu_used", "mem_used_perc", "agent_config_warning", agentStatusName,
	}

	highCard := []string{
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
	name := metric[gloutonTypes.LabelName]
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
	name := metric[gloutonTypes.LabelName]
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

func prioritizeAndFilterMetrics(metrics []gloutonTypes.Metric, onlyEssential bool) []gloutonTypes.Metric {
	cmp := newComparator()

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

func (s *Synchronizer) metricKey(lbls map[string]string, annotations gloutonTypes.MetricAnnotations) string {
	return metricutils.MetricKey(lbls, annotations, s.agentID)
}

// filterMetrics only keeps the points that can be registered.
func (s *Synchronizer) filterMetrics(input []gloutonTypes.Metric) []gloutonTypes.Metric {
	result := make([]gloutonTypes.Metric, 0)

	f := filter.NewFilter(s.option.Cache)

	for _, m := range input {
		allow, denyReason, err := f.IsAllowed(m.Labels(), m.Annotations())
		if err != nil {
			logger.V(2).Printf("sync: %s", err)

			continue
		}

		if allow {
			result = append(result, m)
		} else if denyReason == bleemeoTypes.DenyItemTooLong {
			msg := fmt.Sprintf(
				"Metric %s will be ignored because the item '%s' is too long (> %d characters)",
				m.Labels()[gloutonTypes.LabelName], m.Labels()[gloutonTypes.LabelItem], common.APIMetricItemLength,
			)

			s.logThrottle(msg)
		}
	}

	return s.excludeUnregistrableMetrics(result)
}

// excludeUnregistrableMetrics remove metrics that cannot be registered, due to either
// a missing dependency (like a container that must be registered before) or an item too long.
func (s *Synchronizer) excludeUnregistrableMetrics(metrics []gloutonTypes.Metric) []gloutonTypes.Metric {
	result := make([]gloutonTypes.Metric, 0, len(metrics))
	containersByContainerID := s.option.Cache.ContainersByContainerID()
	servicesByKey := s.option.Cache.ServiceLookupFromList()

	cfg, ok := s.option.Cache.CurrentAccountConfig()
	if !ok {
		return nil
	}

	for _, metric := range metrics {
		annotations := metric.Annotations()

		// Exclude metrics with a missing container dependency.
		if annotations.ContainerID != "" && !common.IgnoreContainer(cfg, metric.Labels()) {
			_, ok := containersByContainerID[annotations.ContainerID]
			if !ok {
				continue
			}
		}

		// Exclude metrics with a missing service dependency.
		if annotations.ServiceName != "" {
			srvKey := common.ServiceNameInstance{Name: annotations.ServiceName, Instance: annotations.ServiceInstance}

			if _, ok := servicesByKey[srvKey]; !ok {
				continue
			}
		}

		result = append(result, metric)
	}

	return result
}

func (s *Synchronizer) findUnregisteredMetrics(metrics []gloutonTypes.Metric) []gloutonTypes.Metric {
	registeredMetricsByKey := s.option.Cache.MetricLookupFromList()

	result := make([]gloutonTypes.Metric, 0)

	for _, v := range metrics {
		key := s.metricKey(v.Labels(), v.Annotations())
		if _, ok := registeredMetricsByKey[key]; ok {
			continue
		}

		result = append(result, v)
	}

	return result
}

func (s *Synchronizer) syncMetrics(ctx context.Context, syncType types.SyncType, execution types.SynchronizationExecution) (updateThresholds bool, err error) {
	localMetrics, err := s.option.Store.Metrics(nil)
	if err != nil {
		return false, err
	}

	if s.successiveErrors == 3 {
		// After 3 error, try to force a full synchronization to see if it solve the issue.
		syncType = types.SyncTypeForceCacheRefresh
	}

	previousMetrics := s.option.Cache.MetricsByUUID()
	activeMetricsCount := 0

	for _, m := range previousMetrics {
		if m.DeactivatedAt.IsZero() {
			activeMetricsCount++
		}
	}

	if syncType == types.SyncTypeForceCacheRefresh {
		s.retryableMetricFailure[bleemeoTypes.FailureUnknown] = true
		s.retryableMetricFailure[bleemeoTypes.FailureAllowList] = true
		s.retryableMetricFailure[bleemeoTypes.FailureTooManyCustomMetrics] = true
		s.retryableMetricFailure[bleemeoTypes.FailureTooManyStandardMetrics] = true
	}

	pendingMetricsUpdate := s.popPendingMetricsUpdate()
	if len(pendingMetricsUpdate) > activeMetricsCount*3/100 {
		// If more than 3% of known active metrics needs update, do a full
		// update. 3% is arbitrarily chosen, based on assumption request for
		// one page of (100) metrics is cheaper than 3 requests for
		// one metric.
		syncType = types.SyncTypeForceCacheRefresh
	}

	filteredMetrics := s.filterMetrics(localMetrics)
	unregisteredMetrics := s.findUnregisteredMetrics(filteredMetrics)

	if len(unregisteredMetrics) > 3*len(previousMetrics)/100 {
		syncType = types.SyncTypeForceCacheRefresh
	}

	apiClient := execution.BleemeoAPIClient()

	err = s.metricUpdatePendingOrSync(ctx, apiClient, syncType == types.SyncTypeForceCacheRefresh, &pendingMetricsUpdate)
	if err != nil {
		return false, err
	}

	// Update the unregistered metrics as metricUpdatePendingOrSync may have changed them.
	unregisteredMetrics = s.findUnregisteredMetrics(filteredMetrics)

	logger.V(2).Printf("Searching %d metrics that may be inactive", len(unregisteredMetrics))

	err = s.metricUpdateInactiveList(ctx, apiClient, unregisteredMetrics, syncType == types.SyncTypeForceCacheRefresh)
	if err != nil {
		return false, err
	}

	updateThresholds = syncType == types.SyncTypeForceCacheRefresh || len(unregisteredMetrics) > 0 || len(pendingMetricsUpdate) > 0

	s.metricRemoveDeletedFromRemote(filteredMetrics, previousMetrics)

	// Update local metrics as metricRemoveDeletedFromRemote may have dropped some metrics in the store.
	localMetrics, err = s.option.Store.Metrics(nil)
	if err != nil {
		return updateThresholds, err
	}

	filteredMetrics = s.filterMetrics(localMetrics)
	filteredMetrics = prioritizeAndFilterMetrics(filteredMetrics, execution.IsOnlyEssential())

	if err = newMetricRegisterer(s, apiClient).registerMetrics(ctx, filteredMetrics); err != nil {
		return updateThresholds, err
	}

	if execution.IsOnlyEssential() {
		return updateThresholds, nil
	}

	if err := s.metricDeleteIgnoredServices(ctx, apiClient); err != nil {
		return updateThresholds, err
	}

	if err := s.metricDeactivate(ctx, apiClient, filteredMetrics); err != nil {
		return updateThresholds, err
	}

	s.state.l.Lock()
	defer s.state.l.Unlock()

	s.state.lastMetricCount = len(localMetrics)

	return updateThresholds, nil
}

func (s *Synchronizer) metricUpdatePendingOrSync(ctx context.Context, apiClient types.MetricClient, fullSync bool, pendingMetricsUpdate *[]string) error {
	if fullSync {
		err := s.metricUpdateAll(ctx, apiClient, false, nil)
		if err != nil {
			// Re-add the metric on the pending update
			s.UpdateMetrics(*pendingMetricsUpdate...)

			return err
		}
	} else if len(*pendingMetricsUpdate) > 0 {
		logger.V(2).Printf("Update %d metrics by UUID", len(*pendingMetricsUpdate))

		if err := s.metricUpdateListUUID(ctx, apiClient, *pendingMetricsUpdate); err != nil {
			// Re-add the metric on the pending update
			s.UpdateMetrics(*pendingMetricsUpdate...)

			return err
		}
	}

	return nil
}

// UpdateUnitsAndThresholds update metrics units & threshold (from cache).
func (s *Synchronizer) UpdateUnitsAndThresholds(firstUpdate bool) {
	thresholds := make(map[string]threshold.Threshold)
	units := make(map[string]threshold.Unit)
	defaultSoftPeriod := time.Duration(s.option.Config.Metric.SoftStatusPeriodDefault) * time.Second

	softPeriods := make(map[string]time.Duration, len(s.option.Config.Metric.SoftStatusPeriod))
	for metric, period := range s.option.Config.Metric.SoftStatusPeriod {
		softPeriods[metric] = time.Duration(period) * time.Second
	}

	s.l.Lock()
	thresholdOverrides := s.thresholdOverrides
	s.l.Unlock()

	for _, m := range s.option.Cache.Metrics() {
		if !m.DeactivatedAt.IsZero() {
			continue
		}

		units[m.LabelsText] = m.Unit

		metricName := m.Labels[gloutonTypes.LabelName]

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

		if !thresh.IsZero() {
			thresholds[m.LabelsText] = thresh
		}
	}

	if s.option.UpdateThresholds != nil {
		s.option.UpdateThresholds(thresholds, firstUpdate)
	}

	if s.option.UpdateUnits != nil {
		s.option.UpdateUnits(units)
	}
}

func (s *Synchronizer) isOwnedMetric(metric bleemeoapi.MetricPayload) bool {
	if metric.AgentID == s.agentID {
		return true
	}

	agentUUID, present := gloutonTypes.TextToLabels(metric.LabelsText)[gloutonTypes.LabelScraperUUID]

	if present && agentUUID == s.agentID {
		return true
	}

	scraperName, present := gloutonTypes.TextToLabels(metric.LabelsText)[gloutonTypes.LabelScraper]
	if present && scraperName == s.option.BlackboxScraperName {
		return true
	}

	// Only monitors have metric that are owned by multiple agents
	agentTypeID, found := s.getAgentType(bleemeo.AgentType_Monitor)
	if found {
		agents := s.option.Cache.AgentsByUUID()
		if agentTypeID == agents[metric.AgentID].AgentType {
			return false
		}
	}

	return true
}

// metricsListWithAgentID fetches the list of all metrics for a given agent, and returns a UUID:metric mapping.
func (s *Synchronizer) metricsListWithAgentID(ctx context.Context, apiClient types.MetricClient, fetchInactive bool, stopSearchingPredicate func(labelsText string) bool) (map[string]bleemeoTypes.Metric, error) {
	var (
		result []bleemeoapi.MetricPayload
		err    error
	)

	if fetchInactive {
		result, err = apiClient.ListInactiveMetrics(ctx, stopSearchingPredicate)
	} else {
		result, err = apiClient.ListActiveMetrics(ctx)
	}

	if err != nil {
		return nil, err
	}

	metricsByUUID := make(map[string]bleemeoTypes.Metric, len(result))

	for _, m := range s.option.Cache.Metrics() {
		if m.DeactivatedAt.IsZero() == fetchInactive {
			metricsByUUID[m.ID] = m
		}
	}

	for _, metric := range result {
		// If the metric is associated with another agent, make sure we "own" the metric.
		// This may not be the case for monitor because multiple agents will process such metrics.
		if !s.isOwnedMetric(metric) {
			continue
		}

		metricsByUUID[metric.ID] = metricFromAPI(metric, metricsByUUID[metric.ID].FirstSeenAt)
	}

	return metricsByUUID, nil
}

func (s *Synchronizer) metricUpdateAll(ctx context.Context, apiClient types.MetricClient, fetchInactive bool, stopSearchingPredicate func(labelsText string) bool) error {
	metricsMap, err := s.metricsListWithAgentID(ctx, apiClient, fetchInactive, stopSearchingPredicate)
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
func (s *Synchronizer) metricUpdateInactiveList(ctx context.Context, apiClient types.MetricClient, metrics []gloutonTypes.Metric, allowList bool) error {
	if len(metrics) < 10 || !allowList {
		return s.metricUpdateList(ctx, apiClient, metrics)
	}

	count, err := apiClient.CountInactiveMetrics(ctx)
	if err != nil {
		return err
	}

	if len(metrics) <= count/100 {
		// should be faster with one HTTP request per metrics
		return s.metricUpdateList(ctx, apiClient, metrics)
	}

	// Creating a hash-table of the metrics to find,
	// to allow ending the listing when they're all found.
	metricsToFind := make(map[string]struct{}, len(metrics))

	for _, metric := range metrics {
		metricsToFind[gloutonTypes.LabelsToText(metric.Labels())] = struct{}{}
	}

	// This stop-searching predicate returns true when
	// all the specified metrics have been found.
	predicate := func(labelsText string) bool {
		delete(metricsToFind, labelsText)

		return len(metricsToFind) == 0
	}

	// fewer queries with fetching the whole list
	return s.metricUpdateAll(ctx, apiClient, true, predicate)
}

// metricUpdateList fetches a list of metrics, and updates the cache.
func (s *Synchronizer) metricUpdateList(ctx context.Context, apiClient types.MetricClient, metrics []gloutonTypes.Metric) error {
	if len(metrics) == 0 {
		return nil
	}

	metricsByUUID := s.option.Cache.MetricsByUUID()

	for _, metric := range metrics {
		agentID := s.agentID
		if metric.Annotations().BleemeoAgentID != "" {
			agentID = metric.Annotations().BleemeoAgentID
		}

		params := url.Values{
			"labels_text": {gloutonTypes.LabelsToText(metric.Labels())},
			"agent":       {agentID},
			"fields":      {metricFields},
		}

		if metricutils.MetricOnlyHasItem(metric.Labels(), agentID) {
			params.Set("label", metric.Labels()[gloutonTypes.LabelName])
			params.Set("item", metric.Labels()[gloutonTypes.LabelItem])
			params.Del("labels_text")
		}

		listedMetrics, err := apiClient.ListMetricsBy(ctx, params)
		if err != nil {
			return err
		}

		for id, metric := range listedMetrics {
			// Don't modify metrics declared by other agents when the target agent is a monitor
			agentUUID, present := gloutonTypes.TextToLabels(metric.LabelsText)[gloutonTypes.LabelScraperUUID]
			if present && agentUUID != s.agentID {
				continue
			}

			if !present {
				scraperName, present := gloutonTypes.TextToLabels(metric.LabelsText)[gloutonTypes.LabelScraper]
				if present && scraperName != s.option.BlackboxScraperName {
					continue
				}
			}

			metricsByUUID[id] = metric
		}
	}

	rebuiltMetrics := make([]bleemeoTypes.Metric, 0, len(metricsByUUID))

	for _, m := range metricsByUUID {
		rebuiltMetrics = append(rebuiltMetrics, m)
	}

	s.option.Cache.SetMetrics(rebuiltMetrics)

	return nil
}

func (s *Synchronizer) metricUpdateListUUID(ctx context.Context, apiClient types.MetricClient, requests []string) error {
	metricsByUUID := s.option.Cache.MetricsByUUID()

	for _, key := range requests {
		metric, err := apiClient.GetMetricByID(ctx, key)
		if err != nil && IsNotFound(err) {
			delete(metricsByUUID, key)

			continue
		} else if err != nil {
			return err
		}

		metricsByUUID[metric.ID] = metricFromAPI(metric, metricsByUUID[metric.ID].FirstSeenAt)
	}

	metrics := make([]bleemeoTypes.Metric, 0, len(metricsByUUID))

	for _, m := range metricsByUUID {
		metrics = append(metrics, m)
	}

	s.option.Cache.SetMetrics(metrics)

	return nil
}

// metricRemoveDeletedFromRemote removes the local metrics that were deleted on the API.
func (s *Synchronizer) metricRemoveDeletedFromRemote(
	localMetrics []gloutonTypes.Metric,
	previousMetrics map[string]bleemeoTypes.Metric,
) {
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
	apiClient               types.MetricClient
	registeredMetricsByUUID map[string]bleemeoTypes.Metric
	registeredMetricsByKey  map[string]bleemeoTypes.Metric
	containersByContainerID map[string]bleemeoTypes.Container
	servicesByKey           map[common.ServiceNameInstance]bleemeoTypes.Service
	failedRegistrationByKey map[string]bleemeoTypes.MetricRegistration
	monitors                []bleemeoTypes.Monitor

	// Metric that need to be re-created
	needReregisterMetrics []gloutonTypes.Metric
	// Metric that should be re-tried (likely because they depend on another metric)
	retryMetrics []gloutonTypes.Metric

	regCountBeforeUpdate int
	errorCount           int
	pendingErr           error
	doneAtLeastOne       bool
}

func newMetricRegisterer(s *Synchronizer, apiClient types.MetricClient) *metricRegisterer {
	fails := s.option.Cache.MetricRegistrationsFail()
	servicesByKey := s.option.Cache.ServiceLookupFromList()
	failedRegistrationByKey := make(map[string]bleemeoTypes.MetricRegistration, len(fails))

	for _, v := range s.option.Cache.MetricRegistrationsFail() {
		failedRegistrationByKey[v.LabelsText] = v
	}

	return &metricRegisterer{
		s:                       s,
		apiClient:               apiClient,
		registeredMetricsByUUID: s.option.Cache.MetricsByUUID(),
		registeredMetricsByKey:  common.MetricLookupFromList(s.option.Cache.Metrics()),
		containersByContainerID: s.option.Cache.ContainersByContainerID(),
		servicesByKey:           servicesByKey,
		failedRegistrationByKey: failedRegistrationByKey,
		monitors:                s.option.Cache.Monitors(),
		regCountBeforeUpdate:    30,
		needReregisterMetrics:   make([]gloutonTypes.Metric, 0),
		retryMetrics:            make([]gloutonTypes.Metric, 0),
	}
}

func (mr *metricRegisterer) registerMetrics(ctx context.Context, localMetrics []gloutonTypes.Metric) error {
	err := mr.do(ctx, localMetrics)

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
		// Registration that haven't been retried for a long time correspond to metrics
		// that are no longer in the store, we can remove them.
		if now.Sub(failedRegistration.LastFailAt) > failedRegistrationExpiration {
			delete(mr.failedRegistrationByKey, key)

			continue
		}

		if !failedRegistration.LastFailKind.IsPermanentFailure() && minRetryAt.After(failedRegistration.RetryAfter()) {
			minRetryAt = failedRegistration.RetryAfter()
		}

		if failedRegistration.LastFailKind == bleemeoTypes.FailureTooManyCustomMetrics ||
			failedRegistration.LastFailKind == bleemeoTypes.FailureTooManyStandardMetrics {
			nbTooManyMetrics[failedRegistration.LastFailKind]++
		}

		failedRegistrations = append(failedRegistrations, failedRegistration)
	}

	mr.s.state.l.Lock()

	mr.s.state.metricRetryAt = minRetryAt

	if mr.doneAtLeastOne {
		mr.s.state.lastMetricActivation = mr.s.now()
	}

	mr.s.state.l.Unlock()

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
				"(https://go.bleemeo.com/l/agent-metrics-filtering)."

			logger.V(0).Printf(msg, nbFailedCustomMetrics, maxCustomMetricsStr)
		})
	}

	if nbFailedStandardMetrics > 0 {
		mr.s.logOnce.Do(func() {
			const msg = "Failed to register %d metrics because you reached the maximum number of metrics. " +
				"Consider removing some metrics from your allowlist, see the documentation for more details " +
				"(https://go.bleemeo.com/l/agent-metrics-filtering)."

			logger.V(0).Printf(msg, nbFailedStandardMetrics)
		})
	}
}

func (mr *metricRegisterer) do(ctx context.Context, localMetrics []gloutonTypes.Metric) error {
	for state := metricPassAgentStatus; state <= metricPassRetry; state++ {
		var currentList []gloutonTypes.Metric

		switch state {
		case metricPassAgentStatus:
			currentList = []gloutonTypes.Metric{
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

				if err := mr.s.metricUpdateList(ctx, mr.apiClient, mr.needReregisterMetrics); err != nil {
					return err
				}

				mr.registeredMetricsByUUID = mr.s.option.Cache.MetricsByUUID()
				mr.registeredMetricsByKey = common.MetricLookupFromList(mr.s.option.Cache.Metrics())
			}
		case metricPassRetry:
			currentList = mr.retryMetrics
		}

		if err := mr.doOnePass(ctx, currentList, state); err != nil {
			return err
		}
	}

	return mr.pendingErr
}

func (mr *metricRegisterer) doOnePass(ctx context.Context, currentList []gloutonTypes.Metric, state metricRegisterPass) error {
	logger.V(3).Printf("Metric registration phase %v start with %d metrics to process", state, len(currentList))

	for _, metric := range currentList {
		if ctx.Err() != nil {
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

		err := mr.metricRegisterAndUpdateOne(ctx, metric)
		if err != nil && errors.Is(err, errRetryLater) && state < metricPassRetry {
			mr.retryMetrics = append(mr.retryMetrics, metric)

			continue
		}

		if errReReg := new(needRegisterError); errors.As(err, errReReg) && state < metricPassRecreate {
			mr.needReregisterMetrics = append(mr.needReregisterMetrics, metric)

			delete(mr.registeredMetricsByUUID, errReReg.remoteMetric.ID)
			delete(mr.registeredMetricsByKey, errReReg.remoteMetric.LabelsText)

			continue
		}

		if err != nil {
			if IsServerError(err) {
				return err
			}

			if IsBadRequest(err) {
				content := APIErrorContent(err)
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

	return ctx.Err()
}

func (mr *metricRegisterer) metricRegisterAndUpdateOne(ctx context.Context, metric gloutonTypes.Metric) error {
	labels := metric.Labels()
	annotations := metric.Annotations()
	key := mr.s.metricKey(labels, annotations)
	remoteMetric, remoteFound := mr.registeredMetricsByKey[key]

	if remoteFound {
		result, err := mr.s.metricUpdateOne(ctx, mr.apiClient, metric, remoteMetric)
		if err != nil {
			return err
		}

		mr.registeredMetricsByKey[key] = result
		mr.registeredMetricsByUUID[result.ID] = result
		mr.doneAtLeastOne = true

		return nil
	}

	payload, err := mr.s.prepareMetricPayload(metric, mr.registeredMetricsByKey, mr.containersByContainerID, mr.servicesByKey, mr.monitors)
	if errors.Is(err, errIgnore) {
		return nil
	}

	if err != nil {
		return err
	}

	result, err := mr.apiClient.RegisterMetric(ctx, payload)
	if err != nil {
		return err
	}

	logger.V(2).Printf("Metric %v registered with UUID %s", key, result.ID)
	mr.registeredMetricsByKey[key] = metricFromAPI(result, mr.registeredMetricsByKey[result.ID].FirstSeenAt)
	mr.registeredMetricsByUUID[result.ID] = metricFromAPI(result, mr.registeredMetricsByKey[result.ID].FirstSeenAt)
	mr.doneAtLeastOne = true

	return nil
}

func (s *Synchronizer) prepareMetricPayload(
	metric gloutonTypes.Metric,
	registeredMetricsByKey map[string]bleemeoTypes.Metric,
	containersByContainerID map[string]bleemeoTypes.Container,
	servicesByKey map[common.ServiceNameInstance]bleemeoTypes.Service,
	monitors []bleemeoTypes.Monitor,
) (bleemeoapi.MetricPayload, error) {
	labels := metric.Labels()
	annotations := metric.Annotations()
	key := s.metricKey(labels, annotations)

	payload := bleemeoapi.MetricPayload{
		Metric: bleemeoTypes.Metric{
			LabelsText: key,
			AgentID:    s.agentID,
		},
		Name: labels[gloutonTypes.LabelName],
	}

	agentID := s.agentID
	if metric.Annotations().BleemeoAgentID != "" {
		agentID = metric.Annotations().BleemeoAgentID
	}

	if metricutils.MetricOnlyHasItem(labels, agentID) {
		payload.Item = labels[gloutonTypes.LabelItem]
		payload.LabelsText = ""
	}

	if annotations.StatusOf != "" {
		subLabels := make(map[string]string, len(labels))

		maps.Copy(subLabels, labels)

		subLabels[gloutonTypes.LabelName] = annotations.StatusOf

		subKey := s.metricKey(subLabels, annotations)
		metricStatusOf, ok := registeredMetricsByKey[subKey]

		if !ok {
			return payload, errRetryLater
		}

		payload.StatusOf = metricStatusOf.ID
	}

	cfg, ok := s.option.Cache.CurrentAccountConfig()
	if !ok {
		return payload, errRetryLater
	}

	if annotations.ContainerID != "" && !common.IgnoreContainer(cfg, metric.Labels()) {
		container, ok := containersByContainerID[annotations.ContainerID]
		if !ok {
			// No error. When container get registered we trigger a metric synchronization.
			return payload, errIgnore
		}

		payload.ContainerID = container.ID
	}

	if annotations.ServiceName != "" {
		srvKey := common.ServiceNameInstance{Name: annotations.ServiceName, Instance: annotations.ServiceInstance}

		service, ok := servicesByKey[srvKey]
		if !ok {
			// No error. When service get registered we trigger a metric synchronization
			return payload, errIgnore
		}

		payload.ServiceID = service.ID
	}

	// override the agent and service UUIDs when the metric is a probe's
	if metric.Annotations().BleemeoAgentID != "" {
		if err := s.prepareMetricPayloadOtherAgent(&payload, metric.Annotations().BleemeoAgentID, labels, monitors); err != nil {
			return payload, err
		}
	}

	return payload, nil
}

func (s *Synchronizer) prepareMetricPayloadOtherAgent(payload *bleemeoapi.MetricPayload, agentID string, labels map[string]string, monitors []bleemeoTypes.Monitor) error {
	if scaperID := labels[gloutonTypes.LabelScraperUUID]; scaperID != "" && scaperID != s.agentID {
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
	// never be registered, or this is from a monitor that is not yet loaded, and it is
	// not an issue per se as it will get registered when monitors are loaded, as it will
	// trigger a metric synchronization.
	return errIgnore
}

func (s *Synchronizer) metricUpdateOne(ctx context.Context, apiClient types.MetricClient, metric gloutonTypes.Metric, remoteMetric bleemeoTypes.Metric) (bleemeoTypes.Metric, error) {
	shouldMarkActive := false

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

			shouldMarkActive = true
			remoteMetric.DeactivatedAt = time.Time{}
		}
	}

	if shouldMarkActive {
		err := apiClient.SetMetricActive(ctx, remoteMetric.ID, true)
		if err != nil && IsNotFound(err) {
			return remoteMetric, needRegisterError{remoteMetric: remoteMetric}
		} else if err != nil {
			return remoteMetric, err
		}
	}

	return remoteMetric, nil
}

// metricDeleteIgnoredServices deletes the metrics $SERVICE_NAME_status from services in service_ignore_check.
func (s *Synchronizer) metricDeleteIgnoredServices(ctx context.Context, apiClient types.MetricClient) error {
	localServices, _ := s.option.Discovery.GetLatestDiscovery()

	registeredMetrics := s.option.Cache.MetricsByUUID()
	registeredMetricsByKey := s.option.Cache.MetricLookupFromList()

	for _, srv := range localServices {
		if !srv.CheckIgnored {
			continue
		}

		labels := srv.LabelsOfStatus()
		metricKey := s.metricKey(labels, srv.AnnotationsOfStatus())

		if metric, ok := registeredMetricsByKey[metricKey]; ok {
			err := apiClient.DeleteMetric(ctx, metric.ID)

			// If the metric wasn't found, it has already been deleted.
			if err != nil && !IsNotFound(err) {
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
	if v.Labels[gloutonTypes.LabelName] == "agent_sent_message" {
		return true
	}

	agent := agents[v.AgentID]

	if v.Labels[gloutonTypes.LabelName] == agentStatusName {
		snmpTypeID, found := s.getAgentType(bleemeo.AgentType_SNMP)

		// We only skip deactivation of agent_status when it not an SNMP agent.
		// On SNMP agent, "agent_status" isn't special.
		if !found || agent.AgentType != snmpTypeID {
			return true
		}
	}

	kubernetesTypeID, found := s.getAgentType(bleemeo.AgentType_K8s)
	if found && agent.AgentType == kubernetesTypeID && !s.option.Cache.Agent().IsClusterLeader {
		// Can't deactivate Kubernetes metrics if I'm not the leader.
		return true
	}

	return false
}

func (s *Synchronizer) localMetricToMap(localMetrics []gloutonTypes.Metric) map[string]gloutonTypes.Metric {
	localByMetricKey := make(map[string]gloutonTypes.Metric, len(localMetrics))

	for _, v := range localMetrics {
		labels := v.Labels()
		key := s.metricKey(labels, v.Annotations())
		localByMetricKey[key] = v
	}

	return localByMetricKey
}

// metricDeactivate deactivates the registered metrics that didn't receive any points for some time.
func (s *Synchronizer) metricDeactivate(ctx context.Context, apiClient types.MetricClient, localMetrics []gloutonTypes.Metric) error {
	deactivatedMetricsExpirationDays := time.Duration(s.option.Config.Bleemeo.Cache.DeactivatedMetricsExpirationDays) * 24 * time.Hour

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

		// If it's an old service status metric, wait a bit before deactivating it.
		// Without this wait, the following would happen:
		// * Glouton start, and don't yet created the new service_status metric (because check didn't yet run)
		// * but because the old metric isn't allowed, it get deactivated
		// * then later, when new metric is created, the old metric is renamed & reactivated.
		// With this wait, we avoid one HTTP request to deactivate the metric.
		if v.ServiceID != "" && strings.HasSuffix(v.Labels[gloutonTypes.LabelName], "_status") && now.Sub(s.startedAt) < 5*time.Minute {
			continue
		}

		if !v.DeactivatedAt.IsZero() {
			if s.now().Sub(v.DeactivatedAt) > deactivatedMetricsExpirationDays {
				delete(registeredMetrics, k)
			}

			continue
		}

		if s.undeactivableMetric(v, agents) {
			continue
		}

		key := v.LabelsText
		metric, ok := localByMetricKey[key]

		deactivateReason := "not found in local store, searched key: " + key

		if ok && !duplicatedKey[key] {
			duplicatedKey[key] = true

			deactivateReason = "found in local store, but last point received is too old: " + metric.LastPointReceivedAt().String()

			if s.now().Sub(metric.LastPointReceivedAt()) < 70*time.Minute {
				continue
			}
		} else if ok {
			deactivateReason = "found in local store, but was duplicate of another identical metric"
		}

		logger.V(2).Printf("Mark inactive the metric %v (uuid %s). Reason: %s", key, v.ID, deactivateReason)

		err := apiClient.SetMetricActive(ctx, v.ID, false)
		if err != nil && IsNotFound(err) {
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
			s.state.l.Lock()
			s.state.metricRetryAt = s.now()
			s.state.l.Unlock()
		}
	}

	metrics := make([]bleemeoTypes.Metric, 0, len(registeredMetrics))

	for _, v := range registeredMetrics {
		metrics = append(metrics, v)
	}

	s.option.Cache.SetMetrics(metrics)

	return nil
}
