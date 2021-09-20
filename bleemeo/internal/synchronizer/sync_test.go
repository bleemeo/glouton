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
	"testing"
	"time"

	"github.com/google/uuid"
)

const (
	// fixed "random" values are enought for tests.
	accountID        string = "9da59f53-1d90-4441-ae58-42c661cfea83"
	registrationKey  string = "e2c22e59-0621-49e6-b5dd-bdb02cbac9f1"
	activeMonitorURL string = "http://bleemeo.com"
)

var (
	errUnknownURLFormat   = errors.New("unknown URL format")
	errUnknownResource    = errors.New("unknown resource")
	errUnknownBool        = errors.New("unknown boolean")
	errUnknownRequestType = errors.New("type of request unknown")
	errIncorrectID        = errors.New("incorrect id")
	errInvalidAccountID   = errors.New("invalid accountId supplied")
)

// this is a go limitation, these are constants but we have to treat them as variables
//nolint:gochecknoglobals
var (
	newAgent bleemeoTypes.Agent = bleemeoTypes.Agent{
		ID:        "33708da4-28d4-45aa-b811-49c82b594627",
		AccountID: accountID,
		// same one as in newAccountConfig
		CurrentConfigID: "02eb5b38-d4a0-4db4-9b43-06f63594a515",
	}

	newAccountConfig bleemeoTypes.AccountConfig = bleemeoTypes.AccountConfig{
		ID:   "02eb5b38-d4a0-4db4-9b43-06f63594a515",
		Name: "the-default",
	}

	newMetric1 bleemeoTypes.Metric = bleemeoTypes.Metric{
		ID:            "decce8cf-c2f7-43c3-b66e-10429debd994",
		LabelsText:    "__name__=\"some_metric_1\",label=\"value\"",
		Labels:        map[string]string{"__name__": "some_metric_1", "label": "value"},
		DeactivatedAt: time.Time{},
	}
	newMetric2 bleemeoTypes.Metric = bleemeoTypes.Metric{
		ID:         "055af752-5c01-4abc-9bb2-9d64032ef970",
		LabelsText: "__name__=\"some_metric_2\",label=\"another_value !\"",
		Labels:     map[string]string{"__name__": "some_metric_2", "label": "another_value !"},
	}
	newMetricActiveMonitor bleemeoTypes.Metric = bleemeoTypes.Metric{
		ID:         "52b9c46e-00b9-4e80-a852-781426a3a193",
		LabelsText: "__name__=\"probe_whatever\",instance=\"http://bleemeo.com\"",
		Labels:     map[string]string{"__name__": "probe_whatever", "instance": "http://bleemeo.com"},
		ServiceID:  newMonitor.ID,
	}
	newMetrics []bleemeoTypes.Metric = []bleemeoTypes.Metric{newMetric1, newMetric2, newMetricActiveMonitor}

	newMonitor bleemeoTypes.Monitor = bleemeoTypes.Monitor{
		Service: bleemeoTypes.Service{
			ID: "fdd9d999-e2ff-45d3-af2b-6519cf8e3e70",
		},
		URL:     activeMonitorURL,
		AgentID: newAgent.ID,
	}

	newAgentType1 bleemeoTypes.AgentType = bleemeoTypes.AgentType{
		DisplayName: "A server monitored with Bleemeo agent",
		ID:          "61zb6a83-d90a-4165-bf04-944e0b2g2a10",
		Name:        "agent",
	}
	newAgentType2 bleemeoTypes.AgentType = bleemeoTypes.AgentType{
		DisplayName: "A server monitored with SNMP agent",
		ID:          "823b6a83-d70a-4768-be64-50450b282a30",
		Name:        "snmp",
	}

	agentConfigAgent = bleemeoTypes.AgentConfig{
		ID:               "cab64659-a765-4878-84d8-c7b0112aaecb",
		AccountConfig:    newAccountConfig.ID,
		AgentType:        newAgentType1.ID,
		MetricResolution: 10,
	}
	agentConfigSNMP = bleemeoTypes.AgentConfig{
		ID:               "a89d16c1-55be-4d89-9c9b-489c2d86d3fa",
		AccountConfig:    newAccountConfig.ID,
		AgentType:        newAgentType2.ID,
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

func newAPI() *mockAPI {
	api := &mockAPI{
		JWTToken:       "random-value",
		PreRequestHook: nil,
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

			if agent.AccountID != accountID {
				err := fmt.Errorf("%w, got %v, want %v", errInvalidAccountID, agent.AccountID, accountID)

				return err
			}

			agent.CurrentConfigID = newAccountConfig.ID
			api.JWTUsername = fmt.Sprintf("%s@bleemeo.com", agent.ID)
			api.JWTPassword = agent.InitialPassword

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
		Type:        servicePayload{},
		ValidFilter: []string{"agent", "active", "monitor"},
	})

	api.resources["agenttype"].SetStore(
		newAgentType1,
		newAgentType2,
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
}

func (res *genericResource) SetStore(values ...interface{}) {
	res.store = nil

	res.AddStore(values...)
}

func (res *genericResource) AddStore(values ...interface{}) {
	if res.store == nil {
		res.store = make(map[string]interface{})
	}

	for _, x := range values {
		value := reflect.ValueOf(x)
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

func (res *genericResource) List(r *http.Request) ([]interface{}, error) {
	results := make([]interface{}, 0, len(res.store))

	filter, err := res.getFilter(r)
	if err != nil {
		return nil, err
	}

	for _, x := range res.store {
		match := true

		value := reflect.ValueOf(x)

		for k, v := range filter {
			if !match {
				break
			}

			for n := 0; n < value.Type().NumField(); n++ {
				jsonTag := value.Type().Field(n).Tag.Get("json")
				if jsonTag == k {
					got := value.Field(n).String()

					// Note: we match *all* field as string... this seems to work
					// up to now, but if you do comparison with non-string field (boolean, date,...)
					// it test don't work, it could be due to this cheap comparison :)
					if v != got {
						match = false

						break
					}
				}
			}
		}

		if match {
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

func TestSync(t *testing.T) {
	api := newAPI()
	httpServer := api.Server()

	api.resources["metric"].AddStore(newMetric1, newMetric2, newMetricActiveMonitor)
	api.resources["service"].AddStore(newMonitor)

	defer httpServer.Close()

	cfg := &config.Configuration{}

	if err := cfg.LoadByte([]byte("")); err != nil {
		t.Fatal(err)
	}

	cfg.Set("logging.level", "debug")
	cfg.Set("bleemeo.api_base", httpServer.URL)
	cfg.Set("bleemeo.account_id", accountID)
	cfg.Set("bleemeo.registration_key", registrationKey)
	cfg.Set("blackbox.enable", true)

	cache := cache.Cache{}

	facts := facts.NewMockFacter()
	// necessary for registration
	facts.SetFact("fqdn", "test.bleemeo.com")

	state := state.NewMock()

	discovery := &discovery.MockDiscoverer{}

	store := store.New()
	store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{
				Time:  time.Now(),
				Value: 42.0,
			},
			Labels: map[string]string{"__name__": "cpu_used"},
		},
	})

	s, err := New(Option{
		Cache: &cache,
		GlobalOption: bleemeoTypes.GlobalOption{
			Config:                  cfg,
			Facts:                   facts,
			State:                   state,
			Discovery:               discovery,
			Store:                   store,
			MonitorManager:          (*blackbox.RegisterManager)(nil),
			NotifyFirstRegistration: func(ctx context.Context) {},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	s.ctx = context.Background()

	// necessary to prevent the sync to try to deactivate every metrics
	s.startedAt = time.Now()

	err = s.setClient()
	if err != nil {
		t.Fatal(err)
	}

	if err := s.runOnce(false); err != nil {
		t.Fatal(err)
	}

	if api.ServerErrorCount > 0 {
		t.Fatalf("Had %d server error, last: %v", api.ServerErrorCount, api.LastServerError)
	}

	if api.ClientErrorCount > 0 {
		t.Fatalf("Had %d client error", api.ClientErrorCount)
	}

	// Did we store all the metrics ?
	syncedMetrics := s.option.Cache.MetricsByUUID()

	for _, v := range newMetrics {
		syncedMetric, present := syncedMetrics[v.ID]

		if !present {
			t.Fatalf("missing metric %s", v.ID)
		}

		if !reflect.DeepEqual(v, syncedMetric) {
			t.Fatalf("got invalid metrics %v, want %v", syncedMetric, v)
		}
	}

	// Did we sync and enable the monitor present in the configuration ?
	syncedMonitors := s.option.Cache.Monitors()
	syncedMonitor := syncedMonitors[0]

	if !reflect.DeepEqual(newMonitor, syncedMonitor) {
		t.Fatalf("got invalid metrics %v, want %v", syncedMonitor, newMonitor)
	}
}
