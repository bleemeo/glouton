package matcher

import (
	"fmt"
	"glouton/types"
	"net/url"
	"testing"

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
