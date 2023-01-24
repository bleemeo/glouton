package fluentbit

import (
	"glouton/config"
	"io"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestFluentbitConfigWriter(t *testing.T) {
	inputs := []config.LogInput{
		{
			Path: "/var/log/apache/access.log",
			Filters: []config.LogFilter{
				{
					Metric: "apache_errors_count",
					Regex:  "\\[error\\]",
				},
				{
					Metric: "apache_requests_count",
					Regex:  "GET /",
				},
			},
		},
	}

	expectedFile, err := os.Open("testdata/fluentbit.conf")
	if err != nil {
		t.Fatal(err)
	}

	expectedConfig, err := io.ReadAll(expectedFile)
	if err != nil {
		t.Fatal(err)
	}

	gotConfig := inputsToFluentbitConfig(inputs)

	if diff := cmp.Diff(gotConfig, string(expectedConfig)); diff != "" {
		t.Fatalf("Unexpected config:\n%s", diff)
	}
}
