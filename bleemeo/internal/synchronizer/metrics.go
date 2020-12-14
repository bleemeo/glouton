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
	"encoding/json"
	"errors"
	"fmt"
	"glouton/bleemeo/client"
	"glouton/bleemeo/internal/common"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/logger"
	"glouton/threshold"
	"glouton/types"
	"math/rand"
	"strings"
	"time"
)

// agentStatusName is the name of the special metrics used to store the agent connection status.
const agentStatusName = "agent_status"

var (
	errRetryLater = errors.New("metric registration should be retried laster")
)

type errNeedRegister struct {
	remoteMetric bleemeoTypes.Metric
	key          string
}

func (e errNeedRegister) Error() string {
	return fmt.Sprintf("metric %v was deleted from API, it need to be re-registered", e.key)
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
	return nil, errors.New("not implemented on fakeMetric")
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
	Name  string `json:"label,omitempty"`
	Agent string `json:"agent"`
}

// metricFromAPI convert a metricPayload received from API to a bleemeoTypes.Metric.
func (mp metricPayload) metricFromAPI() bleemeoTypes.Metric {
	if mp.Item != "" || mp.LabelsText == "" {
		mp.Labels = map[string]string{
			types.LabelName: mp.Name,
		}
		annotations := types.MetricAnnotations{
			BleemeoItem: mp.Item,
		}
		mp.LabelsText = common.LabelsToText(mp.Labels, annotations, true)
	} else {
		mp.Labels = types.TextToLabels(mp.LabelsText)
	}

	return mp.Metric
}

func prioritizeAndFilterMetrics(metrics []types.Metric, onlyEssential bool) []types.Metric {
	swapIdx := 0
	results := metrics

	if onlyEssential {
		results = make([]types.Metric, 0, 100)
	}

	for i, m := range metrics {
		switch m.Labels()[types.LabelName] {
		case "cpu_idle", "cpu_wait", "cpu_nice", "cpu_user", "cpu_system", "cpu_interrupt", "cpu_softirq", "cpu_steal",
			"mem_free", "mem_cached", "mem_buffered", "mem_used",
			"io_utilization", "io_read_bytes", "io_write_bytes", "io_reads",
			"io_writes", "net_bits_recv", "net_bits_sent", "net_packets_recv",
			"net_packets_sent", "net_err_in", "net_err_out", "disk_used_perc",
			"swap_used_perc", "cpu_used", "mem_used_perc",
			agentStatusName:
			if onlyEssential {
				results = append(results, m)
			} else {
				metrics[i], metrics[swapIdx] = metrics[swapIdx], metrics[i]
				swapIdx++
			}
		}
	}

	return results
}

func httpResponseToMetricFailureKind(content string) bleemeoTypes.FailureKind {
	switch {
	case strings.Contains(content, "metric is not whitelisted"):
		return bleemeoTypes.FailureAllowList
	case strings.Contains(content, "metric is not in allow-list"):
		return bleemeoTypes.FailureAllowList
	case strings.Contains(content, "Too many non standard metrics"):
		return bleemeoTypes.FailureTooManyMetric
	default:
		return bleemeoTypes.FailureUnknown
	}
}

// nearly a duplicate of mqtt.filterPoint, but not quite. Alas we cannot easily generalize this as go doesn't have generics (yet).
func (s *Synchronizer) filterMetrics(input []types.Metric) []types.Metric {
	result := make([]types.Metric, 0)

	currentAccountConfig := s.option.Cache.CurrentAccountConfig()
	accountConfigs := s.option.Cache.AccountConfigs()
	monitors := s.option.Cache.MonitorsByAgentUUID()

	for _, m := range input {
		// retrieve the appropriate configuration for the metric
		whitelist := currentAccountConfig.MetricsAgentWhitelistMap()

		if m.Annotations().BleemeoAgentID != "" {
			monitor, present := monitors[bleemeoTypes.AgentID(m.Annotations().BleemeoAgentID)]
			if !present {
				logger.V(2).Printf("mqtt: missing monitor for agent '%s'", m.Annotations().BleemeoAgentID)
				continue
			}

			accountConfig, present := accountConfigs[monitor.AccountConfig]
			if !present {
				logger.V(2).Printf("mqtt: missing account configuration '%s'", monitor.AccountConfig)
				continue
			}

			whitelist = accountConfig.MetricsAgentWhitelistMap()
		}

		if common.AllowMetric(m.Labels(), m.Annotations(), whitelist) {
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
		key := common.LabelsToText(v.Labels(), v.Annotations(), s.option.MetricFormat == types.MetricFormatBleemeo)

		if _, ok := registeredMetricsByKey[key]; ok {
			continue
		}

		result = append(result, v)
	}

	return result
}

func (s *Synchronizer) syncMetrics(fullSync bool, onlyEssential bool) error {
	localMetrics, err := s.option.Store.Metrics(nil)
	if err != nil {
		return err
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
		s.retryableMetricFailure[bleemeoTypes.FailureTooManyMetric] = true
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

	if fullSync {
		err := s.metricUpdateAll(false)
		if err != nil {
			s.UpdateMetrics(pendingMetricsUpdate...)
			return err
		}
	} else if len(pendingMetricsUpdate) > 0 {
		logger.V(2).Printf("Update %d metrics by UUID", len(pendingMetricsUpdate))

		if err := s.metricUpdateListUUID(pendingMetricsUpdate); err != nil {
			s.UpdateMetrics(pendingMetricsUpdate...)
			return err
		}
	}

	unregisteredMetrics = s.findUnregisteredMetrics(filteredMetrics)
	unregisteredMetrics = s.excludeUnregistrableMetrics(unregisteredMetrics)

	logger.V(2).Printf("Searching %d metrics that may be inactive", len(unregisteredMetrics))

	err = s.metricUpdateInactiveList(unregisteredMetrics, fullSync)
	if err != nil {
		return err
	}

	if fullSync || len(unregisteredMetrics) > 0 || len(pendingMetricsUpdate) > 0 {
		s.UpdateUnitsAndThresholds(false)
	}

	if err := s.metricDeleteFromRemote(filteredMetrics, previousMetrics); err != nil {
		return err
	}

	localMetrics, err = s.option.Store.Metrics(nil)
	if err != nil {
		return err
	}

	filteredMetrics = s.filterMetrics(localMetrics)

	// If one metric fail to register, it may block other metric that would register correctly.
	// To reduce this risk, randomize the list, so on next run, the metric that failed to register
	// may no longer block other.
	rand.Shuffle(len(filteredMetrics), func(i, j int) {
		filteredMetrics[i], filteredMetrics[j] = filteredMetrics[j], filteredMetrics[i]
	})

	filteredMetrics = prioritizeAndFilterMetrics(filteredMetrics, onlyEssential)

	if err := s.metricRegisterAndUpdate(filteredMetrics); err != nil {
		return err
	}

	if onlyEssential {
		return nil
	}

	if err := s.metricDeleteFromLocal(); err != nil {
		return err
	}

	if s.now().Sub(s.startedAt) > 5*time.Minute {
		if err := s.metricDeactivate(filteredMetrics); err != nil {
			return err
		}
	}

	s.lastMetricCount = len(localMetrics)

	return nil
}

// UpdateUnitsAndThresholds update metrics units & threshold (from cache).
func (s *Synchronizer) UpdateUnitsAndThresholds(firstUpdate bool) {
	thresholds := make(map[threshold.MetricNameItem]threshold.Threshold)
	units := make(map[threshold.MetricNameItem]threshold.Unit)

	for _, m := range s.option.Cache.Metrics() {
		key := threshold.MetricNameItem{Name: m.Labels[types.LabelName], Item: m.Item}
		thresholds[key] = m.Threshold.ToInternalThreshold()
		units[key] = m.Unit
	}

	if s.option.UpdateThresholds != nil {
		s.option.UpdateThresholds(thresholds, firstUpdate)
	}

	if s.option.UpdateUnits != nil {
		s.option.UpdateUnits(units)
	}
}

// metricsListWithAgentID fetches the list of all metrics for a given agent, and returns a UUID:metric mapping.
func (s *Synchronizer) metricsListWithAgentID(agentID string, fetchInactive bool) (map[string]bleemeoTypes.Metric, error) {
	params := map[string]string{
		"agent":  agentID,
		"fields": "id,item,label,labels_text,unit,unit_text,deactivated_at,threshold_low_warning,threshold_low_critical,threshold_high_warning,threshold_high_critical,service,container,status_of",
	}

	if fetchInactive {
		params["active"] = "False"
	} else {
		params["active"] = "True"
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

		// Do not modify metrics declared by other agents when the target agent is a monitor
		if agentID != s.agentID {
			agentUUID, present := types.TextToLabels(metric.LabelsText)[types.LabelScraperUUID]

			if !present || agentUUID != s.agentID {
				continue
			}
		}

		metricsByUUID[metric.ID] = metric.metricFromAPI()
	}

	return metricsByUUID, nil
}

func (s *Synchronizer) metricUpdateAll(fetchInactive bool) error {
	// iterate over our agent AND every monitor for metrics
	metrics := []bleemeoTypes.Metric{}

	agentMetrics, err := s.metricsListWithAgentID(s.agentID, fetchInactive)
	if err != nil {
		return err
	}

	for _, metric := range agentMetrics {
		metrics = append(metrics, metric)
	}

	for _, monitor := range s.option.Cache.Monitors() {
		monitorMetrics, err := s.metricsListWithAgentID(monitor.AgentID, fetchInactive)
		if err != nil {
			return err
		}

		for _, metric := range monitorMetrics {
			metrics = append(metrics, metric)
		}
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
			"agent":     s.agentID,
			"page_size": "1",
			"page":      "1", // force using page number paginator which return count
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
			"fields":      "id,label,item,labels_text,unit,unit_text,service,container,deactivated_at,threshold_low_warning,threshold_low_critical,threshold_high_warning,threshold_high_critical,status_of",
		}

		if s.option.MetricFormat == types.MetricFormatBleemeo {
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
			if agentID != s.agentID {
				agentUUID, present := types.TextToLabels(metric.LabelsText)[types.LabelScraperUUID]

				if !present || agentUUID != s.agentID {
					continue
				}
			}

			metricsByUUID[metric.ID] = metric.metricFromAPI()
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
			"fields": "id,label,item,labels_text,unit,unit_text,service,container,deactivated_at,threshold_low_warning,threshold_low_critical,threshold_high_warning,threshold_high_critical,status_of",
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

		metricsByUUID[metric.ID] = metric.metricFromAPI()
	}

	metrics := make([]bleemeoTypes.Metric, 0, len(metricsByUUID))

	for _, m := range metricsByUUID {
		metrics = append(metrics, m)
	}

	s.option.Cache.SetMetrics(metrics)

	return nil
}

func (s *Synchronizer) metricDeleteFromRemote(localMetrics []types.Metric, previousMetrics map[string]bleemeoTypes.Metric) error {
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
		key := common.LabelsToText(labels, m.Annotations(), s.option.MetricFormat == types.MetricFormatBleemeo)

		if _, ok := deletedMetricLabelItem[key]; ok {
			localMetricToDelete = append(localMetricToDelete, labels)
		}
	}

	s.option.Store.DropMetrics(localMetricToDelete)

	return nil
}

func (s *Synchronizer) metricRegisterAndUpdate(localMetrics []types.Metric) error { // nolint: gocyclo
	registeredMetricsByUUID := s.option.Cache.MetricsByUUID()
	registeredMetricsByKey := common.MetricLookupFromList(s.option.Cache.Metrics())

	containersByContainerID := s.option.Cache.ContainersByContainerID()
	services := s.option.Cache.Services()
	servicesByKey := make(map[serviceNameInstance]bleemeoTypes.Service, len(services))
	failedRegistrationByKey := make(map[string]bleemeoTypes.MetricRegistration)

	for _, v := range s.option.Cache.MetricRegistrationsFail() {
		failedRegistrationByKey[v.LabelsText] = v
	}

	for _, v := range services {
		k := serviceNameInstance{name: v.Label, instance: v.Instance}
		servicesByKey[k] = v
	}

	monitors := s.option.Cache.Monitors()

	params := map[string]string{
		"fields": "id,label,item,labels_text,unit,unit_text,service,container,deactivated_at,threshold_low_warning,threshold_low_critical,threshold_high_warning,threshold_high_critical,status_of,agent",
	}
	regCountBeforeUpdate := 30
	errorCount := 0

	var firstErr error

	retryMetrics := make([]types.Metric, 0)
	registerMetrics := make([]types.Metric, 0)

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
			currentList = registerMetrics

			if len(registerMetrics) > 0 {
				metrics := make([]bleemeoTypes.Metric, 0, len(registeredMetricsByUUID))

				for _, v := range registeredMetricsByUUID {
					metrics = append(metrics, v)
				}

				s.option.Cache.SetMetrics(metrics)

				if err := s.metricUpdateList(registerMetrics); err != nil {
					return err
				}

				registeredMetricsByUUID = s.option.Cache.MetricsByUUID()
				registeredMetricsByKey = common.MetricLookupFromList(s.option.Cache.Metrics())
			}
		case metricPassRetry:
			currentList = retryMetrics
		}

		logger.V(3).Printf("Metric registration phase %v start with %d metrics to process", state, len(currentList))

	metricLoop:
		for _, metric := range currentList {
			if s.ctx.Err() != nil {
				break
			}

			labels := metric.Labels()
			annotations := metric.Annotations()
			key := common.LabelsToText(labels, annotations, s.option.MetricFormat == types.MetricFormatBleemeo)

			registration, hadPreviousFailure := failedRegistrationByKey[key]
			if hadPreviousFailure {
				if registration.LastFailKind.IsPermanentFailure() && !s.retryableMetricFailure[registration.LastFailKind] {
					continue
				}

				if s.now().Before(registration.RetryAfter()) {
					continue
				}
			}

			err := s.metricRegisterAndUpdateOne(metric, registeredMetricsByUUID, registeredMetricsByKey, containersByContainerID, servicesByKey, params, monitors)
			if err != nil && err == errRetryLater && state < metricPassRetry {
				retryMetrics = append(retryMetrics, metric)
				continue metricLoop
			}

			if errReReg, ok := err.(errNeedRegister); ok && err != nil && state < metricPassRecreate {
				registerMetrics = append(registerMetrics, metric)

				delete(registeredMetricsByUUID, errReReg.remoteMetric.ID)
				delete(registeredMetricsByKey, errReReg.key)

				continue metricLoop
			}

			if err != nil {
				if client.IsServerError(err) {
					return err
				}

				if client.IsBadRequest(err) {
					content := client.APIErrorContent(err)
					registration.LastFailKind = httpResponseToMetricFailureKind(content)
					registration.LastFailAt = s.now()
					registration.FailCounter++
					registration.LabelsText = key

					failedRegistrationByKey[key] = registration
					s.retryableMetricFailure[registration.LastFailKind] = false

					continue
				}

				if firstErr == nil {
					firstErr = err
				}

				errorCount++
				if errorCount > 10 {
					return err
				}

				time.Sleep(time.Duration(errorCount/2) * time.Second)

				continue metricLoop
			}

			delete(failedRegistrationByKey, key)

			regCountBeforeUpdate--
			if regCountBeforeUpdate == 0 {
				regCountBeforeUpdate = 60

				metrics := make([]bleemeoTypes.Metric, 0, len(registeredMetricsByUUID))

				for _, v := range registeredMetricsByUUID {
					metrics = append(metrics, v)
				}

				s.option.Cache.SetMetrics(metrics)

				registrations := make([]bleemeoTypes.MetricRegistration, 0, len(failedRegistrationByKey))
				for _, v := range failedRegistrationByKey {
					registrations = append(registrations, v)
				}

				s.option.Cache.SetMetricRegistrationsFail(registrations)
			}
		}
	}

	metrics := make([]bleemeoTypes.Metric, 0, len(registeredMetricsByUUID))

	for _, v := range registeredMetricsByUUID {
		metrics = append(metrics, v)
	}

	s.option.Cache.SetMetrics(metrics)

	minRetryAt := s.now().Add(time.Hour)

	registrations := make([]bleemeoTypes.MetricRegistration, 0, len(failedRegistrationByKey))

	for _, v := range failedRegistrationByKey {
		if !v.LastFailKind.IsPermanentFailure() && minRetryAt.After(v.RetryAfter()) {
			minRetryAt = v.RetryAfter()
		}

		registrations = append(registrations, v)
	}

	s.metricRetryAt = minRetryAt
	s.option.Cache.SetMetricRegistrationsFail(registrations)

	return firstErr
}

func (s *Synchronizer) metricRegisterAndUpdateOne(metric types.Metric, registeredMetricsByUUID map[string]bleemeoTypes.Metric,
	registeredMetricsByKey map[string]bleemeoTypes.Metric, containersByContainerID map[string]bleemeoTypes.Container,
	servicesByKey map[serviceNameInstance]bleemeoTypes.Service, params map[string]string, monitors []bleemeoTypes.Monitor) error {
	labels := metric.Labels()
	annotations := metric.Annotations()
	key := common.LabelsToText(labels, annotations, s.option.MetricFormat == types.MetricFormatBleemeo)
	remoteMetric, remoteFound := registeredMetricsByKey[key]

	if remoteFound {
		result, err := s.metricUpdateOne(key, metric, remoteMetric)
		if err != nil {
			return err
		}

		registeredMetricsByKey[key] = result
		registeredMetricsByUUID[result.ID] = result

		return nil
	}

	payload := metricPayload{
		Metric: bleemeoTypes.Metric{
			LabelsText: key,
		},
		Name:  labels[types.LabelName],
		Agent: s.agentID,
	}

	if s.option.MetricFormat == types.MetricFormatBleemeo {
		payload.Item = common.TruncateItem(annotations.BleemeoItem, annotations.ServiceName != "")
		payload.LabelsText = ""
	}

	var (
		containerName string
		result        metricPayload
	)

	if annotations.StatusOf != "" {
		subLabels := make(map[string]string, len(labels))

		for k, v := range labels {
			subLabels[k] = v
		}

		subLabels[types.LabelName] = annotations.StatusOf

		subKey := common.LabelsToText(subLabels, annotations, s.option.MetricFormat == types.MetricFormatBleemeo)
		metricStatusOf, ok := registeredMetricsByKey[subKey]

		if !ok {
			return errRetryLater
		}

		payload.StatusOf = metricStatusOf.ID
	}

	if annotations.ContainerID != "" {
		container, ok := containersByContainerID[annotations.ContainerID]
		if !ok {
			// No error. When container get registered we trigger a metric synchronization
			return nil
		}

		containerName = container.Name
		payload.ContainerID = container.ID
		// TODO: For now all metrics are created no-associated with a container.
		// PRODUCT-970 track the progress on this point
		payload.ContainerID = ""
	}

	if annotations.ServiceName != "" {
		srvKey := serviceNameInstance{name: annotations.ServiceName, instance: containerName}
		srvKey.truncateInstance()

		service, ok := servicesByKey[srvKey]
		if !ok {
			// No error. When service get registered we trigger a metric synchronization
			return nil
		}

		payload.ServiceID = service.ID
	}

	// override the agent and service UUIDs when the metric is a probe's
	if metric.Annotations().BleemeoAgentID != "" {
		found := false

		if metric.Labels()[types.LabelScraperUUID] != s.agentID {
			return fmt.Errorf("attempt to spoof another agent (or missing label): %s, want %s", metric.Labels()[types.LabelScraperUUID], s.agentID)
		}

		for _, monitor := range monitors {
			if monitor.AgentID == metric.Annotations().BleemeoAgentID {
				payload.Agent = monitor.AgentID
				payload.ServiceID = monitor.ID
				found = true

				break
			}
		}

		if !found {
			// This is not an error: either this is a metric from a local monitor, and it should
			// never be registred, or this is from a monitor that is not yet loaded, and it is
			// not an issue per se as it will get registered when monitors are loaded, as it will
			// trigger a metric synchronization.
			return nil
		}
	}

	_, err := s.client.Do(s.ctx, "POST", "v1/metric/", params, payload, &result)
	if err != nil {
		return err
	}

	logger.V(2).Printf("Metric %v registered with UUID %s", key, result.ID)
	registeredMetricsByKey[key] = result.metricFromAPI()
	registeredMetricsByUUID[result.ID] = result.metricFromAPI()

	return nil
}

func (s *Synchronizer) metricUpdateOne(key string, metric types.Metric, remoteMetric bleemeoTypes.Metric) (bleemeoTypes.Metric, error) {
	if !remoteMetric.DeactivatedAt.IsZero() {
		points, err := metric.Points(s.now().Add(-10*time.Minute), s.now())
		if err != nil {
			return remoteMetric, nil // assume not seen, we don't re-activate this metric
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
			logger.V(2).Printf("Mark active the metric %v (uuid %s)", key, remoteMetric.ID)

			_, err := s.client.Do(
				s.ctx,
				"PATCH",
				fmt.Sprintf("v1/metric/%s/", remoteMetric.ID),
				map[string]string{"fields": "active"},
				map[string]string{"active": "True"},
				nil,
			)
			if err != nil && client.IsNotFound(err) {
				return remoteMetric, errNeedRegister{remoteMetric: remoteMetric, key: key}
			} else if err != nil {
				return remoteMetric, err
			}

			remoteMetric.DeactivatedAt = time.Time{}
		}
	}

	return remoteMetric, nil
}

func (s *Synchronizer) metricDeleteFromLocal() error {
	// We only delete metric $SERVICE_NAME_status from service with ignore_check=True
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
		metricKey := common.LabelsToText(labels, srv.AnnotationsOfStatus(), s.option.MetricFormat == types.MetricFormatBleemeo)

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

func (s *Synchronizer) metricDeactivate(localMetrics []types.Metric) error {
	duplicatedKey := make(map[string]bool)
	localByMetricKey := make(map[string]types.Metric, len(localMetrics))

	for _, v := range localMetrics {
		labels := v.Labels()
		key := common.LabelsToText(labels, v.Annotations(), s.option.MetricFormat == types.MetricFormatBleemeo)
		localByMetricKey[key] = v
	}

	registeredMetrics := s.option.Cache.MetricsByUUID()
	for k, v := range registeredMetrics {
		if !v.DeactivatedAt.IsZero() {
			if s.now().Sub(v.DeactivatedAt) > 200*24*time.Hour {
				delete(registeredMetrics, k)
			}

			continue
		}

		if v.Labels[types.LabelName] == "agent_sent_message" || v.Labels[types.LabelName] == agentStatusName {
			continue
		}

		key := v.LabelsText
		metric, ok := localByMetricKey[key]

		if ok && !duplicatedKey[key] {
			duplicatedKey[key] = true

			points, _ := metric.Points(s.now().Add(-70*time.Minute), s.now())
			if len(points) > 0 {
				continue
			}
		}

		logger.V(2).Printf("Mark inactive the metric %v (uuid %s)", key, v.ID)

		_, err := s.client.Do(
			s.ctx,
			"PATCH",
			fmt.Sprintf("v1/metric/%s/", v.ID),
			map[string]string{"fields": "active"},
			map[string]string{"active": "False"},
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
		s.retryableMetricFailure[bleemeoTypes.FailureTooManyMetric] = true

		if len(s.option.Cache.MetricRegistrationsFail()) > 0 {
			s.metricRetryAt = s.now()
		}
	}

	metrics := make([]bleemeoTypes.Metric, 0, len(registeredMetrics))

	for _, v := range registeredMetrics {
		metrics = append(metrics, v)
	}

	s.option.Cache.SetMetrics(metrics)

	return nil
}
