package matcher

import (
	"fmt"
	"glouton/types"
	"net/url"
	"testing"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/pkg/labels"
)

func Test_NormalizeMetric(t *testing.T) {
	tests := []struct {
		allowListString string
		scrapperName    string
		scrapeInstance  string
		scrapeJob       string
		want            Matchers
	}{
		{
			allowListString: "cpu_percent",
			scrapeInstance:  "instance_1",
			scrapeJob:       "job_1",
			want: Matchers{
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelName,
					Value: "cpu_percent",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: "instance_1",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: "job_1",
				},
			},
		},
		{
			allowListString: "cpu_*",
			scrapeInstance:  "instance_2",
			scrapeJob:       "job_2",
			want: Matchers{
				&labels.Matcher{
					Type:  labels.MatchRegexp,
					Name:  types.LabelName,
					Value: "cpu_.*",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: "instance_2",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: "job_2",
				},
			},
		},
		{
			allowListString: "prom*_cpu_*",
			scrapeInstance:  "instance_3",
			scrapeJob:       "job_3",
			want: Matchers{
				&labels.Matcher{
					Type:  labels.MatchRegexp,
					Name:  types.LabelName,
					Value: "prom.*_cpu_.*",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: "instance_3",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: "job_3",
				},
			},
		},
		{
			allowListString: "prom_cpu{test=\"Hello\",scrape_instance=\"instance_4\",scrape_job=\"job_3\"}",
			scrapeInstance:  "instance_4",
			scrapeJob:       "job_4",
			want: Matchers{
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  "test",
					Value: "Hello",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: "instance_4",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: "job_4",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelName,
					Value: "prom_cpu",
				},
			},
		},
	}

	for _, test := range tests {
		got, err := NormalizeMetricScrapper(test.allowListString, test.scrapeInstance, test.scrapeJob)

		if err != nil {
			t.Errorf("Invalid result: Got error => %v ", err)
		}
		if len(got) != len(test.want) {
			t.Errorf("Invalid Matchers length for metric %v: expected=%d, got=%d", test, len(test.want), len(got))
			continue
		}

		for idx, val := range got {
			if val.Type != test.want[idx].Type {
				t.Errorf("Invalid Match Type for metric %v:\nexpected %s got %s", test, test.want[idx].Type, val.Type)
			}

			if !val.Matches(test.want[idx].Value) {
				t.Errorf("Invalid unmatched for metric %v:\nexpected={%s: '%s'} got={%s: '%s'}\n",
					test, test.want[idx].Name, test.want[idx].Value, val.Name, val.Value)
			}
		}
	}
}

func Test_Fail_NormalizeMetrics(t *testing.T) {
	metric := "this*_should$_fail{scrape_instance=\"error\"}"

	_, err := NormalizeMetric(metric)
	_, err2 := NormalizeMetricScrapper(metric, "instance", "job")

	if err == nil || err2 == nil {
		t.Errorf("Invalid case not treated as error: expected metric %s to fail", metric)
	}

}

func Test_Add_Same_Type(t *testing.T) {
	metric := "cpu"
	m, _ := NormalizeMetricScrapper(metric, "instance", "job")
	before := len(m)

	err := m.Add(types.LabelName, "cpu2", labels.MatchEqual)

	if err != nil {
		t.Errorf("An error occurred: %v", err)
	}

	if before != len(m) {
		t.Errorf("Add function did not overwrite properly: expected size %d, got %d", before, len(m))
	}

	for _, val := range m {
		if val.Name == types.LabelName {
			if val.Value != "cpu2" {
				t.Errorf("Invalid value for label %s: got %s, expected %s", types.LabelName, val.Name, "cpu2")
			}
		}
	}
}

func Test_Add_Different_Type(t *testing.T) {
	metric := "cpu"
	m, _ := NormalizeMetricScrapper(metric, "instance", "job")
	before := len(m)

	fmt.Println(before)
	err := m.Add(types.LabelName, "cpu2", labels.MatchRegexp)

	if err != nil {
		t.Errorf("An error occurred: %v", err)
	}
	fmt.Println(m)

	if before == len(m) {
		t.Errorf("Add function did not add properly: expected size %d, got %d", before, len(m))
	}
}

func Test_Add_Error(t *testing.T) {
	metric := "cpu"
	m, _ := NormalizeMetricScrapper(metric, "instance", "job")
	err := m.Add("test", "cpu[", labels.MatchRegexp)

	if err == nil {
		t.Errorf("An error was not caught: expected a regex compile error on metric %s", metric)
	}
}

func Test_Host_Port(t *testing.T) {
	url, _ := url.Parse("https://example.com:8080")
	want := "example.com:8080"

	got := HostPort(url)

	if got != want {
		t.Errorf("An error occurred: expected %s, got %s", want, got)
	}
}

func Test_Matches_Basic_Point(t *testing.T) {
	tests := []struct {
		name     string
		point    types.MetricPoint
		matchers Matchers
		want     bool
	}{
		{
			name: "basic metric glob",
			point: types.MetricPoint{
				Labels: map[string]string{
					types.LabelName:           "cpu_percent",
					types.LabelScrapeInstance: "instance_1",
					types.LabelScrapeJob:      "job_1",
				},
			},
			matchers: Matchers{
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelName,
					Value: "cpu_percent",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: "instance_1",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: "job_1",
				},
			},
			want: true,
		},
		{
			name: "basic metric glob fail",
			point: types.MetricPoint{
				Labels: map[string]string{
					types.LabelName:           "should_fail",
					types.LabelScrapeInstance: "instance_3",
					types.LabelScrapeJob:      "job_3",
				},
			},
			matchers: Matchers{
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelName,
					Value: "cpu_percent",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: "instance_3",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: "job_3",
				},
			},
			want: false,
		},
		{
			name: "basic metric glob fail missing label",
			point: types.MetricPoint{
				Labels: map[string]string{
					types.LabelName:           "cpu_percent",
					types.LabelScrapeInstance: "instance_3",
					// missing scrape job
				},
			},
			matchers: Matchers{
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelName,
					Value: "cpu_percent",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: "instance_3",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: "job_3",
				},
			},
			want: false,
		},
		{
			name: "basic metric regex",
			point: types.MetricPoint{
				Labels: map[string]string{
					types.LabelName:           "cpu_percent_whatever",
					types.LabelScrapeInstance: "instance_2",
					types.LabelScrapeJob:      "job_2",
				},
			},
			matchers: Matchers{
				labels.MustNewMatcher(labels.MatchRegexp, types.LabelName, "cpu_percent.*"),
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: "instance_2",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: "job_2",
				},
			},
			want: true,
		},
		{
			name: "basic metric regex fail",
			point: types.MetricPoint{
				Labels: map[string]string{
					types.LabelName:           "cpu_percent_whatever_should_fail",
					types.LabelScrapeInstance: "instance_4",
					types.LabelScrapeJob:      "job_4",
				},
			},
			matchers: Matchers{
				labels.MustNewMatcher(labels.MatchRegexp, types.LabelName, "cpu_percent.*_whatever"),
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: "instance_4",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: "job_4",
				},
			},
			want: false,
		},
		{
			name: "basic metric regex fail missing label",
			point: types.MetricPoint{
				Labels: map[string]string{
					types.LabelName:           "cpu_percent_whatever",
					types.LabelScrapeInstance: "instance_2",
				},
			},
			matchers: Matchers{
				labels.MustNewMatcher(labels.MatchRegexp, types.LabelName, "cpu_percent.*"),
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: "instance_2",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: "job_2",
				},
			},
			want: false,
		},
	}

	for _, test := range tests {
		got := test.matchers.MatchesPoint(test.point)

		if got != test.want {
			t.Errorf("Incorrect result for test %s: expected %v, got %v", test.name, test.want, got)
		}
		fmt.Printf("Test %s done\n", test.name)
	}
}

func Test_Matches_Point_Error(t *testing.T) {
	point := types.MetricPoint{
		Labels: map[string]string{
			types.LabelScrapeInstance: "should_fail",
			types.LabelScrapeJob:      "should_fail",
		},
	}
	matchers := Matchers{
		&labels.Matcher{
			Type:  labels.MatchEqual,
			Name:  types.LabelName,
			Value: "cpu_percent",
		},
		&labels.Matcher{
			Type:  labels.MatchEqual,
			Name:  types.LabelScrapeInstance,
			Value: "instance_1",
		},
		&labels.Matcher{
			Type:  labels.MatchEqual,
			Name:  types.LabelScrapeJob,
			Value: "job_1",
		},
	}

	res := matchers.MatchesPoint(point)

	if res {
		t.Errorf("Incorrect value on match: expected false but got true")
	}
}

func Test_Matches_Basic_Family(t *testing.T) {
	fn := []string{"cpu_percent", "should_fail", "cpu_whatever_should_fail"}
	lbln := []string{types.LabelScrapeInstance, types.LabelScrapeJob}
	lblv := []string{"instance_1", "job_1", "instance_2", "job_2"}
	tests := []struct {
		name     string
		family   dto.MetricFamily
		matchers Matchers
		want     bool
	}{
		{
			name: "basic metric glob",
			family: dto.MetricFamily{
				Name: &fn[0],
				Metric: []*dto.Metric{
					{
						Label: []*dto.LabelPair{
							{
								Name:  &lbln[0],
								Value: &lblv[0],
							},
							{
								Name:  &lbln[1],
								Value: &lblv[1],
							},
						},
					},
				},
			},
			matchers: Matchers{
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelName,
					Value: "cpu_percent",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: "instance_1",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: "job_1",
				},
			},
			want: true,
		},
		{
			name: "basic metric regex",
			family: dto.MetricFamily{
				Name: &fn[0],
				Metric: []*dto.Metric{
					{
						Label: []*dto.LabelPair{
							{
								Name:  &lbln[0],
								Value: &lblv[0],
							},
							{
								Name:  &lbln[1],
								Value: &lblv[1],
							},
						},
					},
				},
			},
			matchers: Matchers{
				labels.MustNewMatcher(labels.MatchRegexp, types.LabelName, "cpu.*"),
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: "instance_1",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: "job_1",
				},
			},
			want: true,
		},
		{
			name: "basic metric glob fail",
			family: dto.MetricFamily{
				Name: &fn[1],
				Metric: []*dto.Metric{
					{
						Label: []*dto.LabelPair{
							{
								Name:  &lbln[0],
								Value: &lblv[0],
							},
							{
								Name:  &lbln[1],
								Value: &lblv[1],
							},
						},
					},
				},
			},
			matchers: Matchers{
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelName,
					Value: "cpu_percent",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: "instance_1",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: "job_1",
				},
			},
			want: false,
		},
		{
			name: "basic metric glob fail missing label",
			family: dto.MetricFamily{
				Name: &fn[0],
				Metric: []*dto.Metric{
					{
						Label: []*dto.LabelPair{
							{
								Name:  &lbln[0],
								Value: &lblv[0],
							}, // missing scrape job label
						},
					},
				},
			},
			matchers: Matchers{
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelName,
					Value: "cpu_percent",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: "instance_1",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: "job_1",
				},
			},
			want: false,
		},
		{
			name: "basic metric regex fail",
			family: dto.MetricFamily{
				Name: &fn[2],
				Metric: []*dto.Metric{
					{
						Label: []*dto.LabelPair{
							{
								Name:  &lbln[0],
								Value: &lblv[0],
							},
							{
								Name:  &lbln[1],
								Value: &lblv[1],
							},
						},
					},
				},
			},
			matchers: Matchers{
				labels.MustNewMatcher(labels.MatchRegexp, types.LabelName, "cpu.*whatever"),
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: "instance_1",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: "job_1",
				},
			},
			want: false,
		},
		{
			name: "metric regex with unchecked labels",
			family: dto.MetricFamily{
				Name: &fn[0],
				Metric: []*dto.Metric{
					{
						Label: []*dto.LabelPair{
							{
								Name:  &lbln[0],
								Value: &lblv[0],
							},
							{
								Name:  &lbln[1],
								Value: &lblv[1],
							},
						},
					},
				},
			},
			matchers: Matchers{
				labels.MustNewMatcher(labels.MatchRegexp, types.LabelName, "cpu.*"),
			},
			want: true,
		},
		{
			name: "metric regex with different labels",
			family: dto.MetricFamily{
				Name: &fn[2],
				Metric: []*dto.Metric{
					{
						Label: []*dto.LabelPair{
							{
								Name:  &lbln[0],
								Value: &lblv[2],
							},
							{
								Name:  &lbln[1],
								Value: &lblv[3],
							},
						},
					},
				},
			},
			matchers: Matchers{
				labels.MustNewMatcher(labels.MatchRegexp, types.LabelName, "cpu.*whatever"),
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeInstance,
					Value: "instance_1",
				},
				&labels.Matcher{
					Type:  labels.MatchEqual,
					Name:  types.LabelScrapeJob,
					Value: "job_1",
				},
			},
			want: false,
		},
	}

	for _, test := range tests {
		got := test.matchers.MatchesMetricFamily(&test.family)

		if got != test.want {
			t.Errorf("An error occurred for test %s: expected %v, got %v", test.name, test.want, got)
		}
	}
}
