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
	"agentgo/bleemeo/client"
	"agentgo/bleemeo/internal/common"
	"agentgo/bleemeo/types"
	"agentgo/logger"
	"agentgo/threshold"
	agentTypes "agentgo/types"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"
)

var (
	errRetryLater = errors.New("metric registration should be retried laster")
)

type errNeedRegister struct {
	remoteMetric types.Metric
	key          common.MetricLabelItem
}

func (e errNeedRegister) Error() string {
	return fmt.Sprintf("metric %v was deleted from API, it need to be re-registered", e.key)
}

// The metric registration function is done in 4 pass
// * first will only ensure agent_status is registered (this metric is required before connecting to MQTT and is produced once connected to MQTT...)
// * main pass will do the bulk of the jobs. But some metric may fail and will be done in Recreate and Retry pass
// * The Recreate pass is for metric that failed (to update) because they were deleted from API
// * The Retry pass is for metric that failed to create due to dependency with other metric (e.g. status_of)
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

func (m fakeMetric) Points(time.Time, time.Time) ([]agentTypes.PointStatus, error) {
	return nil, errors.New("not implemented on fakeMetric")
}
func (m fakeMetric) Labels() map[string]string {
	return map[string]string{
		"__name__": m.label,
	}
}

type metricPayload struct {
	types.Metric
	Agent string `json:"agent"`
	Item  string `json:"item,omitempty"`
}

// metricFromAPI convert a metricPayload received from API to a types.Metric
func (mp metricPayload) metricFromAPI() types.Metric {
	if mp.Labels["item"] == "" && mp.Item != "" {
		if mp.Labels == nil {
			mp.Labels = map[string]string{
				"item": mp.Item,
			}
		}
	}
	return mp.Metric
}

func prioritizeMetrics(metrics []agentTypes.Metric) {
	swapIdx := 0
	for i, m := range metrics {
		switch m.Labels()["__name__"] {
		case "cpu_idle", "cpu_wait", "cpu_nice", "cpu_user", "cpu_system", "cpu_interrupt", "cpu_softirq", "cpu_steal",
			"mem_free", "mem_cached", "mem_buffered", "mem_used",
			"io_utilization", "io_read_bytes", "io_write_bytes", "io_reads",
			"io_writes", "net_bits_recv", "net_bits_sent", "net_packets_recv",
			"net_packets_sent", "net_err_in", "net_err_out", "disk_used_perc",
			"swap_used_perc", "cpu_used", "mem_used_perc",
			"agent_status":
			metrics[i], metrics[swapIdx] = metrics[swapIdx], metrics[i]
			swapIdx++
		}
	}
}

func filterMetrics(input []agentTypes.Metric, metricWhitelist map[string]bool) []agentTypes.Metric {
	result := make([]agentTypes.Metric, 0)
	for _, m := range input {
		if common.AllowMetric(m.Labels(), metricWhitelist) {
			result = append(result, m)
		}
	}
	return result
}

func (s *Synchronizer) findUnregisteredMetrics(metrics []agentTypes.Metric) []common.MetricLabelItem {

	registeredMetrics := s.option.Cache.Metrics()
	registeredMetricsByKey := common.MetricLookupFromList(registeredMetrics)

	result := make([]common.MetricLabelItem, 0)
	for _, v := range metrics {
		key := common.MetricLabelItemFromMetric(v)
		if _, ok := registeredMetricsByKey[key]; ok {
			continue
		}
		result = append(result, key)

	}
	return result
}

func (s *Synchronizer) syncMetrics(fullSync bool) error {

	metricWhitelist := s.option.Cache.AccountConfig().MetricsAgentWhitelistMap()

	localMetrics, err := s.option.Store.Metrics(nil)
	if err != nil {
		return err
	}
	filteredMetrics := filterMetrics(localMetrics, metricWhitelist)

	unregisteredMetrics := s.findUnregisteredMetrics(filteredMetrics)

	if s.successiveErrors == 3 {
		// After 3 error, try to force a full synchronization to see if it solve the issue.
		fullSync = true
	}

	fullForInactive := false
	previousMetrics := s.option.Cache.MetricsByUUID()
	activeMetricsCount := 0
	for _, m := range previousMetrics {
		if m.DeactivatedAt.IsZero() {
			activeMetricsCount++
		}
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
		fullForInactive = true
		fullSync = true
	}

	if fullSync {
		s.apiSupportLabels = true // retry labels update
		err := s.metricUpdateList(fullForInactive)
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
	if !fullForInactive {
		logger.V(2).Printf("Searching %d metrics that may be inactive", len(unregisteredMetrics))
		err := s.metricUpdateListSearch(unregisteredMetrics)
		if err != nil {
			return err
		}
	}

	if fullSync || (!fullForInactive && len(unregisteredMetrics) > 0) || len(pendingMetricsUpdate) > 0 {
		s.UpdateUnitsAndThresholds(false)
	}

	if err := s.metricDeleteFromRemote(filteredMetrics, previousMetrics); err != nil {
		return err
	}

	localMetrics, err = s.option.Store.Metrics(nil)
	if err != nil {
		return err
	}
	filteredMetrics = filterMetrics(localMetrics, metricWhitelist)

	// If one metric fail to register, it may block other metric that would register correctly.
	// To reduce this risk, randomize the list, so on next run, the metric that failed to register
	// may no longer block other.
	rand.Shuffle(len(filteredMetrics), func(i, j int) {
		filteredMetrics[i], filteredMetrics[j] = filteredMetrics[j], filteredMetrics[i]
	})
	prioritizeMetrics(filteredMetrics)

	if err := s.metricRegisterAndUpdate(filteredMetrics, fullForInactive); err != nil {
		return err
	}
	if err := s.metricDeleteFromLocal(); err != nil {
		return err
	}
	if time.Since(s.startedAt) > 70*time.Minute {
		if err := s.metricDeactivate(filteredMetrics); err != nil {
			return err
		}
	}
	s.lastMetricCount = len(localMetrics)
	return nil
}

// UpdateUnitsAndThresholds update metrics units & threshold (from cache)
func (s *Synchronizer) UpdateUnitsAndThresholds(firstUpdate bool) {
	thresholds := make(map[threshold.MetricNameItem]threshold.Threshold)
	units := make(map[threshold.MetricNameItem]threshold.Unit)
	for _, m := range s.option.Cache.Metrics() {
		key := threshold.MetricNameItem{Name: m.Label, Item: m.Labels["item"]}
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

func (s *Synchronizer) metricUpdateList(includeInactive bool) error {
	params := map[string]string{
		"agent":  s.agentID,
		"fields": "id,item,label,labels,unit,unit_text,deactivated_at,threshold_low_warning,threshold_low_critical,threshold_high_warning,threshold_high_critical,service,container,status_of",
	}
	if !includeInactive {
		params["active"] = "True"
	}
	result, err := s.client.Iter("metric", params)
	if err != nil {
		return err
	}

	metricsByUUID := make(map[string]types.Metric, len(result))
	if !includeInactive {
		for _, m := range s.option.Cache.Metrics() {
			if !m.DeactivatedAt.IsZero() {
				metricsByUUID[m.ID] = m
			}
		}
	}

	for _, jsonMessage := range result {
		var metric metricPayload
		if err := json.Unmarshal(jsonMessage, &metric); err != nil {
			continue
		}
		metricsByUUID[metric.ID] = metric.metricFromAPI()
	}
	metrics := make([]types.Metric, 0, len(metricsByUUID))
	for _, m := range metricsByUUID {
		metrics = append(metrics, m)
	}
	s.option.Cache.SetMetrics(metrics)
	return nil
}

func (s *Synchronizer) metricUpdateListSearch(requests []common.MetricLabelItem) error {

	metricsByUUID := s.option.Cache.MetricsByUUID()

	for _, key := range requests {
		params := map[string]string{
			"agent":  s.agentID,
			"label":  key.Label,
			"item":   key.Item,
			"fields": "id,label,labels,item,unit,unit_text,service,container,deactivated_at,threshold_low_warning,threshold_low_critical,threshold_high_warning,threshold_high_critical,status_of",
		}
		result, err := s.client.Iter("metric", params)
		if err != nil {
			return err
		}

		for _, jsonMessage := range result {
			var metric metricPayload
			if err := json.Unmarshal(jsonMessage, &metric); err != nil {
				continue
			}
			metricsByUUID[metric.ID] = metric.metricFromAPI()
		}
	}
	metrics := make([]types.Metric, 0, len(metricsByUUID))
	for _, m := range metricsByUUID {
		metrics = append(metrics, m)
	}
	s.option.Cache.SetMetrics(metrics)
	return nil
}

func (s *Synchronizer) metricUpdateListUUID(requests []string) error {
	metricsByUUID := s.option.Cache.MetricsByUUID()

	for _, key := range requests {
		var metric metricPayload
		params := map[string]string{
			"fields": "id,label,labels,item,unit,unit_text,service,container,deactivated_at,threshold_low_warning,threshold_low_critical,threshold_high_warning,threshold_high_critical,status_of",
		}
		_, err := s.client.Do(
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
	metrics := make([]types.Metric, 0, len(metricsByUUID))
	for _, m := range metricsByUUID {
		metrics = append(metrics, m)
	}
	s.option.Cache.SetMetrics(metrics)
	return nil
}

func (s *Synchronizer) metricDeleteFromRemote(localMetrics []agentTypes.Metric, previousMetrics map[string]types.Metric) error {

	newMetrics := s.option.Cache.MetricsByUUID()

	deletedMetricLabelItem := make(map[common.MetricLabelItem]bool)
	for _, m := range previousMetrics {
		if _, ok := newMetrics[m.ID]; !ok {
			key := common.MetricLabelItem{Label: m.Label, Item: m.Labels["item"]}
			deletedMetricLabelItem[key] = true
		}
	}

	localMetricToDelete := make([]map[string]string, 0)
	for _, m := range localMetrics {
		labels := m.Labels()
		key := common.MetricLabelItemFromMetric(labels)
		if _, ok := deletedMetricLabelItem[key]; ok {
			localMetricToDelete = append(localMetricToDelete, labels)
		}
	}
	s.option.Store.DropMetrics(localMetricToDelete)
	return nil
}

func (s *Synchronizer) metricRegisterAndUpdate(localMetrics []agentTypes.Metric, fullForInactive bool) error {

	registeredMetricsByUUID := s.option.Cache.MetricsByUUID()
	registeredMetricsByKey := common.MetricLookupFromList(s.option.Cache.Metrics())

	containersByContainerID := s.option.Cache.ContainersByContainerID()
	services := s.option.Cache.Services()
	servicesByKey := make(map[serviceNameInstance]types.Service, len(services))
	for _, v := range services {
		k := serviceNameInstance{name: v.Label, instance: v.Instance}
		servicesByKey[k] = v
	}

	params := map[string]string{
		"fields": "id,label,labels,item,unit,unit_text,service,container,deactivated_at,threshold_low_warning,threshold_low_critical,threshold_high_warning,threshold_high_critical,status_of,agent,last_status,last_status_changed_at,problem_origins",
	}
	regCountBeforeUpdate := 30
	errorCount := 0
	var lastErr error
	retryMetrics := make([]agentTypes.Metric, 0)
	registerMetrics := make([]agentTypes.Metric, 0)
	for state := metricPassAgentStatus; state <= metricPassRetry; state++ {
		var currentList []agentTypes.Metric
		switch state {
		case metricPassAgentStatus:
			currentList = []agentTypes.Metric{
				fakeMetric{label: "agent_status"},
			}
		case metricPassMain:
			currentList = localMetrics
		case metricPassRecreate:
			currentList = registerMetrics
			if len(registerMetrics) > 0 && !fullForInactive {
				metrics := make([]types.Metric, 0, len(registeredMetricsByUUID))
				for _, v := range registeredMetricsByUUID {
					metrics = append(metrics, v)
				}
				s.option.Cache.SetMetrics(metrics)
				requests := make([]common.MetricLabelItem, len(registerMetrics))
				for i, m := range registerMetrics {
					labels := m.Labels()
					requests[i] = common.MetricLabelItem{Label: labels["__name__"], Item: labels["item"]}
				}
				if err := s.metricUpdateListSearch(requests); err != nil {
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
			err := s.metricRegisterAndUpdateOne(metric, registeredMetricsByUUID, registeredMetricsByKey, containersByContainerID, servicesByKey, params)
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
				lastErr = err
				errorCount++
				if errorCount > 10 {
					return err
				}
				time.Sleep(time.Duration(errorCount/2) * time.Second)
				continue metricLoop
			}
			regCountBeforeUpdate--
			if regCountBeforeUpdate == 0 {
				regCountBeforeUpdate = 60
				metrics := make([]types.Metric, 0, len(registeredMetricsByUUID))
				for _, v := range registeredMetricsByUUID {
					metrics = append(metrics, v)
				}
				s.option.Cache.SetMetrics(metrics)
			}
		}
	}

	metrics := make([]types.Metric, 0, len(registeredMetricsByUUID))
	for _, v := range registeredMetricsByUUID {
		metrics = append(metrics, v)
	}
	s.option.Cache.SetMetrics(metrics)
	return lastErr
}

func (s *Synchronizer) metricRegisterAndUpdateOne(metric agentTypes.Metric, registeredMetricsByUUID map[string]types.Metric, registeredMetricsByKey map[common.MetricLabelItem]types.Metric, containersByContainerID map[string]types.Container, servicesByKey map[serviceNameInstance]types.Service, params map[string]string) error {
	labels := metric.Labels()
	key := common.MetricLabelItemFromMetric(labels)
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
		Metric: types.Metric{
			Label:  labels["__name__"],
			Labels: labels,
		},
		Item:  key.Item,
		Agent: s.agentID,
	}
	var result metricPayload
	if labels["status_of"] != "" {
		subKey := common.MetricLabelItem{Label: labels["status_of"], Item: key.Item}
		metricStatusOf, ok := registeredMetricsByKey[subKey]
		if !ok {
			return errRetryLater
		}
		payload.StatusOf = metricStatusOf.ID
	}
	if labels["container_id"] != "" {
		container, ok := containersByContainerID[labels["container_id"]]
		if !ok {
			// No error. When container get registered we trigger a metric synchronization
			return nil
		}
		payload.ContainerID = container.ID
		// TODO: For now all metrics are created no-associated with a container.
		// PRODUCT-970 track the progress on this point
		payload.ContainerID = ""
	}
	if labels["service_name"] != "" {
		srvKey := serviceNameInstance{name: labels["service_name"], instance: labels["container_name"]}
		srvKey.truncateInstance()
		service, ok := servicesByKey[srvKey]
		if !ok {
			// No error. When service get registered we trigger a metric synchronization
			return nil
		}
		payload.ServiceID = service.ID
	}
	_, err := s.client.Do("POST", "v1/metric/", params, payload, &result)
	if err != nil {
		return err
	}
	logger.V(2).Printf("Metric %v registrered with UUID %s", key, result.ID)
	registeredMetricsByKey[key] = result.metricFromAPI()
	registeredMetricsByUUID[result.ID] = result.metricFromAPI()
	return nil
}

func (s *Synchronizer) metricUpdateOne(key common.MetricLabelItem, metric agentTypes.Metric, remoteMetric types.Metric) (types.Metric, error) {
	if !remoteMetric.DeactivatedAt.IsZero() {
		points, err := metric.Points(time.Now().Add(-10*time.Minute), time.Now())
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
	// Only update labels if we have label not present on API.
	// This is needed for the transition from (label, item) to
	// (label, labels). Once done, labels will be set at
	// registration time and never change.
	if !s.apiSupportLabels {
		return remoteMetric, nil
	}
	hasNewLabel := false
	for k := range metric.Labels() {
		if k == "__name__" {
			continue
		}
		if _, ok := remoteMetric.Labels[k]; !ok {
			hasNewLabel = true
		}
	}
	if hasNewLabel {
		newLabels := make(map[string]string)
		for k, v := range remoteMetric.Labels {
			newLabels[k] = v
		}
		for k, v := range metric.Labels() {
			newLabels[k] = v
		}
		logger.V(2).Printf("Update labels of metric %v (%v -> %v)", key, remoteMetric.Labels, newLabels)
		var response map[string]interface{}
		_, err := s.client.Do(
			"PATCH",
			fmt.Sprintf("v1/metric/%s/", remoteMetric.ID),
			map[string]string{"fields": "labels,item"},
			map[string]map[string]string{"labels": newLabels},
			&response,
		)
		if err != nil && client.IsNotFound(err) {
			return remoteMetric, errNeedRegister{remoteMetric: remoteMetric, key: key}
		} else if err != nil {
			return remoteMetric, err
		}
		if _, ok := response["labels"]; !ok {
			s.apiSupportLabels = false
			logger.V(2).Printf("API does not yet support labels. Skipping updates")
			remoteMetric.Labels = make(map[string]string)
			if item, ok := response["item"].(string); ok && item != "" {
				remoteMetric.Labels["item"] = item
			}
		} else {
			remoteMetric.Labels = newLabels
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
	registeredMetricsByKey := common.MetricLookupFromList(s.option.Cache.Metrics())

	for _, srv := range localServices {
		if !srv.CheckIgnored {
			continue
		}
		key := serviceNameInstance{name: string(srv.Name), instance: srv.ContainerName}
		shortKey, ok := longToShortKeyLookup[key]
		if !ok {
			continue
		}
		metricKey := common.MetricLabelItem{Label: fmt.Sprintf("%s_status", srv.Name), Item: shortKey.instance}
		if metric, ok := registeredMetricsByKey[metricKey]; ok {
			_, err := s.client.Do("DELETE", fmt.Sprintf("v1/metric/%s/", metric.ID), nil, nil, nil)
			if err != nil {
				return err
			}
			delete(registeredMetrics, metric.ID)
			logger.V(2).Printf("Metric %v deleted", metricKey)
		}
	}

	metrics := make([]types.Metric, 0, len(registeredMetrics))
	for _, v := range registeredMetrics {
		metrics = append(metrics, v)
	}
	s.option.Cache.SetMetrics(metrics)
	return nil
}

func (s *Synchronizer) metricDeactivate(localMetrics []agentTypes.Metric) error {

	duplicatedKey := make(map[common.MetricLabelItem]bool)
	localByMetricKey := make(map[common.MetricLabelItem]agentTypes.Metric, len(localMetrics))
	for _, v := range localMetrics {
		key := common.MetricLabelItemFromMetric(v)
		localByMetricKey[key] = v
	}

	registeredMetrics := s.option.Cache.MetricsByUUID()
	for k, v := range registeredMetrics {
		if !v.DeactivatedAt.IsZero() {
			if time.Since(v.DeactivatedAt) > 200*24*time.Hour {
				delete(registeredMetrics, k)
			}
			continue
		}
		if v.Label == "agent_sent_message" || v.Label == "agent_status" {
			continue
		}
		key := common.MetricLabelItem{Label: v.Label, Item: v.Labels["item"]}
		metric, ok := localByMetricKey[key]
		if ok && !duplicatedKey[key] {
			duplicatedKey[key] = true
			points, _ := metric.Points(time.Now().Add(-70*time.Minute), time.Now())
			if len(points) > 0 {
				continue
			}
		}
		logger.V(2).Printf("Mark inactive the metric %v", key)
		_, err := s.client.Do(
			"PATCH",
			fmt.Sprintf("v1/metric/%s/", v.ID),
			map[string]string{"fields": "active"},
			map[string]string{"active": "False"},
			nil,
		)
		if err != nil {
			return err
		}
		v.DeactivatedAt = time.Now()
		registeredMetrics[k] = v
	}
	metrics := make([]types.Metric, 0, len(registeredMetrics))
	for _, v := range registeredMetrics {
		metrics = append(metrics, v)
	}
	s.option.Cache.SetMetrics(metrics)
	return nil
}
