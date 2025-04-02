// Copyright 2015-2025 Bleemeo
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

package common

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/crashreport"
)

var errHandshakeTimeout = errors.New("TLS handshake timeout")

// DiagnosticTCP return information about the ability to open a TCP connection to given host:port.
// If tlsConfig is not nil, open a TLS connection.
func DiagnosticTCP(host string, port int, tlsConfig *tls.Config) string {
	builder := &strings.Builder{}

	hostPort := fmt.Sprintf("%s:%d", host, port)

	conn, err := net.DialTimeout("tcp", hostPort, 5*time.Second)
	if err != nil {
		fmt.Fprintf(builder, "Unable to open TCP connection to %s: %v\n", hostPort, err)

		hostIP, err := net.ResolveTCPAddr("tcp", hostPort)
		if err != nil {
			fmt.Fprintf(builder, "Unable to resolve DNS name %s: %v\n", host, err)
		} else {
			fmt.Fprintf(
				builder,
				"%s resolve to %s: is your firewall blocking connection to %s on TCP port %d\n",
				host,
				hostIP.String(),
				hostIP.String(),
				port,
			)
		}

		return builder.String()
	}
	defer conn.Close()

	if tlsConfig != nil {
		diagnosticTLS(builder, tlsConfig, host, hostPort, conn)

		return builder.String()
	}

	fmt.Fprintf(
		builder,
		"Glouton is able to establish TCP connection to %s (%s). TLS is disabled in configuration\n",
		hostPort,
		conn.RemoteAddr().String(),
	)

	return builder.String()
}

func diagnosticTLS(builder io.Writer, tlsConfig *tls.Config, host string, hostPort string, rawConn net.Conn) {
	tlsConfig = tlsConfig.Clone()
	tlsConfig.ServerName = host

	errChannel := make(chan error, 2)
	timer := time.AfterFunc(5*time.Second, func() {
		errChannel <- errHandshakeTimeout
	})

	defer timer.Stop()

	tlsConn := tls.Client(rawConn, tlsConfig)

	go func() {
		defer crashreport.ProcessPanic()

		errChannel <- tlsConn.Handshake()
	}()

	if err := <-errChannel; err != nil {
		fmt.Fprintf(
			builder,
			"Glouton is NOT able to establish TLS connection to %s (%s): %v\n",
			hostPort,
			rawConn.LocalAddr().String(),
			err,
		)

		_ = rawConn.Close()
	} else {
		fmt.Fprintf(
			builder,
			"Glouton is able to establish TLS connection to %s (%s)\n",
			hostPort,
			rawConn.RemoteAddr().String(),
		)

		_ = tlsConn.Close()
	}
}

// DiagnosticHTTP return information about the ability to execute an HTTP request against the Bleemeo API.
func DiagnosticHTTP(apiClient *bleemeo.Client) string {
	req, err := apiClient.ParseRequest(http.MethodGet, "/v1/info/", nil, nil, nil)
	if err != nil {
		return fmt.Sprintf("can't make diagnostic request: %v\n", err)
	}

	urlWithoutPath := strings.TrimSuffix(req.URL.String(), "/v1/info/")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := apiClient.DoRequest(ctx, req, false)
	if err != nil {
		return fmt.Sprintf("Glouton is NOT able to perform HTTP request to %q: %v\n", urlWithoutPath, err)
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	return fmt.Sprintf("Glouton is able to perform HTTP request to %q\n", urlWithoutPath)
}
