package synchronizer

import (
	"agentgo/bleemeo/client"
	"agentgo/bleemeo/types"
	"agentgo/logger"
	agentTypes "agentgo/types"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"
)

const apiMetricItemLength = 100
const apiMetricItemLengthIfService = 50

var (
	errRetryLater   = errors.New("metric registration should be retried laster")
	errNeedRegister = errors.New("metric was deleted from API, it need to be re-registered")
)

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

type metricLabelItem struct {
	label string
	item  string
}

func (key *metricLabelItem) truncateItem(isService bool) {
	if len(key.item) > apiMetricItemLength {
		key.item = key.item[:apiMetricItemLength]
	}
	if isService && len(key.item) > apiMetricItemLengthIfService {
		key.item = key.item[:apiMetricItemLengthIfService]
	}
}

func (key metricLabelItem) String() string {
	if key.item != "" {
		return fmt.Sprintf("%s (item %s)", key.label, key.item)
	}
	return key.label
}

func metricLabelItemFromLabels(labels map[string]string) metricLabelItem {
	key := metricLabelItem{label: labels["__name__"], item: labels["item"]}
	key.truncateItem(labels["service_name"] != "")
	return key
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

func (s *Synchronizer) findUnregisteredMetrics(metrics []agentTypes.Metric) []metricLabelItem {

	registeredMetrics := s.option.Cache.Metrics()
	registeredMetricsByKey := make(map[metricLabelItem]types.Metric)
	for _, v := range registeredMetrics {
		key := metricLabelItem{label: v.Label, item: v.Labels["item"]}
		registeredMetricsByKey[key] = v
	}

	result := make([]metricLabelItem, 0)
	for _, v := range metrics {
		key := metricLabelItemFromLabels(v.Labels())
		if _, ok := registeredMetricsByKey[key]; ok {
			continue
		}
		result = append(result, key)

	}
	return result
}

func (s *Synchronizer) syncMetrics(fullSync bool) error {

	localMetrics, err := s.option.Store.Metrics(nil)
	if err != nil {
		return err
	}

	unregisteredMetrics := s.findUnregisteredMetrics(localMetrics)

	if s.successiveErrors == 3 {
		// After 3 error, try to force a full synchronization to see if it solve the issue.
		fullSync = true
	}

	fullForInactive := false
	previousMetrics := s.option.Cache.MetricsByUUID()
	// TODO "update_metrics": metric to excplicitly update that come from MQTT message
	if fullSync {
		s.apiSupportLabels = true // retry labels update
		if len(unregisteredMetrics) > 3*len(previousMetrics)/100 {
			fullForInactive = true
		}
		err := s.metricUpdateList(fullForInactive)
		if err != nil {
			return err
		}

	}
	if !fullSync && !fullForInactive {
		err := s.metricUpdateListSearch(unregisteredMetrics)
		if err != nil {
			return err
		}
	}

	if err := s.metricDeleteFromRemote(localMetrics, previousMetrics); err != nil {
		return err
	}

	localMetrics, err = s.option.Store.Metrics(nil)
	if err != nil {
		return err
	}

	// If one metric fail to register, it may block other metric that would register correctly.
	// To reduce this risk, randomize the list, so on next run, the metric that failed to register
	// may no longer block other.
	rand.Shuffle(len(localMetrics), func(i, j int) {
		localMetrics[i], localMetrics[j] = localMetrics[j], localMetrics[i]
	})
	prioritizeMetrics(localMetrics)

	if err := s.metricRegisterAndUpdate(localMetrics, fullForInactive); err != nil {
		return err
	}
	if err := s.metricDeleteFromLocal(); err != nil {
		return err
	}
	if time.Since(s.startedAt) > 70*time.Minute {
		if err := s.metricDeactivate(localMetrics); err != nil {
			return err
		}
	}
	s.lastMetricCount = len(localMetrics)
	return nil
}

func (s *Synchronizer) metricUpdateList(includeInactive bool) error {
	params := map[string]string{
		"agent":  s.option.State.AgentID(),
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
		if metric.Labels == nil {
			metric.Labels = make(map[string]string)
		}
		if metric.Labels["item"] == "" && metric.Item != "" {
			metric.Labels["item"] = metric.Item
		}
		metricsByUUID[metric.ID] = metric.Metric
	}
	metrics := make([]types.Metric, 0, len(metricsByUUID))
	for _, m := range metricsByUUID {
		metrics = append(metrics, m)
	}
	s.option.Cache.SetMetrics(metrics)
	return nil
}

func (s *Synchronizer) metricUpdateListSearch(requests []metricLabelItem) error {

	metricsByUUID := s.option.Cache.MetricsByUUID()

	for _, key := range requests {
		params := map[string]string{
			"agent":  s.option.State.AgentID(),
			"label":  key.label,
			"item":   key.item,
			"fields": "id,label,labels,item,unit,unit_text,service,container,deactivated_at,threshold_low_warning,threshold_low_critical,threshold_high_warning,threshold_high_critical,status_of",
		}
		result, err := s.client.Iter("metric", params)
		if err != nil {
			return err
		}

		for _, jsonMessage := range result {
			var metric types.Metric
			if err := json.Unmarshal(jsonMessage, &metric); err != nil {
				continue
			}
			metricsByUUID[metric.ID] = metric
		}
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

	deletedMetricLabelItem := make(map[metricLabelItem]bool)
	for _, m := range previousMetrics {
		if _, ok := newMetrics[m.ID]; !ok {
			key := metricLabelItem{label: m.Label, item: m.Labels["item"]}
			deletedMetricLabelItem[key] = true
		}
	}

	localMetricToDelete := make([]map[string]string, 0)
	for _, m := range localMetrics {
		labels := m.Labels()
		key := metricLabelItemFromLabels(labels)
		if _, ok := deletedMetricLabelItem[key]; ok {
			localMetricToDelete = append(localMetricToDelete, labels)
		}
	}
	s.option.Store.DropMetrics(localMetricToDelete)
	return nil
}

func (s *Synchronizer) metricRegisterAndUpdate(localMetrics []agentTypes.Metric, fullForInactive bool) error {

	registeredMetricsByUUID := s.option.Cache.MetricsByUUID()
	registeredMetricsByKey := make(map[metricLabelItem]types.Metric)
	for _, v := range registeredMetricsByUUID {
		key := metricLabelItem{label: v.Label, item: v.Labels["item"]}
		registeredMetricsByKey[key] = v
	}

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
				requests := make([]metricLabelItem, len(registerMetrics))
				for i, m := range registerMetrics {
					labels := m.Labels()
					requests[i] = metricLabelItem{label: labels["__name__"], item: labels["item"]}
				}
				if err := s.metricUpdateListSearch(requests); err != nil {
					return err
				}
				registeredMetricsByUUID := s.option.Cache.MetricsByUUID()
				registeredMetricsByKey := make(map[metricLabelItem]types.Metric)
				for _, v := range registeredMetricsByUUID {
					key := metricLabelItem{label: v.Label, item: v.Labels["item"]}
					registeredMetricsByKey[key] = v
				}
			}
		case metricPassRetry:
			currentList = retryMetrics
		}
	metricLoop:
		for _, metric := range currentList {
			if s.ctx.Err() != nil {
				break
			}
			err := s.metricRegisterAndUpdateOne(metric, registeredMetricsByUUID, registeredMetricsByKey, containersByContainerID, servicesByKey, params)
			switch {
			case err != nil && err == errRetryLater && state < 2:
				retryMetrics = append(retryMetrics, metric)
				continue metricLoop
			case err != nil && err == errNeedRegister && state < 1:
				registerMetrics = append(registerMetrics, metric)
				continue metricLoop
			case err != nil:
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
				metrics := make([]types.Metric, 0, len(registeredMetricsByKey))
				for _, v := range registeredMetricsByKey {
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

func (s *Synchronizer) metricRegisterAndUpdateOne(metric agentTypes.Metric, registeredMetricsByUUID map[string]types.Metric, registeredMetricsByKey map[metricLabelItem]types.Metric, containersByContainerID map[string]types.Container, servicesByKey map[serviceNameInstance]types.Service, params map[string]string) error {
	labels := metric.Labels()
	key := metricLabelItemFromLabels(labels)
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
		Item:  key.item,
		Agent: s.option.State.AgentID(),
	}
	var result types.Metric
	if labels["status_of"] != "" {
		subKey := metricLabelItem{label: labels["status_of"], item: key.item}
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
	}
	if labels["service_name"] != "" && labels["service_instance"] != "" {
		srvKey := serviceNameInstance{name: labels["service_name"], instance: labels["service_instance"]}
		srvKey.truncateInstance()
		service, ok := servicesByKey[srvKey]
		if !ok {
			// No error. When service get registered we trigger a metric synchronization
			return nil
		}
		payload.ServiceID = service.ID
	}
	if remoteFound {
		_, err := s.client.Do("PUT", fmt.Sprintf("v1/metric/%s/", remoteMetric.ID), params, payload, &result)
		if err != nil {
			return err
		}
		logger.V(2).Printf("Metric %v updated with UUID %s", key, result.ID)
	} else {
		_, err := s.client.Do("POST", "v1/metric/", params, payload, &result)
		if err != nil {
			return err
		}
		logger.V(2).Printf("Metric %v registrered with UUID %s", key, result.ID)
	}
	registeredMetricsByKey[key] = result
	registeredMetricsByUUID[result.ID] = result
	return nil
}

func (s *Synchronizer) metricUpdateOne(key metricLabelItem, metric agentTypes.Metric, remoteMetric types.Metric) (types.Metric, error) {
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
				return remoteMetric, errNeedRegister
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
			return remoteMetric, errNeedRegister
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
	registeredMetricsByKey := make(map[metricLabelItem]types.Metric)
	for _, v := range registeredMetrics {
		key := metricLabelItem{label: v.Label, item: v.Labels["item"]}
		registeredMetricsByKey[key] = v
	}

	for _, srv := range localServices {
		if !srv.CheckIgnored {
			continue
		}
		key := serviceNameInstance{name: string(srv.Name), instance: srv.ContainerName}
		shortKey, ok := longToShortKeyLookup[key]
		if !ok {
			continue
		}
		metricKey := metricLabelItem{label: fmt.Sprintf("%s_status", srv.Name), item: shortKey.instance}
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

	duplicatedKey := make(map[metricLabelItem]bool)
	localByMetricKey := make(map[metricLabelItem]agentTypes.Metric, len(localMetrics))
	for _, v := range localMetrics {
		key := metricLabelItemFromLabels(v.Labels())
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
		key := metricLabelItem{label: v.Label, item: v.Labels["item"]}
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
