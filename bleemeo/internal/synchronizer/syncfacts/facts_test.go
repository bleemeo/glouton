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

package syncfacts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/executiontest"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/facts"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

const testAgentID = "agent-id"

var (
	errRegister  = errors.New("register failed")
	errDuplicate = errors.New("duplicated agent")
)

// fakeClient keeps the facts registered on the API, and records the calls. RegisterFact fails
// for the keys of failOn.
type fakeClient struct {
	types.Client

	facts  []bleemeoTypes.AgentFact
	nextID int
	failOn map[string]bool

	registered []string
	deleted    []string
}

func (c *fakeClient) ListFacts(context.Context) ([]bleemeoTypes.AgentFact, error) {
	return slices.Clone(c.facts), nil
}

func (c *fakeClient) RegisterFact(_ context.Context, payload bleemeoTypes.AgentFact) (bleemeoTypes.AgentFact, error) {
	if c.failOn[payload.Key] {
		return bleemeoTypes.AgentFact{}, errRegister
	}

	c.nextID++
	payload.ID = fmt.Sprintf("new-%d", c.nextID)
	c.facts = append(c.facts, payload)
	c.registered = append(c.registered, payload.Key+"="+payload.Value)

	return payload, nil
}

func (c *fakeClient) DeleteFact(_ context.Context, id string) error {
	c.facts = slices.DeleteFunc(c.facts, func(f bleemeoTypes.AgentFact) bool { return f.ID == id })
	c.deleted = append(c.deleted, id)

	return nil
}

type fakeState struct {
	bleemeoTypes.State
}

func (fakeState) BleemeoCredentials() (string, string) { return testAgentID, "" }

// fakeExecution gives the local facts, the cache, the client and the last successful
// synchronizations to the entity. Any other call panics.
type fakeExecution struct {
	executiontest.Recorder

	client        *fakeClient
	cache         *cache.Cache
	facts         *facts.FactProviderMock
	onlyEssential bool
	lastSync      map[types.EntityName]time.Time
}

func newFakeExecution(local map[string]string, remote ...bleemeoTypes.AgentFact) *fakeExecution {
	c := &cache.Cache{}
	c.SetFacts(remote)
	// Without the SNMP agent type, the facts of the other agents can't be found.
	c.SetAgentTypes([]bleemeoTypes.AgentType{{ID: "snmp-type", Name: bleemeo.AgentType_SNMP}})

	return &fakeExecution{
		client: &fakeClient{facts: slices.Clone(remote)},
		cache:  c,
		facts:  facts.NewMockFacter(local),
	}
}

func (e *fakeExecution) BleemeoAPIClient() types.Client { return e.client }
func (e *fakeExecution) IsOnlyEssential() bool          { return e.onlyEssential }
func (e *fakeExecution) StartedAt() time.Time           { return time.Time{} }

func (e *fakeExecution) LastSuccessfulSync(entityName types.EntityName) time.Time {
	return e.lastSync[entityName]
}

func (e *fakeExecution) Option() types.Option {
	return types.Option{
		Facts:          e.facts,
		State:          fakeState{},
		VSphereDevices: func(context.Context, time.Duration) []bleemeoTypes.VSphereDevice { return nil },
		Cache:          e.cache,
	}
}

// newEntity returns the entity, with a duplicated agent check which returns checkErr. The cache
// of the fake execution already has the facts of the API, as the check leaves it.
func newEntity(checkErr error) *SyncFacts {
	return New(nil, nil, func(context.Context, types.Client, time.Time) error {
		return checkErr
	})
}

// memoryArchive keeps the content of the last file created.
type memoryArchive struct {
	bytes.Buffer
}

func (a *memoryArchive) Create(string) (io.Writer, error) {
	a.Reset()

	return a, nil
}

func (a *memoryArchive) CurrentFileName() string { return "" }

func archiveContent(t *testing.T, s *SyncFacts) string {
	t.Helper()

	archive := &memoryArchive{}

	if err := s.DiagnosticArchive(t.Context(), archive); err != nil {
		t.Fatal(err)
	}

	return archive.String()
}

func fact(id, key, value string) bleemeoTypes.AgentFact {
	return bleemeoTypes.AgentFact{ID: id, AgentID: testAgentID, Key: key, Value: value}
}

func syncRemote(t *testing.T, s *SyncFacts, execution *fakeExecution) error {
	t.Helper()

	e, err := s.PrepareExecution(t.Context(), execution)
	if err != nil {
		t.Fatal(err)
	}

	defer e.FinishExecution(t.Context())

	return e.SyncRemoteAndLocal(t.Context(), types.SyncTypeNormal)
}

// factValues returns the facts as sorted key=value strings.
func factValues(list []bleemeoTypes.AgentFact) []string {
	result := make([]string, 0, len(list))

	for _, f := range list {
		result = append(result, f.Key+"="+f.Value)
	}

	slices.Sort(result)

	return result
}

// TestSyncRegistersChangedFacts checks only the changed facts are registered, and the facts with
// an old value deleted, on the API and in the cache.
func TestSyncRegistersChangedFacts(t *testing.T) {
	t.Parallel()

	execution := newFakeExecution(
		map[string]string{FactFQDN: "host.example.com", factKernel: "6.1", facts.FactUpdatedAt: "t1"},
		fact("1", FactFQDN, "host.example.com"),
		fact("2", factKernel, "6.0"),
		fact("3", "gone", "value"),
	)

	if err := syncRemote(t, newEntity(nil), execution); err != nil {
		t.Fatal(err)
	}

	sortStrings := cmpopts.SortSlices(func(a, b string) bool { return a < b })

	if diff := cmp.Diff([]string{factKernel + "=6.1", facts.FactUpdatedAt + "=t1"}, execution.client.registered, sortStrings); diff != "" {
		t.Errorf("registered facts mismatch (-want +got)\n%s", diff)
	}

	if diff := cmp.Diff([]string{"2", "3"}, execution.client.deleted, sortStrings); diff != "" {
		t.Errorf("deleted facts mismatch (-want +got)\n%s", diff)
	}

	want := []string{facts.FactUpdatedAt + "=t1", FactFQDN + "=host.example.com", factKernel + "=6.1"}
	slices.Sort(want)

	if diff := cmp.Diff(want, factValues(execution.cache.Facts())); diff != "" {
		t.Errorf("cached facts mismatch (-want +got)\n%s", diff)
	}
}

// TestDuplicatedFactsDeleted checks only one fact is kept for a key registered more than once:
// the one with the local value, else the one with the smallest ID.
func TestDuplicatedFactsDeleted(t *testing.T) {
	t.Parallel()

	execution := newFakeExecution(
		map[string]string{FactFQDN: "host.example.com", factKernel: "6.1"},
		fact("1", FactFQDN, "old.example.com"),
		fact("2", FactFQDN, "host.example.com"),
		fact("3", FactFQDN, "host.example.com"),
		fact("4", factKernel, "6.1"),
	)

	if err := syncRemote(t, newEntity(nil), execution); err != nil {
		t.Fatal(err)
	}

	if len(execution.client.registered) != 0 {
		t.Errorf("registered %v, want nothing", execution.client.registered)
	}

	if diff := cmp.Diff([]string{"2", "4"}, factIDs(execution.cache.Facts())); diff != "" {
		t.Errorf("cached facts mismatch (-want +got)\n%s", diff)
	}
}

func factIDs(list []bleemeoTypes.AgentFact) []string {
	result := make([]string, 0, len(list))

	for _, f := range list {
		result = append(result, f.ID)
	}

	slices.Sort(result)

	return result
}

// TestRegisterErrorKeepsRegistered checks the facts registered before a registration error stay
// in the cache: without them, the next duplicated agent check would see facts it doesn't know
// on the API, and report a duplicated agent.
func TestRegisterErrorKeepsRegistered(t *testing.T) {
	t.Parallel()

	execution := newFakeExecution(map[string]string{"a": "1", "b": "2", "c": "3", facts.FactUpdatedAt: "t1"})
	execution.client.failOn = map[string]bool{"b": true}

	s := newEntity(nil)

	if err := syncRemote(t, s, execution); !errors.Is(err, errRegister) {
		t.Fatalf("SyncRemoteAndLocal() = %v, want %v", err, errRegister)
	}

	if diff := cmp.Diff(factValues(execution.client.facts), factValues(execution.cache.Facts())); diff != "" {
		t.Errorf("cached facts aren't the registered ones (-api +cache)\n%s", diff)
	}

	needSync(t, s, execution, true)
}

// TestOnlyEssential checks only the essential facts are registered, nothing is deleted, and the
// facts are synchronized again on the next execution.
func TestOnlyEssential(t *testing.T) {
	t.Parallel()

	execution := newFakeExecution(
		map[string]string{FactFQDN: "host.example.com", "cpu_cores": "4", facts.FactUpdatedAt: "t1"},
		fact("1", "memory", "16 GB"),
	)
	execution.onlyEssential = true

	s := newEntity(nil)

	if err := syncRemote(t, s, execution); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{FactFQDN + "=host.example.com"}, execution.client.registered); diff != "" {
		t.Errorf("registered facts mismatch (-want +got)\n%s", diff)
	}

	if len(execution.client.deleted) != 0 {
		t.Errorf("deleted %v, want nothing", execution.client.deleted)
	}

	needSync(t, s, execution, true)
}

// TestDuplicatedAgentStopsSync checks nothing is written when the duplicated agent check fails.
func TestDuplicatedAgentStopsSync(t *testing.T) {
	t.Parallel()

	execution := newFakeExecution(map[string]string{FactFQDN: "host.example.com"}, fact("1", "gone", "value"))

	if err := syncRemote(t, newEntity(errDuplicate), execution); !errors.Is(err, errDuplicate) {
		t.Fatalf("SyncRemoteAndLocal() = %v, want %v", err, errDuplicate)
	}

	if len(execution.client.registered) != 0 || len(execution.client.deleted) != 0 {
		t.Errorf("registered %v and deleted %v, want nothing", execution.client.registered, execution.client.deleted)
	}
}

func needSync(t *testing.T, s *SyncFacts, execution *fakeExecution, want bool) {
	t.Helper()

	e, err := s.PrepareExecution(t.Context(), execution)
	if err != nil {
		t.Fatal(err)
	}

	defer e.FinishExecution(t.Context())

	need, err := e.NeedSynchronization(t.Context())
	if err != nil || need != want {
		t.Errorf("NeedSynchronization() = %v, %v, want %v", need, err, want)
	}
}

// TestNeedSynchronization checks the facts are synchronized when fact_updated_at changes, and
// after the SNMP agents synchronized.
func TestNeedSynchronization(t *testing.T) {
	t.Parallel()

	s := newEntity(nil)
	execution := newFakeExecution(map[string]string{FactFQDN: "host.example.com"})

	// No fact_updated_at yet: nothing to synchronize.
	needSync(t, s, execution, false)

	execution.facts.SetFact(facts.FactUpdatedAt, "t1")
	needSync(t, s, execution, true)

	if err := syncRemote(t, s, execution); err != nil {
		t.Fatal(err)
	}

	needSync(t, s, execution, false)

	// Another fact changing alone doesn't trigger a synchronization.
	execution.facts.SetFact(FactFQDN, "renamed.example.com")
	needSync(t, s, execution, false)

	// The SNMP agents synchronized since the facts did.
	execution.lastSync = map[types.EntityName]time.Time{types.EntitySNMP: time.Unix(2, 0), types.EntityFact: time.Unix(1, 0)}
	needSync(t, s, execution, true)

	execution.lastSync[types.EntityFact] = execution.lastSync[types.EntitySNMP]
	needSync(t, s, execution, false)

	execution.facts.SetFact(facts.FactUpdatedAt, "t2")
	needSync(t, s, execution, true)

	if !strings.Contains(archiveContent(t, s), `"LastFactUpdatedAt": "t1"`) {
		t.Errorf("the archive doesn't have the last fact_updated_at synchronized:\n%s", archiveContent(t, s))
	}
}
