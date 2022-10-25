// Copyright 2015-2022 Bleemeo
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

package mqtt

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"glouton/logger"
	"os"
)

var errNotPem = errors.New("not a PEM file")

func TLSConfig(skipVerify bool, caFile string) *tls.Config {
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: skipVerify, //nolint:gosec // G402: TLS InsecureSkipVerify set true.
	}

	if caFile != "" {
		if rootCAs, err := loadRootCAs(caFile); err != nil {
			logger.Printf("Unable to load CAs from %#v", caFile)
		} else {
			tlsConfig.RootCAs = rootCAs
		}
	}

	return tlsConfig
}

func loadRootCAs(caFile string) (*x509.CertPool, error) {
	rootCAs := x509.NewCertPool()

	certs, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}

	ok := rootCAs.AppendCertsFromPEM(certs)
	if !ok {
		return nil, errNotPem
	}

	return rootCAs, nil
}
