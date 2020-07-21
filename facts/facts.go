// Copyright 2015-2019 Bleemeo
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

package facts

import (
	"context"
	"glouton/logger"
	"glouton/version"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/host"
	"github.com/shirou/gopsutil/mem"
	psutilNet "github.com/shirou/gopsutil/net"
	"gopkg.in/yaml.v3"
)

// FactProvider provider information about system. Mostly static facts like OS version, architecture, ...
//
// It also possible to define fixed facts that this provider won't discover. This is useful for
// fact like "featureX_enabled" that other part of the code may set.
//
// There is also the possibility to add callback that are called on each update.
//
// There is one special fact named "fact_updated_at" which contains the last update of the facts.
type FactProvider struct {
	l sync.Mutex

	factPath       string
	hostRootPath   string
	ipIndicatorURL string

	manualFact map[string]string
	callbacks  []FactCallback

	facts           map[string]string
	lastFactsUpdate time.Time
}

// FactCallback is a function called on each update of facts that may return additional facts.
//
// It returns the list of new or updated facts.
type FactCallback func(ctx context.Context, currentFact map[string]string) map[string]string

// NewFacter creates a new Fact provider
//
// factPath is the path to a yaml file that contains additional facts, usually
// facts that require root privilege to be read.
//
// hostRootPath is the path where host filesystem is visible. When running outside
// any container, it should be "/". When running inside a container it should be the path
// where host root is mounted.
//
// ipIndicatorURL is and URL which return the public IP.
func NewFacter(factPath, hostRootPath, ipIndicatorURL string) *FactProvider {
	return &FactProvider{
		factPath:       factPath,
		hostRootPath:   hostRootPath,
		ipIndicatorURL: ipIndicatorURL,
	}
}

// AddCallback adds a FactCallback to provide additional facts.
// It currently not possible to remove a callback.
func (f *FactProvider) AddCallback(cb FactCallback) {
	f.l.Lock()
	defer f.l.Unlock()

	f.callbacks = append(f.callbacks, cb)
}

// Facts returns the list of facts for this system.
func (f *FactProvider) Facts(ctx context.Context, maxAge time.Duration) (facts map[string]string, err error) {
	f.l.Lock()
	defer f.l.Unlock()

	if time.Since(f.lastFactsUpdate) > maxAge {
		f.updateFacts(ctx)
	}

	return f.facts, nil
}

// SetFact override/add a manual facts
//
// Any fact set using this method is valid until next call to SetFact.
func (f *FactProvider) SetFact(key string, value string) {
	f.l.Lock()
	defer f.l.Unlock()

	if f.manualFact == nil {
		f.manualFact = make(map[string]string)
	}

	if f.facts == nil {
		f.facts = make(map[string]string)
	}

	f.manualFact[key] = value
	f.facts[key] = value
}

func (f *FactProvider) updateFacts(ctx context.Context) {
	newFacts := make(map[string]string)

	// get a copy of callbacks while lock is held
	callbacks := make([]FactCallback, len(f.callbacks))
	copy(callbacks, f.callbacks)

	if f.factPath != "" {
		if data, err := ioutil.ReadFile(f.factPath); err != nil {
			logger.V(1).Printf("unable to read fact file: %v", err)
		} else {
			var fileFacts map[string]string

			if err := yaml.Unmarshal(data, &fileFacts); err != nil {
				logger.V(1).Printf("fact file is invalid: %v", err)
			} else {
				for k, v := range fileFacts {
					newFacts[k] = v
				}
			}
		}
	}

	for k, v := range f.platformFacts() {
		newFacts[k] = v
	}

	primaryAddress, primaryMacAddress := f.primaryAddress(ctx)
	newFacts["primary_address"] = primaryAddress
	newFacts["primary_mac_address"] = primaryMacAddress

	if f.ipIndicatorURL != "" {
		newFacts["public_ip"] = urlContent(ctx, f.ipIndicatorURL)
	}

	newFacts["architecture"] = runtime.GOARCH

	hostname, fqdn := getFQDN(ctx)
	newFacts["fqdn"] = fqdn
	newFacts["hostname"] = hostname

	if strings.Contains(fqdn, ".") {
		l := strings.SplitN(fqdn, ".", 2)
		newFacts["domain"] = l[1]
	}

	vType, vRole, err := host.VirtualizationWithContext(ctx)
	if err == nil && vRole == "guest" {
		if vType == "vbox" {
			vType = "virtualbox"
		}

		newFacts["virtual"] = vType
	} else {
		newFacts["virtual"] = guessVirtual(newFacts)
	}

	// while this could also identify a Surface device, it is much more probable that this is a VM (or maybe a container) on Microsoft Azure
	if newFacts["system_vendor"] == "Microsoft Corporation" {
		for k, v := range azureFacts(ctx) {
			newFacts[k] = v
		}
	}

	if strings.Contains(newFacts["bios_version"], "Google") {
		for k, v := range gceFacts(ctx) {
			newFacts[k] = v
		}
	}

	if strings.Contains(newFacts["bios_version"], "amazon") {
		for k, v := range awsFacts(ctx) {
			newFacts[k] = v
		}
	}

	if s, err := mem.SwapMemoryWithContext(ctx); err == nil {
		if s.Total > 0 {
			newFacts["swap_present"] = "true"
		} else {
			newFacts["swap_present"] = "false"
		}
	}

	if f.hostRootPath != "" {
		if v, err := ioutil.ReadFile(filepath.Join(f.hostRootPath, "etc/timezone")); err == nil {
			newFacts["timezone"] = strings.TrimSpace(string(v))
		}
	}

	newFacts["glouton_version"] = version.Version
	// TODO: drop agent_version. It's deprecated and is replaced by glouton_version
	newFacts["agent_version"] = version.Version
	newFacts["fact_updated_at"] = time.Now().UTC().Format(time.RFC3339)

	for _, c := range callbacks {
		for k, v := range c(ctx, newFacts) {
			newFacts[k] = v
		}
	}

	for k, v := range f.manualFact {
		newFacts[k] = v
	}

	for k, v := range newFacts {
		if v == "" {
			delete(newFacts, k)
		}
	}

	f.facts = newFacts
	f.lastFactsUpdate = time.Now()
}

func getFQDN(ctx context.Context) (hostname string, fqdn string) {
	hostname, _ = os.Hostname()

	fqdn, err := net.DefaultResolver.LookupCNAME(ctx, hostname)
	if err != nil {
		fqdn = hostname
	}

	if fqdn == "" {
		// With pure-Go resolver, it may happen. Perform what C-resolver seems to do
		if addrs, err := net.DefaultResolver.LookupHost(ctx, hostname); err == nil && len(addrs) > 0 {
			if names, err := net.DefaultResolver.LookupAddr(ctx, addrs[0]); err == nil && len(names) > 0 {
				fqdn = names[0]
			}
		}
	}

	if len(fqdn) > 0 && fqdn[len(fqdn)-1] == '.' {
		fqdn = fqdn[:len(fqdn)-1]
	}

	switch fqdn {
	case "", "localhost", "localhost.local", "localhost.localdomain":
		if hostname != "localhost" {
			fqdn = hostname
		}
	}

	return
}

func decodeOsRelease(data string) (map[string]string, error) {
	result := make(map[string]string)

	lines := strings.Split(data, "\n")
	for _, line := range lines {
		if line == "" || !strings.Contains(line, "=") {
			continue
		}

		t := strings.SplitN(line, "=", 2)
		key := t[0]

		if t[1] == "" {
			continue
		}

		if t[1][0] == '"' {
			value, err := strconv.Unquote(t[1])
			if err != nil {
				return nil, err
			}

			result[key] = value
		} else {
			result[key] = t[1]
		}
	}

	return result, nil
}

func guessVirtual(facts map[string]string) string {
	vendorName := strings.ToLower(facts["system_vendor"])
	biosVendor := strings.ToLower(facts["bios_vendor"])
	biosVersion := strings.ToLower(facts["bios_version"])

	switch {
	case strings.Contains(vendorName, "qemu"), strings.Contains(vendorName, "bochs"), strings.Contains(vendorName, "digitalocean"):
		return "kvm"
	case strings.Contains(vendorName, "xen"):
		if strings.Contains(biosVersion, "amazon") {
			return "aws"
		}

		return "xen"
	case strings.Contains(vendorName, "amazon ec2"):
		return "aws"
	case strings.Contains(vendorName, "innotek"):
		return "virtualbox"
	case strings.Contains(vendorName, "microsoft"):
		return "hyper-v"
	case strings.Contains(vendorName, "google"):
		return "gce"
	case strings.Contains(vendorName, "vmware"):
		return "vmware"
	case strings.Contains(vendorName, "openstack"):
		switch {
		case strings.Contains(biosVendor, "bochs"):
			return "kvm"
		case strings.Contains(strings.ToLower(facts["serial_number"]), "vmware"):
			return "vmware"
		default:
			return "openstack"
		}
	default:
		return "physical"
	}
}

func macAddressByAddress(ctx context.Context, ipAddress string) string {
	ifs, err := psutilNet.InterfacesWithContext(ctx)
	if err != nil {
		return ""
	}

	for _, i := range ifs {
		for _, a := range i.Addrs {
			if a.Addr == ipAddress {
				return i.HardwareAddr
			}
		}
	}

	return ""
}

func urlContent(ctx context.Context, url string) string {
	return httpQuery(ctx, url, []string{})
}

func httpQuery(ctx context.Context, url string, headers []string) string {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return ""
	}

	for _, h := range headers {
		splits := strings.SplitN(h, ":", 2)
		if len(splits) != 2 {
			continue
		}

		req.Header.Add(splits[0], splits[1])
	}

	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		return ""
	}

	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	return string(body)
}
