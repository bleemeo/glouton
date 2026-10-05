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
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/types"
)

// inputSupportedServiceTypes lists the service types createInput builds an input for. See
// ServiceState.InputSupported for why support is per type. CustomService is absent: a custom
// check has no input, which is not the same as an unsupported type.
//
// TestInputSupportedServiceTypesMatchesCreateInput keeps it in sync with createInput.
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

// jmxServiceTypes lists the service types whose metrics only jmxtrans collects.
// TestJMXServiceTypesMatchDiscovery, in jmxtrans, keeps it in sync with jmxtrans.
var jmxServiceTypes = map[ServiceName]bool{ //nolint:gochecknoglobals
	BitBucketService:  true,
	CassandraService:  true,
	ConfluenceService: true,
	JIRAService:       true,
	KafkaService:      true,
}

// InputSupported tells whether Glouton can collect metrics for this service type, with an
// input or through JMX.
func InputSupported(serviceType ServiceName) bool {
	return inputSupportedServiceTypes[serviceType] || jmxServiceTypes[serviceType]
}

// inputOutcome is what configureMetricInputs could do for a service: the paths ending with no
// input mean very different things to a user, such as an unsupported type or a missing
// password.
type inputOutcome int

const (
	// inputRegistered means an input was registered; its scrape state tells whether it works.
	inputRegistered inputOutcome = iota
	// inputInactive means the service is not Active, which is not "not running" (see
	// ServicesState). It is never reported: ServicesState leaves inactive services out.
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

// Messages reported as a service's error when nothing collects its metrics. They are fixed:
// createInput's error text comes from Telegraf and may quote the configuration, such as a
// MySQL DSN with its password.
const (
	msgCreateFailed        = "failed to create the input"
	msgMissingInfo         = msgCreateFailed + ": missing credentials or address"
	msgMissingCommand      = msgCreateFailed + ": required command not found in the container"
	msgUnexpectedType      = msgCreateFailed + ": unexpected input type"
	msgUnsupportedPlatform = "input not available on this platform"
	msgIgnored             = "metrics disabled by configuration"
	msgJMXDisabled         = "metrics are collected through JMX, which is disabled"
	msgJMXNoPort           = "metrics are collected through JMX: jmx_port is not set for this service"
	msgJMXNoAddress        = "metrics are collected through JMX: no address found for this service"
)

// inputResult is what configureMetricInputs records per service: the outcome, and the
// message to report when that outcome left the service without an input.
type inputResult struct {
	outcome inputOutcome
	message string
}

// newInputResult builds the inputResult of an outcome. Outcomes that the service type or the
// Container object already explain get no message.
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

// scrapeStateProvider is implemented by registry registrations. It is asserted at the call
// site, like the registry's diagnosticer, rather than added to types.Registration.
type scrapeStateProvider interface {
	LastScrapeState() types.ScrapeState
}

// JMXStateProvider tells how jmxtrans collects a service's metrics, which go through no
// registry registration.
type JMXStateProvider interface {
	ServiceScrapeState(service NameInstance) types.ScrapeState
}

// SetJMXState gives the JMX collection state, to report the services monitored through
// JMX. It is only set when JMX is enabled.
func (d *Discovery) SetJMXState(provider JMXStateProvider) {
	d.l.Lock()
	defer d.l.Unlock()

	d.jmxState = provider
	d.publishServicesState()
}

// jmxScrapeState is the scrape state of one service collected through JMX.
type jmxScrapeState struct {
	provider JMXStateProvider
	service  NameInstance
}

func (s jmxScrapeState) LastScrapeState() types.ScrapeState {
	return s.provider.ServiceScrapeState(s.service)
}

// ServiceState is what Glouton knows about one service, so that Bleemeo can tell a user why
// it is or is not monitored. The synchronizer adds the service UUID, from its API cache.
type ServiceState struct {
	// Name tells apart custom checks, which all share ServiceType CustomService.
	Name          string      `json:"name"`
	ServiceType   ServiceName `json:"type"`
	Instance      string      `json:"instance"`
	ContainerName string      `json:"container_name"`
	// Dynamic tells whether discovery found this service, as opposed to it being declared
	// in the configuration.
	Dynamic bool `json:"dynamic_discovery"`
	// InputSupported tells whether Glouton can collect metrics for this service type at all. It
	// is a property of the type, true even when collection failed here, so that Bleemeo need not
	// know which Glouton version supports which service.
	InputSupported bool `json:"input_supported"`
	// Error is the last error reported by the input or, when Glouton has no input for
	// this service, why: missing credentials, unsupported platform, ... It is empty when
	// there is nothing to report.
	Error string `json:"error,omitempty"`
	// MetricFamiliesBucket is the number of metric families (names, not series) of the last
	// scrape, as 0, 1 or 2 meaning "two or more": whether the input produces anything, kept
	// coarse so that the value stays stable.
	MetricFamiliesBucket int `json:"metrics"`
	// MetricsIgnored tells whether the service's metrics are disabled by the configuration.
	// Error then says so, but reports a choice the user made rather than a failure. Not
	// sent: Error already tells Bleemeo.
	MetricsIgnored bool `json:"-"`
}

// ServicesState returns what Glouton knows about each active service, sorted so that the
// result can be hashed. Services declared in the configuration are included.
//
// Active is not "running" (see setServiceActiveAndContainer): a stopped daemon whose binary
// still exists stays active, and is reported with its input's error.
func (d *Discovery) ServicesState() []ServiceState {
	// From the snapshot, not under d.l: a discovery run holds d.l while it builds inputs,
	// which can take seconds.
	d.servicesStateL.Lock()
	result := slices.Clone(d.servicesStateSnapshot.states)
	providers := d.servicesStateSnapshot.pending
	d.servicesStateL.Unlock()

	// Read outside d.l, so that discovery never waits on a registration's lock.
	for i, pending := range providers {
		if pending.provider == nil {
			continue
		}

		result[i].Error, result[i].MetricFamiliesBucket = scrapeResult(pending.provider, pending.secrets)
	}

	return result
}

// pendingScrape is a scrape state still to read, with the secrets its error may quote.
type pendingScrape struct {
	provider scrapeStateProvider
	secrets  []string
}

// servicesStateSnapshot is the report as of the last change to discovery's state, with the
// scrape state each entry still has to read.
type servicesStateSnapshot struct {
	states  []ServiceState
	pending []pendingScrape
}

// publishServicesState updates the snapshot ServicesState reads. Must be called with d.l
// held, after any change to the services, their inputs or the JMX state.
//
// The snapshot is sorted here rather than on each read: the order only depends on the
// identifying fields, which reading the scrape states leaves alone.
func (d *Discovery) publishServicesState() {
	states, pending := d.buildServicesState()

	order := make([]int, len(states))
	for i := range order {
		order[i] = i
	}

	slices.SortFunc(order, func(a, b int) int { return compareServiceState(states[a], states[b]) })

	snapshot := servicesStateSnapshot{
		states:  make([]ServiceState, 0, len(order)),
		pending: make([]pendingScrape, 0, len(order)),
	}

	for _, i := range order {
		snapshot.states = append(snapshot.states, states[i])
		snapshot.pending = append(snapshot.pending, pending[i])
	}

	d.servicesStateL.Lock()
	d.servicesStateSnapshot = snapshot
	d.servicesStateL.Unlock()
}

// buildServicesState builds the report from discovery's own state, with the scrape state
// each entry still has to read (a nil provider when none). Must be called with d.l held.
func (d *Discovery) buildServicesState() ([]ServiceState, []pendingScrape) {
	result := make([]ServiceState, 0, len(d.servicesMap))
	providers := make([]pendingScrape, 0, len(d.servicesMap))

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

		// No result: configureMetricInputs has not run for this service yet.
		input, hasResult := d.inputResult[key]

		var pending pendingScrape

		switch {
		case !hasResult:
		case input.outcome == inputRegistered:
			if collector, ok := d.activeCollector[key]; ok {
				pending.provider, _ = collector.gathererRegistration.(scrapeStateProvider)
				pending.secrets = serviceSecrets(service.Config)
			}
		case input.outcome == inputUnsupportedType || input.outcome == inputCheckOnly:
			// jmxtrans collects the metrics of any service with a jmx_port. InputSupported
			// stays the type's: jmxServiceTypes covers the types only JMX collects.
			switch {
			case d.jmxState != nil && service.Config.JMXPort != 0 && service.IPAddress == "":
				// jmxtrans skips a service it has no address for.
				state.Error = msgJMXNoAddress
			case d.jmxState != nil && service.Config.JMXPort != 0:
				pending.provider = jmxScrapeState{
					provider: d.jmxState,
					service:  NameInstance{Name: service.Name, Instance: service.Instance},
				}
				pending.secrets = serviceSecrets(service.Config)
			case !jmxServiceTypes[service.ServiceType]:
			case d.jmxState == nil:
				state.Error = msgJMXDisabled
			default:
				state.Error = msgJMXNoPort
			}
		default:
			state.Error = input.message
			state.MetricsIgnored = input.outcome == inputIgnored
		}

		result = append(result, state)
		providers = append(providers, pending)
	}

	return result, providers
}

// compareServiceState orders on every identifying field: the input comes from map iteration,
// and ties would make the hash differ between calls.
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

// maxErrorLength bounds the error reported per service: a Prometheus MultiError grows with
// the number of offending metrics.
const maxErrorLength = 500

// truncateError bounds an error message, saying so when it was cut.
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

// localAddressRegex matches the local address of a net.OpError, as in
// "read tcp 127.0.0.1:52431->127.0.0.1:3306", anchored on the network name.
var localAddressRegex = regexp.MustCompile(`\b((?:tcp|udp|ip|unix)(?:4|6|gram|packet)?) [^\s>]*->`)

// stabilizeError removes what changes between scrapes while the problem stays the same: the
// local address of a network error, whose port is ephemeral, and the order of joined errors,
// which concurrent inputs such as RabbitMQ add differently each time (lines are sorted and
// deduplicated).
func stabilizeError(message string) string {
	message = localAddressRegex.ReplaceAllString(message, "$1 ")

	lines := strings.Split(message, "\n")
	slices.Sort(lines)

	return strings.Join(slices.Compact(lines), "\n")
}

// urlPasswordRegex matches the password of a URL quoted in an error, "://user:password@", up
// to the last "@" before a "/", a quote or a newline.
var urlPasswordRegex = regexp.MustCompile(`(://[^/\s:@"]*):[^/"\n]*@`)

// redactError hides the passwords an input's error may quote, as Telegraf's HAProxy input
// does with a stats URL that fails to parse. secrets, from serviceSecrets, are replaced
// wherever they appear: the URL pattern cannot find a password holding a "/" or a quote, nor
// the prefix Go's URL parser quotes as a port. The pattern covers passwords that are not in
// the configuration.
func redactError(message string, secrets []string) string {
	for _, secret := range secrets {
		message = strings.ReplaceAll(message, secret, "xxxxx")
	}

	return urlPasswordRegex.ReplaceAllString(message, "$1:xxxxx@")
}

// minSecretLength is the shortest secret redactError replaces: a shorter one would also
// replace ordinary text.
const minSecretLength = 3

// serviceSecrets returns the secrets of a service's configuration, in each form an error may
// quote them, longest first so that a secret is replaced before its prefix.
func serviceSecrets(cfg config.Service) []string {
	var secrets []string

	add := func(secret string) {
		if len(secret) >= minSecretLength {
			secrets = append(secrets, secret)
		}
	}

	passwords := append([]string{cfg.Password, cfg.JMXPassword}, urlSecrets(cfg.StatsURL)...)

	for _, password := range passwords {
		add(password)

		// As quoted by %q, which Telegraf and net/url use for URLs.
		if quoted := strconv.Quote(password); len(quoted) >= 2 {
			add(quoted[1 : len(quoted)-1])
		}

		// What Go's URL parser reads as a port, and quotes, when the password holds a "/",
		// "?" or "#": they end the authority before the "@".
		if i := strings.IndexAny(password, "/?#"); i >= 0 {
			add(password[:i])
		}
	}

	// Equal secrets must end up next to each other for Compact to drop them.
	slices.SortFunc(secrets, func(a, b string) int {
		if c := cmp.Compare(len(b), len(a)); c != 0 {
			return c
		}

		return strings.Compare(a, b)
	})

	return slices.Compact(secrets)
}

// urlSecrets returns the password of "scheme://user:password@host" and the values of
// secret-looking query parameters, also decoded. The URL is not parsed: one quoted in an
// error often does not parse.
func urlSecrets(rawURL string) []string {
	_, rest, ok := strings.Cut(rawURL, "://")
	if !ok {
		return nil
	}

	var secrets []string

	if password, ok := userinfoPassword(rest); ok {
		secrets = append(secrets, password)
	}

	_, query, _ := strings.Cut(rest, "?")
	query, _, _ = strings.Cut(query, "#")

	for pair := range strings.SplitSeq(query, "&") {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || !isSecretParameter(key) {
			continue
		}

		secrets = append(secrets, value)

		if decoded, err := url.QueryUnescape(value); err == nil && decoded != value {
			secrets = append(secrets, decoded)
		}
	}

	return secrets
}

// userinfoPassword returns the password of "user:password@host...", the URL after its scheme.
// The userinfo ends at the last "@", as a password may hold one, unless that "@" is in a query
// value such as an e-mail address: after a "?" and an "=", which a password rarely holds both.
func userinfoPassword(rest string) (string, bool) {
	at := strings.LastIndex(rest, "@")

	if q := strings.Index(rest[:max(at, 0)], "?"); q >= 0 && strings.Contains(rest[q:at], "=") {
		at = strings.LastIndex(rest[:q], "@")
	}

	if at < 0 {
		return "", false
	}

	_, password, ok := strings.Cut(rest[:at], ":")

	return password, ok
}

// isSecretParameter tells whether a URL query parameter is known to hold a secret. Whole
// names only, so that ordinary ones such as "keyspace" or "bypass" are left out. InfluxDB
// 1.x takes its password as "p".
func isSecretParameter(key string) bool {
	switch strings.ToLower(key) {
	case "access_token", "api_key", "apikey", "auth", "key", "p", "pass", "passwd", "password", "pwd", "secret", "token":
		return true
	default:
		return false
	}
}

// scrapeResult reads the error and metric bucket of a service whose input was registered.
// An input that has not run yet reports neither.
func scrapeResult(provider scrapeStateProvider, secrets []string) (string, int) {
	scrape := provider.LastScrapeState()
	if !scrape.HasRun {
		return "", 0
	}

	// Stabilized first, as redacting could hide what stabilizeError looks for (a password
	// "tcp"); truncated last, so that the cut is always at the same place.
	return truncateError(redactError(stabilizeError(scrape.Error), secrets)), metricsBucket(scrape.FamiliesCount)
}

// metricsBucket reduces a count to 0, 1 or 2, where 2 means "two or more". See
// ServiceState.MetricFamiliesBucket for what is counted and why it is not exact.
func metricsBucket(count int) int {
	if count <= 0 {
		return 0
	}

	return min(count, 2)
}
