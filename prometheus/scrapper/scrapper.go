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

package scrapper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/version"

	dto "github.com/prometheus/client_model/go"
	prometheusModel "github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/textparse"
	"google.golang.org/protobuf/proto"
)

const defaultGatherTimeout = 10 * time.Second

var errParseError = errors.New("text format parsing error: ")

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

	if e.DecodeErr != nil {
		return e.DecodeErr.Error()
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

	return t.GatherWithState(ctx, registry.GatherState{T0: time.Now()})
}

func (t *Target) GatherWithState(ctx context.Context, state registry.GatherState) ([]*dto.MetricFamily, error) {
	u := t.URL

	logger.V(2).Printf("Scrapping Prometheus exporter %s", u.String())

	body, err := t.readAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("read from %s: %w", u.String(), err)
	}

	return parserReader(body, state.HintMetricFilter)
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

func parserReader(data []byte, filter func(lbls labels.Labels) bool) ([]*dto.MetricFamily, error) {
	var (
		et  textparse.Entry
		err error
	)

	p, err := textparse.New(data, "text/plain", "", false, true, nil)
	if err != nil {
		return nil, err
	}

	result := make([]*dto.MetricFamily, 0)
	nameToIdx := make(map[string]int)

	for {
		et, err = p.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
			}

			break
		}

		var (
			tmp         []byte
			tmp2        []byte
			lset        labels.Labels
			metricName  string
			metricType  prometheusModel.MetricType
			metricHelp  string
			seriesTS    *int64
			seriesFloat float64
		)

		switch et { //nolint:exhaustive
		case textparse.EntryType:
			tmp, metricType = p.Type()
			metricName = string(tmp)
		case textparse.EntryHelp:
			tmp, tmp2 = p.Help()
			metricName = string(tmp)
			metricHelp = string(tmp2)
		case textparse.EntrySeries:
			_, seriesTS, seriesFloat = p.Series()
			p.Labels(&lset)
			metricName = lset.Get(types.LabelName)
		case textparse.EntryHistogram:
			// This is native histogram which we don't support
			continue
		default:
			continue
		}

		sort.Sort(lset)

		if lset != nil && filter != nil && !filter(lset) {
			continue
		}

		if metricName == "" {
			continue
		}

		idx, entryExists := nameToIdx[metricName]
		if !entryExists {
			idx = len(result)
			nameToIdx[metricName] = idx

			result = append(result, &dto.MetricFamily{
				Name: &metricName,
			})
		}

		entry := result[idx]

		switch et { //nolint:exhaustive
		case textparse.EntryType:
			// Can only set type before receiving first sample and can't be updated
			if entry.Metric != nil {
				return nil, fmt.Errorf("%w: TYPE for metric %s reported after samples", errParseError, metricName)
			}

			if entry.Type != nil {
				return nil, fmt.Errorf("%w: second TYPE line for metric %s", errParseError, metricName)
			}

			switch metricType { //nolint:exhaustive
			case prometheusModel.MetricTypeCounter:
				entry.Type = dto.MetricType_COUNTER.Enum()
			case prometheusModel.MetricTypeGauge:
				entry.Type = dto.MetricType_GAUGE.Enum()
			case prometheusModel.MetricTypeHistogram:
				entry.Type = dto.MetricType_HISTOGRAM.Enum()
			case prometheusModel.MetricTypeSummary:
				entry.Type = dto.MetricType_SUMMARY.Enum()
			default:
				entry.Type = dto.MetricType_UNTYPED.Enum()
			}
		case textparse.EntryHelp:
			// Help can we set later but not updated
			if entry.Help != nil {
				return nil, fmt.Errorf("%w: second HELP line for metric %s", errParseError, metricName)
			}

			entry.Help = &metricHelp
		case textparse.EntrySeries:
			metric := &dto.Metric{
				Label: model.Labels2DTO(lset),
			}

			if seriesTS != nil {
				metric.TimestampMs = proto.Int64(*seriesTS)
			}

			if entry.Type == nil {
				entry.Type = dto.MetricType_UNTYPED.Enum()
			}

			switch entry.GetType().String() {
			case dto.MetricType_COUNTER.Enum().String():
				metric.Counter = &dto.Counter{Value: proto.Float64(seriesFloat)}
			case dto.MetricType_GAUGE.Enum().String():
				metric.Gauge = &dto.Gauge{Value: proto.Float64(seriesFloat)}
			case dto.MetricType_UNTYPED.Enum().String():
				fallthrough
			default:
				metric.Untyped = &dto.Untyped{Value: proto.Float64(seriesFloat)}
			}

			entry.Metric = append(entry.Metric, metric)
		}
	}

	// Few fixup:
	// * Remove all empty family
	// * Remove type of histogram & summary metrics
	i := 0

	for _, mf := range result {
		if len(mf.GetMetric()) > 0 {
			if mf.GetType().String() == dto.MetricType_SUMMARY.String() || mf.GetType().String() == dto.MetricType_HISTOGRAM.String() {
				mf.Type = dto.MetricType_UNTYPED.Enum()
			}

			if mf.Type == nil {
				mf.Type = dto.MetricType_UNTYPED.Enum()
			}

			result[i] = mf
			i++
		}
	}

	result = result[:i]

	return result, err
}
