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
	"errors"
	"os"
	"testing"

	"github.com/bleemeo/glouton/otel/execlogreceiver"

	"github.com/go-viper/mapstructure/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/adapter"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
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

	// Compare as maps for an easier diff.
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

// errStatFailed stands in for whatever made a size probe fail (a lost race with logrotate, a sudo stat
// timing out): the callers under test only branch on there being an error, never on which one.
var errStatFailed = errors.New("stat failed")

// TestSetupLogReceiverFactoriesDropsFileWhoseSizeProbeFails guards against a regression where a file
// whose size probe failed (logrotate racing the stat, or sudoStatFile timing out under load) was still
// returned in readableFiles/execFiles with no factory behind it, and had already had a persistent storage
// extension registered for it. Callers read those lists to record what they are watching, so the file was
// marked as tailed while nothing tailed it -- and never retried, since the next update() skips whatever is
// already being watched: that log file was silently lost for the rest of the process's life.
func TestSetupLogReceiverFactoriesDropsFileWhoseSizeProbeFails(t *testing.T) {
	t.Parallel()

	var storageCalls []string

	failingStat := func(_ string, _ string, _ CommandRunner) (bool, bool, func() (int64, error)) {
		return false, false, func() (int64, error) { return 0, errStatFailed }
	}

	factories, readable, exec, sizeFns, err := SetupLogReceiverFactories(
		[]string{"/var/log/app.log"},
		"",
		nil,
		nil,
		nil,
		func(logFile string) *component.ID {
			storageCalls = append(storageCalls, logFile)

			return nil
		},
		failingStat,
		nil,
		nil,
	)
	if err != nil {
		t.Fatal("SetupLogReceiverFactories returned an error:", err)
	}

	if len(readable) != 0 || len(exec) != 0 {
		t.Errorf("Expected the file to be dropped from both lists, got readable=%v exec=%v", readable, exec)
	}

	if len(factories) != 0 {
		t.Errorf("Expected no factory, got %d", len(factories))
	}

	if len(sizeFns) != 0 {
		t.Errorf("Expected no size function to be retained, got %v", sizeFns)
	}

	if len(storageCalls) != 0 {
		t.Errorf("Expected no persistent extension to be registered, got %v", storageCalls)
	}
}

// TestSetupLogReceiverFactoriesPrefixesExcludeWithHostroot guards against a regression where a receiver's
// raw "exclude" was passed to fileconsumer verbatim while Include was hostroot-prefixed. fileconsumer
// matches Exclude against the globbed (prefixed) paths, so every exclude pattern silently failed to match
// in any containerized deployment and the excluded file was tailed anyway.
func TestSetupLogReceiverFactoriesPrefixesExcludeWithHostroot(t *testing.T) {
	t.Parallel()

	tmpFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer tmpFile.Close()

	factories, readable, exec, _, err := SetupLogReceiverFactories(
		[]string{tmpFile.Name()},
		"/hostroot",
		nil,
		nil,
		nil,
		func(string) *component.ID { return nil },
		func(string, string, CommandRunner) (bool, bool, func() (int64, error)) {
			return false, false, func() (int64, error) { return 0, nil }
		},
		nil,
		map[string]any{"exclude": []string{"/var/log/app/debug.log"}},
	)
	if err != nil {
		t.Fatal("SetupLogReceiverFactories returned an error:", err)
	}

	if len(readable) != 1 || len(exec) != 0 {
		t.Fatalf("Expected the file to be directly readable, got readable=%v exec=%v", readable, exec)
	}

	var fileCfg *filelogreceiver.FileLogConfig

	for _, cfg := range factories {
		fileCfg, _ = cfg.(*filelogreceiver.FileLogConfig)
	}

	if fileCfg == nil {
		t.Fatal("Expected a *filelogreceiver.FileLogConfig")
	}

	want := []string{"/hostroot/var/log/app/debug.log"}
	if diff := cmp.Diff(want, fileCfg.InputConfig.Exclude); diff != "" {
		t.Errorf("Expected exclude to be hostroot-prefixed like include (-want +got):\n%s", diff)
	}
}

// TestSetupLogReceiverFactoriesExtraRaw checks that extraRaw fields apply, but Glouton's own fields (Include, StartAt) still win.
func TestSetupLogReceiverFactoriesExtraRaw(t *testing.T) {
	t.Parallel()

	tmpFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer tmpFile.Close()

	extraRaw := map[string]any{
		"encoding":  "utf-16le",
		"start_at":  "beginning", // must be overridden by Glouton's own StartAt logic
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

// TestSetupLogReceiverFactoriesKnownFileDoesNotForceStartAtEnd checks that a known lastFileSizes entry alone avoids forcing StartAt=end again.
func TestSetupLogReceiverFactoriesKnownFileDoesNotForceStartAtEnd(t *testing.T) {
	t.Parallel()

	tmpFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer tmpFile.Close()

	extraRaw := map[string]any{"start_at": "beginning"}

	lastFileSizes := map[string]int64{tmpFile.Name(): 0} // already seen before, at size 0

	factories, readable, exec, _, err := SetupLogReceiverFactories(
		[]string{tmpFile.Name()},
		"",
		nil,
		lastFileSizes,
		nil,
		func(string) *component.ID { return nil },
		StatFile,
		nil,
		extraRaw,
	)
	if err != nil {
		t.Fatal("SetupLogReceiverFactories returned an error:", err)
	}

	if len(readable) != 1 || len(exec) != 0 {
		t.Fatalf("Expected exactly 1 directly-readable file and no exec fallback, got readable=%v exec=%v", readable, exec)
	}

	var fileCfg *filelogreceiver.FileLogConfig

	for _, cfg := range factories {
		fileCfg, _ = cfg.(*filelogreceiver.FileLogConfig)
	}

	if fileCfg == nil {
		t.Fatal("Expected a *filelogreceiver.FileLogConfig")
	}

	if fileCfg.InputConfig.StartAt != "beginning" {
		t.Errorf(`Expected StartAt to be left at extraRaw's "beginning" for an already-known file, got %q`, fileCfg.InputConfig.StartAt)
	}
}

// TestSetupLogReceiverFactoriesExtraRawOperatorsStripped checks that a raw "operators" shorthand in extraRaw is stripped before it would be rejected.
func TestSetupLogReceiverFactoriesExtraRawOperatorsStripped(t *testing.T) {
	t.Parallel()

	tmpFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer tmpFile.Close()

	extraRaw := map[string]any{
		"operators": []any{map[string]any{"include": "some_format"}}, // shorthand, no "type" key
	}

	expandedOperators := []operator.Config{}

	factories, readable, exec, _, err := SetupLogReceiverFactories(
		[]string{tmpFile.Name()},
		"",
		expandedOperators,
		nil,
		nil,
		func(string) *component.ID { return nil },
		StatFile,
		nil,
		extraRaw,
	)
	if err != nil {
		t.Fatal("SetupLogReceiverFactories returned an error (extraRaw's raw operators shorthand should have been stripped):", err)
	}

	if len(factories) != 1 {
		t.Fatalf("Expected exactly 1 factory, got %d", len(factories))
	}

	if len(readable) != 1 || len(exec) != 0 {
		t.Fatalf("Expected exactly 1 directly-readable file and no exec fallback, got readable=%v exec=%v", readable, exec)
	}

	// extraRaw must not be mutated: it's reused across retries.
	if _, stillPresent := extraRaw["operators"]; !stillPresent {
		t.Error("Expected the caller's extraRaw map to be left untouched")
	}
}

// TestSetupLogReceiverFactoriesExtraRawSudoFallback checks that extraRaw settings also apply on the sudo-tail fallback path.
func TestSetupLogReceiverFactoriesExtraRawSudoFallback(t *testing.T) {
	t.Parallel()

	extraRaw := map[string]any{
		"encoding":  "utf-16le",
		"multiline": map[string]any{"line_start_pattern": `^\d{4}-\d{2}-\d{2}`},
	}

	forceSudoStatFile := func(_, _ string, _ CommandRunner) (ignore, needSudo bool, sizeFn func() (int64, error)) {
		return false, true, func() (int64, error) { return 0, nil }
	}

	factories, readable, exec, _, err := SetupLogReceiverFactories(
		[]string{"/some/protected.log"},
		"",
		nil,
		nil,
		nil,
		func(string) *component.ID { return nil },
		forceSudoStatFile,
		nil,
		extraRaw,
	)
	if err != nil {
		t.Fatal("SetupLogReceiverFactories returned an error:", err)
	}

	if len(readable) != 0 {
		t.Fatalf("Expected no directly-readable file, got %v", readable)
	}

	if len(exec) != 1 {
		t.Fatalf("Expected exactly 1 exec fallback file, got %v", exec)
	}

	if len(factories) != 1 {
		t.Fatalf("Expected exactly 1 factory, got %d", len(factories))
	}

	var execCfg *execlogreceiver.ExecLogConfig

	for _, cfg := range factories {
		execCfg, _ = cfg.(*execlogreceiver.ExecLogConfig)
	}

	if execCfg == nil {
		t.Fatal("Expected a *execlogreceiver.ExecLogConfig")
	}

	if execCfg.InputConfig.Encoding != "utf-16le" {
		t.Errorf("Expected extraRaw's encoding to apply to the sudo-tail fallback too, got %q", execCfg.InputConfig.Encoding)
	}

	if execCfg.InputConfig.SplitConfig.LineStartPattern != `^\d{4}-\d{2}-\d{2}` {
		t.Errorf("Expected extraRaw's multiline config to apply to the sudo-tail fallback too, got %q", execCfg.InputConfig.SplitConfig.LineStartPattern)
	}
}
