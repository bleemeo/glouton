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

package logsource

import (
	"os"
	"testing"

	"github.com/go-viper/mapstructure/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/adapter"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/filelogreceiver"
	"go.opentelemetry.io/collector/component"
)

func TestRetryConfigIsUpToDate(t *testing.T) {
	t.Parallel()

	consumerretryConfig := adapter.BaseConfig{}.RetryOnFailure

	err := mapstructure.Decode(retryCfg, &consumerretryConfig)
	if err != nil {
		t.Fatal("Failed to define consumerretry config:", err)
	}

	// Converting both consumerretryConfig and retryCfg to maps,
	// so we can compare them easily.

	var consumerretryCfgMap, retryCfgMap map[string]any

	err = mapstructure.Decode(consumerretryConfig, &consumerretryCfgMap)
	if err != nil {
		t.Fatal("Failed to convert consumerretry config to a map:", err)
	}

	err = mapstructure.Decode(retryCfg, &retryCfgMap)
	if err != nil {
		t.Fatal("Failed to convert retry config to a map:", err)
	}

	if diff := cmp.Diff(retryCfgMap, consumerretryCfgMap); diff != "" {
		t.Fatalf("Unexpected consumerretry config (-want, +got):\n%s", diff)
	}
}

// TestSetupLogReceiverFactoriesExtraRaw is the regression test for pasting an
// existing OTel Collector filelogreceiver config almost verbatim: extraRaw
// fields (here, Encoding) must land on the built FileLogConfig, while
// Glouton's own authoritative fields (Include, StartAt for a never-seen
// file) must still win over anything conflicting extraRaw might set.
func TestSetupLogReceiverFactoriesExtraRaw(t *testing.T) {
	t.Parallel()

	tmpFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer tmpFile.Close()

	extraRaw := map[string]any{
		"encoding":  "utf-16le",
		"start_at":  "beginning", // Glouton's own StartAt-for-new-files logic must override this
		"multiline": map[string]any{"line_start_pattern": `^\d{4}-\d{2}-\d{2}`},
	}

	factories, readable, exec, _, err := SetupLogReceiverFactories(
		[]string{tmpFile.Name()},
		"",
		nil,
		nil, // lastFileSizes: never seen before
		nil,
		func(string) *component.ID { return nil },
		StatFile,
		nil,
		extraRaw,
	)
	if err != nil {
		t.Fatal("SetupLogReceiverFactories returned an error:", err)
	}

	if len(exec) != 0 {
		t.Fatalf("Expected no exec fallback for a directly-readable file, got %v", exec)
	}

	if len(readable) != 1 {
		t.Fatalf("Expected exactly 1 readable file, got %v", readable)
	}

	if len(factories) != 1 {
		t.Fatalf("Expected exactly 1 factory, got %d", len(factories))
	}

	var fileCfg *filelogreceiver.FileLogConfig

	for _, cfg := range factories {
		fileCfg, _ = cfg.(*filelogreceiver.FileLogConfig)
	}

	if fileCfg == nil {
		t.Fatal("Expected a *filelogreceiver.FileLogConfig")
	}

	if fileCfg.InputConfig.Encoding != "utf-16le" {
		t.Errorf("Expected extraRaw's encoding to apply, got %q", fileCfg.InputConfig.Encoding)
	}

	if fileCfg.InputConfig.SplitConfig.LineStartPattern != `^\d{4}-\d{2}-\d{2}` {
		t.Errorf("Expected extraRaw's multiline config to apply, got %q", fileCfg.InputConfig.SplitConfig.LineStartPattern)
	}

	if fileCfg.InputConfig.StartAt != "end" {
		t.Errorf(`Expected Glouton's own StartAt="end" (never-seen file) to win over extraRaw's "beginning", got %q`, fileCfg.InputConfig.StartAt)
	}

	if len(fileCfg.InputConfig.Include) != 1 || fileCfg.InputConfig.Include[0] != tmpFile.Name() {
		t.Errorf("Expected Glouton's own Include to be set to the resolved file, got %v", fileCfg.InputConfig.Include)
	}
}
