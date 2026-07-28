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

// Package logsource holds the OTel log-receiver building blocks shared by
// otel/logprocessing (log shipping) and otel/logmetrics (log-to-metric): both
// features tail the same kind of log sources (static files, container logs,
// externally-pushed OTLP), so this package is where that logic lives once.
//
// Sharing stops at code: each caller keeps its own persisted state, its own
// component IDs and its own pipeline/manager lifecycle, so a bug in one
// feature's state can't leak into the other's.
package logsource

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/execlogreceiver"
	"github.com/bleemeo/glouton/utils/gloutonexec"
	"github.com/bleemeo/glouton/version"

	"github.com/go-viper/mapstructure/v2"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/fileconsumer/attrs"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/helper"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/filelogreceiver"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/receiver"
)

// ReceiverKind identifies which OTel receiver is tailing a given log file.
type ReceiverKind string

const (
	ReceiverFileLog ReceiverKind = "filelogreceiver"
	ReceiverExecLog ReceiverKind = "execlogreceiver"

	tailFollowName = "--follow=name"
)

var errUnexpectedType = errors.New("unexpected type")

// decodeRawReceiverConfig decodes raw (extraRaw's) YAML directly into dest (an
// already-populated real receiver config, e.g. *filelogreceiver.FileLogConfig),
// overwriting only the fields present in raw -- same partial-override
// semantics a real Collector config load has ("any setting you specify
// overrides the default, if present"). Reuses unmarshalMapstructureHook so a
// field like header.metadata_operators (itself []operator.Config) decodes
// correctly too.
func decodeRawReceiverConfig(dest any, raw map[string]any) error {
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:     dest,
		DecodeHook: unmarshalMapstructureHook,
	})
	if err != nil {
		return fmt.Errorf("creating decoder: %w", err)
	}

	return decoder.Decode(raw)
}

// CommandRunner runs external commands, used to `sudo tail`/`sudo stat` files
// this process can't read directly.
type CommandRunner interface {
	Run(ctx context.Context, option gloutonexec.Option, name string, arg ...string) ([]byte, error)
	StartWithPipes(ctx context.Context, option gloutonexec.Option, name string, arg ...string) (stdoutPipe io.ReadCloser, stderrPipe io.ReadCloser, wait func() error, err error)
}

// StatFileFunc reports whether logFile should be ignored (doesn't exist, or an
// unrecoverable error), whether it needs a sudo-tail fallback, and (if not
// ignored) a function returning its current size.
type StatFileFunc = func(logFile string, hostroot string, commandRunner CommandRunner) (ignore bool, needSudo bool, sizeFn func() (int64, error))

// Since github.com/open-telemetry/opentelemetry-collector-contrib/internal/coreinternal/consumerretry is internal,
// we recreate its config type and mapstructure.Decode() it into the receivers' options.
var retryCfg = struct { //nolint:gochecknoglobals
	Enabled         bool          `mapstructure:"enabled"`
	InitialInterval time.Duration `mapstructure:"initial_interval"`
	MaxInterval     time.Duration `mapstructure:"max_interval"`
	MaxElapsedTime  time.Duration `mapstructure:"max_elapsed_time"`
}{
	Enabled:         true,
	InitialInterval: 1 * time.Second,  // default value
	MaxInterval:     30 * time.Second, // default value
	MaxElapsedTime:  1 * time.Hour,
}

// SetupLogReceiverFactories builds receiver factories for the given log files,
// accordingly to whether the file is directly readable or not (falling back to
// a sudo-tail execlogreceiver when it isn't). Files that don't exist at the
// time of the call to this function will be ignored.
//
// extraRaw is raw YAML for any real filelogreceiver/fileconsumer field beyond
// what this function already sets itself (e.g. start_at, on_truncate,
// encoding, multiline, exclude, poll_interval, header) -- decoded straight
// into the real vendored config, same trick used for OTELFilters/OTELOperator
// elsewhere, so an existing OTel Collector receiver config pastes in almost
// verbatim. It's applied before this function's own fields, which always win
// on conflict (e.g. Include/StorageID/StartAt-for-new-files are never
// overridable this way), and only to the filelogreceiver path: it has no
// filelogreceiver-shaped equivalent on the execlogreceiver sudo-tail
// fallback, so it's silently inapplicable there.
func SetupLogReceiverFactories(
	logFiles []string,
	hostroot string,
	operators []operator.Config,
	lastFileSizes map[string]int64,
	commandRunner CommandRunner,
	makeStorageFn func(logFile string) *component.ID,
	statFile StatFileFunc,
	extraAttributes map[string]helper.ExprStringConfig,
	extraRaw map[string]any,
) (
	factories map[receiver.Factory]component.Config,
	readableFiles, execFiles []string,
	sizeFnByFile map[string]func() (int64, error),
	err error,
) {
	sizeFnByFile = make(map[string]func() (int64, error), len(logFiles))

	for _, logFile := range logFiles {
		ignore, needSudo, sizeFn := statFile(logFile, hostroot, commandRunner)
		if ignore {
			continue
		}

		sizeFnByFile[logFile] = sizeFn

		if needSudo {
			execFiles = append(execFiles, logFile)
		} else {
			readableFiles = append(readableFiles, logFile)
		}
	}

	factories = make(map[receiver.Factory]component.Config, len(readableFiles)+len(execFiles))

	for _, logFile := range readableFiles {
		factory := filelogreceiver.NewFactory()
		fileCfg := factory.CreateDefaultConfig()

		fileTypedCfg, ok := fileCfg.(*filelogreceiver.FileLogConfig)
		if !ok {
			return nil, nil, nil, nil, fmt.Errorf("%w for file log receiver: %T", errUnexpectedType, fileCfg)
		}

		if len(extraRaw) > 0 {
			if err := decodeRawReceiverConfig(fileTypedCfg, extraRaw); err != nil {
				return nil, nil, nil, nil, fmt.Errorf("decoding extra receiver config: %w", err)
			}
		}

		fileTypedCfg.InputConfig.Include = []string{filepath.Join(hostroot, logFile)}
		fileTypedCfg.InputConfig.IncludeFileName = true
		fileTypedCfg.InputConfig.IncludeFilePath = false // set manually
		fileTypedCfg.InputConfig.Attributes = map[string]helper.ExprStringConfig{
			attrs.LogFilePath: helper.ExprStringConfig(logFile), // so as to avoid the hostroot prefix
		}
		fileTypedCfg.Operators = operators
		fileTypedCfg.BaseConfig.StorageID = makeStorageFn(logFile)

		if extraAttributes != nil {
			maps.Insert(fileTypedCfg.InputConfig.Attributes, maps.All(extraAttributes))
		}

		_, err := sizeFnByFile[logFile]()
		if err != nil {
			logger.V(1).Printf("Error getting size of file %q (ignoring it): %v", logFile, err)

			continue
		}

		// For filelogreceivers the offset is stored separately, so we don't really care about the size here.
		// However, if this is the first time we've seen this file, we want to read starting at the end.
		if _, ok := lastFileSizes[logFile]; !ok {
			fileTypedCfg.InputConfig.StartAt = "end"
		}

		err = mapstructure.Decode(retryCfg, &fileTypedCfg.RetryOnFailure)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed to define consumerretry config on file log receiver: %w", err)
		}

		factories[factory] = fileTypedCfg
	}

	for _, logFile := range execFiles {
		factory := execlogreceiver.NewFactory()
		execCfg := factory.CreateDefaultConfig()

		execTypedCfg, ok := execCfg.(*execlogreceiver.ExecLogConfig)
		if !ok {
			return nil, nil, nil, nil, fmt.Errorf("%w for exec log receiver: %T", errUnexpectedType, execCfg)
		}

		size, err := sizeFnByFile[logFile]()
		if err != nil {
			logger.V(1).Printf("Error getting size of file %q (ignoring it): %v", logFile, err)

			continue
		}

		tailArgs := []string{"tail", tailFollowName}

		if lastSize, ok := lastFileSizes[logFile]; ok {
			if lastSize > size { // the file has been truncated since the last time
				tailArgs = append(tailArgs, "--bytes=+0") // start at the beginning of the file
			} else { // the file has at least the same size as the last time
				tailArgs = append(tailArgs, fmt.Sprintf("--bytes=+%d", lastSize)) // start where we were the last time
			}
		} else { // the file has never been seen before
			tailArgs = append(tailArgs, "--bytes=0") // start at the end of the file
		}

		execTypedCfg.InputConfig.Argv = append(tailArgs, filepath.Join(hostroot, logFile)) //nolint: gocritic
		execTypedCfg.InputConfig.CommandRunner = commandRunner
		execTypedCfg.InputConfig.RunAsRoot = true
		execTypedCfg.InputConfig.Attributes = map[string]helper.ExprStringConfig{
			attrs.LogFileName: helper.ExprStringConfig(filepath.Base(logFile)),
			attrs.LogFilePath: helper.ExprStringConfig(logFile),
		}
		execTypedCfg.Operators = operators

		if extraAttributes != nil {
			maps.Insert(execTypedCfg.InputConfig.Attributes, maps.All(extraAttributes))
		}

		err = mapstructure.Decode(retryCfg, &execTypedCfg.RetryOnFailure)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed to define consumerretry config on exec log receiver: %w", err)
		}

		factories[factory] = execTypedCfg
	}

	return factories, readableFiles, execFiles, sizeFnByFile, nil
}

// StatFile is the default StatFileFunc: it opens logFile directly, and if that
// fails with a permission error, falls back to `sudo stat` to check whether a
// sudo-tail (execlogreceiver) can read it instead.
func StatFile(logFile, hostroot string, commandRunner CommandRunner) (ignore, needSudo bool, sizeFn func() (int64, error)) {
	logFilePath := filepath.Join(hostroot, logFile)

	f, err := os.OpenFile(logFilePath, os.O_RDONLY, 0) // the mode perm isn't needed for read
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return true, false, nil
		}

		if !errors.Is(err, fs.ErrPermission) {
			logger.V(1).Printf("Failed to open log file %q (ignoring it): %v", logFile, err)

			return true, false, nil
		}

		if version.IsWindows() {
			logger.V(1).Printf("Can't open protected log file on Windows, ignoring %q.", logFile)

			return true, false, nil
		}

		if _, err = sudoStatFile(logFilePath, commandRunner); err != nil {
			logger.V(1).Printf("Can't `sudo stat` log file %q (ignoring it): %v", logFile, err)

			return true, false, nil
		}

		needSudo = true
		sizeFn = func() (int64, error) {
			statOutput, err := sudoStatFile(logFilePath, commandRunner)
			if err != nil {
				return 0, err
			}

			size, err := strconv.ParseInt(string(statOutput), 10, 64)
			if err != nil {
				return 0, fmt.Errorf("unexpected stat output %q: %w", statOutput, err)
			}

			return size, nil
		}
	} else {
		err = f.Close()
		if err != nil {
			logger.V(1).Printf("Failed to close log file %q: %v", logFile, err)
		}

		needSudo = false
		sizeFn = func() (int64, error) {
			stat, err := os.Stat(logFilePath)
			if err != nil {
				return 0, err
			}

			return stat.Size(), nil
		}
	}

	return false, needSudo, sizeFn
}

// sudoStatFile executes a `sudo stat --printf=%s` on the given file and returns its (trimmed) output.
func sudoStatFile(logFile string, commandRunner CommandRunner) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	runOpt := gloutonexec.Option{
		RunAsRoot:      true,
		CombinedOutput: true,
	}

	out, err := commandRunner.Run(ctx, runOpt, "stat", "--printf=%s", logFile)
	trimmedOutput := bytes.TrimSpace(out)

	if err != nil {
		strOut := string(trimmedOutput)
		if strOut != "" {
			strOut = ": " + strOut
		}

		return nil, fmt.Errorf("%w%s", err, strOut)
	}

	return trimmedOutput, nil
}
