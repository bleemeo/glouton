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

package jmxtrans

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
)

func jmxService() discovery.Service {
	return discovery.Service{
		Name:        "cassandra",
		ServiceType: discovery.CassandraService,
		Active:      true,
		IPAddress:   "127.0.0.1",
		Config:      config.Service{JMXPort: 7199},
	}
}

func jmxPoint(name string) types.MetricPoint {
	return types.MetricPoint{
		Labels:      map[string]string{types.LabelName: name},
		Annotations: types.MetricAnnotations{ServiceName: "cassandra"},
	}
}

// TestCollectionState covers the state reported for a service collected through JMX.
func TestCollectionState(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	key := discovery.NameInstance{Name: "cassandra"}

	state := &collectionState{}
	state.now = func() time.Time { return now }

	if got := state.ServiceScrapeState(key); got.Status != types.ScrapeNotRun {
		t.Errorf("a service not given to jmxtrans has a state: %+v", got)
	}

	state.setServices([]discovery.Service{jmxService()})

	configuredAt := now
	now = now.Add(time.Minute)

	if got := state.ServiceScrapeState(key); got.Status != types.ScrapeNotRun {
		t.Errorf("a service configured a minute ago reports %+v, want nothing yet", got)
	}

	state.connected()
	state.pointReceived(jmxPoint("cassandra_read_requests"))
	state.pointReceived(jmxPoint("cassandra_write_requests"))
	state.pointReceived(jmxPoint("cassandra_write_requests"))

	if got := state.ServiceScrapeState(key); got != (types.ScrapeState{Status: types.ScrapeRan, RegisteredAt: configuredAt}) {
		t.Errorf("after points, got %+v, want a run without error", got)
	}

	// Nothing for a whole window, jmxtrans still connected.
	now = now.Add(stateWindow)

	if got := state.ServiceScrapeState(key); got.Error != errMsgNoMetrics {
		t.Errorf("with nothing received for a window, got %+v, want %q", got, errMsgNoMetrics)
	}

	// jmxtrans gone for a whole window.
	state.disconnected()

	now = now.Add(stateWindow)

	if got := state.ServiceScrapeState(key); got.Error != errMsgNotConnected {
		t.Errorf("with jmxtrans disconnected for a window, got %+v, want %q", got, errMsgNotConnected)
	}

	// A service no longer given to jmxtrans is forgotten.
	state.setServices(nil)

	if got := state.ServiceScrapeState(key); got.Status != types.ScrapeNotRun {
		t.Errorf("a service removed from jmxtrans still has a state: %+v", got)
	}
}

// TestCollectionStateNewTarget checks a service given to jmxtrans with another target, as when
// its address changed, starts over: what was received came from the previous target, and its
// metrics in the store are older than the new RegisteredAt.
func TestCollectionStateNewTarget(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	key := discovery.NameInstance{Name: "cassandra"}

	state := &collectionState{}
	state.now = func() time.Time { return now }

	state.setServices([]discovery.Service{jmxService()})
	state.connected()
	state.pointReceived(jmxPoint("cassandra_read_requests"))

	// The same target again changes nothing.
	now = now.Add(time.Minute)

	state.setServices([]discovery.Service{jmxService()})

	if got := state.ServiceScrapeState(key); got.Status != types.ScrapeRan || got.Error != "" {
		t.Fatalf("with the same target, got %+v, want a run without error", got)
	}

	moved := jmxService()
	moved.IPAddress = "127.0.0.2"

	now = now.Add(time.Minute)

	state.setServices([]discovery.Service{moved})

	movedAt := now

	if got := state.ServiceScrapeState(key); got.Status != types.ScrapeNotRun {
		t.Errorf("right after the target changed, got %+v, want nothing yet", got)
	}

	now = now.Add(stateWindow)

	want := types.ScrapeState{Status: types.ScrapeRan, Error: errMsgNoMetrics, RegisteredAt: movedAt}
	if got := state.ServiceScrapeState(key); got != want {
		t.Errorf("with nothing from the new target for a window, got %+v, want %+v", got, want)
	}
}

// TestCollectionStateIgnoresUnknownServices checks a point for a service jmxtrans was not
// given does not create a state.
func TestCollectionStateIgnoresUnknownServices(t *testing.T) {
	state := &collectionState{}
	state.pointReceived(jmxPoint("cassandra_read_requests"))

	if len(state.receivedAt) != 0 {
		t.Errorf("a point for an unknown service was recorded: %v", state.receivedAt)
	}
}

// TestMetricsIgnoredNotCollected checks a service whose metrics are disabled by the
// configuration gets no JMX metrics, so jmxtrans is not configured for it, and is not followed
// by the collection state.
func TestMetricsIgnoredNotCollected(t *testing.T) {
	service := jmxService()
	service.MetricsIgnored = true

	if metrics := GetJMXMetrics(service); len(metrics) != 0 {
		t.Errorf("GetJMXMetrics() returned %d metrics for a service with metrics disabled", len(metrics))
	}

	var state collectionState

	state.setServices([]discovery.Service{service})
	state.pointReceived(jmxPoint("cassandra_read_requests"))

	if got := state.ServiceScrapeState(discovery.NameInstance{Name: service.Name, Instance: service.Instance}); got != (types.ScrapeState{}) {
		t.Errorf("ServiceScrapeState() = %+v, want nothing followed", got)
	}
}

// TestJMXServiceTypesMatchDiscovery checks discovery's list of the service types collected
// only through JMX is the list of types jmxtrans has metrics for, both ways. The lists are
// read from the source: discovery cannot import this package, and its list is unexported.
func TestJMXServiceTypesMatchDiscovery(t *testing.T) {
	jmxtrans := mapKeys(t, "jmx_metrics.go", "defaultServiceMetrics")
	discovered := mapKeys(t, "../discovery/services_state.go", "jmxServiceTypes")

	if diff := cmp.Diff(jmxtrans, discovered); diff != "" {
		t.Errorf("discovery's jmxServiceTypes is out of sync with defaultServiceMetrics (-jmxtrans +discovery):\n%s", diff)
	}
}

// mapKeys returns the names of the keys of a map variable declared in a Go file, keys being
// identifiers such as CassandraService or discovery.CassandraService.
func mapKeys(t *testing.T, path string, variable string) []string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var keys []string

	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != variable || len(spec.Values) != 1 {
			return true
		}

		literal, ok := spec.Values[0].(*ast.CompositeLit)
		if !ok {
			t.Fatalf("%s in %s is not a composite literal", variable, path)
		}

		for _, element := range literal.Elts {
			switch key := element.(*ast.KeyValueExpr).Key.(type) {
			case *ast.Ident:
				keys = append(keys, key.Name)
			case *ast.SelectorExpr:
				keys = append(keys, key.Sel.Name)
			default:
				t.Fatalf("unexpected key %T in %s", key, variable)
			}
		}

		return false
	})

	if len(keys) == 0 {
		t.Fatalf("no keys found for %s in %s", variable, path)
	}

	slices.Sort(keys)

	return keys
}

// TestJMXFeedsCollectionState checks the JMX hooks feed the collection state: services
// given to jmxtrans, points it sends, and its connections.
func TestJMXFeedsCollectionState(t *testing.T) {
	j := &JMX{}
	key := discovery.NameInstance{Name: "cassandra"}

	if err := j.UpdateConfig([]discovery.Service{jmxService()}, 10*time.Second); err != nil {
		t.Fatal(err)
	}

	j.emitPoint(t.Context(), jmxPoint("cassandra_read_requests"))

	if got := j.ServiceScrapeState(key); got.Status != types.ScrapeRan || got.Error != "" {
		t.Errorf("after a point, got %+v, want a run without error", got)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// The test holds the listener, on a port the system picked.
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer listener.Close()

	tcpListener, ok := listener.(*net.TCPListener)
	if !ok {
		t.Fatalf("unexpected listener %T", listener)
	}

	addr := listener.Addr()
	serverDone := make(chan error, 1)

	go func() { serverDone <- j.serve(ctx, tcpListener) }()

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr.String())
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, func() bool { return connections(j) == 1 }, "the connection to be counted")

	_ = conn.Close()

	waitFor(t, func() bool { return connections(j) == 0 }, "the disconnection to be counted")

	cancel()

	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func connections(j *JMX) int {
	j.state.l.Lock()
	defer j.state.l.Unlock()

	return j.state.connections
}

func waitFor(t *testing.T, condition func() bool, what string) {
	t.Helper()

	for deadline := time.Now().Add(5 * time.Second); !condition(); {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// TestCollectionStateConcurrent feeds and reads the collection state at once, as jmxtrans'
// connections, the agent and discovery do, for the race detector.
func TestCollectionStateConcurrent(t *testing.T) {
	j := &JMX{}
	key := discovery.NameInstance{Name: "cassandra"}

	var wg sync.WaitGroup

	wg.Go(func() {
		for range 200 {
			_ = j.UpdateConfig([]discovery.Service{jmxService()}, 10*time.Second)
		}
	})

	wg.Go(func() {
		for range 200 {
			j.emitPoint(t.Context(), jmxPoint("cassandra_read_requests"))
		}
	})

	for range 200 {
		_ = j.ServiceScrapeState(key)
	}

	wg.Wait()
}
