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
	"glouton/store"
	otherTypes "glouton/types"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	// fixed "random" values are enought for tests
	apiBase          string = "localhost:8001"
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
		ServiceID:  newActiveMonitor.ID,
	}
	newMetrics []types.Metric = []types.Metric{newMetric1, newMetric2, newMetricActiveMonitor}

	newActiveMonitor types.Monitor = types.Monitor{
		Service: types.Service{
			ID: "fdd9d999-e2ff-45d3-af2b-6519cf8e3e70",
		},
		URL:     activeMonitorURL,
		AgentID: newAgent.ID,
	}
	newInactiveMonitor types.Monitor = types.Monitor{
		Service: types.Service{
			ID: "d29662a8-a505-4978-9dfc-c18a28dab665",
		},
		URL:     "http://perdu.com",
		AgentID: newAgent.ID,
	}
	newMonitors []types.Monitor = []types.Monitor{newActiveMonitor, newInactiveMonitor}

	uuidRegexp *regexp.Regexp = regexp.MustCompile("^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-4[a-fA-F0-9]{3}-[8|9|aA|bB][a-fA-F0-9]{3}-[a-fA-F0-9]{12}$")
)

// writeListing writes a result listing.
func writeListing(w io.Writer, elems interface{}) {
	var results strings.Builder

	reflect.ValueOf(elems).Len()

	if err := json.NewEncoder(&results).Encode(elems); err != nil {
		panic("Cannot encode a constant object !?")
	}

	fmt.Fprintf(w, "{\"count\": %d, \"next\": null, \"previous\": null, \"results\": %s}", reflect.ValueOf(elems).Len(), results.String())
}

// httpHandleFunc is a very tiny wraper around http.HandleFunc to add proper headers to responses.
func httpHandleFunc(url string, f func(http.ResponseWriter, *http.Request)) {
	http.HandleFunc(url, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		f(w, r)
	})
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

func runFakeAPI(t *testing.T) {
	httpHandleFunc("/v1/agent/", func(w http.ResponseWriter, r *http.Request) {
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

	httpHandleFunc("/v1/jwt-auth/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, jwtToken)
	})

	httpHandleFunc("/v1/agentfact/", func(w http.ResponseWriter, r *http.Request) {
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

	httpHandleFunc("/v1/container/", func(w http.ResponseWriter, r *http.Request) {
		writeListing(w, []types.Container{})
	})

	httpHandleFunc("/v1/service/", func(w http.ResponseWriter, r *http.Request) {
		writeListing(w, newMonitors)
	})

	httpHandleFunc("/v1/metric/", func(w http.ResponseWriter, r *http.Request) {
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

	httpHandleFunc("/v1/accountconfig/", func(w http.ResponseWriter, r *http.Request) {
		if _, present := getUUID(r); present {
			if err := json.NewEncoder(w).Encode(newAccountConfig); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
			}
		} else {
			writeListing(w, []types.AccountConfig{newAccountConfig})
		}
	})

	t.Error(http.ListenAndServe(apiBase, nil))
}

func basicTestSetup(t *testing.T) *Synchronizer {
	go runFakeAPI(t)

	// Wait for the HTTP server to start
	time.Sleep(250 * time.Millisecond)

	cfg := &config.Configuration{}

	if err := cfg.LoadByte([]byte("")); err != nil {
		t.Fatal(err)
	}

	cfg.Set("logging.level", "debug")
	cfg.Set("bleemeo.api_base", "http://"+apiBase)
	cfg.Set("bleemeo.account_id", accountID)
	cfg.Set("bleemeo.registration_key", registrationKey)
	cfg.Set("blackbox.targets", []interface{}{
		map[string]interface{}{
			"url":    activeMonitorURL,
			"module": "http_2xx",
		},
	})
	// excerpt from the default config
	cfg.Set("blackbox.modules", map[string]interface{}{
		"http_2xx": map[string]interface{}{
			"prober": "http",
			"http": map[string]interface{}{
				"valid_http_versions": []string{"HTTP/1.1", "HTTP/2.0"},
			},
		},
	})

	cache := cache.Cache{}

	facts := facts.NewMockFacter()
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

	return s
}

func TestSyncMetrics(t *testing.T) {
	s := basicTestSetup(t)

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

	// Did we store the monitor present in the configuration ?
	syncedMonitors := s.option.Cache.Monitors()
	syncedMonitor, present := syncedMonitors[activeMonitorURL]

	if !present {
		t.Fatalf("missing monitor for %s", activeMonitorURL)
	}

	if !reflect.DeepEqual(newActiveMonitor, syncedMonitor) {
		t.Fatalf("got invalid metrics %v, want %v", syncedMonitor, newActiveMonitor)
	}

	// assert that we did not sync the inactive monitor
	_, present = syncedMonitors[newInactiveMonitor.URL]
	if present {
		t.Fatalf("the monitor for %s should not be available", newInactiveMonitor.URL)
	}

	annotations := otherTypes.MetricAnnotations{Kind: otherTypes.MonitorMetricKind}

	point1 := otherTypes.Point{Time: time.Now().Add(-5 * time.Second), Value: 0.5}
	point2 := otherTypes.Point{Time: time.Now(), Value: 0.75}

	// we can push a few points in the store and check that they match the expected metric
	s.option.Store.(*store.Store).PushPoints([]otherTypes.MetricPoint{{
		Point:       point1,
		Labels:      newMetricActiveMonitor.Labels,
		Annotations: annotations,
	}, {
		Point:       point2,
		Labels:      newMetricActiveMonitor.Labels,
		Annotations: annotations,
	}})

	metrics, err := s.option.Store.Metrics(nil)
	if err != nil {
		t.Fatalf("couldn't obtain metrics from the store")
	}

	if len(metrics) != 1 {
		t.Fatalf("invalid number of metrics found, got %d, want 1", len(metrics))
	}

	if metrics[0].Annotations() != annotations {
		t.Fatalf("invalid annotations, got %v, want %v", metrics[0].Annotations(), annotations)
	}

	if !reflect.DeepEqual(metrics[0].Labels(), newMetricActiveMonitor.Labels) {
		t.Fatalf("invalid labels on metric, got %v, want %v", metrics[0].Labels(), newMetricActiveMonitor.Labels)
	}

	metricPoints, err := metrics[0].Points(time.Now().Add(-10*time.Second), time.Now())
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(metricPoints, []otherTypes.Point{point1, point2}) {
		t.Fatalf("invalid points, got %v, want %v", metricPoints, []otherTypes.Point{point1, point2})
	}
}
