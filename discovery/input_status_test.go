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

package discovery

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	"github.com/prometheus/prometheus/util/gate"
)

// closedPorts are the ports closedPort handed out, which listenLocal does not reuse: the tests run
// in parallel, and a listener on one would make another test's closed port answer.
var (
	closedPortsL sync.Mutex           //nolint:gochecknoglobals
	closedPorts  = make(map[int]bool) //nolint:gochecknoglobals
)

// closedPort returns a local TCP port nothing listens on.
func closedPort(t *testing.T) int {
	t.Helper()

	closedPortsL.Lock()
	defer closedPortsL.Unlock()

	listener := listenFree(t)
	port := listenerPort(t, listener)

	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	closedPorts[port] = true

	return port
}

// listenLocal listens on a free local TCP port, never one closedPort handed out.
func listenLocal(t *testing.T) net.Listener {
	t.Helper()

	closedPortsL.Lock()
	defer closedPortsL.Unlock()

	return listenFree(t)
}

// listenFree listens on a free local TCP port not in closedPorts. Must be called with
// closedPortsL held.
func listenFree(t *testing.T) net.Listener {
	t.Helper()

	var held []net.Listener

	// Ports in closedPorts are kept open until a free one is found, so that the system does
	// not hand them out again.
	defer func() {
		for _, l := range held {
			_ = l.Close()
		}
	}()

	for {
		listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}

		if !closedPorts[listenerPort(t, listener)] {
			return listener
		}

		held = append(held, listener)
	}
}

func listenerPort(t *testing.T, listener net.Listener) int {
	t.Helper()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %v is not TCP", listener.Addr())
	}

	return addr.Port
}

// collectFromClosedPort builds and registers the real input of serviceType, configured with
// credentials and a closed port, runs one collection and returns the service's reported state.
func collectFromClosedPort(t *testing.T, serviceType ServiceName) ServiceState {
	t.Helper()

	return collectFrom(t, serviceType, closedPort(t), "password")
}

// collectFrom builds and registers the real input of serviceType for 127.0.0.1:port, runs one
// collection and returns the service's reported state.
func collectFrom(t *testing.T, serviceType ServiceName, port int, password string) ServiceState {
	t.Helper()

	// As the agent sets it up: inputs with a password wait for their turn on this gate.
	return collectWith(t, registry.Option{SecretInputsGate: gate.New(inputs.MaxParallelSecrets())}, serviceType, port, password)
}

// statsURLRequired lists the types whose input is only built from a stats URL.
var statsURLRequired = map[ServiceName]bool{HAProxyService: true, JenkinsService: true} //nolint:gochecknoglobals

// collectWith is collectFrom with a registry built from option. An input that cannot be built,
// as MongoDB's that connects when it is built, is reported without a collection.
func collectWith(t *testing.T, option registry.Option, serviceType ServiceName, port int, password string) ServiceState {
	t.Helper()

	key := NameInstance{Name: string(serviceType)}
	service := Service{
		Name:        string(serviceType),
		ServiceType: serviceType,
		Active:      true,
		IPAddress:   "127.0.0.1",
		Config: config.Service{
			Address:   "127.0.0.1",
			Port:      port,
			StatsPort: port,
			Username:  "user",
			Password:  password,
		},
	}

	if statsURLRequired[serviceType] {
		service.Config.StatsURL = "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + "/"
	}

	reg, err := registry.New(option)
	if err != nil {
		t.Fatal(err)
	}

	d := newTestDiscovery()
	d.metricRegistry = reg
	d.servicesMap = map[NameInstance]Service{key: service}
	d.discoveredServicesMap = map[NameInstance]Service{key: service}

	outcome, err := d.createInput(service)
	d.inputResult[key] = newInputResult(outcome, err)

	if outcome == inputRegistered {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		d.activeCollector[key].gathererRegistration.InternalRunScrape(ctx, ctx, time.Now())
	}

	got := servicesStateOf(d)
	if len(got) != 1 {
		t.Fatalf("got %d services, want 1", len(got))
	}

	return got[0]
}

// TestUnreachableDetected proves, for the input of every service type that talks to its service,
// that a refused connection reads as unreachable: otherwise a stopped daemon would read as
// answered_error, which Bleemeo takes for a service needing configuration. A new input, or a
// Telegraf upgrade that words its errors otherwise, fails here. Elasticsearch's input keeps only
// the errors' text, and is covered by Go's wording of them. An input that connects when built
// fails to build instead, which does not read as answered either.
func TestUnreachableDetected(t *testing.T) {
	for serviceType := range inputBuilders {
		if inputsWithoutNetwork[serviceType] {
			continue
		}

		t.Run(string(serviceType), func(t *testing.T) {
			t.Parallel()

			got := collectFromClosedPort(t, serviceType)

			// MongoDB's input connects when it is built: it fails to build instead.
			if got.InputStatus == InputStatusCreateFailed {
				return
			}

			if got.Error == "" || got.InputStatus != InputStatusUnreachable {
				t.Errorf("collecting from a closed port: got %+v, want an error with input status %q", got, InputStatusUnreachable)
			}
		})
	}
}

// ran is the state of a collection that ran with an error of this kind.
func ran(message string, kind types.ScrapeErrorKind) types.ScrapeState {
	return types.ScrapeState{Status: types.ScrapeRan, Error: message, ErrorKind: kind}
}

// TestScrapeInputStatus checks the status of a collection that ran: an error says whether the
// service answered only when the error tells it, or the input is known to keep network errors.
func TestScrapeInputStatus(t *testing.T) {
	cases := []struct {
		name       string
		scrape     types.ScrapeState
		classified bool
		want       InputStatus
	}{
		{name: "no error", scrape: types.ScrapeState{Status: types.ScrapeRan}, want: InputStatusOK},
		{name: "did not answer", scrape: ran("refused", types.ScrapeErrorUnreachable), want: InputStatusUnreachable},
		{name: "did not answer, classified", scrape: ran("refused", types.ScrapeErrorUnreachable), classified: true, want: InputStatusUnreachable},
		{name: "answered, classified", scrape: ran("NOAUTH", types.ScrapeErrorGatherer), classified: true, want: InputStatusAnsweredError},
		{name: "answered, unclassified", scrape: ran("NOAUTH", types.ScrapeErrorGatherer), want: InputStatusError},
		{name: "Glouton failed", scrape: ran("no secrets gate", types.ScrapeErrorInternal), want: InputStatusInternalError},
		{name: "Glouton failed, classified", scrape: ran("no secrets gate", types.ScrapeErrorInternal), classified: true, want: InputStatusInternalError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scrapeInputStatus(tc.scrape, tc.classified); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// fakeRedisNoAuth listens as a Redis server requiring a password that was not given: it answers
// every command with NOAUTH. It returns its port.
func fakeRedisNoAuth(t *testing.T) int {
	t.Helper()

	listener := listenLocal(t)

	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go func() {
				defer conn.Close()

				reader := bufio.NewReader(conn)

				for {
					// Each command is an array; answering on its first line is enough for one
					// command at a time.
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}

					if line[0] != '*' {
						continue
					}

					if _, err := conn.Write([]byte("-NOAUTH Authentication required.\r\n")); err != nil {
						return
					}
				}
			}()
		}
	}()

	return listenerPort(t, listener)
}

// TestRedisNoAuthAnswered checks the case Bleemeo needs to flag: a Redis requiring a password
// Glouton does not have answers, so its error reads as answered_error, not as a stopped daemon.
func TestRedisNoAuthAnswered(t *testing.T) {
	got := collectFrom(t, RedisService, fakeRedisNoAuth(t), "")

	if got.InputStatus != InputStatusAnsweredError || !strings.Contains(got.Error, "NOAUTH") {
		t.Errorf("got %+v, want the NOAUTH error with input status %q", got, InputStatusAnsweredError)
	}
}

// TestServicesStateInputStatus checks the input status of each way a service can be reported,
// so that Bleemeo never has to read the error text.
func TestServicesStateInputStatus(t *testing.T) {
	outcomes := map[inputOutcome]InputStatus{
		inputContainerStopped:    InputStatusContainerStopped,
		inputIgnored:             InputStatusIgnored,
		inputCheckOnly:           InputStatusCheckOnly,
		inputUnsupportedType:     InputStatusUnsupportedType,
		inputUnsupportedPlatform: InputStatusUnsupportedPlatform,
		inputMissingInfo:         InputStatusMissingInfo,
		inputMissingCommand:      InputStatusMissingCommand,
		inputCreateFailed:        InputStatusCreateFailed,
	}

	for outcome := range outcomeMessages() {
		if _, ok := outcomes[outcome]; !ok && outcome != inputInactive {
			t.Errorf("outcome %d has no expected input status", outcome)
		}
	}

	for outcome, want := range outcomes {
		d := newTestDiscovery()
		d.inputResult[apacheKey()] = newInputResult(outcome, nil)

		if got := servicesStateOf(d)[0].InputStatus; got != want {
			t.Errorf("outcome %d: got %q, want %q", outcome, got, want)
		}
	}

	t.Run("discovery has not decided yet", func(t *testing.T) {
		if got := servicesStateOf(newTestDiscovery())[0].InputStatus; got != InputStatusPending {
			t.Errorf("got %q, want %q", got, InputStatusPending)
		}
	})

	t.Run("registered input", func(t *testing.T) {
		cases := []struct {
			name         string
			registration types.Registration
			want         InputStatus
		}{
			{name: "not run yet", registration: fakeScrapeRegistration{}, want: InputStatusPending},
			{name: "skipped", registration: fakeScrapeRegistration{state: types.ScrapeState{Status: types.ScrapeSkipped}}, want: InputStatusPending},
			{name: "working", registration: fakeScrapeRegistration{state: types.ScrapeState{Status: types.ScrapeRan}}, want: InputStatusOK},
			// An error from the gatherer of an input talking to the service means it answered.
			{name: "answered", registration: fakeScrapeRegistration{state: ran("401", types.ScrapeErrorGatherer)}, want: InputStatusAnsweredError},
			{name: "state unreadable", registration: bareRegistration{}, want: InputStatusUnknown},
		}

		for _, tc := range cases {
			d := newTestDiscovery()
			d.inputResult[apacheKey()] = newInputResult(inputRegistered, nil)
			d.activeCollector[apacheKey()] = collectorDetails{gathererRegistration: tc.registration}

			if got := servicesStateOf(d)[0].InputStatus; got != tc.want {
				t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
			}
		}
	})

	t.Run("input without network", func(t *testing.T) {
		for serviceType := range inputsWithoutNetwork {
			key := NameInstance{Name: string(serviceType)}

			d := newTestDiscovery()
			d.servicesMap = map[NameInstance]Service{key: {Name: string(serviceType), ServiceType: serviceType, Active: true}}
			d.inputResult[key] = newInputResult(inputRegistered, nil)
			d.activeCollector[key] = collectorDetails{
				gathererRegistration: fakeScrapeRegistration{state: ran("exit status 1", types.ScrapeErrorGatherer)},
			}

			if got := servicesStateOf(d)[0].InputStatus; got != InputStatusError {
				t.Errorf("%s: got %q, want %q: its error cannot tell whether the service answered", serviceType, got, InputStatusError)
			}
		}
	})

	t.Run("JMX", func(t *testing.T) {
		cases := []struct {
			name      string
			jmxPort   int
			ipAddress string
			jmxState  JMXStateProvider
			want      InputStatus
		}{
			{name: "JMX disabled", jmxPort: 7199, ipAddress: "127.0.0.1", want: InputStatusJMXDisabled},
			{name: "no jmx_port", ipAddress: "127.0.0.1", jmxState: fakeJMXState{}, want: InputStatusJMXNoPort},
			{name: "no address", jmxPort: 7199, jmxState: fakeJMXState{}, want: InputStatusJMXNoAddress},
			// JMX errors are never classified.
			{name: "failing", jmxPort: 7199, ipAddress: "127.0.0.1", jmxState: fakeJMXState{state: ran(errMsgFromJMX, types.ScrapeErrorGatherer)}, want: InputStatusError},
		}

		for _, tc := range cases {
			key := NameInstance{Name: "cassandra"}
			service := Service{
				Name: "cassandra", ServiceType: CassandraService, Active: true,
				IPAddress: tc.ipAddress, Config: config.Service{JMXPort: tc.jmxPort},
			}

			d := newTestDiscovery()
			d.servicesMap = map[NameInstance]Service{key: service}
			d.inputResult[key] = newInputResult(inputUnsupportedType, nil)

			if tc.jmxState != nil {
				d.SetJMXState(tc.jmxState)
			}

			if got := servicesStateOf(d)[0].InputStatus; got != tc.want {
				t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
			}
		}
	})

	t.Run("agent gatherer", func(t *testing.T) {
		key := NameInstance{Name: "postfix"}

		d := newTestDiscovery()
		d.servicesMap = map[NameInstance]Service{key: {Name: "postfix", ServiceType: PostfixService, Active: true}}
		d.inputResult[key] = newInputResult(inputUnsupportedType, nil)
		d.SetAgentGathererState(fakeGathererState{state: ran("exit status 69", types.ScrapeErrorGatherer)})

		if got := servicesStateOf(d)[0].InputStatus; got != InputStatusError {
			t.Errorf("got %q, want %q: the agent's gatherers are not classified", got, InputStatusError)
		}
	})
}

// TestInternalErrorNotAnswered checks Glouton failing before it reaches the service, here for
// want of a secrets gate, reads as Glouton's failure, not as the service having answered (its
// input is classified) nor as an error the service may be the cause of.
func TestInternalErrorNotAnswered(t *testing.T) {
	got := collectWith(t, registry.Option{}, MySQLService, closedPort(t), "password")

	if got.Error == "" || got.InputStatus != InputStatusInternalError {
		t.Errorf("got %+v, want an error with input status %q", got, InputStatusInternalError)
	}
}
