// Disable stylecheck because is complain on error message (should not be capitalized)
// but we prefer keeping the exact message used by Prometheus.

//nolint: stylecheck
package promql

import (
	"context"
	"errors"
	"fmt"
	"glouton/store"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-kit/kit/log"
	jsoniter "github.com/json-iterator/go"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/util/httputil"
	"github.com/prometheus/prometheus/util/stats"
)

type errorType string

type status string

const (
	statusSuccess status = "success"
	statusError   status = "error"
)

//nolint: gochecknoglobals
var (
	minTime = time.Unix(0, 0).UTC()
	maxTime = time.Unix(math.MaxInt32*3600, 0).UTC()
)

type response struct {
	Status    status      `json:"status"`
	Data      interface{} `json:"data,omitempty"`
	ErrorType errorType   `json:"errorType,omitempty"`
	Error     string      `json:"error,omitempty"`
	Warnings  []string    `json:"warnings,omitempty"`
}

const (
	errorTimeout  errorType = "timeout"
	errorCanceled errorType = "canceled"
	errorExec     errorType = "execution"
	errorBadData  errorType = "bad_data"
	errorInternal errorType = "internal"
	errorNotFound errorType = "not_found"
)

type PromQL struct {
	CORSOrigin *regexp.Regexp

	logger      log.Logger
	queryEngine *promql.Engine
}

type apiFunc func(r *http.Request, st *store.Store) apiFuncResult

// Register the API's endpoints in the given router.
func (p *PromQL) Register(st *store.Store) http.Handler {
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
	p.logger = log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	opts := promql.EngineOpts{
		Logger:             log.With(p.logger, "component", "query engine"),
		Reg:                nil,
		MaxSamples:         50000000,
		Timeout:            2 * time.Minute,
		ActiveQueryTracker: nil,
		LookbackDelta:      5 * time.Minute,
	}
	p.queryEngine = promql.NewEngine(opts)
}

type apiFuncResult struct {
	data      interface{}
	err       *apiError
	warnings  storage.Warnings
	finalizer func()
}

type queryData struct {
	ResultType parser.ValueType  `json:"resultType"`
	Result     parser.Value      `json:"result"`
	Stats      *stats.QueryStats `json:"stats,omitempty"`
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

	return time.Time{}, fmt.Errorf("cannot parse %q to a valid timestamp", s)
}

func parseTimeParam(r *http.Request, paramName string, defaultValue time.Time) (time.Time, error) {
	val := r.FormValue(paramName)
	if val == "" {
		return defaultValue, nil
	}

	result, err := parseTime(val)
	if err != nil {
		return time.Time{}, fmt.Errorf("Invalid time value for '%s': %w", paramName, err)
	}

	return result, nil
}

func parseDuration(s string) (time.Duration, error) {
	if d, err := strconv.ParseFloat(s, 64); err == nil {
		ts := d * float64(time.Second)
		if ts > float64(math.MaxInt64) || ts < float64(math.MinInt64) {
			return 0, fmt.Errorf("cannot parse %q to a valid duration. It overflows int64", s)
		}

		return time.Duration(ts), nil
	}

	if d, err := model.ParseDuration(s); err == nil {
		return time.Duration(d), nil
	}

	return 0, fmt.Errorf("cannot parse %q to a valid duration", s)
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

func (p *PromQL) queryRange(r *http.Request, st *store.Store) (result apiFuncResult) {
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
		err := errors.New("end timestamp must not be before start time")

		return apiFuncResult{nil, &apiError{errorBadData, err}, nil, nil}
	}

	step, err := parseDuration(r.FormValue("step"))
	if err != nil {
		err = fmt.Errorf("invalid parameter 'step': %w", err)

		return apiFuncResult{nil, &apiError{errorBadData, err}, nil, nil}
	}

	if step <= 0 {
		err := errors.New("zero or negative query resolution step widths are not accepted. Try a positive integer")

		return apiFuncResult{nil, &apiError{errorBadData, err}, nil, nil}
	}

	// For safety, limit the number of returned points per timeseries.
	// This is sufficient for 60s resolution for a week or 1h resolution for a year.
	if end.Sub(start)/step > 11000 {
		err := errors.New("exceeded maximum resolution of 11,000 points per timeseries. Try decreasing the query resolution (?step=XX)")

		return apiFuncResult{nil, &apiError{errorBadData, err}, nil, nil}
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

	qry, err := p.queryEngine.NewRangeQuery(st, r.FormValue("query"), start, end, step)
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
	var qs *stats.QueryStats
	if r.FormValue("stats") != "" {
		qs = stats.NewQueryStats(qry.Stats())
	}

	return apiFuncResult{&queryData{
		ResultType: res.Value.Type(),
		Result:     res.Value,
		Stats:      qs,
	}, nil, res.Warnings, qry.Close}
}

func (p *PromQL) respond(w http.ResponseWriter, data interface{}, warnings storage.Warnings) {
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
		_ = p.logger.Log("msg", "error marshaling json response", "err", err)

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(b); err != nil {
		_ = p.logger.Log("msg", "error writing response", "err", err)
	}
}

func (p *PromQL) respondError(w http.ResponseWriter, apiErr *apiError, data interface{}) {
	json := jsoniter.ConfigCompatibleWithStandardLibrary

	b, err := json.Marshal(&response{
		Status:    statusError,
		ErrorType: apiErr.typ,
		Error:     apiErr.err.Error(),
		Data:      data,
	})
	if err != nil {
		_ = p.logger.Log("msg", "error marshaling json response", "err", err)

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
		_ = p.logger.Log("msg", "error writing response", "err", err)
	}
}
