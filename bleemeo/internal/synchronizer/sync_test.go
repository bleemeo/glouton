package synchronizer

import (
	"context"
	"encoding/json"
	"fmt"
	"glouton/agent/state"
	"glouton/bleemeo/internal/cache"
	"glouton/bleemeo/types"
	"glouton/config"
	"glouton/discovery"
	"glouton/facts"
	"glouton/prometheus/exporter/blackbox"
	"glouton/store"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	// fixed "random" values are enought for tests
	accountID        string = "9da59f53-1d90-4441-ae58-42c661cfea83"
	registrationKey  string = "e2c22e59-0621-49e6-b5dd-bdb02cbac9f1"
	activeMonitorURL string = "http://bleemeo.com"
	//nolint:gosec
	jwtToken string = "{\"token\":\"blebleble\"}"
)

// this is a go limitation, these are constants but we have to treat them as variables
//nolint:gochecknoglobals
var (
	newAgent types.Agent = types.Agent{
		ID:        "33708da4-28d4-45aa-b811-49c82b594627",
		AccountID: accountID,
		// same one as in newAccountConfig
		CurrentConfigID: "02eb5b38-d4a0-4db4-9b43-06f63594a515",
	}

	newAgentFact types.AgentFact = types.AgentFact{
		ID: "9690d66d-b47c-42ea-86d8-339373e4658c",
	}

	newAccountConfig types.AccountConfig = types.AccountConfig{
		ID: "02eb5b38-d4a0-4db4-9b43-06f63594a515",
	}

	newMetric1 types.Metric = types.Metric{
		ID:            "decce8cf-c2f7-43c3-b66e-10429debd994",
		LabelsText:    "__name__=\"some_metric_1\",label=\"value\"",
		Labels:        map[string]string{"__name__": "some_metric_1", "label": "value"},
		DeactivatedAt: time.Time{},
	}
	newMetric2 types.Metric = types.Metric{
		ID:         "055af752-5c01-4abc-9bb2-9d64032ef970",
		LabelsText: "__name__=\"some_metric_2\",label=\"another_value !\"",
		Labels:     map[string]string{"__name__": "some_metric_2", "label": "another_value !"},
	}
	newMetricActiveMonitor types.Metric = types.Metric{
		ID:         "52b9c46e-00b9-4e80-a852-781426a3a193",
		LabelsText: "__name__=\"probe_whatever\",instance=\"http://bleemeo.com\"",
		Labels:     map[string]string{"__name__": "probe_whatever", "instance": "http://bleemeo.com"},
		ServiceID:  newMonitor.ID,
	}
	newMetrics []types.Metric = []types.Metric{newMetric1, newMetric2, newMetricActiveMonitor}

	newMonitor types.Monitor = types.Monitor{
		Service: types.Service{
			ID: "fdd9d999-e2ff-45d3-af2b-6519cf8e3e70",
		},
		URL:     activeMonitorURL,
		AgentID: newAgent.ID,
	}
	newMonitors []types.Monitor = []types.Monitor{newMonitor}

	uuidRegexp *regexp.Regexp = regexp.MustCompile("^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-4[a-fA-F0-9]{3}-[8|9|aA|bB][a-fA-F0-9]{3}-[a-fA-F0-9]{12}$")
)

// writeListing writes a result listing.
func writeListing(w io.Writer, elems interface{}) {
	var results strings.Builder

	reflect.ValueOf(elems).Len()

	if err := json.NewEncoder(&results).Encode(elems); err != nil {
		panic("Cannot encode a constant object !?")
	}

	fmt.Fprintf(w, "{\"next\": null, \"previous\": null, \"results\": %s}", results.String())
}

// getUUID returns the UUID stored in the HTTP query path, if any.
func getUUID(r *http.Request) (string, bool) {
	// we don't care about http parameters
	path := r.RequestURI[:strings.LastIndexByte(r.RequestURI, '/')]
	if uuidRegexp.MatchString(path) {
		return uuidRegexp.FindString(path), true
	}

	return "", false
}

func runFakeAPI(t *testing.T) *httptest.Server {
	serveMux := http.NewServeMux()

	serveMux.HandleFunc("/v1/agent/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			decoder := json.NewDecoder(r.Body)
			values := map[string]string{}
			if err := decoder.Decode(&values); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if values["account"] != accountID {
				t.Fatalf("Invalid accountId supplied, got %v, want %v", values["account"], accountID)
			}
			w.WriteHeader(http.StatusCreated)
			if err := json.NewEncoder(w).Encode(newAgent); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		case http.MethodPatch:
			if err := json.NewEncoder(w).Encode(newAgent); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		default:
			writeListing(w, []types.Agent{newAgent})
		}
	})

	serveMux.HandleFunc("/v1/jwt-auth/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, jwtToken)
	})

	serveMux.HandleFunc("/v1/agentfact/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			if err := json.NewEncoder(w).Encode(newAgentFact); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}
		if _, present := getUUID(r); present {
			if err := json.NewEncoder(w).Encode(newAgentFact); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
			}
		} else {
			writeListing(w, []types.AgentFact{newAgentFact})
		}
	})

	serveMux.HandleFunc("/v1/container/", func(w http.ResponseWriter, r *http.Request) {
		writeListing(w, []types.Container{})
	})

	serveMux.HandleFunc("/v1/service/", func(w http.ResponseWriter, r *http.Request) {
		writeListing(w, newMonitors)
	})

	serveMux.HandleFunc("/v1/metric/", func(w http.ResponseWriter, r *http.Request) {
		uuid, present := getUUID(r)
		if present {
			for _, v := range newMetrics {
				if v.ID == uuid {
					if err := json.NewEncoder(w).Encode(v); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusInternalServerError)
					}
					return
				}
			}

			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeListing(w, newMetrics)
	})

	serveMux.HandleFunc("/v1/accountconfig/", func(w http.ResponseWriter, r *http.Request) {
		if _, present := getUUID(r); present {
			if err := json.NewEncoder(w).Encode(newAccountConfig); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
			}
		} else {
			writeListing(w, []types.AccountConfig{newAccountConfig})
		}
	})

	return httptest.NewServer(serveMux)
}

func TestSyncMetrics(t *testing.T) {
	httpServer := runFakeAPI(t)
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

	discovery := discovery.NewMockDiscoverer()

	store := store.New()

	s := New(Option{
		Cache: &cache,
		GlobalOption: types.GlobalOption{
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

	if err := s.runOnce(); err != nil {
		t.Fatal(err)
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
