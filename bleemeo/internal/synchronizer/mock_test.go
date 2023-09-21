// Copyright 2015-2023 Bleemeo
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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/facts"
	"glouton/types"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const (
	mockAPIResourceAgent         = "agent"
	mockAPIResourceAgentFact     = "agentfact"
	mockAPIResourceAgentType     = "agenttype"
	mockAPIResourceContainer     = "container"
	mockAPIResourceMetric        = "metric"
	mockAPIResourceAccountConfig = "accountconfig"
	mockAPIResourceAgentConfig   = "agentconfig"
	mockAPIResourceService       = "service"
	mockAPIGloutonConfigItem     = "gloutonconfigitem"
	mockAPIGloutonCrashReport    = "gloutoncrashreport"
)

//nolint:gochecknoglobals
var (
	defaultIgnoreParam = []string{"fields", "page_size"}

	errNotFound = errors.New("not found")
)

// mockAPI fake global /v1 API endpoints. Currently only v1/info & v1/jwt-auth
// Use Handle to add additional endpoints.
type mockAPI struct {
	JWTUsername           string
	JWTPassword           string
	JWTToken              string
	AuthCallback          func(*mockAPI, *http.Request) (interface{}, int, error)
	PreRequestHook        func(*mockAPI, *http.Request) (interface{}, int, error)
	resources             map[string]mockResource
	serveMux              *http.ServeMux
	now                   *mockTime
	AccountConfigNewAgent string

	RequestList        []mockRequest
	ServerErrorCount   int
	ClientErrorCount   int
	RequestCount       int
	RequestPerResource map[string]int
	LastServerError    error
}

type mockMetric struct {
	Name   string
	labels map[string]string
}

type mockTime struct {
	now time.Time
}

type mockRequest struct {
	URL          *url.URL
	Method       string
	ResponseCode int
	Error        error
}

type mockDocker struct {
	helper *syncTestHelper
}

type mockMonitorManager struct{}

type stateMock struct {
	data      map[string]interface{}
	agentUUID string
	password  string
}

type reuseIDError struct {
	ID string
}

type paginatedList []interface{}

type clientError struct {
	body       interface{}
	statusCode int
}

func (e clientError) Error() string {
	return fmt.Sprintf("client error %d: %v", e.statusCode, e.body)
}

type mockResource interface {
	List(r *http.Request) ([]interface{}, error)
	Create(r *http.Request) (interface{}, error)
	Patch(id string, r *http.Request) (interface{}, error)
	SetStore(...interface{})
	AddStore(...interface{})
	DelStore(ids ...string)
	Store(interface{})
}

func newAPI() *mockAPI {
	api := &mockAPI{
		JWTToken:              "random-value",
		PreRequestHook:        nil,
		now:                   &mockTime{now: time.Now()},
		RequestPerResource:    make(map[string]int),
		AccountConfigNewAgent: newAccountConfig.ID,
	}

	api.AuthCallback = func(ma *mockAPI, r *http.Request) (interface{}, int, error) {
		if r.Method == "POST" && r.URL.Path == "/v1/agent/" {
			// POST on agent can be authenticated with either JWT or account registration key
			basicAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s@bleemeo.com:%s", accountID, registrationKey)))

			if r.Header.Get("Authorization") == basicAuth {
				return nil, 0, nil
			}
		}

		jwtAuth := "JWT " + ma.JWTToken
		if r.Header.Get("Authorization") != jwtAuth {
			body := fmt.Sprintf("JWT token = %#v, want %#v", r.Header.Get("Authorization"), jwtAuth)

			return body, http.StatusUnauthorized, nil
		}

		return nil, 0, nil
	}

	api.AddResource(mockAPIResourceAgent, &genericResource{
		Type: payloadAgent{},
		CreateHook: func(r *http.Request, body []byte, valuePtr interface{}) error {
			agent, _ := valuePtr.(*payloadAgent)

			// TODO: Glouton currently don't send the AccountID for SNMP type but do for
			// the main agent. We should be consistent (and API should NOT trust the value
			// from Glouton, so likely drop it ?).
			if agent.AccountID == "" && agent.AgentType == agentTypeSNMP.ID {
				agent.AccountID = accountID
			}

			if agent.AccountID != accountID {
				err := fmt.Errorf("%w, got %v, want %v", errInvalidAccountID, agent.AccountID, accountID)

				return err
			}

			if agent.AgentType == "" {
				agent.AgentType = agentTypeAgent.ID
			}

			if agent.AgentType == agentTypeAgent.ID {
				api.JWTUsername = fmt.Sprintf("%s@bleemeo.com", agent.ID)
				api.JWTPassword = agent.InitialPassword
			}

			agent.CurrentConfigID = api.AccountConfigNewAgent
			agent.InitialPassword = "password already set"
			agent.CreatedAt = api.now.Now()

			return nil
		},
	})
	api.AddResource(mockAPIResourceAgentFact, &genericResource{
		Type:        bleemeoTypes.AgentFact{},
		ValidFilter: []string{"agent"},
	})
	api.AddResource(mockAPIResourceAgentType, &genericResource{
		Type:     bleemeoTypes.AgentType{},
		ReadOnly: true,
	})
	api.AddResource(mockAPIResourceAccountConfig, &genericResource{
		Type:     bleemeoTypes.AccountConfig{},
		ReadOnly: true,
	})
	api.AddResource(mockAPIResourceAgentConfig, &genericResource{
		Type:     bleemeoTypes.AgentConfig{},
		ReadOnly: true,
	})
	api.AddResource(mockAPIResourceMetric, &genericResource{
		Type:        metricPayload{},
		ValidFilter: []string{"agent", "active", "labels_text", "item", "label"},
		FilterHook: map[string]func(x interface{}, value string) (bool, error){
			"active": func(x interface{}, value string) (bool, error) {
				m, ok := x.(metricPayload)
				if !ok {
					return false, fmt.Errorf("%w: %T isn't type metricPayload", errUnexpectedOperation, x)
				}

				value = strings.ToLower(value)
				active := (value == "true" || value == "1")

				return active == m.DeactivatedAt.IsZero(), nil
			},
		},
		PatchHook: func(r *http.Request, body []byte, valuePtr interface{}, oldValue interface{}) error {
			var data map[string]interface{}

			metricPtr, _ := valuePtr.(*metricPayload)

			err := json.NewDecoder(bytes.NewReader(body)).Decode(&data)

			switch value := data["active"].(type) {
			case string:
				switch strings.ToLower(value) {
				case "true":
					metricPtr.DeactivatedAt = time.Time{}
				case "false":
					metricPtr.DeactivatedAt = api.now.Now()
				default:
					return fmt.Errorf("%w %v", errUnknownBool, value)
				}
			case bool:
				if value {
					metricPtr.DeactivatedAt = time.Time{}
				} else {
					metricPtr.DeactivatedAt = api.now.Now()
				}
			default:
				if _, ok := data["active"]; ok {
					return fmt.Errorf("%w type invalid for a bool %v", errUnknownBool, value)
				}
			}

			return err
		},
	})
	api.AddResource(mockAPIResourceContainer, &genericResource{
		Type:        containerPayload{},
		ValidFilter: []string{"host"},
		PatchHook: func(r *http.Request, body []byte, valuePtr interface{}, oldValue interface{}) error {
			containerPtr, _ := valuePtr.(*containerPayload)
			oldContainer, _ := oldValue.(containerPayload)

			if !time.Time(containerPtr.DeletedAt).IsZero() && time.Time(oldContainer.DeletedAt).IsZero() {
				// The container was deactivated. Do what API does, it deactivate all metrics associated.
				var metrics []metricPayload

				api.resources[mockAPIResourceMetric].Store(&metrics)

				tmp := make([]interface{}, 0, len(metrics))

				for _, m := range metrics {
					if m.ContainerID == oldContainer.ID {
						m.DeactivatedAt = time.Time(containerPtr.DeletedAt)
					}

					tmp = append(tmp, m)
				}

				api.resources[mockAPIResourceMetric].SetStore(tmp...)
			}

			return nil
		},
	})
	api.AddResource(mockAPIResourceService, &genericResource{
		Type:        serviceMonitor{},
		ValidFilter: []string{"agent", "active", "monitor"},
	})
	api.AddResource(mockAPIGloutonConfigItem, &genericResource{
		Type:        bleemeoTypes.GloutonConfigItem{},
		ValidFilter: []string{"agent"},
	})
	api.AddResource(mockAPIGloutonCrashReport, &genericResource{
		Type: RemoteCrashReport{},
	})

	api.resources[mockAPIResourceAgentType].SetStore(
		agentTypeAgent,
		agentTypeSNMP,
		agentTypeMonitor,
		agentTypeKubernetes,
		agentTypeVSphereHost,
		agentTypeVSphereVM,
	)
	api.resources[mockAPIResourceAccountConfig].SetStore(
		newAccountConfig,
	)
	api.resources[mockAPIResourceAgentConfig].SetStore(
		agentConfigAgent,
		agentConfigSNMP,
		agentConfigMonitor,
		agentConfigKubernetes,
		agentConfigVSphereHost,
		agentConfigVSphereVM,
	)

	return api
}

type apiResponder func(r *http.Request) (interface{}, int, error)

func (api *mockAPI) reply(w http.ResponseWriter, r *http.Request, h apiResponder) {
	api.RequestCount++

	resourceName, _ := api.resourceNameID(r.URL)
	if resourceName != "" {
		api.RequestPerResource[resourceName]++
	}

	mr := mockRequest{
		URL:    r.URL,
		Method: r.Method,
	}

	var (
		response interface{}
		status   int
		err      error
	)

	if api.PreRequestHook != nil {
		response, status, err = api.PreRequestHook(api, r)
	}

	if status == 0 && err == nil {
		response, status, err = h(r)
	}

	var clientErr clientError
	if errors.As(err, &clientErr) {
		err = nil
		response = clientErr.body
		status = clientErr.statusCode
	}

	mr.Error = err
	mr.ResponseCode = status
	api.RequestList = append(api.RequestList, mr)

	if err != nil {
		if status < 500 {
			status = http.StatusInternalServerError
		}

		api.LastServerError = err
		response = err.Error()
	}

	if status >= 500 {
		api.ServerErrorCount++
	} else if status >= 400 {
		api.ClientErrorCount++
	}

	w.WriteHeader(status)

	switch value := response.(type) {
	case string:
		_, err = fmt.Fprint(w, value)
	case []byte:
		_, err = w.Write(value)
	case paginatedList:
		var results struct {
			Next     string        `json:"next"`
			Previous string        `json:"previous"`
			Results  []interface{} `json:"results"`
		}

		results.Results = value
		err = json.NewEncoder(w).Encode(results)
	default:
		err = json.NewEncoder(w).Encode(value)
	}

	if err != nil {
		api.ServerErrorCount++
		api.LastServerError = err

		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (api *mockAPI) jwtHandler(r *http.Request) (interface{}, int, error) {
	decoder := json.NewDecoder(r.Body)
	values := map[string]string{}

	if err := decoder.Decode(&values); err != nil {
		return nil, http.StatusInternalServerError, err
	}

	if values["username"] != api.JWTUsername || values["password"] != api.JWTPassword {
		log.Printf("JWT auth fail: got = %s/%s want %s/%s\n", values["username"], values["password"], api.JWTUsername, api.JWTPassword)

		return `{"non_field_errors":["Unable to log in with provided credentials."]}`, http.StatusBadRequest, nil
	}

	api.JWTToken = uuid.New().String()

	return map[string]string{"token": api.JWTToken}, 200, nil
}

func (api *mockAPI) ResetCount() {
	api.RequestCount = 0
	api.RequestPerResource = make(map[string]int)
	api.ServerErrorCount = 0
	api.ClientErrorCount = 0
	api.LastServerError = nil
	api.RequestList = nil
}

func (api *mockAPI) ShowRequest(t *testing.T, max int) {
	t.Helper()

	for i, req := range api.RequestList {
		if i >= max {
			break
		}

		if req.Error != nil {
			t.Logf("%s %v %s: %d - %v", req.Method, req.URL.Path, req.URL.Query(), req.ResponseCode, req.Error)
		} else {
			t.Logf("%s %v %s: %d", req.Method, req.URL.Path, req.URL.Query(), req.ResponseCode)
		}
	}
}

func (api *mockAPI) resourceNameID(u *url.URL) (string, string) {
	part := strings.Split(u.Path, "/")
	if len(part) < 4 || part[1] != "v1" || len(part) > 5 {
		return "", ""
	}

	var id string
	if len(part) == 5 {
		id = part[3]
	}

	return part[2], id
}

func (api *mockAPI) AssertCallPerResource(t *testing.T, resource string, count int) {
	t.Helper()

	if api.RequestPerResource[resource] == count {
		return
	}

	i := 0
	max := 15

	t.Errorf("Had %d requests on resource %s, want %d", api.RequestPerResource[resource], resource, count)

	for _, req := range api.RequestList {
		if i >= max {
			break
		}

		if name, _ := api.resourceNameID(req.URL); name != resource {
			continue
		}

		i++

		if req.Error != nil {
			t.Logf("%s %v %s: %d - %v", req.Method, req.URL.Path, req.URL.Query(), req.ResponseCode, req.Error)
		} else {
			t.Logf("%s %v %s: %d", req.Method, req.URL.Path, req.URL.Query(), req.ResponseCode)
		}
	}
}

func (api *mockAPI) init() {
	if api.serveMux != nil {
		return
	}

	api.resources = make(map[string]mockResource)

	api.serveMux = http.NewServeMux()
	api.Handle("/v1/jwt-auth/", api.jwtHandler)

	api.Handle("/v1/info/", func(r *http.Request) (interface{}, int, error) {
		return `{"maintenance": false, "agents": {"minimum_versions": {}}}"`, 200, nil
	})

	api.Handle("/", api.defaultHandler)
}

func (api *mockAPI) AddResource(resource string, h mockResource) {
	api.init()
	api.resources[resource] = h
}

func (api *mockAPI) Handle(pattern string, h apiResponder) {
	api.init()
	api.serveMux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		api.reply(w, r, h)
	})
}

func (api *mockAPI) Server() *httptest.Server {
	api.init()

	return httptest.NewServer(api.serveMux)
}

func (api *mockAPI) defaultHandler(r *http.Request) (interface{}, int, error) {
	if api.AuthCallback != nil {
		response, status, err := api.AuthCallback(api, r)

		if status != 0 || err != nil {
			return response, status, err
		}
	}

	resourceName, resourceID := api.resourceNameID(r.URL)
	if resourceName == "" {
		return nil, http.StatusNotImplemented, fmt.Errorf("%w %#v", errUnknownURLFormat, r.URL.Path)
	}

	resource := api.resources[resourceName]
	if resource == nil {
		return nil, http.StatusNotImplemented, fmt.Errorf("%w: %v", errUnknownResource, resourceName)
	}

	switch {
	case resourceID == "" && r.Method == http.MethodGet:
		objects, err := resource.List(r)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}

		return paginatedList(objects), http.StatusOK, nil
	case resourceID == "" && r.Method == http.MethodPost:
		response, err := resource.Create(r)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}

		return response, http.StatusCreated, nil
	case resourceID != "" && (r.Method == http.MethodPatch || r.Method == http.MethodPut):
		response, err := resource.Patch(resourceID, r)
		if errors.Is(err, errNotFound) {
			return nil, http.StatusNotFound, nil
		}

		if err != nil {
			return nil, http.StatusInternalServerError, err
		}

		return response, http.StatusOK, nil
	default:
		return nil, http.StatusNotImplemented, fmt.Errorf("%w for %s %s", errUnknownRequestType, r.Method, r.URL.Path)
	}
}

type genericResource struct {
	Type         interface{}
	ValidFilter  []string
	IgnoredParam []string
	store        map[string]interface{}
	autoinc      int

	ReadOnly   bool
	PatchHook  func(r *http.Request, body []byte, valuePtr interface{}, oldValue interface{}) error
	CreateHook func(r *http.Request, body []byte, valuePtr interface{}) error
	FilterHook map[string]func(x interface{}, value string) (bool, error)
}

func (res *genericResource) SetStore(values ...interface{}) {
	res.store = nil

	res.AddStore(values...)
}

func (res *genericResource) AddStore(values ...interface{}) {
	if res.store == nil {
		res.store = make(map[string]interface{})
	}

	wantType := reflect.ValueOf(res.Type).Type()

	for _, x := range values {
		value := reflect.ValueOf(x)
		gotType := value.Type()

		if wantType != gotType {
			panic(fmt.Sprintf("type is %v, want %v", gotType, wantType))
		}

		id := value.FieldByName("ID").String()
		res.store[id] = x
	}
}

func (res *genericResource) DelStore(ids ...string) {
	if res.store == nil {
		res.store = make(map[string]interface{})
	}

	for _, id := range ids {
		delete(res.store, id)
	}
}

func (res *genericResource) Store(list interface{}) {
	valuePtr := reflect.ValueOf(list)
	if valuePtr.Kind() != reflect.Ptr {
		panic("Store() called without a pointer")
	}

	valueSlice := valuePtr.Elem()
	if valueSlice.Kind() != reflect.Slice {
		panic("Store() called without a pointer to a slice")
	}

	valueSlice = valueSlice.Slice(0, 0)

	for _, x := range res.store {
		valueSlice = reflect.Append(valueSlice, reflect.ValueOf(x))
	}

	valuePtr.Elem().Set(valueSlice)
}

func (res *genericResource) getFilter(r *http.Request) (map[string]string, error) {
	filter := make(map[string]string)

	for k, v := range r.URL.Query() {
		var (
			ignore bool
			ok     bool
		)

		for _, n := range res.IgnoredParam {
			if k == n {
				ignore = true

				break
			}
		}

		for _, n := range defaultIgnoreParam {
			if k == n {
				ignore = true

				break
			}
		}

		if ignore {
			continue
		}

		for _, n := range res.ValidFilter {
			if k == n {
				ok = true

				if len(v) != 1 {
					return nil, fmt.Errorf("%w: multi-valued filter %s=%v", errNotImplemented, k, v)
				}

				filter[k] = v[0]
			}
		}

		if !ok {
			return nil, fmt.Errorf("%w: unknown query param %s (type=%T)", errNotImplemented, k, res.Type)
		}
	}

	return filter, nil
}

func (res *genericResource) filterMatch(x interface{}, filter map[string]string) (bool, error) {
	jsonBytes, err := json.Marshal(x)
	if err != nil {
		return false, err
	}

	var xMap map[string]interface{}

	if err := json.Unmarshal(jsonBytes, &xMap); err != nil {
		return false, err
	}

	for k, v := range filter {
		if hook := res.FilterHook[k]; hook != nil {
			ok, err := hook(x, v)
			if err != nil {
				return false, err
			}

			if !ok {
				return false, nil
			}

			continue
		}

		// Note: we match *all* field as string... this seems to work
		// up to now, but if you do comparison with non-string field (boolean, date,...)
		// it test don't work, it could be due to this cheap comparison :)
		got := fmt.Sprintf("%v", xMap[k])

		if got != v {
			return false, nil
		}
	}

	return true, nil
}

func (res *genericResource) List(r *http.Request) ([]interface{}, error) {
	results := make([]interface{}, 0, len(res.store))

	filter, err := res.getFilter(r)
	if err != nil {
		return nil, err
	}

	for _, x := range res.store {
		if ok, err := res.filterMatch(x, filter); err != nil {
			return nil, err
		} else if ok {
			results = append(results, x)
		}
	}

	return results, nil
}

func (res *genericResource) Create(r *http.Request) (interface{}, error) {
	if res.ReadOnly {
		return nil, clientError{
			body:       "This resource is read-only",
			statusCode: http.StatusUnauthorized,
		}
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	valueReflect := reflect.New(reflect.ValueOf(res.Type).Type())
	value := valueReflect.Interface()

	if err := decoder.Decode(value); err != nil {
		return nil, err
	}

	res.autoinc++
	id := strconv.FormatInt(int64(res.autoinc), 10)

	valueReflect.Elem().FieldByName("ID").SetString(id)

	if res.store == nil {
		res.store = make(map[string]interface{})
	}

	if res.CreateHook != nil {
		var errReuseID reuseIDError

		err := res.CreateHook(r, body, valueReflect.Interface())
		if errors.As(err, &errReuseID) {
			id = errReuseID.ID
		} else if err != nil {
			return nil, err
		}
	}

	res.store[id] = valueReflect.Elem().Interface()

	return res.store[id], nil
}

func (res *genericResource) Patch(id string, r *http.Request) (interface{}, error) {
	if res.ReadOnly {
		return nil, clientError{
			body:       "This resource is read-only",
			statusCode: http.StatusUnauthorized,
		}
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	valueReflect := reflect.New(reflect.ValueOf(res.Type).Type())
	value := valueReflect.Interface()

	current, ok := res.store[id]
	if !ok {
		return nil, fmt.Errorf("%s: %w", id, errNotFound)
	}

	currentValue := reflect.ValueOf(current)

	for n := 0; n < currentValue.NumField(); n++ {
		v := currentValue.Field(n)
		name := currentValue.Type().Field(n).Name
		valueReflect.Elem().FieldByName(name).Set(v)
	}

	if err := decoder.Decode(value); err != nil {
		return nil, err
	}

	id2 := valueReflect.Elem().FieldByName("ID").String()
	if id2 != id {
		if id2 == "" {
			// Our mock API are issue, update are not really working.
			// Example: PUT or PATCH on containerPayload. The ID is in the embedded types.Container.
			// When decoding the whole types.Container is replaced, losing fields like ID.
			// Try fixing field ID
			valueReflect.Elem().FieldByName("ID").Set(currentValue.FieldByName("ID"))
		} else {
			return nil, fmt.Errorf("%w, ID = %v, want %v", errIncorrectID, id2, id)
		}
	}

	if res.store == nil {
		res.store = make(map[string]interface{})
	}

	if res.PatchHook != nil {
		err := res.PatchHook(r, body, valueReflect.Interface(), currentValue.Interface())
		if err != nil {
			return nil, err
		}
	}

	res.store[id] = valueReflect.Elem().Interface()

	return res.store[id], nil
}

func (m mockMetric) Labels() map[string]string {
	if m.labels != nil {
		return m.labels
	}

	return map[string]string{types.LabelName: m.Name}
}

func (m mockMetric) Annotations() types.MetricAnnotations {
	return types.MetricAnnotations{}
}

func (m mockMetric) Points(start, end time.Time) ([]types.Point, error) {
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

func (m mockMonitorManager) UpdateDynamicTargets([]types.Monitor) error {
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
		data: map[string]interface{}{},
	}
}

// Set updates an object.
// No json or anything there, just stupid objects.
func (s *stateMock) Set(key string, object interface{}) error {
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
func (s *stateMock) Get(key string, result interface{}) error {
	val, ok := s.data[key]
	if ok {
		reflect.ValueOf(result).Elem().Set(reflect.ValueOf(val))
	}

	return nil
}

func (reuseIDError) Error() string {
	return "reuseIDError"
}
