package synchronizer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"glouton/agent/state"
	"glouton/bleemeo/internal/cache"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/config"
	"glouton/discovery"
	"glouton/facts"
	"glouton/prometheus/exporter/blackbox"
	"glouton/prometheus/exporter/snmp"
	"glouton/store"
	"glouton/types"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/uuid"
	"github.com/prometheus/prometheus/pkg/labels"
)

const (
	// fixed "random" values are enought for tests.
	accountID        string = "9da59f53-1d90-4441-ae58-42c661cfea83"
	registrationKey  string = "e2c22e59-0621-49e6-b5dd-bdb02cbac9f1"
	activeMonitorURL string = "http://bleemeo.com"
	snmpAddress      string = "127.0.0.1"
)

var (
	errUnknownURLFormat    = errors.New("unknown URL format")
	errUnknownResource     = errors.New("unknown resource")
	errUnknownBool         = errors.New("unknown boolean")
	errUnknownRequestType  = errors.New("type of request unknown")
	errIncorrectID         = errors.New("incorrect id")
	errInvalidAccountID    = errors.New("invalid accountId supplied")
	errUnexpectedOperation = errors.New("unexpected action")
	errServerError         = errors.New("had server error")
	errClientError         = errors.New("had client error")
)

type serviceMonitor struct {
	bleemeoTypes.Monitor
	Account   string `json:"account"`
	IsMonitor bool   `json:"monitor"`
}

// this is a go limitation, these are constants but we have to treat them as variables
//nolint:gochecknoglobals
var (
	newAgent = payloadAgent{
		Agent: bleemeoTypes.Agent{
			ID:        "33708da4-28d4-45aa-b811-49c82b594627",
			AccountID: accountID,
			// same one as in newAccountConfig
			CurrentConfigID: "02eb5b38-d4a0-4db4-9b43-06f63594a515",
		},
		Abstracted:      false,
		InitialPassword: "...",
	}

	newAccountConfig bleemeoTypes.AccountConfig = bleemeoTypes.AccountConfig{
		ID:               "02eb5b38-d4a0-4db4-9b43-06f63594a515",
		Name:             "the-default",
		SNMPIntergration: true,
	}

	newMetric1 = metricPayload{
		Metric: bleemeoTypes.Metric{
			ID:            "decce8cf-c2f7-43c3-b66e-10429debd994",
			AgentID:       newAgent.ID,
			LabelsText:    "__name__=\"some_metric_1\",label=\"value\"",
			DeactivatedAt: time.Time{},
		},
		Name: "some_metric_1",
	}
	newMetric2 = metricPayload{
		Metric: bleemeoTypes.Metric{
			ID:         "055af752-5c01-4abc-9bb2-9d64032ef970",
			AgentID:    newAgent.ID,
			LabelsText: "__name__=\"some_metric_2\",label=\"another_value !\"",
		},
		Name: "some_metric_2",
	}
	newMetricActiveMonitor = metricPayload{
		Metric: bleemeoTypes.Metric{
			ID:         "52b9c46e-00b9-4e80-a852-781426a3a193",
			AgentID:    newMonitor.AgentID,
			LabelsText: "__name__=\"probe_whatever\",instance=\"http://bleemeo.com\"",
			ServiceID:  newMonitor.ID,
		},
		Name: "probe_whatever",
	}

	newMonitor = serviceMonitor{
		Monitor: bleemeoTypes.Monitor{
			Service: bleemeoTypes.Service{
				ID:     "fdd9d999-e2ff-45d3-af2b-6519cf8e3e70",
				Active: true,
			},
			URL:     activeMonitorURL,
			AgentID: "6b0ba586-0111-4a72-9cc7-f19d4f6558b9",
		},
		IsMonitor: true,
	}

	agentTypeAgent bleemeoTypes.AgentType = bleemeoTypes.AgentType{
		DisplayName: "A server monitored with Bleemeo agent",
		ID:          "61zb6a83-d90a-4165-bf04-944e0b2g2a10",
		Name:        "agent",
	}
	agentTypeSNMP bleemeoTypes.AgentType = bleemeoTypes.AgentType{
		DisplayName: "A server monitored with SNMP agent",
		ID:          "823b6a83-d70a-4768-be64-50450b282a30",
		Name:        "snmp",
	}

	agentConfigAgent = bleemeoTypes.AgentConfig{
		ID:               "cab64659-a765-4878-84d8-c7b0112aaecb",
		AccountConfig:    newAccountConfig.ID,
		AgentType:        agentTypeAgent.ID,
		MetricResolution: 10,
	}
	agentConfigSNMP = bleemeoTypes.AgentConfig{
		ID:               "a89d16c1-55be-4d89-9c9b-489c2d86d3fa",
		AccountConfig:    newAccountConfig.ID,
		AgentType:        agentTypeSNMP.ID,
		MetricResolution: 60,
	}

	defaultIgnoreParam = []string{"fields", "page_size"}

	errNotFound = errors.New("not found")
)

// mockAPI fake global /v1 API endpoints. Currently only v1/info & v1/jwt-auth
// Use Handle to add additional endpoints.
type mockAPI struct {
	JWTUsername    string
	JWTPassword    string
	JWTToken       string
	AuthCallback   func(*mockAPI, *http.Request) (interface{}, int, error)
	PreRequestHook func(*mockAPI, *http.Request) (interface{}, int, error)
	resources      map[string]mockResource
	serveMux       *http.ServeMux
	now            *mockTime

	RequestList      []mockRequest
	ServerErrorCount int
	ClientErrorCount int
	RequestCount     int
	LastServerError  error
}

type mockRequest struct {
	URL          *url.URL
	Method       string
	ResponseCode int
	Error        error
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

//nolint: cyclop
func newAPI() *mockAPI {
	api := &mockAPI{
		JWTToken:       "random-value",
		PreRequestHook: nil,
		now:            &mockTime{now: time.Now()},
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

	api.AddResource("agent", &genericResource{
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

			agent.CurrentConfigID = newAccountConfig.ID
			agent.InitialPassword = "password already set"
			agent.CreatedAt = api.now.Now()

			return nil
		},
	})
	api.AddResource("agentfact", &genericResource{
		Type:        bleemeoTypes.AgentFact{},
		ValidFilter: []string{"agent"},
	})
	api.AddResource("agenttype", &genericResource{
		Type:     bleemeoTypes.AgentType{},
		ReadOnly: true,
	})
	api.AddResource("accountconfig", &genericResource{
		Type:     bleemeoTypes.AccountConfig{},
		ReadOnly: true,
	})
	api.AddResource("agentconfig", &genericResource{
		Type:     bleemeoTypes.AgentConfig{},
		ReadOnly: true,
	})
	api.AddResource("metric", &genericResource{
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
		PatchHook: func(r *http.Request, body []byte, valuePtr interface{}) error {
			var data map[string]string

			metricPtr, _ := valuePtr.(*metricPayload)

			err := json.NewDecoder(bytes.NewReader(body)).Decode(&data)
			if boolText, ok := data["active"]; ok {
				switch strings.ToLower(boolText) {
				case "true":
					metricPtr.DeactivatedAt = time.Time{}
				case "false":
					metricPtr.DeactivatedAt = time.Now()
				default:
					return fmt.Errorf("%w %v", errUnknownBool, boolText)
				}
			}

			return err
		},
	})
	api.AddResource("container", &genericResource{
		Type:        containerPayload{},
		ValidFilter: []string{"agent"},
	})
	api.AddResource("service", &genericResource{
		Type:        serviceMonitor{},
		ValidFilter: []string{"agent", "active", "monitor"},
	})

	api.resources["agenttype"].SetStore(
		agentTypeAgent,
		agentTypeSNMP,
	)
	api.resources["accountconfig"].SetStore(
		newAccountConfig,
	)
	api.resources["agentconfig"].SetStore(
		agentConfigAgent,
		agentConfigSNMP,
	)

	return api
}

type apiResponder func(r *http.Request) (interface{}, int, error)

func (api *mockAPI) reply(w http.ResponseWriter, r *http.Request, h apiResponder) {
	api.RequestCount++

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

//nolint:cyclop
func (api *mockAPI) defaultHandler(r *http.Request) (interface{}, int, error) {
	if api.AuthCallback != nil {
		response, status, err := api.AuthCallback(api, r)

		if status != 0 || err != nil {
			return response, status, err
		}
	}

	part := strings.Split(r.URL.Path, "/")
	if len(part) < 4 || part[1] != "v1" || len(part) > 5 {
		return nil, http.StatusNotImplemented, fmt.Errorf("%w %#v", errUnknownURLFormat, part)
	}

	resource := api.resources[part[2]]
	if resource == nil {
		return nil, http.StatusNotImplemented, fmt.Errorf("%w: %v", errUnknownResource, part[2])
	}

	var id string
	if len(part) == 5 {
		id = part[3]
	}

	switch {
	case id == "" && r.Method == http.MethodGet:
		objects, err := resource.List(r)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}

		return paginatedList(objects), http.StatusOK, nil
	case id == "" && r.Method == http.MethodPost:
		response, err := resource.Create(r)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}

		return response, http.StatusCreated, nil
	case id != "" && r.Method == http.MethodPatch:
		response, err := resource.Patch(id, r)
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
	PatchHook  func(r *http.Request, body []byte, valuePtr interface{}) error
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

	body, err := ioutil.ReadAll(r.Body)
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
		err := res.CreateHook(r, body, valueReflect.Interface())
		if err != nil {
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

	body, err := ioutil.ReadAll(r.Body)
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
		return nil, fmt.Errorf("%w, ID = %v, want %v", errIncorrectID, id2, id)
	}

	if res.store == nil {
		res.store = make(map[string]interface{})
	}

	if res.PatchHook != nil {
		err := res.PatchHook(r, body, valueReflect.Interface())
		if err != nil {
			return nil, err
		}
	}

	res.store[id] = valueReflect.Elem().Interface()

	return res.store[id], nil
}

type syncTestHelper struct {
	api        *mockAPI
	s          *Synchronizer
	cfg        *config.Configuration
	facts      *facts.FactProviderMock
	cache      *cache.Cache
	state      *state.Mock
	store      *store.Store
	httpServer *httptest.Server

	// Following fields are options used by some method
	SNMP               []snmp.Target
	MetricFormat       types.MetricFormat
	NotifyLabelsUpdate func(ctx context.Context)
}

// newHelper create an helper with all permanent resource: API, cache, state.
// It does not create the Synchronizer (use initSynchronizer).
func newHelper(t *testing.T) *syncTestHelper {
	t.Helper()

	helper := &syncTestHelper{
		api:   newAPI(),
		cfg:   &config.Configuration{},
		facts: facts.NewMockFacter(),
		cache: &cache.Cache{},
		state: state.NewMock(),

		MetricFormat: types.MetricFormatBleemeo,
	}

	helper.httpServer = helper.api.Server()

	helper.cfg.Set("logging.level", "debug")
	helper.cfg.Set("bleemeo.api_base", helper.httpServer.URL)
	helper.cfg.Set("bleemeo.account_id", accountID)
	helper.cfg.Set("bleemeo.registration_key", registrationKey)
	helper.cfg.Set("blackbox.enable", true)

	helper.facts.SetFact("fqdn", "test.bleemeo.com")

	return helper
}

// preregisterAgent set resource in API, Cache & State as if the agent was previously
// registered.
func (helper *syncTestHelper) preregisterAgent(t *testing.T) {
	t.Helper()

	const password = "the initial password"

	_ = helper.state.Set("password", password)
	_ = helper.state.Set("agent_uuid", newAgent.ID)

	helper.api.JWTPassword = password
	helper.api.JWTUsername = newAgent.ID + "@bleemeo.com"

	helper.api.resources["agent"].AddStore(newAgent)
}

// Create or re-create the Synchronizer. It also reset the store.
func (helper *syncTestHelper) initSynchronizer(t *testing.T) {
	t.Helper()

	helper.store = store.New(time.Hour)
	discovery := &discovery.MockDiscoverer{
		UpdatedAt: helper.api.now.Now(),
	}

	s, err := newWithNow(Option{
		Cache: helper.cache,
		GlobalOption: bleemeoTypes.GlobalOption{
			Config:                  helper.cfg,
			Facts:                   helper.facts,
			State:                   helper.state,
			Discovery:               discovery,
			Store:                   helper.store,
			MonitorManager:          (*blackbox.RegisterManager)(nil),
			NotifyFirstRegistration: func(ctx context.Context) {},
			MetricFormat:            helper.MetricFormat,
			SNMP:                    helper.SNMP,
			NotifyLabelsUpdate:      helper.NotifyLabelsUpdate,
		},
	}, helper.api.now.Now)
	if err != nil {
		t.Fatalf("newWithNow failed: %v", err)
	}

	helper.s = s

	// Do actions done by s.Run()
	s.ctx = context.Background()
	s.startedAt = helper.api.now.Now()

	if err = s.setClient(); err != nil {
		t.Fatalf("setClient failed: %v", err)
	}

	// Some part of synchronizer don't like having *exact* same time for Now() & startedAt
	helper.api.now.Advance(time.Microsecond)
}

// pushPoints write points to the store with current time.
// It known some meta labels (see code for supported one).
func (helper *syncTestHelper) pushPoints(t *testing.T, metrics []labels.Labels) {
	t.Helper()

	points := make([]types.MetricPoint, 0, len(metrics))

	for _, m := range metrics {
		lblsMap := m.Map()
		annotations := types.MetricAnnotations{}

		if id := lblsMap[types.LabelMetaBleemeoTargetAgentUUID]; id != "" {
			delete(lblsMap, types.LabelMetaBleemeoTargetAgentUUID)

			annotations.BleemeoAgentID = id
		}

		points = append(points, types.MetricPoint{
			Point: types.Point{
				Time:  helper.api.now.Now(),
				Value: 42.0,
			},
			Labels:      lblsMap,
			Annotations: annotations,
		})
	}

	if helper.store == nil {
		t.Fatal("pushPoints called before store is initilized")
	}

	helper.store.PushPoints(points)
}

func (helper *syncTestHelper) Close() {
	if helper.httpServer != nil {
		helper.httpServer.Close()

		helper.httpServer = nil
	}
}

func (helper *syncTestHelper) runOnce(t *testing.T) error {
	t.Helper()

	if helper.s == nil {
		return fmt.Errorf("%w: runOnce called before initSynchronizer", errUnexpectedOperation)
	}

	helper.api.ResetCount()

	if err := helper.s.runOnce(false); err != nil {
		return fmt.Errorf("runOnce failed: %w", err)
	}

	if helper.api.ServerErrorCount > 0 {
		return fmt.Errorf("%w: %d server error, last %v", errServerError, helper.api.ServerErrorCount, helper.api.LastServerError)
	}

	if helper.api.ClientErrorCount > 0 {
		return fmt.Errorf("%w: %d server error", errClientError, helper.api.ClientErrorCount)
	}

	return nil
}

func TestSync(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)
	helper.api.resources["metric"].AddStore(newMetric1, newMetric2, newMetricActiveMonitor)
	helper.api.resources["service"].AddStore(newMonitor)

	helper.api.resources["agent"].(*genericResource).CreateHook = func(r *http.Request, body []byte, valuePtr interface{}) error {
		return fmt.Errorf("%w: agent is already registered, shouldn't re-register", errUnexpectedOperation)
	}

	helper.initSynchronizer(t)

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: types.LabelName, Value: "cpu_used"}),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	// Did we store all the metrics ?
	syncedMetrics := helper.s.option.Cache.Metrics()
	want := []bleemeoTypes.Metric{
		newMetric1.metricFromAPI(),
		newMetric2.metricFromAPI(),
		newMetricActiveMonitor.metricFromAPI(),
		metricPayload{
			Metric: bleemeoTypes.Metric{
				ID:      "1",
				AgentID: newAgent.ID,
			},
			Name: "agent_status",
		}.metricFromAPI(),
		metricPayload{
			Metric: bleemeoTypes.Metric{
				ID:      "2",
				AgentID: newAgent.ID,
			},
			Name: "cpu_used",
		}.metricFromAPI(),
	}

	optMetricSort := cmpopts.SortSlices(func(x bleemeoTypes.Metric, y bleemeoTypes.Metric) bool { return x.ID < y.ID })
	if diff := cmp.Diff(want, syncedMetrics, optMetricSort); diff != "" {
		t.Errorf("metrics mistmatch (-want +got)\n%s", diff)
	}

	// Did we sync and enable the monitor present in the configuration ?
	syncedMonitors := helper.s.option.Cache.Monitors()
	wantMonitor := []bleemeoTypes.Monitor{
		newMonitor.Monitor,
	}

	if diff := cmp.Diff(wantMonitor, syncedMonitors); diff != "" {
		t.Errorf("monitors mistmatch (-want +got)\n%s", diff)
	}
}

func TestSyncWithSNMP(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	helper.SNMP = []snmp.Target{
		{InitialName: "Z-The-Initial-Name", Address: snmpAddress, Type: "unused-today"},
	}
	helper.MetricFormat = types.MetricFormatPrometheus

	helper.initSynchronizer(t)

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: types.LabelName, Value: "cpu_used"}),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	var agents []payloadAgent

	helper.api.resources["agent"].Store(&agents)

	var (
		idAgentMain string
		idAgentSNMP string
	)

	for _, a := range agents {
		if a.FQDN == "test.bleemeo.com" {
			idAgentMain = a.ID
		}

		if a.FQDN == snmpAddress {
			idAgentSNMP = a.ID
		}
	}

	wantAgents := []payloadAgent{
		{
			Agent: bleemeoTypes.Agent{
				ID:              idAgentMain,
				CreatedAt:       helper.api.now.Now(),
				AccountID:       accountID,
				CurrentConfigID: newAccountConfig.ID,
				AgentType:       agentTypeAgent.ID,
				FQDN:            "test.bleemeo.com",
				DisplayName:     "test.bleemeo.com",
			},
			Abstracted:      false,
			InitialPassword: "password already set",
		},
		{
			Agent: bleemeoTypes.Agent{
				ID:              idAgentSNMP,
				CreatedAt:       helper.api.now.Now(),
				AccountID:       accountID,
				CurrentConfigID: newAccountConfig.ID,
				AgentType:       agentTypeSNMP.ID,
				FQDN:            snmpAddress,
				DisplayName:     "Z-The-Initial-Name",
			},
			Abstracted:      true,
			InitialPassword: "password already set",
		},
	}

	optAgentSort := cmpopts.SortSlices(func(x payloadAgent, y payloadAgent) bool { return x.ID < y.ID })
	if diff := cmp.Diff(wantAgents, agents, cmpopts.EquateEmpty(), optAgentSort); diff != "" {
		t.Errorf("agents mismatch (-want +got)\n%s", diff)
	}

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: types.LabelName, Value: "cpu_used"}),
		labels.New(
			labels.Label{Name: types.LabelName, Value: "ifOutOctets"},
			labels.Label{Name: types.LabelSNMPTarget, Value: snmpAddress},
			labels.Label{Name: types.LabelMetaBleemeoTargetAgentUUID, Value: idAgentSNMP},
		),
	})

	helper.api.now.Advance(time.Second)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	var metrics []metricPayload

	helper.api.resources["metric"].Store(&metrics)

	wantMetrics := []metricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:         "1",
				AgentID:    idAgentMain,
				LabelsText: `__name__="agent_status"`,
			},
			Name: "agent_status",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:         "2",
				AgentID:    idAgentMain,
				LabelsText: `__name__="cpu_used"`,
			},
			Name: "cpu_used",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:         "3",
				AgentID:    idAgentSNMP,
				LabelsText: fmt.Sprintf(`__name__="ifOutOctets",snmp_target="%s"`, snmpAddress),
			},
			Name: "ifOutOctets",
		},
	}

	optMetricSort := cmpopts.SortSlices(func(x metricPayload, y metricPayload) bool { return x.ID < y.ID })
	if diff := cmp.Diff(wantMetrics, metrics, cmpopts.EquateEmpty(), optMetricSort); diff != "" {
		t.Errorf("metrics mismatch (-want +got)\n%s", diff)
	}

	helper.api.now.Advance(10 * time.Second)

	helper.initSynchronizer(t)

	for n := 1; n <= 2; n++ {
		n := n
		t.Run(fmt.Sprintf("sub-run-%d", n), func(t *testing.T) {
			helper.api.now.Advance(time.Second)

			helper.pushPoints(t, []labels.Labels{
				labels.New(labels.Label{Name: types.LabelName, Value: "cpu_used"}),
				labels.New(
					labels.Label{Name: types.LabelName, Value: "ifOutOctets"},
					labels.Label{Name: types.LabelSNMPTarget, Value: snmpAddress},
					labels.Label{Name: types.LabelMetaBleemeoTargetAgentUUID, Value: idAgentSNMP},
				),
			})

			if err := helper.runOnce(t); err != nil {
				t.Fatal(err)
			}

			helper.api.resources["metric"].Store(&metrics)

			if diff := cmp.Diff(wantMetrics, metrics, cmpopts.EquateEmpty(), optMetricSort); diff != "" {
				t.Errorf("metrics mismatch (-want +got)\n%s", diff)
			}
		})
	}
}

func TestSyncWithSNMPDelete(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	var (
		updateLabelsCallCount int
		l                     sync.Mutex
	)

	helper.SNMP = []snmp.Target{
		{InitialName: "Z-The-Initial-Name", Address: snmpAddress, Type: "unused-today"},
	}
	helper.MetricFormat = types.MetricFormatPrometheus
	helper.NotifyLabelsUpdate = func(_ context.Context) {
		l.Lock()
		defer l.Unlock()

		updateLabelsCallCount++
	}

	helper.initSynchronizer(t)

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: types.LabelName, Value: "cpu_used"}),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	var agents []payloadAgent

	helper.api.resources["agent"].Store(&agents)

	var (
		idAgentMain string
		idAgentSNMP string
	)

	for _, a := range agents {
		if a.FQDN == "test.bleemeo.com" {
			idAgentMain = a.ID
		}

		if a.FQDN == snmpAddress {
			idAgentSNMP = a.ID
		}
	}

	wantAgents := []payloadAgent{
		{
			Agent: bleemeoTypes.Agent{
				ID:              idAgentMain,
				CreatedAt:       helper.api.now.Now(),
				AccountID:       accountID,
				CurrentConfigID: newAccountConfig.ID,
				AgentType:       agentTypeAgent.ID,
				FQDN:            "test.bleemeo.com",
				DisplayName:     "test.bleemeo.com",
			},
			Abstracted:      false,
			InitialPassword: "password already set",
		},
		{
			Agent: bleemeoTypes.Agent{
				ID:              idAgentSNMP,
				CreatedAt:       helper.api.now.Now(),
				AccountID:       accountID,
				CurrentConfigID: newAccountConfig.ID,
				AgentType:       agentTypeSNMP.ID,
				FQDN:            snmpAddress,
				DisplayName:     "Z-The-Initial-Name",
			},
			Abstracted:      true,
			InitialPassword: "password already set",
		},
	}

	optAgentSort := cmpopts.SortSlices(func(x payloadAgent, y payloadAgent) bool { return x.ID < y.ID })
	if diff := cmp.Diff(wantAgents, agents, cmpopts.EquateEmpty(), optAgentSort); diff != "" {
		t.Errorf("agents mismatch (-want +got)\n%s", diff)
	}

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: types.LabelName, Value: "cpu_used"}),
		labels.New(
			labels.Label{Name: types.LabelName, Value: "ifOutOctets"},
			labels.Label{Name: types.LabelSNMPTarget, Value: snmpAddress},
			labels.Label{Name: types.LabelMetaBleemeoTargetAgentUUID, Value: idAgentSNMP},
		),
	})

	helper.api.now.Advance(time.Second)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	var metrics []metricPayload

	helper.api.resources["metric"].Store(&metrics)

	wantMetrics := []metricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:         "1",
				AgentID:    idAgentMain,
				LabelsText: `__name__="agent_status"`,
			},
			Name: "agent_status",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:         "2",
				AgentID:    idAgentMain,
				LabelsText: `__name__="cpu_used"`,
			},
			Name: "cpu_used",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:         "3",
				AgentID:    idAgentSNMP,
				LabelsText: fmt.Sprintf(`__name__="ifOutOctets",snmp_target="%s"`, snmpAddress),
			},
			Name: "ifOutOctets",
		},
	}

	optMetricSort := cmpopts.SortSlices(func(x metricPayload, y metricPayload) bool { return x.ID < y.ID })
	if diff := cmp.Diff(wantMetrics, metrics, cmpopts.EquateEmpty(), optMetricSort); diff != "" {
		t.Errorf("metrics mismatch (-want +got)\n%s", diff)
	}

	helper.api.now.Advance(10 * time.Second)

	// Delete the SNMP agent on API.
	callCountBefore := updateLabelsCallCount

	helper.api.resources["agent"].DelStore(idAgentSNMP)
	helper.api.resources["metric"].DelStore("3")

	helper.initSynchronizer(t)

	helper.api.now.Advance(time.Second)

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: types.LabelName, Value: "cpu_used"}),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	helper.api.resources["agent"].Store(&agents)

	for _, a := range agents {
		if a.FQDN == snmpAddress {
			idAgentSNMP = a.ID
		}
	}

	wantAgents = []payloadAgent{
		wantAgents[0],
		{
			Agent: bleemeoTypes.Agent{
				ID:              idAgentSNMP,
				CreatedAt:       helper.api.now.Now(),
				AccountID:       accountID,
				CurrentConfigID: newAccountConfig.ID,
				AgentType:       agentTypeSNMP.ID,
				FQDN:            snmpAddress,
				DisplayName:     "Z-The-Initial-Name",
			},
			Abstracted:      true,
			InitialPassword: "password already set",
		},
	}

	if diff := cmp.Diff(wantAgents, agents, cmpopts.EquateEmpty(), optAgentSort); diff != "" {
		t.Errorf("agents mismatch (-want +got)\n%s", diff)
	}

	helper.api.now.Advance(time.Second)

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: types.LabelName, Value: "cpu_used"}),
		labels.New(
			labels.Label{Name: types.LabelName, Value: "ifOutOctets"},
			labels.Label{Name: types.LabelSNMPTarget, Value: snmpAddress},
			labels.Label{Name: types.LabelMetaBleemeoTargetAgentUUID, Value: idAgentSNMP},
		),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	wantMetrics[2] = metricPayload{
		Metric: bleemeoTypes.Metric{
			ID:         "4",
			AgentID:    idAgentSNMP,
			LabelsText: fmt.Sprintf(`__name__="ifOutOctets",snmp_target="%s"`, snmpAddress),
		},
		Name: "ifOutOctets",
	}

	helper.api.resources["metric"].Store(&metrics)

	if diff := cmp.Diff(wantMetrics, metrics, cmpopts.EquateEmpty(), optMetricSort); diff != "" {
		t.Errorf("metrics mismatch (-want +got)\n%s", diff)
	}

	if callCountBefore == updateLabelsCallCount {
		t.Errorf("updateLabelsCallCount = %d, want > %d", updateLabelsCallCount, callCountBefore)
	}
}
