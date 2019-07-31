package check

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"agentgo/logger"
	"agentgo/types"
)

// HTTPCheck perform a HTTP check
type HTTPCheck struct {
	*baseCheck

	url                string
	expectedStatusCode int
	client             *http.Client
}

// NewHTTP create a new HTTP check.
//
// For each persitentAddresses (in the format "IP:port") this checker will maintain a TCP connection open, if broken (and unable to re-open),
// the check will be immediately run.
//
// If expectedStatusCode is 0, StatusCode below 400 will generate Ok, between 400 and 499 => warning and above 500 => critical
// If expectedStatusCode is not 0, StatusCode must match the value or result will be critical
func NewHTTP(urlValue string, persitentAddresses []string, expectedStatusCode int, metricName string, item string, acc accumulator) *HTTPCheck {
	myTransport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec
		},
	}
	mainTCPAddress := ""
	if u, err := url.Parse(urlValue); err != nil {
		port := u.Port()
		if port == "" && u.Scheme == "http" {
			port = "80"
		} else if port == "" && u.Scheme == "https" {
			port = "443"
		}
		mainTCPAddress = fmt.Sprintf("%s:%s", u.Hostname(), port)
	}
	hc := &HTTPCheck{
		url:                urlValue,
		expectedStatusCode: expectedStatusCode,
		client: &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: myTransport,
		},
	}
	hc.baseCheck = newBase(mainTCPAddress, persitentAddresses, true, hc.doCheck, metricName, item, acc)
	return hc
}

func (hc *HTTPCheck) doCheck(ctx context.Context) types.StatusDescription {
	req, err := http.NewRequest("GET", hc.url, nil)
	if err != nil {
		logger.V(2).Printf("Unable to create HTTP Request: %v", err)
		return types.StatusDescription{
			CurrentStatus:     types.StatusOk,
			StatusDescription: "Checker error. Unable to create Request",
		}
	}
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := hc.client.Do(req.WithContext(ctx2))
	if urlErr, ok := err.(*url.Error); ok && urlErr.Timeout() {
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: "Connection timed out after 10 seconds",
		}
	}
	if err != nil {
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: "Connection refused",
		}
	}
	defer resp.Body.Close()
	if hc.expectedStatusCode != 0 && resp.StatusCode != hc.expectedStatusCode {
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: fmt.Sprintf("HTTP CRITICAL - http_code=%d (expected %d)", resp.StatusCode, hc.expectedStatusCode),
		}
	}
	if hc.expectedStatusCode == 0 && resp.StatusCode >= 500 {
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: fmt.Sprintf("HTTP CRITICAL - http_code=%d", resp.StatusCode),
		}
	}
	if hc.expectedStatusCode == 0 && resp.StatusCode >= 400 {
		return types.StatusDescription{
			CurrentStatus:     types.StatusWarning,
			StatusDescription: fmt.Sprintf("HTTP WARN - http_code=%d", resp.StatusCode),
		}
	}
	return types.StatusDescription{
		CurrentStatus:     types.StatusOk,
		StatusDescription: fmt.Sprintf("HTTP OK - http_code=%d", resp.StatusCode),
	}
}