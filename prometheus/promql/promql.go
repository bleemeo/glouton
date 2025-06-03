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

package promql

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/bleemeo/glouton/logger"

	"github.com/go-chi/chi/v5"
	"github.com/grafana/regexp"
	jsoniter "github.com/json-iterator/go"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/util/annotations"
	"github.com/prometheus/prometheus/util/httputil"
	"github.com/prometheus/prometheus/util/stats"
)

type errorType string

type status string

const (
	statusSuccess status = "success"
	statusError   status = "error"
)

type response struct {
	Status    status    `json:"status"`
	Data      any       `json:"data,omitempty"`
	ErrorType errorType `json:"errorType,omitempty"`
	Error     string    `json:"error,omitempty"`
	Warnings  []string  `json:"warnings,omitempty"`
}

const (
	errorTimeout  errorType = "timeout"
	errorCanceled errorType = "canceled"
	errorExec     errorType = "execution"
	errorBadData  errorType = "bad_data"
	errorInternal errorType = "internal"
	errorNotFound errorType = "not_found"
)

var (
	errStartTime        = errors.New("end timestamp must not be before start time")
	errPositiveInteger  = errors.New("zero or negative query resolution step widths are not accepted. Try a positive integer")
	errMaxStep          = errors.New("exceeded maximum resolution of 11,000 points per timeseries. Try decreasing the query resolution (?step=XX)")
	errParseDuration    = errors.New("cannot parse to a valid duration")
	errInvalidTimestamp = errors.New("cannot parse to a valid timestamp")
)

type PromQL struct {
	CORSOrigin *regexp.Regexp

	queryEngine *promql.Engine
}

type apiFunc func(r *http.Request, st storage.Queryable) apiFuncResult

// Register the API's endpoints in the given router.
func (p *PromQL) Register(st storage.Queryable) http.Handler {
	r := chi.NewRouter()

	p.init()

	wrap := func(f apiFunc) http.HandlerFunc {
		hf := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p.CORSOrigin != nil {
				httputil.SetCORS(w, p.CORSOrigin, r)
			}

			result := f(r, st)
			if result.finalizer != nil {
				defer result.finalizer()
			}

			if result.err != nil {
				p.respondError(w, result.err, result.data)

				return
			}

			if result.data != nil {
				p.respond(w, result.data, result.warnings)

				return
			}

			w.WriteHeader(http.StatusNoContent)
		})

		return httputil.CompressionHandler{
			Handler: hf,
		}.ServeHTTP
	}

	r.Get("/query_range", wrap(p.queryRange))
	r.Post("/query_range", wrap(p.queryRange))

	return r
}

func (p *PromQL) init() {
	opts := promql.EngineOpts{
		Logger:             logger.NewSlog().With("component", "query engine"),
		Reg:                nil,
		MaxSamples:         50000000,
		Timeout:            2 * time.Minute,
		ActiveQueryTracker: nil,
		LookbackDelta:      5 * time.Minute,
	}
	p.queryEngine = promql.NewEngine(opts)
}

type apiFuncResult struct {
	data      any
	err       *apiError
	warnings  annotations.Annotations
	finalizer func()
}

type queryData struct {
	ResultType parser.ValueType `json:"resultType"`
	Result     parser.Value     `json:"result"`
	Stats      stats.QueryStats `json:"stats,omitempty"`
}

type apiError struct {
	typ errorType
	err error
}

func parseTime(s string) (time.Time, error) {
	if t, err := strconv.ParseFloat(s, 64); err == nil {
		s, ns := math.Modf(t)
		ns = math.Round(ns*1000) / 1000

		return time.Unix(int64(s), int64(ns*float64(time.Second))).UTC(), nil
	}

	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}

	return time.Time{}, errInvalidTimestamp
}

func parseDuration(s string) (time.Duration, error) {
	if d, err := strconv.ParseFloat(s, 64); err == nil {
		ts := d * float64(time.Second)
		if ts > float64(math.MaxInt64) || ts < float64(math.MinInt64) {
			return 0, errParseDuration
		}

		return time.Duration(ts), nil
	}

	if d, err := model.ParseDuration(s); err == nil {
		return time.Duration(d), nil
	}

	return 0, errParseDuration
}

func returnAPIError(err error) *apiError {
	if err == nil {
		return nil
	}

	switch {
	case errors.Is(err, promql.ErrQueryCanceled("")):
		return &apiError{errorCanceled, err}
	case errors.Is(err, promql.ErrQueryTimeout("")):
		return &apiError{errorTimeout, err}
	case errors.Is(err, promql.ErrStorage{}):
		return &apiError{errorInternal, err}
	}

	return &apiError{errorExec, err}
}

func (p *PromQL) queryRange(r *http.Request, st storage.Queryable) (result apiFuncResult) {
	start, err := parseTime(r.FormValue("start"))
	if err != nil {
		err = fmt.Errorf("invalid parameter 'start': %w", err)

		return apiFuncResult{nil, &apiError{errorBadData, err}, nil, nil}
	}

	end, err := parseTime(r.FormValue("end"))
	if err != nil {
		err = fmt.Errorf("invalid parameter 'end': %w", err)

		return apiFuncResult{nil, &apiError{errorBadData, err}, nil, nil}
	}

	if end.Before(start) {
		return apiFuncResult{nil, &apiError{errorBadData, errStartTime}, nil, nil}
	}

	step, err := parseDuration(r.FormValue("step"))
	if err != nil {
		err = fmt.Errorf("invalid parameter 'step': %w", err)

		return apiFuncResult{nil, &apiError{errorBadData, err}, nil, nil}
	}

	if step <= 0 {
		return apiFuncResult{nil, &apiError{errorBadData, errPositiveInteger}, nil, nil}
	}

	// For safety, limit the number of returned points per timeseries.
	// This is sufficient for 60s resolution for a week or 1h resolution for a year.
	if end.Sub(start)/step > 11000 {
		return apiFuncResult{nil, &apiError{errorBadData, errMaxStep}, nil, nil}
	}

	ctx := r.Context()

	if to := r.FormValue("timeout"); to != "" {
		var cancel context.CancelFunc

		timeout, err := parseDuration(to)
		if err != nil {
			err = fmt.Errorf("invalid parameter 'timeout': %w", err)

			return apiFuncResult{nil, &apiError{errorBadData, err}, nil, nil}
		}

		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	qry, err := p.queryEngine.NewRangeQuery(ctx, st, nil, r.FormValue("query"), start, end, step)
	if err != nil {
		return apiFuncResult{nil, &apiError{errorBadData, err}, nil, nil}
	}
	// From now on, we must only return with a finalizer in the result (to
	// be called by the caller) or call qry.Close ourselves (which is
	// required in the case of a panic).
	defer func() {
		if result.finalizer == nil {
			qry.Close()
		}
	}()

	ctx = httputil.ContextFromRequest(ctx, r)

	res := qry.Exec(ctx)
	if res.Err != nil {
		return apiFuncResult{nil, returnAPIError(res.Err), res.Warnings, qry.Close}
	}

	// Optional stats field in response if parameter "stats" is not empty.
	var qs stats.QueryStats
	if r.FormValue("stats") != "" {
		qs = stats.NewQueryStats(qry.Stats())
	}

	return apiFuncResult{&queryData{
		ResultType: res.Value.Type(),
		Result:     res.Value,
		Stats:      qs,
	}, nil, res.Warnings, qry.Close}
}

func (p *PromQL) respond(w http.ResponseWriter, data any, warnings annotations.Annotations) {
	statusMessage := statusSuccess
	warningStrings := make([]string, 0, len(warnings))

	for _, warning := range warnings {
		warningStrings = append(warningStrings, warning.Error())
	}

	json := jsoniter.ConfigCompatibleWithStandardLibrary

	b, err := json.Marshal(&response{
		Status:   statusMessage,
		Data:     data,
		Warnings: warningStrings,
	})
	if err != nil {
		logger.V(1).Printf("Error marshaling PromQL json response: %v", err)

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(b); err != nil {
		logger.V(1).Printf("Error writing PromQL response: %v", err)
	}
}

func (p *PromQL) respondError(w http.ResponseWriter, apiErr *apiError, data any) {
	json := jsoniter.ConfigCompatibleWithStandardLibrary

	b, err := json.Marshal(&response{
		Status:    statusError,
		ErrorType: apiErr.typ,
		Error:     apiErr.err.Error(),
		Data:      data,
	})
	if err != nil {
		logger.V(1).Printf("Error marshaling PromQL error json response: %v", err)

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	var code int

	switch apiErr.typ {
	case errorBadData:
		code = http.StatusBadRequest
	case errorExec:
		code = 422
	case errorCanceled, errorTimeout:
		code = http.StatusServiceUnavailable
	case errorInternal:
		code = http.StatusInternalServerError
	case errorNotFound:
		code = http.StatusNotFound
	default:
		code = http.StatusInternalServerError
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	if _, err := w.Write(b); err != nil {
		logger.V(1).Printf("Error writing PromQL error response: %v", err)
	}
}
