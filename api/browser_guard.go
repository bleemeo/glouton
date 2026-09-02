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

package api

import (
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/bleemeo/glouton/logger"
)

// browserGuard returns a middleware rejecting the requests a browser made on
// behalf of another website.
//
// The local API has no authentication, so any website the user visits could
// otherwise make their browser fetch http://localhost:8015/data/config,
// /data/logs or /diagnostic.zip and send the answer back to its own server.
//
// Only browsers send Sec-Fetch-Site, and only browsers can be lured into
// making such a request: requests without that header (Prometheus scrapers,
// curl, Grafana...) are served as before. This is therefore not an
// authentication: it doesn't protect a listener reachable from the network.
func browserGuard(allowedHosts []string) func(http.Handler) http.Handler {
	allowed := normalizeHosts(allowedHosts)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if reason, hint := denyBrowserRequest(r, allowed); reason != "" {
				logger.V(1).Printf("API: refusing %s %s: %s", r.Method, r.URL.Path, reason)
				http.Error(w, "Glouton refused this request: "+reason+".\n"+hint, http.StatusForbidden)

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// denyBrowserRequest returns why the request must be refused, together with a
// hint at what to do about it, or two empty strings when it may proceed.
func denyBrowserRequest(r *http.Request, allowedHosts []string) (reason string, hint string) {
	site := r.Header.Get("Sec-Fetch-Site")
	if site == "" {
		// Not a browser, or a browser too old to tell us. Nothing to check
		// here: the absence of CORS headers is what keeps a website from
		// reading our answers on those browsers.
		return "", ""
	}

	// The name the browser used must be one we know we answer to. Any other
	// name points to us only because an attacker's DNS said so (DNS
	// rebinding), and the browser then believes its own origin is us.
	if !isAllowedHost(r.Host, allowedHosts) {
		return fmt.Sprintf("unexpected Host %q", r.Host),
			"Only the browsers reaching Glouton by an IP address or by one of " +
				"web.listener.allowed_hosts are answered, because a website could otherwise " +
				"point that name at Glouton and read what it serves. If you reach Glouton " +
				"through a proxy, either make the proxy send the address Glouton listens on " +
				"as the Host header, or add this name to web.listener.allowed_hosts."
	}

	// The user typed the URL, or the page doing the request was served by us.
	if site == "none" || site == "same-origin" {
		return "", ""
	}

	// Another website is at the origin of this request. Following a link to
	// the local UI is fine: the browser displays the answer to the user and
	// the site that linked to it can't read it. A fetch, a script or a
	// frame, on the other hand, is an attempt to read what we serve.
	if r.Header.Get("Sec-Fetch-Mode") == "navigate" && r.Header.Get("Sec-Fetch-Dest") == "document" {
		return "", ""
	}

	return "request made on behalf of another website",
		"Glouton has no authentication: it only answers the browser requests coming " +
			"from the pages it served itself, so that no website can read the information " +
			"it exposes about this machine."
}

// normalizeHosts puts the configured host names in the form isAllowedHost
// compares against.
func normalizeHosts(hosts []string) []string {
	normalized := make([]string, 0, len(hosts))

	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
		if host != "" {
			normalized = append(normalized, host)
		}
	}

	return normalized
}

// isAllowedHost reports whether host, as found in the Host header, is one we
// may answer to.
func isAllowedHost(host string, allowedHosts []string) bool {
	name := host
	if hostOnly, _, err := net.SplitHostPort(host); err == nil {
		name = hostOnly
	}

	name = strings.ToLower(strings.TrimSuffix(name, "."))

	// A literal IP address can't be aimed at us by a DNS answer, so it needs
	// no allow-list: every address this machine answers on is fine.
	if net.ParseIP(strings.Trim(name, "[]")) != nil {
		return true
	}

	return slices.Contains(allowedHosts, name)
}
