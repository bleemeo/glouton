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
