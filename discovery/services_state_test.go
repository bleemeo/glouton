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
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
)

// serviceNameConstants parses the ServiceName constants, returning identifier name -> value.
// inputSupportedServiceTypes is keyed by value, while the AST gives us identifier names.
func serviceNameConstants(t *testing.T) map[string]ServiceName {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), "common.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing common.go: %v", err)
	}

	result := make(map[string]ServiceName)

	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			continue
		}

		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok || len(valueSpec.Names) != 1 || len(valueSpec.Values) != 1 {
				continue
			}

			if ident, ok := valueSpec.Type.(*ast.Ident); !ok || ident.Name != "ServiceName" {
				continue
			}

			lit, ok := valueSpec.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}

			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("unquoting %s: %v", lit.Value, err)
			}

			result[valueSpec.Names[0].Name] = ServiceName(value)
		}
	}

	if len(result) == 0 {
		t.Fatal("no ServiceName constant found in common.go")
	}

	return result
}

// createInputCaseNames returns the identifier names used as cases of the
// "switch service.ServiceType" in createInput.
func createInputCaseNames(t *testing.T) []string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), "metrics.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing metrics.go: %v", err)
	}

	var names []string

	found := false

	ast.Inspect(file, func(node ast.Node) bool {
		funcDecl, ok := node.(*ast.FuncDecl)
		if !ok || funcDecl.Name.Name != "createInput" {
			return true
		}

		ast.Inspect(funcDecl, func(inner ast.Node) bool {
			switchStmt, ok := inner.(*ast.SwitchStmt)
			if !ok {
				return true
			}

			selector, ok := switchStmt.Tag.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "ServiceType" {
				return true
			}

			found = true

			for _, stmt := range switchStmt.Body.List {
				caseClause, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}

				// A nil List is the default clause.
				for _, expr := range caseClause.List {
					if ident, ok := expr.(*ast.Ident); ok {
						names = append(names, ident.Name)
					}
				}
			}

			return false
		})

		return false
	})

	if !found {
		t.Fatal(`no "switch service.ServiceType" found in createInput`)
	}

	return names
}

// TestInputSupportedServiceTypesMatchesCreateInput makes sure inputSupportedServiceTypes
// stays in sync with the switch in createInput. Bleemeo relies on that list to tell an
// unsupported service type apart from one that simply failed here, so a service type
// added to createInput without being added to the list would be reported to our users as
// "Glouton does not support this service".
func TestInputSupportedServiceTypesMatchesCreateInput(t *testing.T) {
	constants := serviceNameConstants(t)

	var want []string

	for _, name := range createInputCaseNames(t) {
		// CustomService is a check, it never has a metric input. It is deliberately
		// excluded from inputSupportedServiceTypes.
		if name == "CustomService" {
			continue
		}

		value, ok := constants[name]
		if !ok {
			t.Fatalf("case %s of createInput is not a known ServiceName constant", name)
		}

		want = append(want, string(value))
	}

	got := make([]string, 0, len(inputSupportedServiceTypes))

	for serviceType := range inputSupportedServiceTypes {
		got = append(got, string(serviceType))
	}

	sort.Strings(want)
	sort.Strings(got)

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("inputSupportedServiceTypes is out of sync with createInput (-createInput +list):\n%s", diff)
	}
}

// TestInputSupportedExcludesCustomService documents that a custom check is not an
// unsupported service type.
func TestInputSupportedExcludesCustomService(t *testing.T) {
	if InputSupported(CustomService) {
		t.Error("CustomService must not be reported as having a metric input")
	}

	if InputSupported(ServiceName("definitely-not-a-service")) {
		t.Error("an unknown service type must not be reported as supported")
	}

	if !InputSupported(ApacheService) {
		t.Error("ApacheService must be reported as having a metric input")
	}
}

// fakeScrapeRegistration is a registration that can report a scrape state.
type fakeScrapeRegistration struct {
	state types.ScrapeState
}

func (f fakeScrapeRegistration) ScheduleRun(types.ScheduleOption) {}
func (f fakeScrapeRegistration) Unregister()                      {}
func (f fakeScrapeRegistration) InternalRunScrape(context.Context, context.Context, time.Time) {
}

func (f fakeScrapeRegistration) LastScrapeState() types.ScrapeState { return f.state }

// bareRegistration is a registration that does NOT report a scrape state, like the
// registrations used elsewhere in these tests. It exists to pin the behaviour of the
// optional-interface fallback.
type bareRegistration struct{}

func (bareRegistration) ScheduleRun(types.ScheduleOption)                              {}
func (bareRegistration) Unregister()                                                   {}
func (bareRegistration) InternalRunScrape(context.Context, context.Context, time.Time) {}

func apacheKey() NameInstance {
	return NameInstance{Name: "apache", Instance: ""}
}

// newTestDiscovery builds a Discovery holding a single active apache service.
func newTestDiscovery() *Discovery {
	key := apacheKey()

	return &Discovery{
		servicesMap: map[NameInstance]Service{
			key: {Name: "apache", ServiceType: ApacheService, Active: true},
		},
		discoveredServicesMap: map[NameInstance]Service{
			key: {Name: "apache", ServiceType: ApacheService, Active: true},
		},
		activeCollector: make(map[NameInstance]collectorDetails),
		inputResult:     make(map[NameInstance]inputResult),
	}
}

// outcomeMessages is every outcome without an input and the error it reports. An empty
// message is reported as no error: the service type, InputSupported or the Container
// object already tell those cases.
func outcomeMessages() map[inputOutcome]string {
	return map[inputOutcome]string{
		inputInactive:            "",
		inputContainerStopped:    "",
		inputIgnored:             msgIgnored,
		inputCheckOnly:           "",
		inputUnsupportedType:     "",
		inputUnsupportedPlatform: msgUnsupportedPlatform,
		inputMissingInfo:         msgMissingInfo,
		inputMissingCommand:      msgMissingCommand,
		inputCreateFailed:        msgCreateFailed,
	}
}

// TestInputResultMessageIsDistinct checks each outcome that reports a message reports its
// own. Collapsing two of them would tell a user "Glouton could not create the input" when
// the truth is "I am missing your password".
//
// This exercises the mapping only. inputInactive is deliberately not reachable through
// ServicesState, which leaves inactive services out: see the comment on inputInactive.
func TestInputResultMessageIsDistinct(t *testing.T) {
	seen := make(map[string]inputOutcome)

	for outcome, want := range outcomeMessages() {
		if got := newInputResult(outcome, nil).message; got != want {
			t.Errorf("newInputResult(%d).message = %q, want %q", outcome, got, want)
		}

		if want == "" {
			continue
		}

		if other, ok := seen[want]; ok {
			t.Errorf("message %q is used by both inputOutcome(%d) and inputOutcome(%d)", want, other, outcome)
		}

		seen[want] = outcome
	}
}

// errDSNWithPassword is what Telegraf's MySQL input returns when it fails to parse a DSN:
// the whole DSN is quoted, password included.
var errDSNWithPassword = errors.New(`parsing "root:secret@tcp(127.0.0.1:3306)/" failed: invalid DSN`)

// TestInputResultCreateFailed checks a creation error is reported as a fixed message,
// with a suffix only for a cause identified with errors.Is.
func TestInputResultCreateFailed(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "unexpected type",
			err:  fmt.Errorf("%q: %w", "overview", inputs.ErrUnexpectedType),
			want: msgUnexpectedType,
		},
		{
			name: "unidentified error",
			err:  errDSNWithPassword,
			want: msgCreateFailed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := newInputResult(inputCreateFailed, tc.err).message
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}

			if strings.Contains(got, "secret") {
				t.Errorf("the error text leaked into the reported message: %q", got)
			}
		})
	}
}

// TestServicesStateReportsOutcome checks the messages that a service can actually reach
// through ServicesState, as opposed to the mapping alone.
func TestServicesStateReportsOutcome(t *testing.T) {
	for outcome, want := range outcomeMessages() {
		if outcome == inputInactive {
			continue // never reported, see the comment on inputInactive
		}

		d := newTestDiscovery()
		d.inputResult[apacheKey()] = newInputResult(outcome, nil)

		got := servicesStateOf(d)
		if len(got) != 1 {
			t.Fatalf("outcome %d: got %d services, want 1", outcome, len(got))
		}

		if got[0].Error != want {
			t.Errorf("outcome %d: got error %q, want %q", outcome, got[0].Error, want)
		}
	}
}

// TestServicesStateInactiveIsNotReported pins the choice behind inputInactive being
// unreachable: an inactive service is left out rather than reported with a status.
func TestServicesStateInactiveIsNotReported(t *testing.T) {
	d := newTestDiscovery()

	key := apacheKey()
	service := d.servicesMap[key]
	service.Active = false
	d.servicesMap[key] = service
	d.inputResult[key] = newInputResult(inputInactive, nil)

	if got := servicesStateOf(d); len(got) != 0 {
		t.Errorf("got %+v, want no service", got)
	}
}

// TestServicesStateDistinguishesCustomChecks checks two custom checks are told apart.
// They share ServiceType CustomService, so without the name the report would contain two
// identical rows and a user could not tell which check is failing.
func TestServicesStateDistinguishesCustomChecks(t *testing.T) {
	d := newTestDiscovery()
	delete(d.servicesMap, apacheKey())

	for _, name := range []string{"redis_session", "redis_cache"} {
		key := NameInstance{Name: name}
		d.servicesMap[key] = Service{Name: name, ServiceType: CustomService, Active: true}
		d.inputResult[key] = newInputResult(inputCheckOnly, nil)
	}

	got := servicesStateOf(d)
	if len(got) != 2 {
		t.Fatalf("got %d services, want 2", len(got))
	}

	if got[0].Name == got[1].Name {
		t.Fatalf("both rows report the same service: %+v", got)
	}

	// Sorted, so the order is fixed.
	if got[0].Name != "redis_cache" || got[1].Name != "redis_session" {
		t.Errorf("got %q then %q, want redis_cache then redis_session", got[0].Name, got[1].Name)
	}
}

// TestServicesStateScrapeState checks the statuses that come from the scrape state of a
// registered input.
func TestServicesStateScrapeState(t *testing.T) {
	cases := []struct {
		name       string
		state      types.ScrapeState
		wantErr    string
		wantBucket int
	}{
		{
			name:  "not scraped yet",
			state: types.ScrapeState{HasRun: false},
		},
		{
			name:       "working",
			state:      types.ScrapeState{HasRun: true, FamiliesCount: 7},
			wantBucket: 2,
		},
		{
			name:       "working with a single metric",
			state:      types.ScrapeState{HasRun: true, FamiliesCount: 1},
			wantBucket: 1,
		},
		{
			name:    "mod_status not enabled",
			state:   types.ScrapeState{HasRun: true, Error: "connection refused"},
			wantErr: "connection refused",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDiscovery()
			d.inputResult[apacheKey()] = newInputResult(inputRegistered, nil)
			d.activeCollector[apacheKey()] = collectorDetails{
				gathererRegistration: fakeScrapeRegistration{state: tc.state},
			}

			got := servicesStateOf(d)
			if len(got) != 1 {
				t.Fatalf("got %d services, want 1", len(got))
			}

			if got[0].Error != tc.wantErr {
				t.Errorf("error: got %q, want %q", got[0].Error, tc.wantErr)
			}

			if got[0].MetricFamiliesBucket != tc.wantBucket {
				t.Errorf("bucket: got %d, want %d", got[0].MetricFamiliesBucket, tc.wantBucket)
			}
		})
	}
}

// TestServicesStateWithoutScrapeState pins the fallback of the optional interface: a
// registration that cannot report its scrape state is still reported, with no error and
// no metric.
func TestServicesStateWithoutScrapeState(t *testing.T) {
	d := newTestDiscovery()
	d.inputResult[apacheKey()] = newInputResult(inputRegistered, nil)
	d.activeCollector[apacheKey()] = collectorDetails{gathererRegistration: bareRegistration{}}

	got := servicesStateOf(d)
	if len(got) != 1 {
		t.Fatalf("got %d services, want 1", len(got))
	}

	if got[0].Error != "" || got[0].MetricFamiliesBucket != 0 {
		t.Errorf("got error %q and bucket %d, want neither", got[0].Error, got[0].MetricFamiliesBucket)
	}
}

// TestServicesStateSkipsInactive checks that a service kept around only by the
// deactivation delay is not reported: it would make the value change as it ages out.
func TestServicesStateSkipsInactive(t *testing.T) {
	d := newTestDiscovery()

	key := apacheKey()
	service := d.servicesMap[key]
	service.Active = false
	d.servicesMap[key] = service

	if got := servicesStateOf(d); len(got) != 0 {
		t.Errorf("got %d services, want none", len(got))
	}
}

// TestServicesStateIsSorted checks the result is deterministic. It is hashed to decide
// whether the API must be updated, so an unstable order would cause an update on every
// single run.
func TestServicesStateIsSorted(t *testing.T) {
	d := newTestDiscovery()

	for _, name := range []string{"redis", "nginx", "mysql"} {
		key := NameInstance{Name: name, Instance: "b"}
		d.servicesMap[key] = Service{Name: name, ServiceType: ServiceName(name), Instance: "b", Active: true}
		d.servicesMap[NameInstance{Name: name, Instance: "a"}] = Service{
			Name: name, ServiceType: ServiceName(name), Instance: "a", Active: true,
		}
	}

	var previous []ServiceState

	for range 20 {
		got := servicesStateOf(d)

		if previous != nil && !cmp.Equal(previous, got) {
			t.Fatalf("ServicesState() is not deterministic:\n%s", cmp.Diff(previous, got))
		}

		previous = got
	}

	if !slices.IsSortedFunc(previous, func(a, b ServiceState) int {
		if c := strings.Compare(string(a.ServiceType), string(b.ServiceType)); c != 0 {
			return c
		}

		return strings.Compare(a.Instance, b.Instance)
	}) {
		t.Error("result is not sorted")
	}
}

func TestMetricFamiliesBucket(t *testing.T) {
	cases := map[int]int{-1: 0, 0: 0, 1: 1, 2: 2, 3: 2, 500: 2}

	for input, want := range cases {
		if got := metricsBucket(input); got != want {
			t.Errorf("metricsBucket(%d) = %d, want %d", input, got, want)
		}
	}
}

// TestCreateInputOutcomes covers the ticket's scenarios that end with no input: a
// supported service Glouton lacks credentials for, a service type with no input at all,
// and a custom check.
func TestCreateInputOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		service Service
		want    inputOutcome
	}{
		{
			name:    "mysql without credentials",
			service: Service{Name: "mysql", ServiceType: MySQLService, Active: true},
			want:    inputMissingInfo,
		},
		{
			name:    "service type with no input",
			service: Service{Name: "asterisk", ServiceType: AsteriskService, Active: true},
			want:    inputUnsupportedType,
		},
		{
			name:    "custom check",
			service: Service{Name: "my-check", ServiceType: CustomService, Active: true},
			want:    inputCheckOnly,
		},
		{
			name:    "not running",
			service: Service{Name: "apache", ServiceType: ApacheService, Active: false},
			want:    inputInactive,
		},
		{
			name:    "metrics disabled by configuration",
			service: Service{Name: "apache", ServiceType: ApacheService, Active: true, MetricsIgnored: true},
			want:    inputIgnored,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDiscovery()

			got, err := d.createInput(tc.service)
			if err != nil {
				t.Fatalf("createInput() returned an error: %v", err)
			}

			if got != tc.want {
				t.Errorf("createInput() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestCreateInputMissingInfoIsNotUnsupported is the distinction the whole feature rests
// on: a supported service Glouton cannot reach must never be reported the same way as a
// service type Glouton does not know about.
func TestCreateInputMissingInfoIsNotUnsupported(t *testing.T) {
	d := newTestDiscovery()

	missing, err := d.createInput(Service{Name: "mysql", ServiceType: MySQLService, Active: true})
	if err != nil {
		t.Fatalf("createInput(mysql): %v", err)
	}

	unsupported, err := d.createInput(Service{Name: "asterisk", ServiceType: AsteriskService, Active: true})
	if err != nil {
		t.Fatalf("createInput(asterisk): %v", err)
	}

	if got := newInputResult(missing, nil).message; got != msgMissingInfo {
		t.Errorf("a service missing credentials reports %q, want %q", got, msgMissingInfo)
	}

	if newInputResult(missing, nil) == newInputResult(unsupported, nil) {
		t.Error("a service missing credentials and an unsupported service type are reported the same way")
	}

	if !InputSupported(MySQLService) {
		t.Error("MySQL must still be reported as a supported service type")
	}

	if InputSupported(AsteriskService) {
		t.Error("Asterisk must not be reported as a supported service type")
	}
}

// TestTruncateError checks the reported error is bounded. An input's error is not: a
// Prometheus MultiError grows with the number of offending metrics, and an unbounded
// string would eventually have the whole payload rejected by the API.
func TestTruncateError(t *testing.T) {
	short := "connection refused"
	if got := truncateError(short); got != short {
		t.Errorf("truncateError(%q) = %q, want it unchanged", short, got)
	}

	exact := strings.Repeat("x", maxErrorLength)
	if got := truncateError(exact); got != exact {
		t.Errorf("a message of exactly maxErrorLength was truncated")
	}

	long := strings.Repeat("y", maxErrorLength*10)

	got := truncateError(long)
	if len(got) >= len(long) {
		t.Errorf("truncateError did not shorten a %d character message", len(long))
	}

	if !strings.HasSuffix(got, "(truncated)") {
		t.Errorf("a truncated message must say so, got %q", got[len(got)-20:])
	}
}

// TestServicesStateTruncatesError checks the bound is applied on the reported path, not
// only by the helper.
func TestServicesStateTruncatesError(t *testing.T) {
	d := newTestDiscovery()
	d.inputResult[apacheKey()] = newInputResult(inputRegistered, nil)
	d.activeCollector[apacheKey()] = collectorDetails{
		gathererRegistration: fakeScrapeRegistration{
			state: types.ScrapeState{HasRun: true, Error: strings.Repeat("z", 10000)},
		},
	}

	got := servicesStateOf(d)
	if len(got) != 1 {
		t.Fatalf("got %d services, want 1", len(got))
	}

	if len(got[0].Error) > maxErrorLength+len("... (truncated)") {
		t.Errorf("reported error is %d characters, want it bounded", len(got[0].Error))
	}
}

// TestServicesStateOutcomeSurvivesDiscovery pins that a service present across two
// discovery runs keeps its recorded outcome.
//
// updateDiscovery publishes a new servicesMap and releases d.l before reconfigure() fills
// inputOutcome, so a ServicesState() call can land between the two. That window only
// affects services that are genuinely new: inputOutcome is keyed by NameInstance and is
// only ever written or deleted one key at a time, never cleared, so everything already
// known keeps its status. A service discovered microseconds ago honestly has no state yet.
func TestServicesStateOutcomeSurvivesDiscovery(t *testing.T) {
	d := newTestDiscovery()
	d.inputResult[apacheKey()] = newInputResult(inputMissingInfo, nil)

	// A discovery run replaces servicesMap wholesale, here also adding a new service,
	// without inputOutcome having been updated yet.
	newKey := NameInstance{Name: "nginx"}
	d.servicesMap = map[NameInstance]Service{
		apacheKey(): {Name: "apache", ServiceType: ApacheService, Active: true},
		newKey:      {Name: "nginx", ServiceType: NginxService, Active: true},
	}
	d.discoveredServicesMap = d.servicesMap

	got := servicesStateOf(d)
	if len(got) != 2 {
		t.Fatalf("got %d services, want 2", len(got))
	}

	byName := make(map[string]ServiceState, len(got))
	for _, state := range got {
		byName[state.Name] = state
	}

	if byName["apache"].Error != msgMissingInfo {
		t.Errorf(
			"a service seen before lost its error across a discovery run: got %q, want %q",
			byName["apache"].Error, msgMissingInfo,
		)
	}

	if byName["nginx"].Error != "" {
		t.Errorf("a service discovered just now should have no error yet: got %q", byName["nginx"].Error)
	}
}

// TestStabilizeError checks the local address of a network error is removed, and nothing
// else. The inputs are real errors, from Go's net package.
func TestStabilizeError(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "tcp read",
			input: "read tcp 127.0.0.1:58669->127.0.0.1:58668: read: connection reset by peer",
			want:  "read tcp 127.0.0.1:58668: read: connection reset by peer",
		},
		{
			name:  "tcp write",
			input: "write tcp 127.0.0.1:58669->127.0.0.1:58668: write: broken pipe",
			want:  "write tcp 127.0.0.1:58668: write: broken pipe",
		},
		{
			name: "http client",
			input: `Get "http://127.0.0.1:58668/server-status?auto": ` +
				"read tcp 127.0.0.1:58671->127.0.0.1:58668: read: connection reset by peer",
			want: `Get "http://127.0.0.1:58668/server-status?auto": ` +
				"read tcp 127.0.0.1:58668: read: connection reset by peer",
		},
		{
			name:  "read timeout",
			input: "read tcp 127.0.0.1:58675->127.0.0.1:58673: i/o timeout",
			want:  "read tcp 127.0.0.1:58673: i/o timeout",
		},
		{
			name:  "udp",
			input: "read udp 127.0.0.1:54876->127.0.0.1:55785: read: connection refused",
			want:  "read udp 127.0.0.1:55785: read: connection refused",
		},
		{
			name:  "ipv6",
			input: "read tcp [::1]:58684->[::1]:58683: read: connection reset by peer",
			want:  "read tcp [::1]:58683: read: connection reset by peer",
		},
		{
			// An unnamed local socket prints as an empty address.
			name:  "unix socket",
			input: "write unix ->/run/mysqld/mysqld.sock: write: broken pipe",
			want:  "write unix /run/mysqld/mysqld.sock: write: broken pipe",
		},
		{
			// No local address on a failed dial: already stable.
			name:  "dial",
			input: "dial tcp 127.0.0.1:1: connect: connection refused",
			want:  "dial tcp 127.0.0.1:1: connect: connection refused",
		},
		{
			name:  "http timeout",
			input: `Get "http://127.0.0.1:58673/": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`,
			want:  `Get "http://127.0.0.1:58673/": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`,
		},
		{
			name:  "arrow outside a network error",
			input: "unexpected token -> in status page",
			want:  "unexpected token -> in status page",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stabilizeError(tc.input); got != tc.want {
				t.Errorf("stabilizeError(%q)\n got %q\nwant %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestServicesStateErrorIsStable checks two scrapes failing the same way, on two
// connections, report the same error: otherwise Bleemeo would be updated on every scrape.
func TestServicesStateErrorIsStable(t *testing.T) {
	report := func(scrapeError string) string {
		d := newTestDiscovery()
		d.inputResult[apacheKey()] = newInputResult(inputRegistered, nil)
		d.activeCollector[apacheKey()] = collectorDetails{
			gathererRegistration: fakeScrapeRegistration{
				state: types.ScrapeState{HasRun: true, Error: scrapeError},
			},
		}

		return servicesStateOf(d)[0].Error
	}

	first := report("read tcp 127.0.0.1:52431->127.0.0.1:3306: read: connection reset by peer")
	second := report("read tcp 127.0.0.1:52987->127.0.0.1:3306: read: connection reset by peer")

	if first != second {
		t.Errorf("the same failure on two connections reports two errors:\n%q\n%q", first, second)
	}
}

// TestStabilizeErrorOrder checks errors an input reported in a different order on two
// scrapes report the same value. RabbitMQ gathers its endpoints concurrently, so its
// errors are joined in whatever order the goroutines finished.
func TestStabilizeErrorOrder(t *testing.T) {
	overview := `Get "http://127.0.0.1:15672/api/overview": dial tcp 127.0.0.1:15672: connect: connection refused`
	nodes := `Get "http://127.0.0.1:15672/api/nodes": dial tcp 127.0.0.1:15672: connect: connection refused`
	queues := `Get "http://127.0.0.1:15672/api/queues": dial tcp 127.0.0.1:15672: connect: connection refused`

	first := stabilizeError(errors.Join(
		errors.New(overview), errors.New(nodes), errors.New(queues), //nolint:err113
	).Error())
	second := stabilizeError(errors.Join(
		errors.New(queues), errors.New(overview), errors.New(nodes), //nolint:err113
	).Error())

	if first != second {
		t.Errorf("the same errors in another order report another value:\n%q\n%q", first, second)
	}

	if dup := stabilizeError(overview + "\n" + overview); dup != overview {
		t.Errorf("a repeated error is reported twice: %q", dup)
	}
}

// TestRedactError checks a password quoted in an error is hidden. The HAProxy messages are
// what Telegraf's input returns when its stats URL fails to parse.
func TestRedactError(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "invalid escape in the password",
			input: `unable parse server address "http://admin:p%zz@127.0.0.1:8404/stats/;csv": ` +
				`parse "http://admin:p%zz@127.0.0.1:8404/stats/;csv": invalid URL escape "%zz"`,
			want: `unable parse server address "http://admin:xxxxx@127.0.0.1:8404/stats/;csv": ` +
				`parse "http://admin:xxxxx@127.0.0.1:8404/stats/;csv": invalid URL escape "%zz"`,
		},
		{
			name: "space in the password",
			input: `unable parse server address "http://admin:pa ss@127.0.0.1:8404/stats/;csv": ` +
				`parse "http://admin:pa ss@127.0.0.1:8404/stats/;csv": net/url: invalid userinfo`,
			want: `unable parse server address "http://admin:xxxxx@127.0.0.1:8404/stats/;csv": ` +
				`parse "http://admin:xxxxx@127.0.0.1:8404/stats/;csv": net/url: invalid userinfo`,
		},
		{ //nolint:gosec // a fake password, to check it is redacted
			name:  "at sign in the password",
			input: `parse "http://admin:p@ss@127.0.0.1:8404/stats": failed`,
			want:  `parse "http://admin:xxxxx@127.0.0.1:8404/stats": failed`,
		},
		{
			name:  "no credentials",
			input: `Get "http://127.0.0.1:8404/stats/;csv": dial tcp 127.0.0.1:8404: connect: connection refused`,
			want:  `Get "http://127.0.0.1:8404/stats/;csv": dial tcp 127.0.0.1:8404: connect: connection refused`,
		},
		{
			name:  "user without password",
			input: `Get "http://admin@127.0.0.1:8404/stats": EOF`,
			want:  `Get "http://admin@127.0.0.1:8404/stats": EOF`,
		},
		{
			name:  "host and port",
			input: `could not connect to 'tcp://127.0.0.1:9999': dial tcp 127.0.0.1:9999: connect: connection refused`,
			want:  `could not connect to 'tcp://127.0.0.1:9999': dial tcp 127.0.0.1:9999: connect: connection refused`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactError(tc.input, nil); got != tc.want {
				t.Errorf("redactError(%q)\n got %q\nwant %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestServicesStateRedactsError checks the redaction is applied on the reported path.
func TestServicesStateRedactsError(t *testing.T) {
	d := newTestDiscovery()
	d.inputResult[apacheKey()] = newInputResult(inputRegistered, nil)
	d.activeCollector[apacheKey()] = collectorDetails{
		gathererRegistration: fakeScrapeRegistration{
			state: types.ScrapeState{ //nolint:gosec // a fake password, to check it is redacted
				HasRun: true,
				Error:  `unable parse server address "http://admin:p%zz@127.0.0.1:8404/stats/;csv": invalid URL escape "%zz"`,
			},
		},
	}

	if got := servicesStateOf(d)[0].Error; strings.Contains(got, "p%zz") {
		t.Errorf("the password reached the report: %q", got)
	}
}

// TestServicesStateRedactsConfiguredPassword checks a password from the service's
// configuration is hidden in every form the HAProxy error quotes it, including those the
// URL pattern cannot find: a "/" or a quote in the password, and the prefix Go's URL parser
// reads as a port.
func TestServicesStateRedactsConfiguredPassword(t *testing.T) {
	for _, password := range []string{"hunter/22", `hunter"22`, "hunter#22", "hunter?22"} {
		t.Run(password, func(t *testing.T) {
			addr := "http://admin:" + password + "@127.0.0.1:8404/stats/;csv"

			_, parseErr := url.Parse(addr)
			if parseErr == nil {
				t.Fatalf("%q parses, the test does not produce the error", addr)
			}

			d := newTestDiscovery()
			service := d.servicesMap[apacheKey()]
			service.Config.StatsURL = addr
			d.servicesMap[apacheKey()] = service

			d.inputResult[apacheKey()] = newInputResult(inputRegistered, nil)
			d.activeCollector[apacheKey()] = collectorDetails{
				gathererRegistration: fakeScrapeRegistration{
					state: types.ScrapeState{
						HasRun: true,
						// What Telegraf's HAProxy input returns.
						Error: fmt.Sprintf("unable parse server address %q: %v", addr, parseErr),
					},
				},
			}

			got := servicesStateOf(d)[0].Error
			if strings.Contains(got, "hunter") {
				t.Errorf("the password, or its start, reached the report: %q", got)
			}
		})
	}
}

// TestTruncateErrorKeepsUTF8 checks the cut does not split a multi-byte character.
func TestTruncateErrorKeepsUTF8(t *testing.T) {
	// "é" is two bytes: with one leading byte, byte maxErrorLength falls inside one.
	message := "x" + strings.Repeat("é", maxErrorLength)

	got := truncateError(message)
	if !utf8.ValidString(got) {
		t.Errorf("truncateError produced invalid UTF-8: %q", got[len(got)-20:])
	}

	if !strings.HasSuffix(got, "... (truncated)") {
		t.Errorf("a truncated message must say so, got %q", got[len(got)-20:])
	}
}

// TestCreateInputVarnishWithoutVarnishstat checks a containerised Varnish whose image has
// no varnishstat is reported as such, not as missing credentials or an address.
func TestCreateInputVarnishWithoutVarnishstat(t *testing.T) {
	service := varnishInstanceService(4242)
	service.Active = true

	d := newTestDiscovery()
	d.containerInfo = mockContainerInfo{
		containers: nil,
		exec: func(string, []string) ([]byte, error) {
			return nil, errNoSuchBinary
		},
	}

	got, err := d.createInput(service)
	if err != nil {
		t.Fatalf("createInput() returned an error: %v", err)
	}

	if got != inputMissingCommand {
		t.Errorf("createInput() = %d, want inputMissingCommand (%d)", got, inputMissingCommand)
	}

	if msg := newInputResult(got, nil).message; msg == msgMissingInfo {
		t.Errorf("a missing varnishstat is reported as %q", msg)
	}
}

// fakeJMXState is a JMX collection state returning the same state for every service.
type fakeJMXState struct {
	state types.ScrapeState
}

func (f fakeJMXState) ServiceScrapeState(NameInstance) types.ScrapeState { return f.state }

// TestServicesStateJMX checks a service whose metrics only JMX collects is reported as
// supported, with the JMX collection state when JMX is set up for it, and with why not
// otherwise.
func TestServicesStateJMX(t *testing.T) {
	cassandra := func(jmxPort int, noAddress bool) *Discovery {
		key := NameInstance{Name: "cassandra"}
		service := Service{
			Name:        "cassandra",
			ServiceType: CassandraService,
			Active:      true,
			IPAddress:   "127.0.0.1",
			Config:      config.Service{JMXPort: jmxPort},
		}

		if noAddress {
			service.IPAddress = ""
		}

		d := newTestDiscovery()
		d.servicesMap = map[NameInstance]Service{key: service}
		d.discoveredServicesMap = map[NameInstance]Service{key: service}
		d.inputResult[key] = newInputResult(inputUnsupportedType, nil)

		return d
	}

	collected := fakeJMXState{state: types.ScrapeState{HasRun: true, FamiliesCount: 12}}

	cases := []struct {
		name      string
		jmxPort   int
		noAddress bool
		jmxState  JMXStateProvider
		wantError string
		wantCount int
	}{
		{name: "collected", jmxPort: 7199, jmxState: collected, wantCount: 2},
		{
			name:      "nothing received",
			jmxPort:   7199,
			jmxState:  fakeJMXState{state: types.ScrapeState{HasRun: true, Error: "no metrics received through JMX"}},
			wantError: "no metrics received through JMX",
		},
		{name: "no jmx_port", jmxState: collected, wantError: msgJMXNoPort},
		{name: "no address", jmxPort: 7199, noAddress: true, jmxState: collected, wantError: msgJMXNoAddress},
		{name: "JMX disabled", jmxPort: 7199, wantError: msgJMXDisabled},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := cassandra(tc.jmxPort, tc.noAddress)
			if tc.jmxState != nil {
				d.SetJMXState(tc.jmxState)
			}

			got := servicesStateOf(d)[0]

			if !got.InputSupported {
				t.Error("a service collected through JMX is reported as unsupported")
			}

			if got.Error != tc.wantError || got.MetricFamiliesBucket != tc.wantCount {
				t.Errorf("got error %q and metrics %d, want %q and %d", got.Error, got.MetricFamiliesBucket, tc.wantError, tc.wantCount)
			}
		})
	}
}

// TestServicesStateJMXOtherTypes checks a service type with no JMX metrics of its own is
// left as it was without a jmx_port, and reads the JMX state with one, while its
// input_supported stays a property of its type.
func TestServicesStateJMXOtherTypes(t *testing.T) {
	key := NameInstance{Name: "my-java-app"}
	service := Service{Name: "my-java-app", ServiceType: CustomService, Active: true, IPAddress: "127.0.0.1"}

	d := newTestDiscovery()
	d.servicesMap = map[NameInstance]Service{key: service}
	d.inputResult[key] = newInputResult(inputCheckOnly, nil)
	d.SetJMXState(fakeJMXState{state: types.ScrapeState{HasRun: true, FamiliesCount: 1}})

	if got := servicesStateOf(d)[0]; got.Error != "" || got.MetricFamiliesBucket != 0 {
		t.Errorf("without a jmx_port, got %+v, want no error and no metrics", got)
	}

	service.Config.JMXPort = 7199
	d.servicesMap[key] = service

	got := servicesStateOf(d)[0]
	if got.MetricFamiliesBucket != 1 {
		t.Errorf("with a jmx_port, got %+v, want the JMX state", got)
	}

	if got.InputSupported {
		t.Error("a custom check reports input_supported because one of them has a jmx_port")
	}
}

// TestServicesStateErrorIsStableWithShortPassword checks a configured password matching the
// network name does not stop the local address from being stripped: the error must not
// change with the ephemeral port.
func TestServicesStateErrorIsStableWithShortPassword(t *testing.T) {
	report := func(localPort int) string {
		d := newTestDiscovery()
		service := d.servicesMap[apacheKey()]
		service.Config.Password = "tcp"
		d.servicesMap[apacheKey()] = service

		d.inputResult[apacheKey()] = newInputResult(inputRegistered, nil)
		d.activeCollector[apacheKey()] = collectorDetails{
			gathererRegistration: fakeScrapeRegistration{
				state: types.ScrapeState{
					HasRun: true,
					Error:  fmt.Sprintf("read tcp 127.0.0.1:%d->127.0.0.1:3306: read: connection reset by peer", localPort),
				},
			},
		}

		return servicesStateOf(d)[0].Error
	}

	if first, second := report(52431), report(52432); first != second {
		t.Errorf("the error changes with the local port:\n%q\n%q", first, second)
	}
}

// TestServicesStateRedactsURLQuerySecrets checks secrets passed in the stats URL's query
// string are hidden too, as written and decoded, and that a password ending in a quote is
// hidden whole in its quoted form.
func TestServicesStateRedactsURLQuerySecrets(t *testing.T) {
	cases := []struct {
		name    string
		config  config.Service
		message string
		secret  string
	}{
		{
			name:    "password parameter",
			config:  config.Service{StatsURL: "http://ch:8123/?user=u&password=hunter2x"},
			message: `Get "http://ch:8123/?user=u&password=hunter2x": dial tcp 127.0.0.1:8123: connect: connection refused`,
			secret:  "hunter2x",
		},
		{
			name:    "encoded token",
			config:  config.Service{StatsURL: "http://consul:8500/v1/agent/metrics?token=ab%2Fcd%3D"},
			message: `token ab/cd= was refused`,
			secret:  "ab/cd=",
		},
		{
			name:    "InfluxDB 1.x password",
			config:  config.Service{StatsURL: "http://influx:8086/debug/vars?u=admin&p=hunter2x"},
			message: `Get "http://influx:8086/debug/vars?u=admin&p=hunter2x": EOF`,
			secret:  "hunter2x",
		},
		{
			name:    "password ending in a quote",
			config:  config.Service{Password: `hunter2"`},
			message: `parse "http://admin:hunter2\"@host/": net/url: invalid userinfo`,
			secret:  `hunter2`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactError(tc.message, serviceSecrets(tc.config))
			if strings.Contains(got, tc.secret) {
				t.Errorf("the secret reached the report: %q", got)
			}

			if strings.Contains(got, `xxxxx"@`) && strings.Contains(tc.message, `\"@`) {
				t.Errorf("the quote ending the password reached the report: %q", got)
			}
		})
	}
}

// TestServicesStateMetricsIgnored checks a service whose metrics the configuration disables
// is flagged as such, which the size limits rely on rather than on the message.
func TestServicesStateMetricsIgnored(t *testing.T) {
	d := newTestDiscovery()
	d.inputResult[apacheKey()] = newInputResult(inputIgnored, nil)

	if got := servicesStateOf(d)[0]; !got.MetricsIgnored || got.Error != msgIgnored {
		t.Errorf("got %+v, want MetricsIgnored and the message", got)
	}

	d.inputResult[apacheKey()] = newInputResult(inputMissingInfo, nil)

	if got := servicesStateOf(d)[0]; got.MetricsIgnored {
		t.Errorf("a service missing credentials is flagged as disabled: %+v", got)
	}
}

// servicesStateOf is ServicesState after publishing the state the test set directly, as
// discovery does after each change.
func servicesStateOf(d *Discovery) []ServiceState {
	d.l.Lock()
	d.publishServicesState()
	d.l.Unlock()

	return d.ServicesState()
}

// TestServicesStateConcurrent reads the report while discovery changes its state and
// publishes it, for the race detector.
func TestServicesStateConcurrent(t *testing.T) {
	d := newTestDiscovery()
	d.activeCollector[apacheKey()] = collectorDetails{
		gathererRegistration: fakeScrapeRegistration{state: types.ScrapeState{HasRun: true, FamiliesCount: 3}},
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		for i := range 200 {
			d.l.Lock()

			outcome := inputRegistered
			if i%2 == 0 {
				outcome = inputMissingInfo
			}

			d.inputResult[apacheKey()] = newInputResult(outcome, nil)
			d.publishServicesState()
			d.l.Unlock()
		}
	}()

	for range 200 {
		for _, state := range d.ServicesState() {
			_ = state.Error
		}
	}

	<-done
}

// TestURLSecretsOnlyKnownParameters checks ordinary query parameters whose names merely
// contain "key" or "pass" are not taken for secrets, which would blank their values
// everywhere in the error.
func TestURLSecretsOnlyKnownParameters(t *testing.T) {
	got := urlSecrets("http://cassandra:8080/stats?keyspace=system&bypass_cache=true&apikey=hunter2x")

	if !slices.Equal(got, []string{"hunter2x"}) {
		t.Errorf("urlSecrets() = %q, want only the apikey value", got)
	}
}
