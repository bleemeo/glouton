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
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	// fixed "random" values are enought for tests.
	accountID        string = "9da59f53-1d90-4441-ae58-42c661cfea83"
	registrationKey  string = "e2c22e59-0621-49e6-b5dd-bdb02cbac9f1"
	activeMonitorURL string = "http://bleemeo.com"
	//nolint:gosec
	jwtToken string = "{\"token\":\"blebleble\"}"
)

var (
	errUnknownURLFormat   = errors.New("unknown URL format")
	errUnknownResource    = errors.New("unknown resource")
	errUnknownBool        = errors.New("unknown boolean")
	errUnknownRequestType = errors.New("type of request unknown")
	errIncorrectID        = errors.New("incorrect id")
	errInvalidAccountID   = errors.New("invalid accountId supplied")
	errInvalidAuthHeader  = errors.New("invalid authorization header")
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

	newAgentFact bleemeoTypes.AgentFact = bleemeoTypes.AgentFact{
		ID: "9690d66d-b47c-42ea-86d8-339373e4658c",
	}

	newAccountConfig bleemeoTypes.AccountConfig = bleemeoTypes.AccountConfig{
		ID: "02eb5b38-d4a0-4db4-9b43-06f63594a515",
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

	uuidRegexp  = regexp.MustCompile("^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-4[a-fA-F0-9]{3}-[8|9|aA|bB][a-fA-F0-9]{3}-[a-fA-F0-9]{12}$")
	errNotFound = errors.New("not found")
)

// getUUID returns the UUID stored in the HTTP query path, if any.
func getUUID(r *http.Request) (string, bool) {
	// we don't care about http parameters
	path := r.RequestURI[:strings.LastIndexByte(r.RequestURI, '/')]
	if uuidRegexp.MatchString(path) {
		return uuidRegexp.FindString(path), true
	}

	return "", false
}

// mockAPI fake global /v1 API endpoints. Currently only v1/info & v1/jwt-auth
// Use Handle to add additional endpoints.
type mockAPI struct {
	JWTUsername    string
	JWTPassword    string
	JWTToken       string
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

type errClient struct {
	body       interface{}
	statusCode int
}

func (e errClient) Error() string {
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
	api.AddResource("agent", &genericResource{
		Type: bleemeoTypes.Agent{},
	})
	api.AddResource("agentfact", &genericResource{
		Type: bleemeoTypes.AgentFact{},
	})
	api.AddResource("metric", &genericResource{
		Type: metricPayload{},
		PatchHook: func(r *http.Request, body []byte, valuePtr interface{}) error {
			var data map[string]interface{}

			metricPtr, _ := valuePtr.(*metricPayload)

			err := json.NewDecoder(bytes.NewReader(body)).Decode(&data)

			if boolText, ok := data["active"].(string); ok {
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

	var clientError errClient
	if errors.As(err, &clientError) {
		err = nil
		response = clientError.body
		status = clientError.statusCode
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
	if api.JWTUsername != "" {
		decoder := json.NewDecoder(r.Body)
		values := map[string]string{}

		if err := decoder.Decode(&values); err != nil {
			return nil, http.StatusInternalServerError, err
		}

		if values["username"] != api.JWTUsername || values["password"] != api.JWTPassword {
			return `{"non_field_errors":["Unable to log in with provided credentials."]}`, http.StatusBadRequest, nil
		}
	}

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

//nolint:gocyclo,cyclop
func (api *mockAPI) defaultHandler(r *http.Request) (interface{}, int, error) {
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
	Type    interface{}
	store   map[string]interface{}
	autoinc int

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

func (res *genericResource) List(r *http.Request) ([]interface{}, error) {
	results := make([]interface{}, 0, len(res.store))

	for _, x := range res.store {
		results = append(results, x)
	}

	return results, nil
}

func (res *genericResource) Create(r *http.Request) (interface{}, error) {
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

func runFakeAPI() *mockAPI {
	api := &mockAPI{
		JWTToken:    jwtToken,
		JWTUsername: newAgent.ID + "@bleemeo.com",
	}

	api.Handle("/v1/agent/", func(r *http.Request) (interface{}, int, error) {
		switch r.Method {
		case http.MethodPost:
			basicAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s@bleemeo.com:%s", accountID, registrationKey)))
			decoder := json.NewDecoder(r.Body)
			values := map[string]string{}
			if err := decoder.Decode(&values); err != nil {
				return nil, http.StatusInternalServerError, err
			}

			if values["account"] != accountID {
				err := fmt.Errorf("%w, got %v, want %v", errInvalidAccountID, values["account"], accountID)

				return nil, http.StatusInternalServerError, err
			}

			if r.Header.Get("Authorization") != basicAuth {
				err := fmt.Errorf("%w, got %v, want %v", errInvalidAuthHeader, r.Header.Get("Authorization"), basicAuth)

				return nil, http.StatusInternalServerError, err
			}

			api.JWTPassword = values["initial_password"]

			return newAgent, http.StatusCreated, nil
		case http.MethodPatch:
			return newAgent, http.StatusOK, nil
		default:
			return paginatedList([]interface{}{newAgent}), http.StatusOK, nil
		}
	})

	api.Handle("/v1/agentfact/", func(r *http.Request) (interface{}, int, error) {
		if r.Method == http.MethodPost {
			return newAgentFact, http.StatusCreated, nil
		}

		if _, present := getUUID(r); present {
			return newAgentFact, http.StatusOK, nil
		}

		return paginatedList([]interface{}{newAgentFact}), http.StatusOK, nil
	})

	api.Handle("/v1/container/", func(r *http.Request) (interface{}, int, error) {
		return paginatedList([]interface{}{}), http.StatusOK, nil
	})

	api.Handle("/v1/service/", func(r *http.Request) (interface{}, int, error) {
		return paginatedList([]interface{}{newMonitor}), http.StatusOK, nil
	})

	api.Handle("/v1/metric/", func(r *http.Request) (interface{}, int, error) {
		uuid, present := getUUID(r)
		if present {
			for _, v := range newMetrics {
				if v.ID == uuid {
					return v, http.StatusOK, nil
				}
			}

			return "", http.StatusNotFound, nil
		}

		results := make([]interface{}, len(newMetrics))
		for i, m := range newMetrics {
			results[i] = m
		}

		return paginatedList(results), http.StatusOK, nil
	})

	api.Handle("/v1/accountconfig/", func(r *http.Request) (interface{}, int, error) {
		if _, present := getUUID(r); present {
			return newAccountConfig, http.StatusOK, nil
		}

		return paginatedList([]interface{}{newAccountConfig}), http.StatusOK, nil
	})

	return api
}

func TestSync(t *testing.T) {
	api := runFakeAPI()
	httpServer := api.Server()

	defer httpServer.Close()

	cfg := &config.Configuration{}

	if err := cfg.LoadByte([]byte("")); err != nil {
		t.Fatal(err)
	}

	cfg.Set("logging.level", "debug")
	cfg.Set("bleemeo.api_base", httpServer.URL)
	cfg.Set("bleemeo.account_id", accountID)
	cfg.Set("bleemeo.registration_key", registrationKey)
	cfg.Set("blackbox.enabled", true)

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

	s := New(Option{
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

	s.ctx = context.Background()

	// necessary to prevent the sync to try to deactivate every metrics
	s.startedAt = time.Now()

	err := s.setClient()
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
