package matcher

import (
	"glouton/types"
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
					Type:  labels.MatchRegexp,
					Name:  types.LabelName,
					Value: "cpu_percent",
				},
			},
			// want:            "{__name__=\"cpu_percent\",scrape_instance=\"instance_1\",scrape_job=\"job_1\"}",
		},
		{
			allowListString: "cpu_*",
			scrapeInstance:  "instance_2",
			scrapeJob:       "job_2",
			// want:            "{__name__=\"cpu_.*\",scrape_instance=\"instance_2\",scrape_job=\"job_2\"}",
		},
		{
			allowListString: "prom*_cpu_*",
			scrapeInstance:  "instance_3",
			scrapeJob:       "job_3",
			// want:            "{__name__=\"prom.*_cpu_.*\",scrape_instance=\"instance_3\",scrape_job=\"job_3\"}",
		},
		{
			allowListString: "prom_cpu{test=\"Hello\",scrape_instance=\"instance_4\",scrape_job=\"job_3\"}",
			scrapeInstance:  "instance_4",
			scrapeJob:       "job_4",
			// want:            "{__name__=\"prom_cpu\",scrape_instance=\"instance_4\",scrape_job=\"job_4\",test=\"Hello\"}",
		},
	}

	for _, test := range tests {
		_, err := NormalizeMetricScrapper(test.allowListString, test.scrapeInstance, test.scrapeJob)

		if err != nil {
			t.Errorf("Invalid result: Got error => %v ", err)
		}
		// fmt.Println(got)
		//FIXME: change tests in order to match the new type
		// if got != test.want {
		// 	t.Errorf("Invalid result:\nexpected=%s got=%s\n", test.want, got)
		// }
	}
}
