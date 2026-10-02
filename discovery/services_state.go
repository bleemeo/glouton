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
	"cmp"
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/types"
)

// inputSupportedServiceTypes lists the service types for which Glouton knows how to build
// a metric input, that is the types handled by createInput.
//
// This is a *static* property of the service type, not of the current run: a type stays
// listed here even when the input could not be built on this agent (missing credentials,
// unsupported platform, ...). Bleemeo uses it to tell "Glouton does not collect metrics
// for this kind of service" apart from "Glouton could not collect them here", without
// having to track which Glouton version introduced support for which service.
//
// CustomService is deliberately absent: a custom check never has a metric input, which is
// not the same thing as a service type Glouton does not support.
//
// TestInputSupportedServiceTypesMatchesCreateInput keeps this list in sync with the
// switch in createInput.
var inputSupportedServiceTypes = map[ServiceName]bool{ //nolint:gochecknoglobals
	ActiveMQService:      true,
	ApacheService:        true,
	BindService:          true,
	ChronyService:        true,
	ClickHouseService:    true,
	ConsulService:        true,
	DovecotService:       true,
	ElasticSearchService: true,
	Fail2banService:      true,
	HAProxyService:       true,
	InfluxDBService:      true,
	JenkinsService:       true,
	MariaDBService:       true,
	MemcachedService:     true,
	MongoDBService:       true,
	MySQLService:         true,
	NSQService:           true,
	NatsService:          true,
	NfsService:           true,
	NginxService:         true,
	NTPService:           true,
	OpenBaoService:       true,
	OpenLDAPService:      true,
	PHPFPMService:        true,
	PgBouncerService:     true,
	PostgreSQLService:    true,
	RabbitMQService:      true,
	RedisService:         true,
	TomcatService:        true,
	UPSDService:          true,
	UWSGIService:         true,
	ValkeyService:        true,
	VarnishService:       true,
	VaultService:         true,
	ZookeeperService:     true,
}

// InputSupported tells whether Glouton knows how to build a metric input for this
// service type. See inputSupportedServiceTypes.
func InputSupported(serviceType ServiceName) bool {
	return inputSupportedServiceTypes[serviceType]
}

// inputOutcome is what happened when Glouton tried to build a metric input for a service.
//
// It is recorded per service by configureMetricInputs, because createInput has several
// paths that all end up with no input registered and which mean very different things to
// a user: "this service type has no input at all" is not "I am missing your password".
type inputOutcome int

const (
	// inputRegistered means an input was built and registered. Whether it actually
	// produces metrics is only known from its scrape state.
	inputRegistered inputOutcome = iota
	// inputInactive means the service is not currently running.
	//
	// It is recorded but never reported: ServicesState leaves inactive services out
	// entirely, because Bleemeo learns a service stopped from its Service object being
	// deactivated, and keeping them here would make the value change as they age out of
	// discovery.
	inputInactive
	// inputContainerStopped means the service's container is stopped.
	inputContainerStopped
	// inputIgnored means metrics for this service are disabled by the configuration.
	inputIgnored
	// inputCheckOnly means the service is a custom check, which never has a metric input.
	inputCheckOnly
	// inputUnsupportedType means Glouton has no input for this service type.
	inputUnsupportedType
	// inputUnsupportedPlatform means Glouton has an input for this service type, but not
	// on this platform or not in this build.
	inputUnsupportedPlatform
	// inputMissingInfo means Glouton has an input for this service type but lacks
	// credentials or a reachable address to build it.
	inputMissingInfo
	// inputMissingCommand means the input needs a command the service's container does not
	// have, such as Varnish's varnishstat.
	inputMissingCommand
	// inputCreateFailed means building the input returned an error.
	inputCreateFailed
)

// Messages reported as a service's error when Glouton has no input for it. They are fixed
// strings, never the error text createInput returned: that text comes from Telegraf, may
// quote the service configuration (a MySQL DSN holds the password) and is not meant for
// a user anyway.
const (
	msgCreateFailed        = "failed to create the input"
	msgMissingInfo         = msgCreateFailed + ": missing credentials or address"
	msgMissingCommand      = msgCreateFailed + ": required command not found in the container"
	msgUnexpectedType      = msgCreateFailed + ": unexpected input type"
	msgUnsupportedPlatform = "input not available on this platform"
	msgIgnored             = "metrics disabled by configuration"
)

// inputResult is what configureMetricInputs records per service: the outcome, and the
// message to report when that outcome left the service without an input.
type inputResult struct {
	outcome inputOutcome
	message string
}

// newInputResult builds the inputResult of a createInput call.
//
// Outcomes that need no explanation get no message: the service type already tells a
// custom check (CustomService) or an unsupported type (InputSupported), and Bleemeo knows
// a container is stopped from the Container object.
func newInputResult(outcome inputOutcome, err error) inputResult {
	result := inputResult{outcome: outcome}

	switch outcome {
	case inputMissingInfo:
		result.message = msgMissingInfo
	case inputMissingCommand:
		result.message = msgMissingCommand
	case inputUnsupportedPlatform:
		result.message = msgUnsupportedPlatform
	case inputIgnored:
		result.message = msgIgnored
	case inputCreateFailed:
		result.message = msgCreateFailed

		// Only causes that can be identified reliably get a suffix. Others would need
		// matching on Telegraf's error text.
		if errors.Is(err, inputs.ErrUnexpectedType) {
			result.message = msgUnexpectedType
		}
	case inputRegistered, inputInactive, inputContainerStopped, inputCheckOnly, inputUnsupportedType:
	}

	return result
}

// scrapeStateProvider is implemented by registry registrations, which can tell how their
// last scrape went.
//
// It is an optional interface, asserted at the call site, rather than a method added to
// types.Registration: only the services state needs it, and this is how this code already
// reaches for capabilities a gatherer may or may not have (see the diagnosticer assertion
// in the registry, or inputs.SecretfulInput).
type scrapeStateProvider interface {
	LastScrapeState() types.ScrapeState
}

// ServiceState is what Glouton knows about one service, so that Bleemeo can tell a user
// why a service is or is not monitored.
//
// It deliberately holds no Bleemeo identifier: discovery does not know them. The service
// UUID is added later, by the Bleemeo synchronizer, which has the API cache.
type ServiceState struct {
	// Name identifies the service. It is not always redundant with ServiceType: every
	// custom check shares ServiceType CustomService, and without the name two checks a
	// user declared would be indistinguishable in the report.
	Name          string      `json:"name"`
	ServiceType   ServiceName `json:"type"`
	Instance      string      `json:"instance"`
	ContainerName string      `json:"container_name"`
	// Dynamic tells whether discovery found this service, as opposed to it being declared
	// in the configuration.
	Dynamic bool `json:"dynamic"`
	// InputSupported tells whether Glouton has a metric input for this service type at
	// all. It is a static property of the type, true even when the input could not be
	// built here, so that Bleemeo does not need to track which Glouton version added
	// support for which service.
	InputSupported bool `json:"input_supported"`
	// Error is the last error reported by the input or, when Glouton has no input for
	// this service, why: missing credentials, unsupported platform, ... It is empty when
	// there is nothing to report.
	Error string `json:"error,omitempty"`
	// MetricFamiliesBucket is 0, 1, or 2 meaning "two or more".
	//
	// It counts metric *families*, that is distinct metric names, not series: an input
	// producing one family of fifty series reports 1. That is the useful signal here,
	// which is whether the input produces anything at all.
	//
	// The exact count is deliberately not reported: this value is kept synchronized with
	// the API, so it must be stable, and an input does not always produce the same number
	// of metrics.
	MetricFamiliesBucket int `json:"metrics"`
}

// ServicesState returns what Glouton knows about each service it currently sees.
//
// Inactive services are left out. Note what Service.Active actually means, because it is
// not simply "the daemon is running" (see setServiceActiveAndContainer):
//   - a containerised service is inactive once its container is gone, ignored or replaced;
//   - a service with an ExePath is inactive only once that binary no longer exists, so a
//     daemon that is merely stopped stays active and is reported, with the error from its
//     input. That is wanted: "MySQL is installed but refuses connections" is precisely
//     what a user needs to be told;
//   - a service with neither is inactive after absentServiceDeactivationDelay.
//
// Services declared in the configuration are kept even when nothing was found for them,
// as a user-declared service that never worked is exactly what this is for.
//
// The result is sorted, because it is hashed to decide whether the API needs updating and
// map iteration order would otherwise make it differ on every call.
func (d *Discovery) ServicesState() []ServiceState {
	result, providers := d.servicesStateLocked()

	// Queried outside d.l on purpose: LastScrapeState takes the registration's own lock,
	// which the registry holds while a scrape finishes. Waiting for it with d.l held would
	// block discovery itself -- GetLatestDiscovery, RemoveIfNonRunning, the next discovery
	// pass -- behind a diagnostic read.
	for i, provider := range providers {
		if provider == nil {
			continue
		}

		result[i].Error, result[i].MetricFamiliesBucket = scrapeResult(provider)
	}

	slices.SortFunc(result, compareServiceState)

	return result
}

// servicesStateLocked builds the report from discovery's own state, returning for each
// entry the provider whose scrape state still has to be read, or nil when it has none.
func (d *Discovery) servicesStateLocked() ([]ServiceState, []scrapeStateProvider) {
	d.l.Lock()
	defer d.l.Unlock()

	result := make([]ServiceState, 0, len(d.servicesMap))
	providers := make([]scrapeStateProvider, 0, len(d.servicesMap))

	for key, service := range d.servicesMap {
		if !service.Active {
			continue
		}

		_, dynamic := d.discoveredServicesMap[key]

		state := ServiceState{
			Name:           service.Name,
			ServiceType:    service.ServiceType,
			Instance:       service.Instance,
			ContainerName:  service.ContainerName,
			Dynamic:        dynamic,
			InputSupported: InputSupported(service.ServiceType),
		}

		// Without a result, configureMetricInputs has not run for this service yet, and
		// there is nothing to report.
		input, hasResult := d.inputResult[key]

		var provider scrapeStateProvider

		switch {
		case !hasResult:
		case input.outcome == inputRegistered:
			if collector, ok := d.activeCollector[key]; ok {
				provider, _ = collector.gathererRegistration.(scrapeStateProvider)
			}
		default:
			state.Error = input.message
		}

		result = append(result, state)
		providers = append(providers, provider)
	}

	return result, providers
}

// compareServiceState orders on every field that identifies a service. slices.SortFunc is
// not stable and the input comes from map iteration, so ties would order randomly and
// change the hash the synchronizer compares, re-sending an unchanged value every time.
func compareServiceState(a, b ServiceState) int {
	if c := cmp.Compare(a.ServiceType, b.ServiceType); c != 0 {
		return c
	}

	if c := cmp.Compare(a.Name, b.Name); c != 0 {
		return c
	}

	if c := cmp.Compare(a.Instance, b.Instance); c != 0 {
		return c
	}

	return cmp.Compare(a.ContainerName, b.ContainerName)
}

// maxErrorLength bounds the error reported per service.
//
// An input's error is not bounded by anything: a Prometheus MultiError concatenates one
// message per offending metric, so a single misbehaving input could otherwise push the
// whole payload past the 2.5 MB body limit of the API and have every collection rejected.
// The head of the message is what identifies the problem.
const maxErrorLength = 500

// truncateError bounds an error message, marking it when it was cut so that a reader is
// not left wondering whether the message really ended there.
func truncateError(message string) string {
	if len(message) <= maxErrorLength {
		return message
	}

	// Cut on a character boundary: half a multi-byte character is invalid UTF-8, which the
	// JSON encoding would turn into a replacement character.
	cut := maxErrorLength
	for cut > 0 && !utf8.RuneStart(message[cut]) {
		cut--
	}

	return message[:cut] + "... (truncated)"
}

// localAddressRegex matches the local address of a network error, as net.OpError prints
// it for an established connection: "read tcp 127.0.0.1:52431->127.0.0.1:3306: ...".
// The network name anchors it, so that a "->" elsewhere in a message is left alone.
var localAddressRegex = regexp.MustCompile(`\b((?:tcp|udp|ip|unix)(?:4|6|gram|packet)?) [^\s>]*->`)

// stabilizeError removes from an input's error what changes on every scrape while the
// problem stays the same, so that Bleemeo is not updated each time for nothing:
//   - the local address of a network error, whose port is ephemeral, a new one for each
//     connection. The remote address, which tells what Glouton failed to reach, is kept;
//   - the order of the errors an input reported. They are joined one per line in the
//     order the input added them, and an input gathering concurrently, such as RabbitMQ,
//     adds them in a different order on every scrape. Lines are sorted, and duplicates
//     dropped. This also reorders the lines of a single multi-line error, which loses
//     nothing a user needs and keeps the value stable.
func stabilizeError(message string) string {
	message = localAddressRegex.ReplaceAllString(message, "$1 ")

	lines := strings.Split(message, "\n")
	slices.Sort(lines)

	return strings.Join(slices.Compact(lines), "\n")
}

// urlPasswordRegex matches the password of a URL quoted in an error: "://user:password@".
// It stops at "/", quote and newline, which end a URL's authority in an error message, and
// takes the last "@" before them, as URL parsing does.
var urlPasswordRegex = regexp.MustCompile(`(://[^/\s:@"]*):[^/"\n]*@`)

// redactError hides the passwords an input's error may quote.
//
// A scrape error is reported as the input wrote it, and some quote their configured URL
// with its credentials: Telegraf's HAProxy input does when the stats URL fails to parse,
// as it does with a "%" or a space in the password. The password is replaced the way
// net/url's Redacted does.
func redactError(message string) string {
	return urlPasswordRegex.ReplaceAllString(message, "$1:xxxxx@")
}

// scrapeResult reads the error and metric bucket of a service whose input was registered.
// An input that has not run yet reports neither.
func scrapeResult(provider scrapeStateProvider) (string, int) {
	scrape := provider.LastScrapeState()
	if !scrape.HasRun {
		return "", 0
	}

	// Stabilized before truncation, so that the cut lands at the same place every time.
	return truncateError(stabilizeError(redactError(scrape.Error))), metricsBucket(scrape.FamiliesCount)
}

// metricsBucket reduces a count to 0, 1 or 2, where 2 means "two or more". See
// ServiceState.MetricFamiliesBucket for what is counted and why it is not exact.
func metricsBucket(count int) int {
	if count <= 0 {
		return 0
	}

	return min(count, 2)
}
