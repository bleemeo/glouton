package api

import (
	"agentgo/types"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Return the most recent point. ok is false if no point are found
func getLastPoint(m types.Metric) (point types.Point, ok bool) {
	points, err := m.Points(time.Now().Add(-5*time.Minute), time.Now())
	if err != nil {
		return
	}
	for _, p := range points {
		ok = true
		if p.Time.After(point.Time) {
			point = p
		}
	}
	return
}

func formatLabels(labels map[string]string) string {
	invalidNameChar := regexp.MustCompile("[^a-zA-Z0-9_]")
	r := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n")
	part := make([]string, 0, len(labels))
	for k, v := range labels {
		part = append(part, fmt.Sprintf("%s=\"%s\"", invalidNameChar.ReplaceAllString(k, "_"), r.Replace(v)))
	}
	if len(part) == 0 {
		return ""
	}
	sort.Strings(part)
	return fmt.Sprintf("{%s}", strings.Join(part, ","))
}

func (a *API) promExporter(w http.ResponseWriter, _ *http.Request) {
	invalidNameChar := regexp.MustCompile("[^a-zA-Z0-9_]")
	metrics, err := a.db.Metrics(nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Unable to get list of metrics: %v\n", err)
	}
	lines := make([]string, 0, len(metrics))
	for _, m := range metrics {
		labels := m.Labels()
		name, ok := labels["__name__"]
		delete(labels, "__name__")
		if !ok {
			continue
		}
		lastPoint, ok := getLastPoint(m)
		if !ok {
			continue
		}
		lines = append(lines, fmt.Sprintf(
			"%s%s %f %d\n",
			invalidNameChar.ReplaceAllString(name, "_"),
			formatLabels(labels),
			lastPoint.Value,
			lastPoint.Time.UnixNano()/1000000,
		))
	}
	sort.Strings(lines)
	fmt.Fprintf(w, strings.Join(lines, ""))
}
