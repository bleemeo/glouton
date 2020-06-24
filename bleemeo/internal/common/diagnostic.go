package common

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"glouton/version"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"strings"
	"time"
)

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
		errChannel <- errors.New("TLS handshake timeout")
	})

	defer timer.Stop()

	tlsConn := tls.Client(rawConn, tlsConfig)

	go func() {
		errChannel <- tlsConn.Handshake()
	}()

	err := <-errChannel
	if err != nil {
		fmt.Fprintf(
			builder,
			"Glouton is NOT able to establish TLS connection to %s (%s): %v\n",
			hostPort,
			rawConn.LocalAddr().String(),
			err,
		)
		rawConn.Close()
	} else {
		fmt.Fprintf(
			builder,
			"Glouton is able to establish TLS connection to %s (%s)\n",
			hostPort,
			rawConn.LocalAddr().String(),
		)
		tlsConn.Close()
	}
}

// DiagnosticHTTP return information about the ability to do a HTTP request.
func DiagnosticHTTP(u string, tlsConfig *tls.Config) string {
	cl := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: tlsConfig,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return fmt.Sprintf("Bad URL %#v: %v\n", u, err)
	}

	req.Header.Add("X-Requested-With", "XMLHttpRequest")
	req.Header.Add("User-Agent", version.UserAgent())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req = req.WithContext(ctx)
	resp, err := cl.Do(req)

	if err != nil {
		return fmt.Sprintf("Glouton is NOT able to perform HTTP request to %#v: %v\n", u, err)
	}

	_, _ = io.Copy(ioutil.Discard, resp.Body)
	defer resp.Body.Close()

	return fmt.Sprintf("Glouton is able to perform HTTP request to %#v\n", u)
}
