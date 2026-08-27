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

// Package logsource holds the OTel log-receiver building blocks shared by otel/logprocessing and
// otel/logmetrics, since both tail the same kind of log sources.

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
	"strings"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/execlogreceiver"
	"github.com/bleemeo/glouton/utils/gloutonexec"
	"github.com/bleemeo/glouton/utils/hostrootsymlink"
	"github.com/bleemeo/glouton/version"

	"github.com/bmatcuk/doublestar/v4"
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

// decodeRawReceiverConfig decodes raw YAML into dest, overwriting only the fields present in raw.
// Reuses unmarshalMapstructureHook so nested operator.Config fields decode correctly.
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

// retryCfg: mapstructure-decoded consumer retry config for receivers.
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

// ResolveIncludeGlobs expands patterns into hostroot-stripped, symlink-resolved, deduplicated file paths
// (symlink resolution is needed for e.g. Kubernetes' /var/log/containers/* -> /var/log/pods/* symlinks).
// warn is called, once per skipped pattern, with a ready-to-format message (no receiver name, no trailing
// punctuation); callers decide where it's surfaced (a plain log line, or a warning visible in the UI).
func ResolveIncludeGlobs(hostroot string, patterns []string, warn func(msg string)) []string {
	hasHostRoot := len(hostroot) > len(string(os.PathSeparator))

	seen := make(map[string]bool)

	var files []string

	for _, pattern := range patterns {
		matching, err := doublestar.FilepathGlob(
			filepath.Join(hostroot, pattern),
			doublestar.WithFilesOnly(),
			doublestar.WithFailOnIOErrors(),
		)
		if err != nil {
			if errors.Is(err, doublestar.ErrBadPattern) {
				warn(fmt.Sprintf("file %q: %v", pattern, err))

				continue
			}

			if errors.Is(err, fs.ErrPermission) {
				if hasHostRoot {
					// We don't support execlogreceiver from a container.
					warn(fmt.Sprintf("resolving file %q: %v (ignoring it)", pattern, err))

					continue
				}

				if strings.Contains(pattern, "*") {
					if unwrapped := errors.Unwrap(err); unwrapped != nil {
						// Getting rid of the operation that failed (stat, open, ...)
						// to only show the actual error (e.g. "permission denied").
						err = unwrapped
					}

					warn(fmt.Sprintf(
						"resolving file pattern %q: %v (ignoring it; Glouton can read a protected log file via "+
							"sudo tail, but only for an explicit path, not a glob pattern)", pattern, err,
					))

					continue
				}

				matching = []string{pattern} // still a chance via sudo tail
			} else {
				warn(fmt.Sprintf("file %q: %v", pattern, err))

				continue
			}
		} else if hasHostRoot {
			// Dropping the hostroot from each log file path, if necessary.
			// We'll re-add it only where it is needed (stat, tail, ...).
			for i, logFile := range matching {
				matching[i] = strings.TrimPrefix(logFile, hostroot)
			}
		}

		for _, file := range matching {
			realFile := file
			// Resolve symlinks relative to hostroot (Kubernetes/containerd's /var/log/containers/XXX -> /var/log/pods/XXX).
			if hostroot != "/" {
				realFile = hostrootsymlink.EvalSymlinks(hostroot, realFile)
			}

			if !seen[realFile] {
				seen[realFile] = true

				files = append(files, realFile)
			}
		}
	}

	return files
}

// SetupLogReceiverFactories builds receiver factories, falling back to sudo-tail for unreadable files. Missing files are ignored; extraRaw is merged with this function's fields taking priority.
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
	if _, hasOperators := extraRaw["operators"]; hasOperators {
		trimmedRaw := maps.Clone(extraRaw)
		delete(trimmedRaw, "operators")
		extraRaw = trimmedRaw
	}

	sizeFnByFile = make(map[string]func() (int64, error), len(logFiles))
	sizeByFile := make(map[string]int64, len(logFiles))

	for _, logFile := range logFiles {
		ignore, needSudo, sizeFn := statFile(logFile, hostroot, commandRunner)
		if ignore {
			continue
		}

		// Probed here, before the file is classified below, rather than inside the per-file factory loops
		// further down. A probe failure (logrotate racing the stat, or sudoStatFile timing out under load)
		// has to drop the file from readableFiles/execFiles entirely: callers read those lists to decide
		// what they are now watching, so a file left in them with no factory behind it gets recorded as
		// tailed while nothing tails it -- and never retried, since the next update() skips whatever is
		// already being watched. Probing first also keeps makeStorageFn from registering a persistent
		// extension for a file that turns out to be unusable.
		size, err := sizeFn()
		if err != nil {
			logger.V(1).Printf("Error getting size of file %q (ignoring it): %v", logFile, err)

			continue
		}

		sizeFnByFile[logFile] = sizeFn
		sizeByFile[logFile] = size

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

		// exclude comes straight from the receiver's raw config, so it is spelled the way the user thinks
		// of the path -- but fileconsumer matches it against the globbed Include paths, which are
		// hostroot-prefixed just above. Left as-is, every exclude pattern silently fails to match in any
		// containerized deployment, so the file the user meant to leave out gets tailed anyway.
		for i, exclude := range fileTypedCfg.InputConfig.Exclude {
			fileTypedCfg.InputConfig.Exclude[i] = filepath.Join(hostroot, exclude)
		}

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

		// Offset is stored separately; only new files need to start at the end.
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

		if len(extraRaw) > 0 {
			if err := decodeRawReceiverConfig(execTypedCfg, extraRaw); err != nil {
				return nil, nil, nil, nil, fmt.Errorf("decoding extra receiver config: %w", err)
			}
		}

		size := sizeByFile[logFile]

		tailArgs := []string{"tail", tailFollowName}

		if lastSize, ok := lastFileSizes[logFile]; ok {
			if lastSize > size { // file was truncated
				tailArgs = append(tailArgs, "--bytes=+0")
			} else {
				tailArgs = append(tailArgs, fmt.Sprintf("--bytes=+%d", lastSize)) // resume from last offset
			}
		} else {
			tailArgs = append(tailArgs, "--bytes=0") // new file: start at the end
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

// StatFile opens logFile directly, falling back to `sudo stat` on permission errors.
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
