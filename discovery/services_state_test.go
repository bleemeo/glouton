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
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

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

		got := d.ServicesState()
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

	if got := d.ServicesState(); len(got) != 0 {
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

	got := d.ServicesState()
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

			got := d.ServicesState()
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

	got := d.ServicesState()
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

	if got := d.ServicesState(); len(got) != 0 {
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
		got := d.ServicesState()

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

	got := d.ServicesState()
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

	got := d.ServicesState()
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
