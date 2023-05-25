// Copyright 2015-2023 Bleemeo
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

package scrapper

import (
	"bytes"
	"context"
	"fmt"
	"glouton/logger"
	"glouton/prometheus/registry"
	"glouton/types"
	"glouton/version"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

const defaultGatherTimeout = 10 * time.Second

type TargetError struct {
	// First 32kb of response
	PartialBody []byte
	StatusCode  int
	ConnectErr  error
	ReadErr     error
	DecodeErr   error
}

func (e TargetError) Error() string {
	if e.ReadErr != nil {
		return e.ReadErr.Error()
	}

	if e.ConnectErr != nil {
		return e.ConnectErr.Error()
	}

	return fmt.Sprintf("unexpected HTTP status %d", e.StatusCode)
}

// Target is an URL to scrape.
type Target struct {
	URL             *url.URL
	AllowList       []string
	DenyList        []string
	Rules           []types.SimpleRule
	ExtraLabels     map[string]string
	ContainerLabels map[string]string
	mockResponse    []byte
}

func NewMock(content []byte, extraLabels map[string]string) *Target {
	return &Target{
		ExtraLabels:  extraLabels,
		mockResponse: content,
		URL: &url.URL{
			Scheme: "mock",
		},
	}
}

func New(u *url.URL, extraLabels map[string]string) *Target {
	return &Target{
		ExtraLabels: extraLabels,
		URL:         u,
	}
}

// HostPort return host:port.
func HostPort(u *url.URL) string {
	if u.Scheme == "file" || u.Scheme == "" {
		return ""
	}

	hostname := u.Hostname()
	port := u.Port()

	return hostname + ":" + port
}

// Gather implement prometheus.Gatherer.
func (t *Target) Gather() ([]*dto.MetricFamily, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultGatherTimeout)
	defer cancel()

	return t.GatherWithState(ctx, registry.GatherState{})
}

func (t *Target) GatherWithState(ctx context.Context, _ registry.GatherState) ([]*dto.MetricFamily, error) {
	u := t.URL

	logger.V(2).Printf("Scrapping Prometheus exporter %s", u.String())

	body, err := t.readAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("read from %s: %w", u.String(), err)
	}

	reader := bytes.NewReader(body)

	var parser expfmt.TextParser

	resultMap, err := parser.TextToMetricFamilies(reader)
	if err != nil {
		return nil, TargetError{
			DecodeErr: err,
		}
	}

	result := make([]*dto.MetricFamily, 0, len(resultMap))

	for _, family := range resultMap {
		result = append(result, family)
	}

	return result, nil
}

func (t *Target) readAll(ctx context.Context) ([]byte, error) {
	if t.URL.Scheme == "file" || t.URL.Scheme == "" {
		return os.ReadFile(t.URL.Path)
	}

	if t.URL.Scheme == "mock" {
		return t.mockResponse, nil
	}

	req, err := http.NewRequest(http.MethodGet, t.URL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("prepare request to Prometheus exporter %s: %w", t.URL.String(), err)
	}

	req.Header.Add("Accept", "text/plain;version=0.0.4")
	req.Header.Set("User-Agent", version.UserAgent())

	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		return nil, TargetError{
			ConnectErr: err,
		}
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		buffer, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024))

		// Ensure response body is read to allow HTTP keep-alive to works
		_, _ = io.Copy(io.Discard, resp.Body)

		return nil, TargetError{
			PartialBody: buffer,
			StatusCode:  resp.StatusCode,
			ReadErr:     err,
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		err = TargetError{
			ReadErr: err,
		}
	}

	return body, err
}
