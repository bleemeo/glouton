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

package promql

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bleemeo/glouton/logger"

	"github.com/go-chi/chi/v5"
	"github.com/grafana/regexp"
	jsoniter "github.com/json-iterator/go"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/timestamp"
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
	errorTimeout         errorType = "timeout"
	errorCanceled        errorType = "canceled"
	errorExec            errorType = "execution"
	errorBadData         errorType = "bad_data"
	errorInternal        errorType = "internal"
	errorNotFound        errorType = "not_found"
	errorTooManyRequests errorType = "too_many_requests"
)

// maxConcurrentQueries bounds the number of PromQL evaluations running at once
// (the memory-heavy part). Excess queries wait up to queryQueueWait for a slot
// before being rejected with 429, rather than all running concurrently and
// risking memory exhaustion. The wait keeps the local UI working (it fires many
// query_range in parallel) while capping the load under real overload.
const (
	maxConcurrentQueries = 6
	queryQueueWait       = 10 * time.Second
)

var (
	errStartTime        = errors.New("end timestamp must not be before start time")
	errPositiveInteger  = errors.New("zero or negative query resolution step widths are not accepted. Try a positive integer")
	errMaxStep          = errors.New("exceeded maximum resolution of 11,000 points per timeseries. Try decreasing the query resolution (?step=XX)")
	errParseDuration    = errors.New("cannot parse to a valid duration")
	errInvalidTimestamp = errors.New("cannot parse to a valid timestamp")
	errServerBusy       = errors.New("too many concurrent queries, try again later")
	errInvalidLabelName = errors.New("invalid label name")
	errNoMatchers       = errors.New("no match[] parameter provided")
	errEmptyMatcher     = errors.New("match[] must contain at least one non-empty matcher")
	errParseForm        = errors.New("error parsing form values")
)

// minTime and maxTime return the default boundaries of the endpoints taking an
// optional time range (/labels, /label/{name}/values and /series).
//
// They are Prometheus' own MinTime and MaxTime, kept identical so that a client
// sending no start/end gets the same behavior as against a real Prometheus
// server. Upstream's values are lower/higher than the extremes representable in
// milliseconds; that is deliberate on their side and we don't "fix" it here.
func minTime() time.Time {
	return time.Unix(math.MinInt64/1000+62135596801, 0).UTC()
}

func maxTime() time.Time {
	return time.Unix(math.MaxInt64/1000-62135596801, 999999999).UTC()
}

// checkContextEveryNIterations is used in the /series loop to check whether the
// context is done without paying for a check on every single series.
const checkContextEveryNIterations = 128

type PromQL struct {
	CORSOrigin *regexp.Regexp

	queryEngine *promql.Engine
	// sem gates concurrent query evaluations to maxConcurrentQueries.
	sem chan struct{}

	parser      parser.Parser
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

	r.Get("/query", wrap(p.query))
	r.Post("/query", wrap(p.query))
	r.Get("/query_range", wrap(p.queryRange))
	r.Post("/query_range", wrap(p.queryRange))

	r.Get("/labels", wrap(p.labelNames))
	r.Post("/labels", wrap(p.labelNames))
	r.Get("/label/{name}/values", wrap(p.labelValues))

	r.Get("/series", wrap(p.series))
	r.Post("/series", wrap(p.series))

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
	p.sem = make(chan struct{}, maxConcurrentQueries)
	p.parser = parser.NewParser(parser.Options{})
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

// parseTimeParam parses the given request parameter as a timestamp, falling back
// to defaultValue when the parameter is absent or empty.
func parseTimeParam(r *http.Request, paramName string, defaultValue time.Time) (time.Time, error) {
	val := r.FormValue(paramName)
	if val == "" {
		return defaultValue, nil
	}

	return parseTime(val)
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

// invalidParamError builds the bad_data error returned when a request parameter
// can't be parsed. The message mirrors Prometheus' own wording.
func invalidParamError(err error, parameter string) *apiError {
	return &apiError{errorBadData, fmt.Errorf("invalid parameter %q: %w", parameter, err)}
}

// invalidParamResult is invalidParamError as a ready-to-return apiFuncResult.
func invalidParamResult(err error, parameter string) apiFuncResult {
	return apiFuncResult{nil, invalidParamError(err, parameter), nil, nil}
}

// parseMatchersParam parses the "match[]" parameters of the /labels,
// /label/{name}/values and /series endpoints into matcher sets. A matcher set
// that would select every series is rejected, as Prometheus does.
func (p *PromQL) parseMatchersParam(matchers []string) ([][]*labels.Matcher, error) {
	matcherSets, err := p.parser.ParseMetricSelectors(matchers)
	if err != nil {
		return nil, err
	}

outerLoop:
	for _, ms := range matcherSets {
		for _, lm := range ms {
			if lm != nil && !lm.Matches("") {
				continue outerLoop
			}
		}

		return nil, errEmptyMatcher
	}

	return matcherSets, nil
}

// query answers /api/v1/query, the instant query endpoint: it evaluates the
// expression at a single point in time and returns a vector (or a scalar, or a
// string, depending on the expression).
//
// Unlike queryRange there is no point-count guard to apply: an instant query
// returns at most one point per series whatever the expression.
func (p *PromQL) query(r *http.Request, st storage.Queryable) (result apiFuncResult) {
	ts, err := parseTimeParam(r, "time", time.Now())
	if err != nil {
		return invalidParamResult(err, "time")
	}

	ctx := r.Context()

	if to := r.FormValue("timeout"); to != "" {
		var cancel context.CancelFunc

		timeout, err := parseDuration(to)
		if err != nil {
			return invalidParamResult(err, "timeout")
		}

		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Gate concurrent evaluations to avoid memory exhaustion. Wait up to
	// queryQueueWait for a slot (so the local UI's parallel queries succeed)
	// before rejecting with 429. Done before NewRangeQuery so the wait does not
	// eat into the engine's own query timeout.
	acqCtx, cancelAcq := context.WithTimeout(ctx, queryQueueWait)

	select {
	case p.sem <- struct{}{}:
		cancelAcq()

		defer func() { <-p.sem }()
	case <-acqCtx.Done():
		cancelAcq()

		return apiFuncResult{nil, &apiError{errorTooManyRequests, errServerBusy}, nil, nil}
	}

	qry, err := p.queryEngine.NewInstantQuery(ctx, st, nil, r.FormValue("query"), ts)

	if err != nil {
		return invalidParamResult(err, "query")
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

func (p *PromQL) queryRange(r *http.Request, st storage.Queryable) (result apiFuncResult) {
	start, err := parseTime(r.FormValue("start"))
	if err != nil {
		return invalidParamResult(err, "start")
	}

	end, err := parseTime(r.FormValue("end"))
	if err != nil {
		return invalidParamResult(err, "end")
	}

	if end.Before(start) {
		return apiFuncResult{nil, &apiError{errorBadData, errStartTime}, nil, nil}
	}

	step, err := parseDuration(r.FormValue("step"))
	if err != nil {
		return invalidParamResult(err, "step")
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
			return invalidParamResult(err, "timeout")
		}

		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	qry, err := p.queryEngine.NewRangeQuery(ctx, st, nil, r.FormValue("query"), start, end, step)
	if err != nil {
		return invalidParamResult(err, "query")
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

// labelNames answers /api/v1/labels: the list of label names present in the
// store, optionally restricted to the series selected by the "match[]"
// parameters and to the [start, end] time range.
func (p *PromQL) labelNames(r *http.Request, st storage.Queryable) (result apiFuncResult) {
	start, end, matcherSets, apiErr := p.parseSelectionParams(r)
	if apiErr != nil {
		return apiFuncResult{nil, apiErr, nil, nil}
	}

	querier, err := st.Querier(timestamp.FromTime(start), timestamp.FromTime(end))
	if err != nil {
		return apiFuncResult{nil, returnAPIError(err), nil, nil}
	}
	// From now on, we must only return with a finalizer in the result (to be
	// called by the caller) or close the querier ourselves (which is required in
	// the case of a panic).
	defer func() {
		if result.finalizer == nil {
			_ = querier.Close()
		}
	}()

	closer := func() { _ = querier.Close() }

	var (
		names    []string
		warnings annotations.Annotations
	)

	if len(matcherSets) > 1 {
		nameSet := make(map[string]struct{})

		for _, matchers := range matcherSets {
			vals, callWarnings, err := querier.LabelNames(r.Context(), labelHints(), matchers...)
			if err != nil {
				return apiFuncResult{nil, returnAPIError(err), warnings, closer}
			}

			warnings.Merge(callWarnings)

			for _, val := range vals {
				nameSet[val] = struct{}{}
			}
		}

		names = keysOf(nameSet)
		slices.Sort(names)
	} else {
		var matchers []*labels.Matcher
		if len(matcherSets) == 1 {
			matchers = matcherSets[0]
		}

		names, warnings, err = querier.LabelNames(r.Context(), labelHints(), matchers...)
		if err != nil {
			return apiFuncResult{nil, &apiError{errorExec, err}, warnings, closer}
		}
	}

	if names == nil {
		names = []string{}
	}

	return apiFuncResult{names, nil, warnings, closer}
}

// labelValues answers /api/v1/label/{name}/values: the values taken by one label
// name. An unknown label name is not an error, it simply has no value.
func (p *PromQL) labelValues(r *http.Request, st storage.Queryable) (result apiFuncResult) {
	ctx := r.Context()
	name := chi.URLParam(r, "name")

	if strings.HasPrefix(name, "U__") {
		name = model.UnescapeName(name, model.ValueEncodingEscaping)
	}

	if !model.UTF8Validation.IsValidLabelName(name) {
		err := fmt.Errorf("%w: %q", errInvalidLabelName, name)

		return apiFuncResult{nil, &apiError{errorBadData, err}, nil, nil}
	}

	start, end, matcherSets, apiErr := p.parseSelectionParams(r)
	if apiErr != nil {
		return apiFuncResult{nil, apiErr, nil, nil}
	}

	querier, err := st.Querier(timestamp.FromTime(start), timestamp.FromTime(end))
	if err != nil {
		return apiFuncResult{nil, &apiError{errorExec, err}, nil, nil}
	}
	// From now on, we must only return with a finalizer in the result (to be
	// called by the caller) or close the querier ourselves (which is required in
	// the case of a panic).
	defer func() {
		if result.finalizer == nil {
			_ = querier.Close()
		}
	}()

	closer := func() { _ = querier.Close() }

	var (
		values   []string
		warnings annotations.Annotations
	)

	if len(matcherSets) > 1 {
		valueSet := make(map[string]struct{})

		for _, matchers := range matcherSets {
			vals, callWarnings, err := querier.LabelValues(ctx, name, labelHints(), matchers...)
			if err != nil {
				return apiFuncResult{nil, &apiError{errorExec, err}, warnings, closer}
			}

			warnings.Merge(callWarnings)

			for _, val := range vals {
				valueSet[val] = struct{}{}
			}
		}

		values = keysOf(valueSet)
	} else {
		var matchers []*labels.Matcher
		if len(matcherSets) == 1 {
			matchers = matcherSets[0]
		}

		values, warnings, err = querier.LabelValues(ctx, name, labelHints(), matchers...)
		if err != nil {
			return apiFuncResult{nil, &apiError{errorExec, err}, warnings, closer}
		}

		if values == nil {
			values = []string{}
		}
	}

	slices.Sort(values)

	return apiFuncResult{values, nil, warnings, closer}
}

// series answers /api/v1/series: the label sets of the series selected by the
// mandatory "match[]" parameters, without any sample.
func (p *PromQL) series(r *http.Request, st storage.Queryable) (result apiFuncResult) {
	ctx := r.Context()

	start, end, matcherSets, apiErr := p.parseSelectionParams(r)
	if apiErr != nil {
		return apiFuncResult{nil, apiErr, nil, nil}
	}

	if len(matcherSets) == 0 {
		return apiFuncResult{nil, &apiError{errorBadData, errNoMatchers}, nil, nil}
	}

	querier, err := st.Querier(timestamp.FromTime(start), timestamp.FromTime(end))
	if err != nil {
		return apiFuncResult{nil, returnAPIError(err), nil, nil}
	}
	// From now on, we must only return with a finalizer in the result (to be
	// called by the caller) or close the querier ourselves (which is required in
	// the case of a panic).
	defer func() {
		if result.finalizer == nil {
			_ = querier.Close()
		}
	}()

	closer := func() { _ = querier.Close() }

	hints := &storage.SelectHints{
		Start: timestamp.FromTime(start),
		End:   timestamp.FromTime(end),
		Func:  "series", // There is no series function, this token is used for lookups that don't need samples.
	}

	var set storage.SeriesSet

	if len(matcherSets) > 1 {
		sets := make([]storage.SeriesSet, 0, len(matcherSets))

		for _, mset := range matcherSets {
			// We need to sort this select results to merge (deduplicate) the series sets later.
			sets = append(sets, querier.Select(ctx, true, hints, mset...))
		}

		set = storage.NewMergeSeriesSet(sets, 0, storage.ChainedSeriesMerge)
	} else {
		set = querier.Select(ctx, false, hints, matcherSets[0]...)
	}

	metrics := []labels.Labels{}
	warnings := set.Warnings()

	for i := 1; set.Next(); i++ {
		if i%checkContextEveryNIterations == 0 {
			if err := ctx.Err(); err != nil {
				return apiFuncResult{nil, returnAPIError(err), warnings, closer}
			}
		}

		metrics = append(metrics, set.At().Labels())
	}

	if set.Err() != nil {
		return apiFuncResult{nil, returnAPIError(set.Err()), warnings, closer}
	}

	return apiFuncResult{metrics, nil, warnings, closer}
}

// parseSelectionParams parses the parameters shared by the endpoints that select
// series without evaluating them: the optional "start"/"end" time range and the
// "match[]" matcher sets.
func (p *PromQL) parseSelectionParams(r *http.Request) (time.Time, time.Time, [][]*labels.Matcher, *apiError) {
	if err := r.ParseForm(); err != nil {
		return time.Time{}, time.Time{}, nil, &apiError{errorBadData, fmt.Errorf("%w: %w", errParseForm, err)}
	}

	start, err := parseTimeParam(r, "start", minTime())
	if err != nil {
		return time.Time{}, time.Time{}, nil, invalidParamError(err, "start")
	}

	end, err := parseTimeParam(r, "end", maxTime())
	if err != nil {
		return time.Time{}, time.Time{}, nil, invalidParamError(err, "end")
	}

	matcherSets, err := p.parseMatchersParam(r.Form["match[]"])
	if err != nil {
		return time.Time{}, time.Time{}, nil, &apiError{errorBadData, err}
	}

	return start, end, matcherSets, nil
}

// labelHints returns the hints passed to the queriers' LabelNames/LabelValues.
//
// The "limit" request parameter is deliberately not implemented: a limit could
// only be honoured by truncating the result, and a truncated label list is
// indistinguishable from a complete one for the caller. Leaving Limit at 0 means
// "no limit", so we always answer with the full list rather than silently
// dropping values.
func labelHints() *storage.LabelHints {
	return &storage.LabelHints{}
}

// keysOf returns the keys of the given set, in an unspecified order.
func keysOf(set map[string]struct{}) []string {
	result := make([]string, 0, len(set))

	for key := range set {
		result = append(result, key)
	}

	return result
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
	case errorTooManyRequests:
		code = http.StatusTooManyRequests
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
