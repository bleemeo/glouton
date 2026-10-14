// Copyright 2015-2026 Bleemeo
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

package syncregistrationstatus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"
	gloutonTypes "github.com/bleemeo/glouton/types"
)

var errTestAPI = errors.New("test API error")

// fakeState is a State holding nothing but the agent's credentials.
type fakeState struct {
	agentID string
}

func (fakeState) Set(string, any) error                           { return nil }
func (fakeState) Get(string, any) error                           { return nil }
func (fakeState) GetByPrefix(string, any) (map[string]any, error) { return map[string]any{}, nil }
func (fakeState) Delete(string) error                             { return nil }
func (s fakeState) BleemeoCredentials() (string, string)          { return s.agentID, "password" }
func (fakeState) SetBleemeoCredentials(string, string) error      { return nil }

// fakeStore holds the metrics the synchronizer reads. Any other call panics.
type fakeStore struct {
	bleemeoTypes.Store

	metrics []gloutonTypes.Metric
	err     error
}

func (s *fakeStore) Metrics(map[string]string) ([]gloutonTypes.Metric, error) {
	return s.metrics, s.err
}

// storeMetric is a metric of the store, with its last point at lastPoint.
type storeMetric struct {
	name        string
	annotations gloutonTypes.MetricAnnotations
	lastPoint   time.Time
}

func (m storeMetric) Labels() map[string]string {
	return map[string]string{gloutonTypes.LabelName: m.name}
}

func (m storeMetric) Annotations() gloutonTypes.MetricAnnotations { return m.annotations }

func (m storeMetric) Points(time.Time, time.Time) ([]gloutonTypes.Point, error) { return nil, nil }

func (m storeMetric) LastPointReceivedAt() time.Time { return m.lastPoint }

// serviceMetric is a metric of service received just now.
func serviceMetric(name string, service string) storeMetric {
	return storeMetric{
		name:        name,
		annotations: gloutonTypes.MetricAnnotations{ServiceName: service},
		lastPoint:   time.Now(),
	}
}

// fakeClient records each registration status sent. Any other call panics.
type fakeClient struct {
	types.Client

	ids    []string
	bodies []string
	err    error
}

func (c *fakeClient) UpdateAgentRegistrationStatus(_ context.Context, id string, state any) error {
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}

	c.ids = append(c.ids, id)
	c.bodies = append(c.bodies, string(body))

	return c.err
}

// fakeExecution is an execution started at now. Any call it does not define panics.
type fakeExecution struct {
	types.SynchronizationExecution

	option        types.Option
	client        *fakeClient
	now           time.Time
	onlyEssential bool
}

func (e *fakeExecution) BleemeoAPIClient() types.Client { return e.client }
func (e *fakeExecution) IsOnlyEssential() bool          { return e.onlyEssential }
func (e *fakeExecution) Option() types.Option           { return e.option }
func (e *fakeExecution) StartedAt() time.Time           { return e.now }

// countingDiscoverer counts the calls to ServicesState, one per payload built, and to
// LastDiscoveryAt.
type countingDiscoverer struct {
	*discovery.MockDiscoverer

	builds  int
	latests int
}

func (d *countingDiscoverer) ServicesState() []discovery.ServiceState {
	d.builds++

	return d.MockDiscoverer.ServicesState()
}

func (d *countingDiscoverer) LastDiscoveryAt() time.Time {
	d.latests++

	return d.MockDiscoverer.LastDiscoveryAt()
}

type entityTest struct {
	entity    *SyncRegistrationStatus
	execution *fakeExecution
	discovery *countingDiscoverer
	store     *fakeStore
	metrics   []bleemeoTypes.MetricState
	// metricsUnpublished acts as a synchronizer that did not publish the metrics state yet.
	metricsUnpublished bool
}

// newEntityTest returns an entity for agentID, whose discovery ran and whose metrics state
// was published.
func newEntityTest(agentID string) *entityTest {
	state := fakeState{agentID: agentID}
	test := &entityTest{
		discovery: &countingDiscoverer{MockDiscoverer: &discovery.MockDiscoverer{UpdatedAt: t0.Add(-time.Minute)}},
		execution: &fakeExecution{client: &fakeClient{}, now: t0},
		store:     &fakeStore{},
	}

	test.execution.option.State = state
	test.execution.option.Discovery = test.discovery
	test.execution.option.Store = test.store
	test.execution.option.Cache = cache.Load(state)
	setAllowlist(test.execution.option.Cache, "mysql_queries,mysql_threads,postfix_queue_size,service_status")
	setSendRegistrationStatus(test.execution.option.Cache, true)
	test.entity = New(
		func() ([]bleemeoTypes.MetricState, bool) { return test.metrics, !test.metricsUnpublished },
		func() time.Time { return test.execution.now },
	)

	return test
}

// setSendRegistrationStatus sets the account's GLOUTON_SEND_REGISTRATION_STATUS config to value,
// or removes it when value is nil.
func setSendRegistrationStatus(c *cache.Cache, value any) {
	configs := slices.DeleteFunc(c.Configs(), func(cfg bleemeoTypes.Config) bool {
		return cfg.Type == bleemeoTypes.ConfigTypeGloutonSendRegistrationStatus
	})

	if value != nil {
		configs = append(configs, bleemeoTypes.Config{
			ID:      "send-registration-status-id",
			Type:    bleemeoTypes.ConfigTypeGloutonSendRegistrationStatus,
			Account: "account-id",
			Value:   value,
		})
	}

	c.SetConfigs(configs)
}

// setAllowlist gives the cache an account configuration whose agent metrics allow-list is
// allowlist, as Bleemeo's filter reads it.
func setAllowlist(c *cache.Cache, allowlist string) {
	c.SetAccountID("account-id")
	c.SetAgentTypes([]bleemeoTypes.AgentType{{ID: "agent-type-id", Name: bleemeo.AgentType_Agent}})
	c.SetConfigs([]bleemeoTypes.Config{{
		ID:        "allowlist-id",
		Type:      bleemeoTypes.ConfigTypeAgentMetricsAllowlist,
		Account:   "account-id",
		AgentType: "agent-type-id",
		Value:     allowlist,
	}})
}

// run does what Execution.run does with one entity, at now. It returns whether the entity
// asked for a synchronization.
func (e *entityTest) run(t *testing.T, now time.Time) (bool, error) {
	t.Helper()

	e.execution.now = now
	ctx := t.Context()

	execution, err := e.entity.PrepareExecution(ctx, e.execution)
	if err != nil {
		t.Fatal(err)
	}

	defer execution.FinishExecution(ctx)

	need, err := execution.NeedSynchronization(ctx)
	if err != nil || !need {
		return need, err
	}

	if err := execution.RefreshCache(ctx, types.SyncTypeNormal); err != nil {
		return need, err
	}

	return need, execution.SyncRemoteAndLocal(ctx, types.SyncTypeNormal)
}

func mysqlMissingCredentials() discovery.ServiceState {
	return discovery.ServiceState{
		Name:           "mysql",
		ServiceType:    discovery.MySQLService,
		Dynamic:        true,
		InputSupported: true,
		Error:          "failed to create the input: missing credentials or address",
		InputStatus:    discovery.InputStatusMissingInfo,
	}
}

// TestEntityGoldenPayload pins the registration_status value sent to Bleemeo, the only guard on the format:
// the API does not validate what is inside registration_status.
func TestEntityGoldenPayload(t *testing.T) {
	test := newEntityTest(testAgent)
	test.discovery.States = []discovery.ServiceState{mysqlMissingCredentials()}
	test.metrics = []bleemeoTypes.MetricState{{Name: "disk_used_perc", Reason: bleemeoTypes.ReasonNotAvailableInPlan, Count: 3}}
	test.execution.option.Cache.SetServices([]bleemeoTypes.Service{{ID: "service-uuid", Label: "mysql", Active: true}})

	if _, err := test.run(t, t0); err != nil {
		t.Fatal(err)
	}

	client := test.execution.client
	if len(client.bodies) != 1 {
		t.Fatalf("sent %d times, want once", len(client.bodies))
	}

	want := `{"glouton_version":"0.1","generated_at":"2026-10-02T12:00:00Z","last_discovery_at":"2026-10-02T11:59:00Z",` +
		`"services":[{"name":"mysql","type":"mysql","instance":"","container_name":"",` +
		`"dynamic_discovery":true,"input_supported":true,` +
		`"error":"failed to create the input: missing credentials or address","input_status":"missing_info",` +
		`"service_uuid":"service-uuid","metrics":"zero"}],` +
		`"services_total":1,` +
		`"metrics":[{"name":"disk_used_perc","reason":"not-available-in-plan","count":3}],"metrics_total":1}`

	if client.bodies[0] != want {
		t.Errorf("body:\n%s\nwant:\n%s", client.bodies[0], want)
	}

	if client.ids[0] != testAgent {
		t.Errorf("sent to agent %q, want %q", client.ids[0], testAgent)
	}
}

// TestEntityEmptyLists checks an agent with nothing to report sends empty lists, not null.
func TestEntityEmptyLists(t *testing.T) {
	test := newEntityTest(testAgent)

	if _, err := test.run(t, t0); err != nil {
		t.Fatal(err)
	}

	want := `{"glouton_version":"0.1","generated_at":"2026-10-02T12:00:00Z","last_discovery_at":"2026-10-02T11:59:00Z",` +
		`"services":[],"services_total":0,"metrics":[],"metrics_total":0}`
	if bodies := test.execution.client.bodies; len(bodies) != 1 || bodies[0] != want {
		t.Errorf("bodies = %q, want [%q]", bodies, want)
	}
}

// TestEntityDoesNotResendSameValue checks an unchanged value is not sent again, however long
// after and however many discovery runs since, and a changed one is once the interval has passed.
func TestEntityDoesNotResendSameValue(t *testing.T) {
	test := newEntityTest(testAgent)
	test.discovery.States = []discovery.ServiceState{mysqlMissingCredentials()}

	if _, err := test.run(t, t0); err != nil {
		t.Fatal(err)
	}

	test.discovery.UpdatedAt = t0.Add(time.Hour)

	need, err := test.run(t, t0.Add(2*time.Hour))
	if err != nil || need {
		t.Fatalf("unchanged value: need = %v, err = %v; want false, nil", need, err)
	}

	test.metrics = []bleemeoTypes.MetricState{{Name: "mysql_queries", Reason: bleemeoTypes.ReasonPending, Count: 1}}

	// A minute later, as the payload was just built.
	if need, err := test.run(t, t0.Add(2*time.Hour+minNewServiceInterval)); err != nil || !need {
		t.Fatalf("changed value: need = %v, err = %v; want true, nil", need, err)
	}

	if sent := len(test.execution.client.bodies); sent != 2 {
		t.Errorf("sent %d times, want 2", sent)
	}
}

// TestEntityBadRequest checks a refused value does not fail the synchronization, and is
// retried only after minSendInterval.
func TestEntityBadRequest(t *testing.T) {
	test := newEntityTest(testAgent)
	test.execution.client.err = &bleemeo.APIError{StatusCode: http.StatusBadRequest, Err: errTestAPI}

	if _, err := test.run(t, t0); err != nil {
		t.Fatalf("a refused value failed the synchronization: %v", err)
	}

	sent := test.execution.option.Cache.RegistrationStatusSent()
	if !sent.LastAttemptFailed || sent.Hash != "" {
		t.Errorf("recorded %+v, want a failed attempt and no accepted value", sent)
	}

	if need, _ := test.run(t, t0.Add(minNewServiceInterval)); need {
		t.Error("retried before minSendInterval")
	}

	test.execution.client.err = nil

	if need, err := test.run(t, t0.Add(minSendInterval)); err != nil || !need {
		t.Fatalf("after minSendInterval: need = %v, err = %v; want true, nil", need, err)
	}

	if sent := test.execution.option.Cache.RegistrationStatusSent(); sent.LastAttemptFailed || sent.Hash == "" {
		t.Errorf("recorded %+v, want an accepted value", sent)
	}
}

// TestEntityRefusedValues checks every refusal of the value itself, including a proxy's 413 for
// a body too large, is recorded as a failed attempt without failing the synchronization.
func TestEntityRefusedValues(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			test := newEntityTest(testAgent)
			test.execution.client.err = &bleemeo.APIError{StatusCode: status, Err: errTestAPI}

			if _, err := test.run(t, t0); err != nil {
				t.Errorf("a refused value failed the synchronization: %v", err)
			}

			if sent := test.execution.option.Cache.RegistrationStatusSent(); !sent.LastAttemptFailed {
				t.Errorf("recorded %+v, want a failed attempt", sent)
			}
		})
	}
}

// TestEntityOtherErrorFails checks an error other than a refusal fails the synchronization,
// as it does for other entities, and is recorded as a failed attempt. The synchronizer reacts
// to some of them: an authentication error, throttling.
func TestEntityOtherErrorFails(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			test := newEntityTest(testAgent)
			test.execution.client.err = &bleemeo.APIError{StatusCode: status, Err: errTestAPI}

			if _, err := test.run(t, t0); !errors.Is(err, errTestAPI) {
				t.Errorf("err = %v, want %v", err, errTestAPI)
			}

			if sent := test.execution.option.Cache.RegistrationStatusSent(); !sent.LastAttemptFailed {
				t.Errorf("recorded %+v, want a failed attempt", sent)
			}
		})
	}
}

// TestEntityWaitsForFirstRuns checks nothing is sent before discovery ran and the metrics state
// was published: both lists would be empty for lack of a run, as after a restart.
func TestEntityWaitsForFirstRuns(t *testing.T) {
	t.Run("discovery not run", func(t *testing.T) {
		test := newEntityTest(testAgent)
		test.discovery.UpdatedAt = time.Time{}

		if need, err := test.run(t, t0); err != nil || need {
			t.Errorf("need = %v, err = %v; want false, nil", need, err)
		}
	})

	t.Run("metrics state not published", func(t *testing.T) {
		test := newEntityTest(testAgent)
		test.metricsUnpublished = true

		if need, err := test.run(t, t0); err != nil || need {
			t.Errorf("need = %v, err = %v; want false, nil", need, err)
		}

		test.metricsUnpublished = false

		if need, err := test.run(t, t0); err != nil || !need {
			t.Errorf("once published: need = %v, err = %v; want true, nil", need, err)
		}
	})
}

// TestEntityBuildsAtMostOncePerInterval checks the payload is not built on every execution,
// which happen every 15 seconds, but at most once per minNewServiceInterval.
func TestEntityBuildsAtMostOncePerInterval(t *testing.T) {
	test := newEntityTest(testAgent)

	if _, err := test.run(t, t0); err != nil {
		t.Fatal(err)
	}

	for _, at := range []time.Duration{2 * time.Hour, 2*time.Hour + 15*time.Second, 2*time.Hour + 45*time.Second} {
		if _, err := test.run(t, t0.Add(at)); err != nil {
			t.Fatal(err)
		}
	}

	if test.discovery.builds != 2 {
		t.Errorf("built %d times, want 2: once to send, once in the following minute", test.discovery.builds)
	}

	// Discovery is read only when a build is due, not on every execution.
	if test.discovery.latests != test.discovery.builds {
		t.Errorf("read discovery %d times for %d builds", test.discovery.latests, test.discovery.builds)
	}

	if _, err := test.run(t, t0.Add(2*time.Hour+minNewServiceInterval)); err != nil {
		t.Fatal(err)
	}

	if test.discovery.builds != 3 {
		t.Errorf("built %d times, want 3 once minNewServiceInterval passed", test.discovery.builds)
	}
}

// TestEntitySkips checks the cases where nothing is sent.
func TestEntitySkips(t *testing.T) {
	t.Run("only essential", func(t *testing.T) {
		test := newEntityTest(testAgent)
		test.execution.onlyEssential = true

		if need, err := test.run(t, t0); err != nil || need {
			t.Errorf("need = %v, err = %v; want false, nil", need, err)
		}
	})

	t.Run("not registered", func(t *testing.T) {
		test := newEntityTest("")

		if need, err := test.run(t, t0); err != nil || need {
			t.Errorf("need = %v, err = %v; want false, nil", need, err)
		}
	})

	t.Run("requested without need", func(t *testing.T) {
		test := newEntityTest(testAgent)
		ctx := t.Context()

		execution, err := test.entity.PrepareExecution(ctx, test.execution)
		if err != nil {
			t.Fatal(err)
		}

		if err := execution.SyncRemoteAndLocal(ctx, types.SyncTypeForceCacheRefresh); err != nil {
			t.Fatal(err)
		}

		execution.FinishExecution(ctx)

		if sent := len(test.execution.client.bodies); sent != 0 {
			t.Errorf("sent %d times, want none", sent)
		}
	})
}

// TestEntityCountsServiceMetrics checks a service's metrics count only those meant to reach
// Bleemeo: received lately, of this agent, and allowed by Bleemeo's filter. Glouton's own
// metric filter already applied to the store the synchronizer reads.
func TestEntityCountsServiceMetrics(t *testing.T) {
	cases := []struct {
		name    string
		metrics []gloutonTypes.Metric
		// staleBefore is the service's StaleMetricsBefore.
		staleBefore time.Time
		want        MetricsAmount
	}{
		{name: "none", want: MetricsZero},
		{
			name:    "two names, several series",
			metrics: []gloutonTypes.Metric{serviceMetric("mysql_queries", "mysql"), serviceMetric("mysql_threads", "mysql"), serviceMetric("mysql_threads", "mysql")},
			want:    MetricsSome,
		},
		{
			name:    "one allowed, one not in the plan",
			metrics: []gloutonTypes.Metric{serviceMetric("mysql_queries", "mysql"), serviceMetric("mysql_innodb_rows", "mysql")},
			want:    MetricsOne,
		},
		{
			name:    "the service check's status only",
			metrics: []gloutonTypes.Metric{serviceMetric(gloutonTypes.MetricServiceStatus, "mysql")},
			want:    MetricsZero,
		},
		{
			name: "stale",
			metrics: []gloutonTypes.Metric{storeMetric{
				name:        "mysql_queries",
				annotations: gloutonTypes.MetricAnnotations{ServiceName: "mysql"},
				lastPoint:   time.Now().Add(-time.Hour),
			}},
			want: MetricsZero,
		},
		{
			name: "another agent's",
			metrics: []gloutonTypes.Metric{storeMetric{
				name:        "mysql_queries",
				annotations: gloutonTypes.MetricAnnotations{ServiceName: "mysql", BleemeoAgentID: "other-agent"},
				lastPoint:   time.Now(),
			}},
			want: MetricsZero,
		},
		{
			name:    "another service's",
			metrics: []gloutonTypes.Metric{serviceMetric("mysql_queries", "mariadb")},
			want:    MetricsZero,
		},
		{
			// As when the service moved to another address: the earlier input's metrics are
			// still fresh, while the current one gathers nothing.
			name: "from an earlier input",
			metrics: []gloutonTypes.Metric{storeMetric{
				name:        "mysql_queries",
				annotations: gloutonTypes.MetricAnnotations{ServiceName: "mysql"},
				lastPoint:   time.Now().Add(-5 * time.Minute),
			}},
			staleBefore: time.Now().Add(-time.Minute),
			want:        MetricsZero,
		},
		{
			name:        "from the current input",
			metrics:     []gloutonTypes.Metric{serviceMetric("mysql_queries", "mysql")},
			staleBefore: time.Now().Add(-time.Minute),
			want:        MetricsOne,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			test := newEntityTest(testAgent)
			state := mysqlMissingCredentials()
			state.StaleMetricsBefore = tc.staleBefore
			test.discovery.States = []discovery.ServiceState{state}
			test.store.metrics = tc.metrics

			if _, err := test.run(t, t0); err != nil {
				t.Fatal(err)
			}

			var sent struct {
				Services []ServiceState `json:"services"`
			}

			if err := json.Unmarshal([]byte(test.execution.client.bodies[0]), &sent); err != nil {
				t.Fatal(err)
			}

			if got := sent.Services[0].Metrics; got != tc.want {
				t.Errorf("metrics = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEntityMetricsUnknown checks the services are not held back forever when no metrics state
// is published: past metricsStateWait, they are sent with the metrics null, not empty.
func TestEntityMetricsUnknown(t *testing.T) {
	test := newEntityTest(testAgent)
	test.metricsUnpublished = true

	if need, _ := test.run(t, t0); need {
		t.Fatal("sent right away without the metrics state")
	}

	if need, err := test.run(t, t0.Add(metricsStateWait)); err != nil || !need {
		t.Fatalf("after metricsStateWait: need = %v, err = %v; want true, nil", need, err)
	}

	want := `{"glouton_version":"0.1","generated_at":"2026-10-02T12:10:00Z","last_discovery_at":"2026-10-02T11:59:00Z",` +
		`"services":[],"services_total":0,"metrics":null,"metrics_total":null}`
	if bodies := test.execution.client.bodies; len(bodies) != 1 || bodies[0] != want {
		t.Errorf("bodies = %q, want [%q]", bodies, want)
	}

	// Once published, the known state is a different value, sent as such.
	test.metricsUnpublished = false

	if need, err := test.run(t, t0.Add(metricsStateWait+minSendInterval)); err != nil || !need {
		t.Fatalf("once published: need = %v, err = %v; want true, nil", need, err)
	}
}

// TestEntityWaitsForCollections checks nothing is sent while a service's collection has not run
// yet, as right after a start, when its metrics would read as none; but not past collectionWait.
func TestEntityWaitsForCollections(t *testing.T) {
	test := newEntityTest(testAgent)
	pending := mysqlMissingCredentials()
	pending.Error = ""
	pending.CollectionPending = true
	test.discovery.States = []discovery.ServiceState{pending}

	if need, _ := test.run(t, t0); need {
		t.Fatal("sent while a collection has not run")
	}

	// Discovery was first seen ready at t0: past collectionWait, a collection that never runs
	// no longer blocks.
	if need, err := test.run(t, t0.Add(collectionWait)); err != nil || !need {
		t.Fatalf("after collectionWait: need = %v, err = %v; want true, nil", need, err)
	}
}

// TestEntityFeatureFlag checks the registration status is sent only while the account's
// GLOUTON_SEND_REGISTRATION_STATUS config is true: Bleemeo can turn it off, and an account
// without the config, or with a value Glouton does not know, is off. So is one whose
// configuration is not known yet.
func TestEntityFeatureFlag(t *testing.T) {
	cases := []struct {
		name  string
		value any
		// configUnknown leaves the account configuration incomplete: no agent metrics allow-list.
		configUnknown bool
		want          bool
	}{
		{name: "enabled", value: true, want: true},
		{name: "disabled", value: false, want: false},
		{name: "absent", value: nil, want: false},
		{name: "unknown value", value: "partial", want: false},
		{name: "account configuration not known", value: true, configUnknown: true, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			test := newEntityTest(testAgent)

			if tc.configUnknown {
				test.execution.option.Cache.SetConfigs(nil)
			}

			setSendRegistrationStatus(test.execution.option.Cache, tc.value)

			if need, err := test.run(t, t0); err != nil || need != tc.want {
				t.Errorf("need = %v, err = %v; want %v, nil", need, err, tc.want)
			}

			if sent := len(test.execution.client.bodies) > 0; sent != tc.want {
				t.Errorf("sent = %v, want %v", sent, tc.want)
			}
		})
	}
}

// TestEntitySkippedCollection checks a service whose input the registry skips: once registered
// it holds the send like a pending collection, as the skip is retried within minutes; without a
// Service object it does not, and is sent with why its metrics are not gathered.
func TestEntitySkippedCollection(t *testing.T) {
	skipped := mysqlMissingCredentials()
	skipped.Error = ""
	skipped.CollectionSkipped = true

	t.Run("registered", func(t *testing.T) {
		test := newEntityTest(testAgent)
		test.discovery.States = []discovery.ServiceState{skipped}
		test.execution.option.Cache.SetServices([]bleemeoTypes.Service{{ID: "service-uuid", Label: "mysql", Active: true}})

		if need, _ := test.run(t, t0); need {
			t.Error("sent while the registered service's collection is skipped")
		}
	})

	t.Run("not registered", func(t *testing.T) {
		test := newEntityTest(testAgent)
		test.discovery.States = []discovery.ServiceState{skipped}

		if need, err := test.run(t, t0); err != nil || !need {
			t.Fatalf("need = %v, err = %v; want true, nil", need, err)
		}

		if body := test.execution.client.bodies[0]; !strings.Contains(body, msgNotRegistered) {
			t.Errorf("body %s does not say why the metrics are not gathered", body)
		}
	})
}

// TestEntityRecordsSendTime checks a send is recorded when it happens, not when the execution
// started: the delays before the next send run from it.
func TestEntityRecordsSendTime(t *testing.T) {
	test := newEntityTest(testAgent)
	sentAt := t0.Add(40 * time.Second)
	test.entity.now = func() time.Time { return sentAt }

	if _, err := test.run(t, t0); err != nil {
		t.Fatal(err)
	}

	if got := test.execution.option.Cache.RegistrationStatusSent().LastAttemptAt; !got.Equal(sentAt) {
		t.Errorf("recorded the attempt at %v, want %v", got, sentAt)
	}
}

// fakeArchive records the files written to a diagnostic archive.
type fakeArchive struct {
	files   map[string]*bytes.Buffer
	current string
}

func (a *fakeArchive) Create(filename string) (io.Writer, error) {
	if a.files == nil {
		a.files = make(map[string]*bytes.Buffer)
	}

	a.files[filename] = &bytes.Buffer{}
	a.current = filename

	return a.files[filename], nil
}

func (a *fakeArchive) CurrentFileName() string { return a.current }

// archivedStatus is the part of the archived file the test reads.
type archivedStatus struct {
	LastBuilt *struct {
		GeneratedAt time.Time                  `json:"generated_at"`
		Metrics     []bleemeoTypes.MetricState `json:"metrics"`
	} `json:"last_built"`
	LastBuiltAccepted bool `json:"last_built_accepted"`
}

func (e *entityTest) archive(t *testing.T) archivedStatus {
	t.Helper()

	archive := &fakeArchive{}

	if err := e.entity.DiagnosticArchive(archive, e.execution.option); err != nil {
		t.Fatal(err)
	}

	var got archivedStatus

	if err := json.Unmarshal(archive.files["bleemeo-registration-status.json"].Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	return got
}

// TestEntityDiagnosticArchive checks the archive holds a payload built from the current state,
// even while the send delays allow no build: a metric that failed to register since the last
// send is in the archive, not only in the next payload built.
func TestEntityDiagnosticArchive(t *testing.T) {
	test := newEntityTest(testAgent)

	if _, err := test.run(t, t0); err != nil {
		t.Fatal(err)
	}

	if got := test.archive(t); got.LastBuilt == nil || !got.LastBuiltAccepted {
		t.Errorf("after a send, archived %+v, want the payload sent, accepted", got)
	}

	// The metrics synchronization of the same execution records a failure. The next execution,
	// within minNewServiceInterval of the send, builds nothing.
	failed := []bleemeoTypes.MetricState{{Name: "mysql_queries", Reason: bleemeoTypes.ReasonTooManyCustomMetrics, Count: 1}}
	test.metrics = failed
	archivedAt := t0.Add(minNewServiceInterval / 2)

	if need, err := test.run(t, archivedAt); err != nil || need {
		t.Fatalf("within the delay: need = %v, err = %v; want false, nil", need, err)
	}

	got := test.archive(t)
	if got.LastBuilt == nil || got.LastBuiltAccepted || !reflect.DeepEqual(got.LastBuilt.Metrics, failed) {
		t.Errorf("a value no send built yet is archived as %+v, want it with metrics %v, not accepted", got, failed)
	}

	if got.LastBuilt != nil && !got.LastBuilt.GeneratedAt.Equal(archivedAt) {
		t.Errorf("generated_at = %v, want the archive's time %v", got.LastBuilt.GeneratedAt, archivedAt)
	}
}

// TestEntityDiagnosticArchiveIgnoresGates checks the archive builds a payload whatever holds a
// send back, and none when it cannot.
func TestEntityDiagnosticArchiveIgnoresGates(t *testing.T) {
	t.Run("before any run", func(t *testing.T) {
		test := newEntityTest(testAgent)
		test.discovery.UpdatedAt = time.Time{}
		test.metricsUnpublished = true

		got := test.archive(t)
		if got.LastBuilt == nil || got.LastBuiltAccepted || got.LastBuilt.Metrics != nil {
			t.Errorf("archived %+v, want a payload with metrics unknown, not accepted", got)
		}
	})

	t.Run("feature off", func(t *testing.T) {
		test := newEntityTest(testAgent)
		setSendRegistrationStatus(test.execution.option.Cache, false)

		if got := test.archive(t); got.LastBuilt == nil {
			t.Errorf("archived %+v, want a payload", got)
		}
	})

	t.Run("not registered", func(t *testing.T) {
		test := newEntityTest("")

		if got := test.archive(t); got.LastBuilt != nil {
			t.Errorf("archived %+v, want no payload", got)
		}
	})

	t.Run("account configuration not known", func(t *testing.T) {
		test := newEntityTest(testAgent)
		test.execution.option.Cache.SetConfigs(nil)

		if got := test.archive(t); got.LastBuilt != nil {
			t.Errorf("archived %+v, want no payload: every service would read as sending no metric", got)
		}
	})

	t.Run("store error", func(t *testing.T) {
		test := newEntityTest(testAgent)
		test.store.err = errTestAPI

		if got := test.archive(t); got.LastBuilt != nil {
			t.Errorf("archived %+v, want no payload", got)
		}
	})
}
