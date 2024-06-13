// Copyright 2015-2024 Bleemeo
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
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/facts"
	gloutonTypes "github.com/bleemeo/glouton/types"
	"github.com/go-viper/mapstructure/v2"
)

const (
	mockAPIResourceAgent         = "agent"
	mockAPIResourceAgentFact     = "agentfact"
	mockAPIResourceAgentType     = "agenttype"
	mockAPIResourceContainer     = "container"
	mockAPIResourceMetric        = "metric"
	mockAPIResourceAccountConfig = "accountconfig"
	mockAPIResourceAgentConfig   = "agentconfig"
	mockAPIResourceApplication   = "application"
	mockAPIResourceService       = "service"
	mockAPIGloutonConfigItem     = "gloutonconfigitem"
	mockAPIGloutonDiagnostic     = "gloutondiagnostic"
)

type mockMetric struct {
	Name   string
	labels map[string]string
}

type mockTime struct {
	now time.Time
}

type mockDocker struct {
	helper *syncTestHelper
}

type mockMonitorManager struct{}

type stateMock struct {
	data      map[string]any
	agentUUID string
	password  string
}

type reuseIDError struct {
	elemIndex int
}

type clientError struct {
	body       string
	statusCode int
}

func (e clientError) Error() string {
	return fmt.Sprintf("client error %d: %v", e.statusCode, e.body)
}

func wrapIfClientError(err error) error {
	if clientErr := new(clientError); errors.As(err, clientErr) {
		return &bleemeo.APIError{
			StatusCode: clientErr.statusCode,
			Response:   []byte(clientErr.body),
		}
	}

	return err
}

func (m mockMetric) Labels() map[string]string {
	if m.labels != nil {
		return m.labels
	}

	return map[string]string{gloutonTypes.LabelName: m.Name}
}

func (m mockMetric) Annotations() gloutonTypes.MetricAnnotations {
	return gloutonTypes.MetricAnnotations{}
}

func (m mockMetric) Points(start, end time.Time) ([]gloutonTypes.Point, error) {
	_ = start
	_ = end

	return nil, errNotImplemented
}

func (m mockMetric) LastPointReceivedAt() time.Time {
	return time.Now()
}

func (mt *mockTime) Now() time.Time {
	return mt.now
}

func (mt *mockTime) Advance(d time.Duration) {
	mt.now = mt.now.Add(d)
}

func (d mockDocker) Containers(_ context.Context, maxAge time.Duration, includeIgnored bool) (containers []facts.Container, err error) {
	_ = maxAge
	_ = includeIgnored

	return d.helper.containers, nil
}

func (d mockDocker) ContainerLastKill(string) time.Time {
	return time.Time{}
}

func (d mockDocker) LastUpdate() time.Time {
	return d.helper.s.now()
}

func (m mockMonitorManager) UpdateDynamicTargets([]gloutonTypes.Monitor) error {
	return nil
}

// mockProcessLister is a process lister that returns fake processes.
// TopInfo is not implemented.
type mockProcessLister struct {
	processes map[int]facts.Process
}

func (m mockProcessLister) Processes(_ context.Context, maxAge time.Duration) (processes map[int]facts.Process, err error) {
	_ = maxAge

	return m.processes, nil
}

func (m mockProcessLister) TopInfo(_ context.Context, maxAge time.Duration) (topinfo facts.TopInfo, err error) {
	_ = maxAge

	return facts.TopInfo{}, errNotImplemented
}

func newStateMock() *stateMock {
	return &stateMock{
		data: map[string]any{},
	}
}

// Set updates an object.
// No json or anything there, just stupid objects.
func (s *stateMock) Set(key string, object any) error {
	s.data[key] = object

	return nil
}

// Delete an key from state.
func (s *stateMock) Delete(key string) error {
	if _, ok := s.data[key]; !ok {
		return nil
	}

	delete(s.data, key)

	return nil
}

func (s *stateMock) BleemeoCredentials() (string, string) {
	return s.agentUUID, s.password
}

func (s *stateMock) SetBleemeoCredentials(agentUUID string, password string) error {
	s.agentUUID = agentUUID
	s.password = password

	return nil
}

// Get returns an object.
func (s *stateMock) Get(key string, result any) error {
	val, ok := s.data[key]
	if ok {
		reflect.ValueOf(result).Elem().Set(reflect.ValueOf(val))
	}

	return nil
}

func (s *stateMock) GetByPrefix(keyPrefix string, resultType any) (map[string]any, error) {
	result := make(map[string]any)

	for key, val := range s.data {
		if strings.HasPrefix(key, keyPrefix) {
			reflect.ValueOf(&resultType).Elem().Set(reflect.ValueOf(val))

			result[key] = resultType
		}
	}

	return result, nil
}

func (reuseIDError) Error() string {
	return "reuseIDError"
}

type resourceHolder[T any] struct {
	elems   []T
	autoInc int

	createHook func(e *T) error
	patchHook  func(e *T) error
}

func (rh *resourceHolder[T]) clone() []T {
	return slices.Clone(rh.elems)
}

func (rh *resourceHolder[T]) add(elems ...T) {
	rh.elems = append(rh.elems, elems...)
}

func (rh *resourceHolder[T]) dropByID(id string) {
	filtered := make([]T, 0, len(rh.elems)-1) // result length approximation

	for _, el := range rh.elems {
		refEl := reflect.ValueOf(el)
		if refEl.Kind() == reflect.Struct {
			idField := refEl.FieldByName("ID")
			if idField.Kind() == reflect.String && idField.String() == id {
				continue // dropping this element
			}
		}

		filtered = append(filtered, el)
	}

	rh.elems = filtered
}

func (rh *resourceHolder[T]) createResource(e *T, errorCounter *int) error {
	if rh.createHook != nil {
		err := rh.createHook(e)
		if err != nil {
			if reuseIDErr := new(reuseIDError); errors.As(err, reuseIDErr) {
				// Just update an existing element
				rh.elems[reuseIDErr.elemIndex] = *e

				return nil
			}

			*errorCounter++

			return wrapIfClientError(err)
		}
	}

	rh.elems = append(rh.elems, *e)

	return nil
}

func (rh *resourceHolder[T]) findResource(predicate func(T) bool) *T {
	for i, e := range rh.elems {
		if predicate(e) {
			return &rh.elems[i]
		}
	}

	return nil
}

func (rh *resourceHolder[T]) filterResources(predicate func(T) bool) []T {
	var result []T

	for _, e := range rh.elems {
		if predicate(e) {
			result = append(result, e)
		}
	}

	return result
}

func (rh *resourceHolder[T]) deleteResource(predicate func(T) bool) error {
	idx := slices.IndexFunc(rh.elems, predicate)
	if idx < 0 {
		return &bleemeo.APIError{StatusCode: http.StatusNotFound}
	}

	rh.elems = slices.Delete(rh.elems, idx, idx+1)

	return nil
}

func (rh *resourceHolder[T]) incID() string {
	rh.autoInc++

	return strconv.Itoa(rh.autoInc)
}

func (rh *resourceHolder[T]) applyPatchHook(e *T, errorCounter *int) error {
	if rh.patchHook != nil {
		err := rh.patchHook(e)
		if err != nil {
			*errorCounter++

			return wrapIfClientError(err)
		}
	}

	return nil
}

type resourcesSet struct {
	applications       resourceHolder[bleemeoTypes.Application]
	agents             resourceHolder[bleemeoapi.AgentPayload]
	agentFacts         resourceHolder[bleemeoTypes.AgentFact]
	agentTypes         resourceHolder[bleemeoTypes.AgentType]
	containers         resourceHolder[bleemeoapi.ContainerPayload]
	metrics            resourceHolder[bleemeoapi.MetricPayload]
	accountConfigs     resourceHolder[bleemeoTypes.AccountConfig]
	agentConfigs       resourceHolder[bleemeoTypes.AgentConfig]
	monitors           resourceHolder[bleemeoTypes.Monitor]
	services           resourceHolder[bleemeoapi.ServicePayload]
	gloutonConfigItems resourceHolder[bleemeoTypes.GloutonConfigItem]
	gloutonDiagnostics resourceHolder[bleemeoapi.RemoteDiagnostic]
}

func newMapstructJSONDecoder(target any, decodeHook mapstructure.DecodeHookFunc) *mapstructure.Decoder {
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		DecodeHook: decodeHook,
		Result:     target,
		Squash:     true,
		TagName:    "json",
	})
	if err != nil {
		panic(fmt.Sprintf("invalid target %T for mapstructure decoding", target))
	}

	return decoder
}
